package services

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

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
	PostsCTechInvoices(ctx context.Context, sp space.ResolvedSpace) (bool, error)
}

type organizationReader interface {
	Get(ctx context.Context, organizationID string, livemode bool) (*billing.Organization, error)
}

type customerReader interface {
	Get(ctx context.Context, organizationID string, livemode bool, customerID string) (*billing.Customer, error)
}

// invoiceQueue is the finance replay queue of paid invoices (InvoiceRepository).
type invoiceQueue interface {
	PendingFinancePostings(ctx context.Context, livemode bool, limit int, startKey map[string]types.AttributeValue) (*repositories.Page[billing.Invoice], error)
	FinancePosted(ctx context.Context, inv *billing.Invoice, now time.Time) error
	Get(ctx context.Context, organizationID string, livemode bool, invoiceID string) (*billing.Invoice, error)
}

// creditQueue is the same for credit notes (CreditNoteRepository).
type creditQueue interface {
	PendingFinanceCredits(ctx context.Context, livemode bool, limit int, startKey map[string]types.AttributeValue) (*repositories.Page[billing.CreditNote], error)
	FinanceCredited(ctx context.Context, cn *billing.CreditNote, now time.Time) error
}

// FinanceReplayWindow bounds the replay: a posting still not written this long
// after the payment (or the credit) is given up, so a space that never sets a
// receiving account is not retried forever. Inside it, a person who sets one up
// later still gets the bill.
const FinanceReplayWindow = 30 * 24 * time.Hour

// actorFinanceReplay names the replay pass in the ledger's audit rows.
const actorFinanceReplay = "service:billing-finance-replay"

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
//
// What it could not settle stays on a durable queue: a paid invoice (and a
// credit note on one) is queued in the same write that makes it so, and leaves
// the queue only when every side is settled one way or the other — recorded, or
// skipped for a reason no later attempt can change. Replay, run by
// cmd/reconcile, retries the rest inside FinanceReplayWindow.
type FinanceInvoices struct {
	books      invoiceBooks
	orgs       organizationReader
	customers  customerReader
	invoices   invoiceQueue
	credits    creditQueue
	tenantZero string
}

// NewFinanceInvoices wires the rule. tenantZero is PORTAL_ORGANIZATION_ID: the
// only tenant whose customers' own spaces are written to (6.7 plan, scope
// decision 2).
func NewFinanceInvoices(books invoiceBooks, orgs organizationReader, customers customerReader, invoices invoiceQueue, credits creditQueue, tenantZero string) *FinanceInvoices {
	return &FinanceInvoices{books: books, orgs: orgs, customers: customers, invoices: invoices, credits: credits, tenantZero: tenantZero}
}

// retryable are the skips a later attempt can turn into a posting: the space
// may set a receiving account, the tenant may be linked, the invoice a credit
// needs may get recorded. Everything else that is skipped is skipped for good.
var retryable = map[string]bool{
	"no_receiving_account": true,
	"issuer_not_linked":    true,
	"invoice_not_recorded": true,
}

// settled reports whether no side of out is worth another attempt.
func settled(out []Posting) bool {
	for _, p := range out {
		if p.Result == PostingFailed || (p.Result == PostingSkipped && retryable[p.Reason]) {
			return false
		}
	}
	return len(out) > 0
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
		_ = s.invoices.FinancePosted(ctx, inv, now) // never queued; a no-op
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
	if settled(out) {
		if err := s.invoices.FinancePosted(ctx, inv, now); err != nil {
			// Harmless: the replay finds it, and every side reads as done.
			slog.WarnContext(ctx, "invoice recorded in finance but still queued", "invoice_id", inv.ID, "error", err)
		}
	}
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
		case errors.Is(err, finance.ErrInvalidTransaction):
			// What is left on the bill no longer covers this credit; no later
			// attempt changes that.
			out = append(out, Posting{Side: t.side, Result: PostingSkipped, Reason: "credit_exceeds_bill"})
		default:
			out = append(out, outcome(t.side, posted, err))
		}
	}
	s.log(ctx, inv, "credited", out)
	if settled(out) {
		if err := s.credits.FinanceCredited(ctx, cn, now); err != nil {
			slog.WarnContext(ctx, "credit note recorded in finance but still queued", "credit_note_id", cn.ID, "error", err)
		}
	}
	return out
}

