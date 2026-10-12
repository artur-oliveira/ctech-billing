package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/byokrpc"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// ErrRefundWindowClosed: Inter refunds a Pix up to 90 days after it was paid.
var ErrRefundWindowClosed = errors.New("byok: the 90-day refund window closed")

// RefundExcess gives an excess payment back to its payer, only when the
// merchant asks (D19, spec § 7.5). Idempotent: the refund id is stored on the
// row before the provider is called, and a second press finds it refunding.
func (c *ByokCollector) RefundExcess(ctx context.Context, organizationID string, livemode bool, excessID, actor, requestID string, now time.Time) (*billing.ExcessPayment, error) {
	e, err := c.payments.GetExcess(ctx, organizationID, livemode, excessID)
	if err != nil {
		return nil, err
	}
	if e.Status != billing.ExcessOpen && e.Status != billing.ExcessRefundFailed {
		return e, nil // already refunding or refunded
	}
	if !e.Refundable(now) {
		return nil, ErrRefundWindowClosed
	}
	org, err := c.orgs.Get(ctx, organizationID, livemode)
	if err != nil {
		return nil, err
	}
	if err := c.gates.Authorize(ctx, org); err != nil {
		return nil, err
	}
	if err := c.payments.SetExcessStatus(ctx, e, billing.ExcessRefunding, billing.NewRefundID(), now); err != nil {
		if errors.Is(err, repositories.ErrConcurrentModification) {
			return c.payments.GetExcess(ctx, organizationID, livemode, excessID)
		}
		return nil, err
	}
	slog.InfoContext(ctx, "byok refund requested", "excess_id", e.ID, "actor", actor, "request_id", requestID)
	return e, c.sendRefund(ctx, org, e, now)
}

// creditRefundSource is the payment a credit refund gives back from, and the
// day it was paid; ErrNotByokRail when the invoice was not paid by BYOK.
func (c *ByokCollector) creditRefundSource(ctx context.Context, inv *billing.Invoice, now time.Time) (*billing.PaymentAttempt, time.Time, error) {
	if inv.Status != billing.InvoicePaid {
		return nil, time.Time{}, fmt.Errorf("%w: only a paid invoice is refunded", ErrInvoiceNotPayable)
	}
	attempts, err := c.payments.ListAttempts(ctx, inv.OrganizationID, inv.Livemode, inv.ID)
	if err != nil {
		return nil, time.Time{}, err
	}
	var paid *billing.PaymentAttempt
	for i := range attempts {
		if attempts[i].Status == billing.AttemptSucceeded && attempts[i].Method == billing.MethodBYOK {
			paid = &attempts[i]
		}
	}
	if paid == nil || paid.PaymentID == "" {
		return nil, time.Time{}, ErrNotByokRail
	}
	paidAt, _ := time.Parse(time.RFC3339, inv.PaidAt)
	if paidAt.IsZero() {
		paidAt = now
	}
	return paid, paidAt, nil
}

// CheckRefundCredit answers, before a credit note exists, whether "Devolver
// via Pix" can happen for it: paid by BYOK, inside Inter's 90 days, the gate
// open. The console refuses the note otherwise, so a retry never issues two.
func (c *ByokCollector) CheckRefundCredit(ctx context.Context, inv *billing.Invoice, now time.Time) error {
	_, paidAt, err := c.creditRefundSource(ctx, inv, now)
	if err != nil {
		return err
	}
	if !(billing.ExcessPayment{PaidAt: paidAt}).Refundable(now) {
		return ErrRefundWindowClosed
	}
	org, err := c.orgs.Get(ctx, inv.OrganizationID, inv.Livemode)
	if err != nil {
		return err
	}
	return c.gates.Authorize(ctx, org)
}

