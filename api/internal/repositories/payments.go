package repositories

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

// ErrAttemptExists reports that the attempt number was taken between reading the
// invoice's attempts and writing the new one. It is the expected outcome of a
// customer double-clicking "pay", not an internal error: the caller re-reads and
// shows the session that already exists.
var ErrAttemptExists = errors.New("payment attempt already exists")

// PaymentRepository stores payment attempts and checkout sessions.
//
// Both hang off the invoice partition (see PaymentAttemptSK, CheckoutSK), so
// every read here is a prefix Query inside a tenant partition the caller was
// already entitled to. The one exception is GetAttemptByCharge, which is the
// webhook path and is documented where it is defined.
type PaymentRepository struct {
	base   Base
	audit  Base
	events Base
}

func NewPaymentRepository(db *dynamodb.Client, cfg *config.Config) *PaymentRepository {
	return &PaymentRepository{
		base:   NewBase(db, cfg, TableInvoices),
		audit:  NewBase(db, cfg, TableAudit),
		events: NewBase(db, cfg, TableWebhooks),
	}
}

// CreateAttempt writes an attempt create-only.
//
// Create-only is the concurrency control, and it is why no counter is needed: the
// attempt number comes from ListAttempts, and two racing requests compute the
// same number, so exactly one write survives. The loser gets ErrAttemptExists
// and re-reads — which is the correct answer, because the charge it would have
// opened is the one the winner already opened.
//
// The row is written **after** wallet returned the charge id, so an attempt that
// exists always names a charge that exists. The reverse order would leave rows
// pointing at nothing every time the wallet call timed out. A charge opened and
// never recorded is recoverable instead: the idempotency key is deterministic
// ({invoice}:{n}), so the retry gets the same charge back rather than a second one.
func (r *PaymentRepository) CreateAttempt(ctx context.Context, a *billing.PaymentAttempt, now time.Time) error {
	if a.WalletChargeID == "" && a.Method != billing.MethodManual {
		return fmt.Errorf("repositories: an automatic attempt is recorded with its charge id")
	}
	lookup := ""
	if a.WalletChargeID != "" {
		lookup = LookupWalletChargePK(a.Livemode, a.WalletChargeID)
	}
	// Only a PENDING attempt against a real charge is worth reconciling. A manual
	// receipt has nothing to poll, and an attempt written already-terminal has
	// nothing to wait for.
	schedulePK, scheduleSK := scheduleKeys(
		a.Livemode, JobChargeReconcile, brcal.FromTime(now), a.ID,
		a.Status == billing.AttemptPending && a.WalletChargeID != "",
	)
	item, err := Encode(paymentAttemptRow{
		keys: newKeys(
			TenantPK(a.OrganizationID, a.Livemode),
			PaymentAttemptSK(a.InvoiceID, a.AttemptNumber),
			RetentionPaymentAttempt, now,
		),
		PeriodAttrs:    NewPeriodAttrs(a.OrganizationID, a.Livemode, EntityPaymentAttempt, brcal.FromTime(now), a.ID),
		PaymentAttempt: *a,
		LookupPK:       lookup,
		SchedulePK:     schedulePK,
		ScheduleSK:     scheduleSK,
	})
	if err != nil {
		return err
	}
	err = r.base.TransactWrite(ctx, txItems(r.base.BuildPutTxItemIfAbsent(item)))
	if IsConditionFailed(err) {
		return fmt.Errorf("%w: %s attempt %d", ErrAttemptExists, a.InvoiceID, a.AttemptNumber)
	}
	return err
}