// ReplayResult is one replay pass's tally.
type ReplayResult struct {
	Examined int
	Done     int // every side settled; off the queue
	Pending  int // a side can still be written; stays queued
	GivenUp  int // outside FinanceReplayWindow; off the queue, logged
	Errors   []error
}

// Replay retries the finance postings that are still queued (6.7 review, I1):
// paid invoices first, then credit notes, so a credit finds the bill its
// invoice just got. Each is idempotent — the bill's id and the credit's
// transaction id — so a side already recorded costs one read. Anything older
// than FinanceReplayWindow is taken off the queue with a warning instead.
//
// It is a retry, not a backfill: it sees only invoices and notes queued by this
// code, i.e. paid or issued after it shipped, and only for FinanceReplayWindow.
//
// Cross-tenant by design (ADR 0002): reachable only from cmd/reconcile.
func (s *FinanceInvoices) Replay(ctx context.Context, livemode bool, now time.Time) ReplayResult {
	var res ReplayResult
	cutoff := now.Add(-FinanceReplayWindow)
	tally := func(out []Posting) {
		if settled(out) {
			res.Done++
			return
		}
		res.Pending++
		for _, p := range out {
			if p.Result == PostingFailed {
				res.Errors = append(res.Errors, fmt.Errorf("%s side: %w", p.Side, p.Err))
			}
		}
	}

	var start map[string]types.AttributeValue
	for {
		page, err := s.invoices.PendingFinancePostings(ctx, livemode, 100, start)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("reading pending finance postings: %w", err))
			return res
		}
		for i := range page.Items {
			inv := &page.Items[i]
			res.Examined++
			if paid, err := time.Parse(time.RFC3339, inv.PaidAt); err == nil && paid.Before(cutoff) {
				s.giveUp(ctx, &res, "invoice", inv.ID, s.invoices.FinancePosted(ctx, inv, now))
				continue
			}
			tally(s.Paid(ctx, inv, actorFinanceReplay, "", now))
		}
		if page.LastEvaluatedKey == nil {
			break
		}
		start = page.LastEvaluatedKey
	}

	start = nil
	for {
		page, err := s.credits.PendingFinanceCredits(ctx, livemode, 100, start)
		if err != nil {
			res.Errors = append(res.Errors, fmt.Errorf("reading pending finance credits: %w", err))
			return res
		}
		for i := range page.Items {
			cn := &page.Items[i]
			res.Examined++
			if cn.CreatedAt.Before(cutoff) {
				s.giveUp(ctx, &res, "credit note", cn.ID, s.credits.FinanceCredited(ctx, cn, now))
				continue
			}
			inv, err := s.invoices.Get(ctx, cn.OrganizationID, cn.Livemode, cn.InvoiceID)
			if err != nil {
				res.Pending++
				res.Errors = append(res.Errors, fmt.Errorf("credit note %s: reading invoice %s: %w", cn.ID, cn.InvoiceID, err))
				continue
			}
			tally(s.Credited(ctx, inv, cn, actorFinanceReplay, "", now))
		}
		if page.LastEvaluatedKey == nil {
			return res
		}
		start = page.LastEvaluatedKey
	}
}

func (s *FinanceInvoices) giveUp(ctx context.Context, res *ReplayResult, kind, id string, err error) {
	if err != nil {
		res.Errors = append(res.Errors, fmt.Errorf("%s %s: leaving the finance queue: %w", kind, id, err))
		return
	}
	res.GivenUp++
	slog.WarnContext(ctx, "finance posting given up: outside the replay window",
		"kind", kind, "id", id, "window_days", int(FinanceReplayWindow.Hours()/24))
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
	// "Lançar minhas faturas da CTech automaticamente neste espaço", read at
	// posting time, so the replay respects what the person decided since.
	post, err := s.books.PostsCTechInvoices(ctx, sp)
	if err != nil {
		return failed(SidePayer, err)
	}
	if !post {
		return skipped(SidePayer, "payer_opted_out")
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
			slog.WarnContext(ctx, "invoice not recorded in finance: no receiving account chosen and not exactly one bank or cash account", attrs...)
		case p.Result == PostingSkipped:
			slog.InfoContext(ctx, "invoice not recorded in finance", append(attrs, "reason", p.Reason)...)
		default:
			slog.InfoContext(ctx, "invoice recorded in finance", attrs...)
		}
	}
}
