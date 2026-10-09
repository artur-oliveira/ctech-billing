package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// invoiceBooks is the slice of BillRepository the posting rule writes through.
type invoiceBooks interface {
	RecordInvoice(ctx context.Context, sp space.ResolvedSpace, f repositories.InvoiceFact, meta repositories.PostMeta, now time.Time) (finance.Bill, bool, error)
	RecordInvoiceCredit(ctx context.Context, sp space.ResolvedSpace, invoiceID, creditNoteID string, amount billing.Cents, date brcal.Date, meta repositories.PostMeta, now time.Time) (bool, error)
}

type organizationReader interface {
	Get(ctx context.Context, organizationID string, livemode bool) (*billing.Organization, error)
}

type customerReader interface {
	Get(ctx context.Context, organizationID string, livemode bool, customerID string) (*billing.Customer, error)
}

// PostingSide names the space a posting is for.
type PostingSide string

const (
	SideIssuer PostingSide = "issuer" // the organization that issued the invoice: revenue
	SidePayer  PostingSide = "payer"  // the person who paid it: an expense
)

// PostingResult is what happened in one space.
type PostingResult string

const (
	PostingPosted        PostingResult = "posted"
	PostingAlreadyPosted PostingResult = "already_posted"
	PostingSkipped       PostingResult = "skipped"
	PostingFailed        PostingResult = "failed"
)

// Posting is the outcome in one space. Reason says why a posting was skipped;
// Err is the failure.
type Posting struct {
	Side   PostingSide
	Result PostingResult
	Reason string
	Err    error
}

// FinanceInvoices is the posting rule for billing's own invoices (spec § 3.8):
// a paid invoice becomes revenue in the issuing organization's space and an
// expense in the paying person's, and a credit note against it takes its amount
// back from both. In-process, never over HTTP.
//
// It never returns an error and never panics into its caller: the settlement
// that calls it has already happened — the money arrived — and a ledger that
// could not be written is logged per side, never a reason to refuse the payment
// (the activateSubscription rule). Each side is independent: one failing does
// not stop the other.
type FinanceInvoices struct {
	books      invoiceBooks
	orgs       organizationReader
	customers  customerReader
	tenantZero string
}

// NewFinanceInvoices wires the rule. tenantZero is PORTAL_ORGANIZATION_ID: the
// only tenant whose customers' own spaces are written to (6.7 plan, scope
// decision 2).
func NewFinanceInvoices(books invoiceBooks, orgs organizationReader, customers customerReader, tenantZero string) *FinanceInvoices {
	return &FinanceInvoices{books: books, orgs: orgs, customers: customers, tenantZero: tenantZero}
}

// target is one side's space, or the posting that says why there is none.
type target struct {
	side PostingSide
	sp   space.ResolvedSpace
	skip *Posting
}

// Paid records a PAID invoice in both spaces. It is called on every settlement,
// the repeats included; the bill's id makes a repeat a read.
func (s *FinanceInvoices) Paid(ctx context.Context, inv *billing.Invoice, actor, requestID string, now time.Time) (out []Posting) {
	if inv == nil || inv.Status != billing.InvoicePaid {
		return nil
	}
	defer s.contain(ctx, inv.ID, "paid", &out)
	if inv.AmountPaid <= 0 {
		// ADR 0019: a zero-total invoice is settled with nothing paid, and a bill
		// of zero is not a bill.
		out = []Posting{
			{Side: SideIssuer, Result: PostingSkipped, Reason: "nothing_paid"},
			{Side: SidePayer, Result: PostingSkipped, Reason: "nothing_paid"},
		}
		s.log(ctx, inv, "paid", out)
		return out
	}
	org, orgErr := s.orgs.Get(ctx, inv.OrganizationID, inv.Livemode)
	paid := paidDay(inv, now)
	charge := invoiceChargeDescription(inv)
	for _, t := range s.targets(ctx, inv, org, orgErr) {
		if t.skip != nil {
			out = append(out, *t.skip)
			continue
		}
		fact := repositories.InvoiceFact{
			InvoiceID: inv.ID, Direction: finance.Receivable, Amount: inv.AmountPaid,
			Competence: inv.Period.Start, Due: inv.DueDate, Paid: paid, Description: charge,
		}
		if t.side == SidePayer {
			fact.Direction = finance.Payable
			if org != nil && org.DisplayName != "" {
				fact.Description = org.DisplayName + " · " + charge
			}
		}
		_, created, err := s.books.RecordInvoice(ctx, t.sp, fact, repositories.PostMeta{Actor: actor, RequestID: requestID}, now)
		out = append(out, outcome(t.side, created, err))
	}
	s.log(ctx, inv, "paid", out)
	return out
}