// ListAttempts returns an invoice's attempts, oldest first.
func (r *PaymentRepository) ListAttempts(ctx context.Context, organizationID string, livemode bool, invoiceID string) ([]billing.PaymentAttempt, error) {
	res, err := r.base.Query(ctx, QueryOpts{
		PK:               TenantPK(organizationID, livemode),
		SKPrefix:         InvoiceSK(invoiceID) + "#ATTEMPT#",
		ScanIndexForward: true,
		Limit:            100,
	})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[paymentAttemptRow](res.Items)
	if err != nil {
		return nil, err
	}
	out := make([]billing.PaymentAttempt, len(rows))
	for i, row := range rows {
		out[i] = row.PaymentAttempt
	}
	return out, nil
}

// GetAttemptByCharge finds the attempt a wallet webhook refers to.
//
// This is the one read in the payment path that precedes knowing the tenant, and
// it cannot be otherwise: wallet knows a charge id and nothing else. The row it
// returns carries the organization and the mode, and every read after this one
// is scoped by those — never by anything in the request body.
func (r *PaymentRepository) GetAttemptByCharge(ctx context.Context, livemode bool, walletChargeID string) (*billing.PaymentAttempt, error) {
	res, err := r.base.Query(ctx, QueryOpts{
		IndexName: IndexLookup,
		PKField:   "lookup_pk",
		PK:        LookupWalletChargePK(livemode, walletChargeID),
		Limit:     1,
	})
	if err != nil {
		return nil, err
	}
	if len(res.Items) == 0 {
		return nil, fmt.Errorf("%w: charge %s", ErrNotFound, walletChargeID)
	}
	row, err := Decode[paymentAttemptRow](res.Items[0])
	if err != nil {
		return nil, err
	}
	return &row.PaymentAttempt, nil
}

// PendingOn returns the attempts opened on a date that are still waiting on
// wallet.
//
// Cross-tenant, like every schedule-index read and for the same reason (ADR
// 0002): a charge with no answer is not one tenant's problem to notice. It must
// never be reachable from a request-scoped path, which is why the reconciliation
// job is a binary and not a route.
//
// Rows are only in this partition while they are PENDING — TransitionAttempt
// removes the keys — so what comes back is the work, not the day's history.
func (r *PaymentRepository) PendingOn(
	ctx context.Context,
	livemode bool,
	opened brcal.Date,
	limit int,
	startKey map[string]types.AttributeValue,
) (*Page[billing.PaymentAttempt], error) {
	res, err := r.base.Query(ctx, QueryOpts{
		IndexName:         IndexSchedule,
		PKField:           "schedule_pk",
		SKField:           "schedule_sk",
		PK:                SchedulePK(livemode, JobChargeReconcile, opened),
		Limit:             limit,
		ExclusiveStartKey: startKey,
		ScanIndexForward:  true,
	})
	if err != nil {
		return nil, err
	}
	rows, err := DecodeItems[paymentAttemptRow](res.Items)
	if err != nil {
		return nil, err
	}
	out := make([]billing.PaymentAttempt, len(rows))
	for i, row := range rows {
		out[i] = row.PaymentAttempt
	}
	return &Page[billing.PaymentAttempt]{Items: out, LastEvaluatedKey: res.LastEvaluatedKey}, nil
}

// TransitionAttempt applies a state change with its audit entry.
//
// The lookup key is **not** removed on a terminal status. A webhook that arrives
// twice, or arrives late, must still find the attempt — otherwise the second
// delivery looks like a charge billing has never heard of, which is the shape of
// a real integration bug and would drown it in noise.
func (r *PaymentRepository) TransitionAttempt(
	ctx context.Context,
	a *billing.PaymentAttempt,
	to billing.PaymentAttemptStatus,
	cause billing.Cause,
	actor, requestID string,
	now time.Time,
) ([]billing.EventType, error) {
	updated := *a
	events, err := updated.Transition(to, cause)
	if err != nil {
		return nil, err
	}

	set := map[string]types.AttributeValue{}
	if updated.FailureReason != "" {
		set["failure_reason"] = &types.AttributeValueMemberS{Value: updated.FailureReason}
	}

	change := StatusChange{
		OrganizationID: a.OrganizationID,
		Livemode:       a.Livemode,
		PK:             TenantPK(a.OrganizationID, a.Livemode),
		SK:             PaymentAttemptSK(a.InvoiceID, a.AttemptNumber),
		From:           string(a.Status),
		To:             string(to),
		Set:            set,
		// Every status reachable from PENDING is terminal, so the attempt always
		// leaves the reconciliation sweep here. That is what keeps a settled charge
		// from being re-asked about on every run for the rest of the day.
		Remove: []string{"schedule_pk", "schedule_sk"},
		Audit: AuditEntry{
			Entity:    EntityPaymentAttempt,
			EntityID:  a.ID,
			Action:    string(events[0]),
			Cause:     cause,
			Actor:     actor,
			RequestID: requestID,
		},
	}
	if err := CommitStatusChange(ctx, r.tables(), change, now); err != nil {
		return nil, err
	}
	*a = updated
	return events, nil
}