// RefundCredit gives back part of a BYOK-paid invoice with a credit note
// ("devolver via Pix", spec § 7.5), from the payment that settled it. The
// row is keyed by the credit note: a second call finds it and resumes.
func (c *ByokCollector) RefundCredit(ctx context.Context, inv *billing.Invoice, cn *billing.CreditNote, actor, requestID string, now time.Time) (*billing.ExcessPayment, error) {
	if cn.Amount <= 0 {
		return nil, fmt.Errorf("%w: nothing to refund", ErrInvoiceNotPayable)
	}
	paid, paidAt, err := c.creditRefundSource(ctx, inv, now)
	if err != nil {
		return nil, err
	}
	e := &billing.ExcessPayment{ID: "ex_" + id.New(), OrganizationID: inv.OrganizationID, Livemode: inv.Livemode, InvoiceID: inv.ID,
		AttemptID: paid.ID, AttemptNumber: paid.AttemptNumber, Provider: paid.Provider, PaymentID: cn.ID, SourcePaymentID: paid.PaymentID,
		Amount: cn.Amount, Status: billing.ExcessOpen, Kind: billing.RefundOfCredit, CreditNoteID: cn.ID, PaidAt: paidAt}
	created, err := c.payments.RecordExcess(ctx, e, now)
	if err != nil {
		return nil, err
	}
	if !created {
		existing, err := c.payments.GetExcessByPayment(ctx, inv.OrganizationID, inv.Livemode, inv.ID, cn.ID)
		if err != nil {
			return nil, err
		}
		e = existing
	}
	return c.RefundExcess(ctx, inv.OrganizationID, inv.Livemode, e.ID, actor, requestID, now)
}

// sendRefund calls the provider for a row already marked refunding.
func (c *ByokCollector) sendRefund(ctx context.Context, org *billing.Organization, e *billing.ExcessPayment, now time.Time) error {
	sess, err := c.session(ctx, org)
	if err != nil {
		return err
	}
	if err := pix(ctx, sess); err != nil {
		return err
	}
	r, err := byokrpc.Call(ctx, sess, byok.CapabilityCollection, func(cr byok.Credentials, t byok.Token) (byok.Refund, error) {
		return sess.P.Refund(ctx, cr, t, byok.RefundRequest{PaymentID: e.SourcePaymentID, RefundID: e.RefundID, Amount: int64(e.Amount),
			Reason: "Devolução pela CTech a pedido do recebedor"})
	})
	if err != nil {
		// The row stays refunding with its id: the sweep asks the provider
		// with the same id, which is the refund's idempotency key.
		slog.WarnContext(ctx, "byok refund call failed; the sweep retries", "excess_id", e.ID, "error", err)
		return nil
	}
	return c.applyRefund(ctx, e, r.Status, now)
}

func (c *ByokCollector) applyRefund(ctx context.Context, e *billing.ExcessPayment, st byok.RefundStatus, now time.Time) error {
	switch st {
	case byok.RefundDone:
		return c.payments.SetExcessStatus(ctx, e, billing.ExcessRefunded, "", now)
	case byok.RefundFailed:
		return c.payments.SetExcessStatus(ctx, e, billing.ExcessRefundFailed, "", now)
	}
	return nil
}

// FollowRefunds is the sweep's half of refunds (spec § 7.5): every refund in
// flight is asked about; one the provider never received is sent again with
// the same id.
func (c *ByokCollector) FollowRefunds(ctx context.Context, livemode bool, now time.Time) []error {
	var errs []error
	var start map[string]types.AttributeValue
	for {
		page, err := c.payments.Refunding(ctx, livemode, start, 100)
		if err != nil {
			return append(errs, err)
		}
		for i := range page.Items {
			if err := c.followRefund(ctx, &page.Items[i], now); err != nil {
				errs = append(errs, fmt.Errorf("refund %s: %w", page.Items[i].ID, err))
			}
		}
		if page.LastEvaluatedKey == nil {
			return errs
		}
		start = page.LastEvaluatedKey
	}
}

func (c *ByokCollector) followRefund(ctx context.Context, e *billing.ExcessPayment, now time.Time) error {
	org, err := c.orgs.Get(ctx, e.OrganizationID, e.Livemode)
	if err != nil {
		return err
	}
	sess, err := c.session(ctx, org)
	if errors.Is(err, ErrNotByokRail) || errors.Is(err, billing.ErrByokNotConnected) {
		// Disconnected since: CTech can no longer ask; the merchant sees the
		// refund at the bank. Left as it is, logged, never an hourly error.
		slog.WarnContext(ctx, "byok refund in flight on a disconnected organization", "excess_id", e.ID)
		return nil
	}
	if err != nil {
		return err
	}
	if err := pix(ctx, sess); err != nil {
		return err
	}
	r, err := byokrpc.Call(ctx, sess, byok.CapabilityCollection, func(cr byok.Credentials, t byok.Token) (byok.Refund, error) {
		return sess.P.GetRefund(ctx, cr, t, e.SourcePaymentID, e.RefundID)
	})
	if errors.Is(err, byok.ErrNotFound) {
		return c.sendRefund(ctx, org, e, now)
	}
	if err != nil {
		return err
	}
	return c.applyRefund(ctx, e, r.Status, now)
}