// Credited records a credit note issued against a PAID invoice in both spaces,
// for the note's amount, dated the note's day. A space where the invoice was
// never recorded (it had no receiving account when it was paid) is skipped.
func (s *FinanceInvoices) Credited(ctx context.Context, inv *billing.Invoice, cn *billing.CreditNote, actor, requestID string, now time.Time) (out []Posting) {
	if inv == nil || cn == nil || inv.Status != billing.InvoicePaid || cn.Amount <= 0 {
		return nil
	}
	defer s.contain(ctx, inv.ID, "credited", &out)
	day := brcal.FromTime(now)
	if !cn.CreatedAt.IsZero() {
		day = brcal.FromTime(cn.CreatedAt)
	}
	org, orgErr := s.orgs.Get(ctx, inv.OrganizationID, inv.Livemode)
	for _, t := range s.targets(ctx, inv, org, orgErr) {
		if t.skip != nil {
			out = append(out, *t.skip)
			continue
		}
		posted, err := s.books.RecordInvoiceCredit(ctx, t.sp, inv.ID, cn.ID, cn.Amount, day, repositories.PostMeta{Actor: actor, RequestID: requestID}, now)
		switch {
		case errors.Is(err, repositories.ErrNotFound):
			out = append(out, Posting{Side: t.side, Result: PostingSkipped, Reason: "invoice_not_recorded"})
		case errors.Is(err, finance.ErrBillState):
			out = append(out, Posting{Side: t.side, Result: PostingSkipped, Reason: "bill_not_paid"})
		default:
			out = append(out, outcome(t.side, posted, err))
		}
	}
	s.log(ctx, inv, "credited", out)
	return out
}

// targets resolves the two spaces from stored data only: the issuer through the
// link its tenant plan provisioned, the payer through the customer's user id,
// in tenant zero only.
func (s *FinanceInvoices) targets(ctx context.Context, inv *billing.Invoice, org *billing.Organization, orgErr error) []target {
	var issuer target
	switch {
	case orgErr != nil:
		issuer = failed(SideIssuer, orgErr)
	case org.AccountOrganizationID == "":
		issuer = skipped(SideIssuer, "issuer_not_linked")
	default:
		sp, err := space.ForInvoiceIssuer(org.AccountOrganizationID, inv.Livemode)
		if err != nil {
			issuer = failed(SideIssuer, fmt.Errorf("organization %s has an unusable link: %w", org.ID, err))
		} else {
			issuer = target{side: SideIssuer, sp: sp}
		}
	}
	return []target{issuer, s.payer(ctx, inv)}
}

func (s *FinanceInvoices) payer(ctx context.Context, inv *billing.Invoice) target {
	if s.tenantZero == "" || inv.OrganizationID != s.tenantZero {
		// A third-party merchant's customer.user_id is that merchant's claim; it
		// never writes into a person's own ledger.
		return skipped(SidePayer, "not_tenant_zero")
	}
	c, err := s.customers.Get(ctx, inv.OrganizationID, inv.Livemode, inv.CustomerID)
	if err != nil {
		return failed(SidePayer, err)
	}
	switch {
	case c.Anonymized:
		return skipped(SidePayer, "payer_anonymized")
	case c.UserID == "":
		// Organization customers (spec § 1) are not modelled yet: nothing on a
		// customer names an organization space.
		return skipped(SidePayer, "payer_has_no_account")
	}
	sp, err := space.ForInvoicePayer(c.UserID, inv.Livemode)
	if err != nil {
		return skipped(SidePayer, "payer_has_no_account")
	}
	return target{side: SidePayer, sp: sp}
}

func skipped(side PostingSide, reason string) target {
	return target{side: side, skip: &Posting{Side: side, Result: PostingSkipped, Reason: reason}}
}

func failed(side PostingSide, err error) target {
	return target{side: side, skip: &Posting{Side: side, Result: PostingFailed, Err: err}}
}

func outcome(side PostingSide, created bool, err error) Posting {
	switch {
	case errors.Is(err, repositories.ErrNoReceivingAccount):
		return Posting{Side: side, Result: PostingSkipped, Reason: "no_receiving_account"}
	case err != nil:
		return Posting{Side: side, Result: PostingFailed, Err: err}
	case created:
		return Posting{Side: side, Result: PostingPosted}
	default:
		return Posting{Side: side, Result: PostingAlreadyPosted}
	}
}

// paidDay is paid_at as a São Paulo civil day. The repository writes paid_at
// once, on the first arrival, so every replay posts the same day.
func paidDay(inv *billing.Invoice, now time.Time) brcal.Date {
	if t, err := time.Parse(time.RFC3339, inv.PaidAt); err == nil {
		return brcal.FromTime(t)
	}
	return brcal.FromTime(now)
}

// contain turns a panic into a failed posting, so the settlement that called the
// rule is never the thing that breaks.
func (s *FinanceInvoices) contain(ctx context.Context, invoiceID, fact string, out *[]Posting) {
	if r := recover(); r != nil {
		slog.ErrorContext(ctx, "finance posting panicked", "invoice_id", invoiceID, "fact", fact, "panic", r)
		*out = []Posting{{Side: SideIssuer, Result: PostingFailed, Err: fmt.Errorf("panic: %v", r)}}
	}
}

func (s *FinanceInvoices) log(ctx context.Context, inv *billing.Invoice, fact string, out []Posting) {
	for _, p := range out {
		attrs := []any{"invoice_id", inv.ID, "livemode", inv.Livemode, "fact", fact, "side", p.Side, "result", p.Result}
		switch {
		case p.Result == PostingFailed:
			slog.ErrorContext(ctx, "invoice not recorded in finance", append(attrs, "error", p.Err)...)
		case p.Reason == "no_receiving_account":
			slog.WarnContext(ctx, "invoice not recorded in finance: the space has no default receiving account", attrs...)
		case p.Result == PostingSkipped:
			slog.InfoContext(ctx, "invoice not recorded in finance", append(attrs, "reason", p.Reason)...)
		default:
			slog.InfoContext(ctx, "invoice recorded in finance", attrs...)
		}
	}
}
