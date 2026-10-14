package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/byokrpc"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/limits"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

var (
	// ErrPayoutInvalid: a field of the request cannot make a payout.
	ErrPayoutInvalid = errors.New("invalid payout")
	// ErrPayoutConflict: the reference already names a payout for another CPF or amount.
	ErrPayoutConflict = errors.New("the reference belongs to another payout")
	// ErrPayoutNotAllowed: the tenant has no connection that can pay out.
	ErrPayoutNotAllowed = errors.New("this tenant cannot pay out")
)

const prefixPayout = "po_"

// PayoutInput is one payment out to a person's Pix key. The destination is the
// CPF: the caller has already matched it to the person (the wallet takes it
// from the user's KYC record), and Billing pays a CPF key and nothing else.
type PayoutInput struct {
	Reference   string
	CPF         string
	AmountCents int64
	Message     string
}

// Payouts pays Pix keys out of a tenant's connected bank account. It reserves
// the payout before calling the bank, sends the bank a UUID it deduplicates on,
// and never reads a failure of the call as "not paid": only the bank's own
// answer settles a payout.
type Payouts struct {
	payments *repositories.PaymentRepository
	orgs     *repositories.OrganizationRepository
	conns    *repositories.ByokRepository
	sessions func(*byokconn.Connection) byokrpc.Session
}

func NewPayouts(payments *repositories.PaymentRepository, orgs *repositories.OrganizationRepository, conns *repositories.ByokRepository,
	sessions func(*byokconn.Connection) byokrpc.Session) *Payouts {
	return &Payouts{payments: payments, orgs: orgs, conns: conns, sessions: sessions}
}

func (in *PayoutInput) validate() error {
	canonical, kind, ok := billing.NormalizeTaxID(in.CPF)
	switch {
	case !ok || kind != billing.TaxIDCPF:
		return fmt.Errorf("%w: cpf", ErrPayoutInvalid)
	case in.AmountCents <= 0 || in.AmountCents > limits.MaxAmountCents:
		return fmt.Errorf("%w: amount_cents", ErrPayoutInvalid)
	case !validReference(in.Reference):
		return fmt.Errorf("%w: reference", ErrPayoutInvalid)
	case len([]rune(in.Message)) > limits.Description:
		return fmt.Errorf("%w: message", ErrPayoutInvalid)
	}
	in.CPF = canonical
	return nil
}

func payoutHash(in PayoutInput) string {
	sum := sha256.Sum256([]byte(in.CPF + "|" + strconv.FormatInt(in.AmountCents, 10)))
	return hex.EncodeToString(sum[:])
}

// connection is the tenant's connection that may pay: its collecting one, with
// the payout capability.
func (s *Payouts) connection(ctx context.Context, org *billing.Organization) (*byokconn.Connection, error) {
	if org.Rail() != billing.RailBYOK {
		return nil, ErrPayoutNotAllowed
	}
	conn, err := s.conns.GetConnection(ctx, org.AccountOrganizationID, org.Livemode, org.PayoutProvider)
	if errors.Is(err, repositories.ErrNotFound) {
		return nil, ErrPayoutNotAllowed
	}
	if err != nil {
		return nil, err
	}
	if conn.State != byokconn.StateActive || !conn.Has(byok.CapabilityPayout) {
		return nil, ErrPayoutNotAllowed
	}
	if err := s.conns.LoadSealed(ctx, conn); err != nil {
		return nil, err
	}
	return conn, nil
}