// CreateSession writes a checkout session create-only.
func (r *PaymentRepository) CreateSession(ctx context.Context, s *billing.CheckoutSession, now time.Time) error {
	if err := s.Metadata.Validate(); err != nil {
		return err
	}
	item, err := Encode(checkoutRow{
		keys: newKeys(
			TenantPK(s.OrganizationID, s.Livemode),
			CheckoutSK(s.InvoiceID, s.ID),
			RetentionCheckoutSession, now,
		),
		PeriodAttrs:     NewPeriodAttrs(s.OrganizationID, s.Livemode, EntityCheckout, brcal.FromTime(now), s.ID),
		CheckoutSession: *s,
	})
	if err != nil {
		return err
	}
	return r.base.TransactWrite(ctx, txItems(r.base.BuildPutTxItemIfAbsent(item)))
}

// LatestSession returns the newest session for an invoice, or nil when there is
// none.
//
// Newest first, because the only question the checkout page asks is "is the most
// recent session still usable?". An older one never becomes usable again.
func (r *PaymentRepository) LatestSession(ctx context.Context, organizationID string, livemode bool, invoiceID string) (*billing.CheckoutSession, error) {
	res, err := r.base.Query(ctx, QueryOpts{
		PK:       TenantPK(organizationID, livemode),
		SKPrefix: InvoiceSK(invoiceID) + "#" + skCheckout,
		Limit:    1,
	})
	if err != nil {
		return nil, err
	}
	if len(res.Items) == 0 {
		return nil, nil
	}
	row, err := Decode[checkoutRow](res.Items[0])
	if err != nil {
		return nil, err
	}
	return &row.CheckoutSession, nil
}

// TransitionSession applies a session state change with its audit entry.
func (r *PaymentRepository) TransitionSession(
	ctx context.Context,
	s *billing.CheckoutSession,
	to billing.CheckoutSessionStatus,
	cause billing.Cause,
	actor, requestID string,
	now time.Time,
) ([]billing.EventType, error) {
	updated := *s
	events, err := updated.Transition(to, cause)
	if err != nil {
		return nil, err
	}
	change := StatusChange{
		OrganizationID: s.OrganizationID,
		Livemode:       s.Livemode,
		PK:             TenantPK(s.OrganizationID, s.Livemode),
		SK:             CheckoutSK(s.InvoiceID, s.ID),
		From:           string(s.Status),
		To:             string(to),
		Audit: AuditEntry{
			Entity:    EntityCheckout,
			EntityID:  s.ID,
			Action:    string(events[0]),
			Cause:     cause,
			Actor:     actor,
			RequestID: requestID,
		},
	}
	if err := CommitStatusChange(ctx, r.tables(), change, now); err != nil {
		return nil, err
	}
	*s = updated
	return events, nil
}

// tables is the set every transition in this repository writes across.
func (r *PaymentRepository) tables() Tables {
	return Tables{Rows: r.base, Audit: r.audit, Events: r.events}
}