// Create reserves the payout and asks the bank to pay it. created=false means
// the reference already had one (returned as it stands now). A payout whose
// bank call failed in a way that says nothing about the money stays processing
// and is finished by Resolve.
func (s *Payouts) Create(ctx context.Context, organizationID string, livemode bool, in PayoutInput, now time.Time) (*billing.Payout, bool, error) {
	if err := in.validate(); err != nil {
		return nil, false, err
	}
	org, err := s.orgs.Get(ctx, organizationID, livemode)
	if err != nil {
		return nil, false, err
	}
	conn, err := s.connection(ctx, org)
	if err != nil {
		return nil, false, err
	}
	p := &billing.Payout{ID: id.NewWithPrefix(prefixPayout), OrganizationID: organizationID, Livemode: livemode, Reference: in.Reference,
		RequestHash: payoutHash(in), Ref: uuid.NewString(), Amount: billing.Cents(in.AmountCents), CPF: in.CPF,
		Message: strings.TrimSpace(in.Message), Status: billing.PayoutProcessing}
	got, created, err := s.payments.CreatePayout(ctx, p, now)
	if err != nil {
		return nil, false, err
	}
	if !created {
		if got.RequestHash != p.RequestHash {
			return nil, false, fmt.Errorf("%w: %s", ErrPayoutConflict, in.Reference)
		}
		if !got.Final() {
			// The first call may have died before the bank answered: finish it.
			if err := s.advance(ctx, conn, got, now); err != nil {
				slog.WarnContext(ctx, "payout not advanced on replay", "payout_id", got.ID, "error", err)
			}
		}
		return got, false, nil
	}
	if err := s.advance(ctx, conn, got, now); err != nil {
		slog.WarnContext(ctx, "payout left processing", "payout_id", got.ID, "error", err)
	}
	return got, true, nil
}

// advance does the next step of a processing payout: send it when the bank
// has not taken it yet (the same UUID, so a repeat cannot pay twice), then read
// the bank's answer. It settles only on the bank's word.
func (s *Payouts) advance(ctx context.Context, conn *byokconn.Connection, p *billing.Payout, now time.Time) error {
	sess := s.sessions(conn)
	if p.ProviderRef == "" {
		sent, err := sess.Payout(ctx, byok.PayoutRequest{Ref: p.Ref, Amount: int64(p.Amount), PixKey: p.CPF, Message: p.Message})
		switch {
		case errors.Is(err, byok.ErrPixKeyNotFound):
			return s.payments.SettlePayout(ctx, p, billing.PayoutFailed, "", billing.PayoutKeyNotFound, now)
		case errors.Is(err, byok.ErrInvalidRequest), errors.Is(err, byok.ErrScopesRefused):
			// The bank rejected the request itself (4xx): it was not processed.
			return s.payments.SettlePayout(ctx, p, billing.PayoutFailed, "", billing.PayoutRefused, now)
		case err != nil:
			return err // unavailable, rate limited, unauthorized, unknown: says nothing about the money
		}
		if sent.ProviderRef == "" {
			return fmt.Errorf("%w: the bank accepted payout %s without an id", byok.ErrUnavailable, p.ID)
		}
		if err := s.payments.SetPayoutSent(ctx, p, sent.ProviderRef, now); err != nil {
			return err
		}
	}
	got, err := sess.GetPayout(ctx, p.ProviderRef)
	if err != nil {
		return err // ErrNotFound included: the bank not knowing it yet is not a verdict
	}
	switch got.Status {
	case byok.PayoutDone:
		return s.payments.SettlePayout(ctx, p, billing.PayoutDone, got.PaymentID, "", now)
	case byok.PayoutFailed:
		reason := got.Reason
		if reason == "" {
			reason = billing.PayoutRefused
		}
		return s.payments.SettlePayout(ctx, p, billing.PayoutFailed, "", reason, now)
	case byok.PayoutUnknown:
		slog.ErrorContext(ctx, "payout in a status nobody mapped; left processing", "payout_id", p.ID)
	}
	return nil
}

// Resolve is the sweep's step for a payout still processing.
func (s *Payouts) Resolve(ctx context.Context, p *billing.Payout, now time.Time) error {
	org, err := s.orgs.Get(ctx, p.OrganizationID, p.Livemode)
	if err != nil {
		return err
	}
	conn, err := s.connection(ctx, org)
	if err != nil {
		return err
	}
	fresh, err := s.payments.GetPayout(ctx, p.OrganizationID, p.Livemode, p.ID)
	if err != nil {
		return err
	}
	if fresh.Final() {
		*p = *fresh
		return nil
	}
	err = s.advance(ctx, conn, fresh, now)
	*p = *fresh
	return err
}

func (s *Payouts) Get(ctx context.Context, organizationID string, livemode bool, payoutID string) (*billing.Payout, error) {
	return s.payments.GetPayout(ctx, organizationID, livemode, payoutID)
}

func (s *Payouts) GetByReference(ctx context.Context, organizationID string, livemode bool, reference string) (*billing.Payout, error) {
	return s.payments.GetPayoutByReference(ctx, organizationID, livemode, reference)
}
