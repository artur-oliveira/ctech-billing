package services

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"errors"
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// ---- fakes --------------------------------------------------------------------

type fakeBooks struct {
	recorded map[string]repositories.InvoiceFact // space PK -> fact
	credits  map[string]billing.Cents            // space PK -> credited
	calls    int
	failPK   string // writes fail in this space
	noRecvPK string // this space has no receiving account
	overPK   string // a credit in this space exceeds what is left on the bill
	panics   bool
}

func newFakeBooks() *fakeBooks {
	return &fakeBooks{recorded: map[string]repositories.InvoiceFact{}, credits: map[string]billing.Cents{}}
}

func (f *fakeBooks) RecordInvoice(_ context.Context, sp space.ResolvedSpace, fact repositories.InvoiceFact, _ repositories.PostMeta, _ time.Time) (finance.Bill, bool, error) {
	f.calls++
	if f.panics {
		panic("boom")
	}
	switch sp.PK() {
	case f.failPK:
		return finance.Bill{}, false, errors.New("dynamodb: boom")
	case f.noRecvPK:
		return finance.Bill{}, false, repositories.ErrNoReceivingAccount
	}
	if _, ok := f.recorded[sp.PK()]; ok {
		return finance.Bill{}, false, nil
	}
	f.recorded[sp.PK()] = fact
	return finance.Bill{ID: "b"}, true, nil
}

func (f *fakeBooks) RecordInvoiceCredit(_ context.Context, sp space.ResolvedSpace, _, _ string, amount billing.Cents, _ brcal.Date, _ repositories.PostMeta, _ time.Time) (bool, error) {
	f.calls++
	if _, ok := f.recorded[sp.PK()]; !ok {
		return false, repositories.ErrNotFound
	}
	if sp.PK() == f.overPK {
		return false, finance.ErrInvalidTransaction
	}
	f.credits[sp.PK()] += amount
	return true, nil
}

// fakeQueue is the finance replay queue: what is still pending, and what was
// taken off it.
type fakeQueue struct {
	postings []billing.Invoice
	credits  []billing.CreditNote
	invoices map[string]*billing.Invoice
	posted   map[string]bool // invoice id -> taken off
	credited map[string]bool // credit note id -> taken off
}

func newFakeQueue() *fakeQueue {
	return &fakeQueue{invoices: map[string]*billing.Invoice{}, posted: map[string]bool{}, credited: map[string]bool{}}
}

func (q *fakeQueue) PendingFinancePostings(context.Context, bool, int, map[string]types.AttributeValue) (*repositories.Page[billing.Invoice], error) {
	return &repositories.Page[billing.Invoice]{Items: q.postings}, nil
}

func (q *fakeQueue) FinancePosted(_ context.Context, inv *billing.Invoice, _ time.Time) error {
	q.posted[inv.ID] = true
	return nil
}

func (q *fakeQueue) Get(_ context.Context, _ string, _ bool, id string) (*billing.Invoice, error) {
	if inv, ok := q.invoices[id]; ok {
		return inv, nil
	}
	return nil, repositories.ErrNotFound
}

func (q *fakeQueue) PendingFinanceCredits(context.Context, bool, int, map[string]types.AttributeValue) (*repositories.Page[billing.CreditNote], error) {
	return &repositories.Page[billing.CreditNote]{Items: q.credits}, nil
}

func (q *fakeQueue) FinanceCredited(_ context.Context, cn *billing.CreditNote, _ time.Time) error {
	q.credited[cn.ID] = true
	return nil
}

type fakeOrgReader map[string]*billing.Organization

func (f fakeOrgReader) Get(_ context.Context, id string, _ bool) (*billing.Organization, error) {
	if o, ok := f[id]; ok {
		return o, nil
	}
	return nil, repositories.ErrNotFound
}

type fakeCustomerReader map[string]*billing.Customer

func (f fakeCustomerReader) Get(_ context.Context, _ string, _ bool, id string) (*billing.Customer, error) {
	if c, ok := f[id]; ok {
		return c, nil
	}
	return nil, repositories.ErrNotFound
}

// ---- fixture ------------------------------------------------------------------

const (
	linkedOrg = "0190a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"
	issuerPK  = linkedOrg + "#live"
	payerPK   = "USER#sub-1#live"
)

type invoiceFixture struct {
	books     *fakeBooks
	orgs      fakeOrgReader
	customers fakeCustomerReader
	queue     *fakeQueue
	rule      *FinanceInvoices
	inv       *billing.Invoice
}

func newInvoiceFixture() *invoiceFixture {
	f := &invoiceFixture{
		books:     newFakeBooks(),
		orgs:      fakeOrgReader{"ctech": {ID: "ctech", DisplayName: "CTech", Livemode: true, AccountOrganizationID: linkedOrg}},
		customers: fakeCustomerReader{"cus_1": {ID: "cus_1", OrganizationID: "ctech", Livemode: true, UserID: "sub-1"}},
		queue:     newFakeQueue(),
	}
	f.rule = NewFinanceInvoices(f.books, f.orgs, f.customers, f.queue, f.queue, "ctech")
	f.inv = &billing.Invoice{
		ID: "in_1", OrganizationID: "ctech", Livemode: true, CustomerID: "cus_1", Status: billing.InvoicePaid,
		Number: 12, Period: billing.Period{Start: brcal.New(2026, time.February, 1), End: brcal.New(2026, time.March, 1)},
		DueDate: brcal.New(2026, time.March, 10), Total: 4990, AmountPaid: 4990,
		PaidAt: "2026-03-05T14:00:00Z",
	}
	return f
}

var at = time.Date(2026, time.March, 5, 15, 0, 0, 0, time.UTC)

func results(ps []Posting) map[PostingSide]Posting {
	out := map[PostingSide]Posting{}
	for _, p := range ps {
		out[p.Side] = p
	}
	return out
}

func wantResult(t *testing.T, ps []Posting, side PostingSide, result PostingResult, reason string) {
	t.Helper()
	p, ok := results(ps)[side]
	if !ok || p.Result != result || p.Reason != reason {
		t.Errorf("%s = %+v (ok=%v), want %s/%s", side, p, ok, result, reason)
	}
}

// ---- tests --------------------------------------------------------------------

func TestAPaidInvoicePostsRevenueToTheIssuerAndAnExpenseToThePayer(t *testing.T) {
	f := newInvoiceFixture()
	out := f.rule.Paid(context.Background(), f.inv, "service:ctech-wallet", "req", at)
	wantResult(t, out, SideIssuer, PostingPosted, "")
	wantResult(t, out, SidePayer, PostingPosted, "")

	issuer, payer := f.books.recorded[issuerPK], f.books.recorded[payerPK]
	if issuer.Direction != finance.Receivable || issuer.Amount != 4990 || issuer.InvoiceID != "in_1" ||
		issuer.Competence != brcal.New(2026, time.February, 1) || issuer.Paid != brcal.New(2026, time.March, 5) ||
		issuer.Due != brcal.New(2026, time.March, 10) || issuer.Description != "Fatura #12" {
		t.Errorf("issuer fact = %+v", issuer)
	}
	if payer.Direction != finance.Payable || payer.Amount != 4990 || payer.Paid != issuer.Paid ||
		payer.Competence != issuer.Competence || payer.Description != "CTech · Fatura #12" {
		t.Errorf("payer fact = %+v", payer)
	}
}

func TestAReplayPostsNothingNew(t *testing.T) {
	f := newInvoiceFixture()
	f.rule.Paid(context.Background(), f.inv, "a", "r", at)
	out := f.rule.Paid(context.Background(), f.inv, "service:billing-reconciler", "r2", at)
	wantResult(t, out, SideIssuer, PostingAlreadyPosted, "")
	wantResult(t, out, SidePayer, PostingAlreadyPosted, "")
}

// ADR 0019: settled with nothing paid.
func TestAZeroTotalInvoicePostsNothing(t *testing.T) {
	f := newInvoiceFixture()
	f.inv.Total, f.inv.AmountPaid = 0, 0
	out := f.rule.Paid(context.Background(), f.inv, "a", "r", at)
	wantResult(t, out, SideIssuer, PostingSkipped, "nothing_paid")
	wantResult(t, out, SidePayer, PostingSkipped, "nothing_paid")
	if f.books.calls != 0 {
		t.Fatalf("%d writes for a zero-total invoice", f.books.calls)
	}
}

func TestAnUnpaidInvoicePostsNothing(t *testing.T) {
	f := newInvoiceFixture()
	f.inv.Status = billing.InvoiceOpen
	if out := f.rule.Paid(context.Background(), f.inv, "a", "r", at); out != nil || f.books.calls != 0 {
		t.Fatalf("out = %+v, calls = %d", out, f.books.calls)
	}
}

func TestAnUnlinkedIssuerIsSkipped(t *testing.T) {
	f := newInvoiceFixture()
	f.orgs["ctech"].AccountOrganizationID = ""
	out := f.rule.Paid(context.Background(), f.inv, "a", "r", at)
	wantResult(t, out, SideIssuer, PostingSkipped, "issuer_not_linked")
	wantResult(t, out, SidePayer, PostingPosted, "")
}

// Scope decision 2: a third-party merchant's customer.user_id never writes into
// a person's own ledger.
func TestOnlyTenantZeroPostsToThePayer(t *testing.T) {
	f := newInvoiceFixture()
	f.orgs["merchant"] = &billing.Organization{ID: "merchant", Livemode: true, AccountOrganizationID: linkedOrg}
	f.inv.OrganizationID = "merchant"
	out := f.rule.Paid(context.Background(), f.inv, "a", "r", at)
	wantResult(t, out, SideIssuer, PostingPosted, "")
	wantResult(t, out, SidePayer, PostingSkipped, "not_tenant_zero")

	g := newInvoiceFixture()
	g.rule = NewFinanceInvoices(g.books, g.orgs, g.customers, g.queue, g.queue, "") // no tenant zero configured
	wantResult(t, g.rule.Paid(context.Background(), g.inv, "a", "r", at), SidePayer, PostingSkipped, "not_tenant_zero")
}

func TestAPayerWithNoAccountOrAnonymizedIsSkipped(t *testing.T) {
	f := newInvoiceFixture()
	f.customers["cus_1"].UserID = ""
	wantResult(t, f.rule.Paid(context.Background(), f.inv, "a", "r", at), SidePayer, PostingSkipped, "payer_has_no_account")

	g := newInvoiceFixture()
	g.customers["cus_1"].Anonymized = true
	wantResult(t, g.rule.Paid(context.Background(), g.inv, "a", "r", at), SidePayer, PostingSkipped, "payer_anonymized")
}

func TestNoReceivingAccountIsASkipNotAFailure(t *testing.T) {
	f := newInvoiceFixture()
	f.books.noRecvPK = payerPK
	out := f.rule.Paid(context.Background(), f.inv, "a", "r", at)
	wantResult(t, out, SideIssuer, PostingPosted, "")
	wantResult(t, out, SidePayer, PostingSkipped, "no_receiving_account")
}

// Review Focus 5.
func TestOneSideFailingDoesNotStopTheOther(t *testing.T) {
	f := newInvoiceFixture()
	f.books.failPK = issuerPK
	out := f.rule.Paid(context.Background(), f.inv, "a", "r", at)
	if p := results(out)[SideIssuer]; p.Result != PostingFailed || p.Err == nil {
		t.Errorf("issuer = %+v", p)
	}
	wantResult(t, out, SidePayer, PostingPosted, "")

	g := newInvoiceFixture()
	delete(g.customers, "cus_1")
	out = g.rule.Paid(context.Background(), g.inv, "a", "r", at)
	wantResult(t, out, SideIssuer, PostingPosted, "")
	if p := results(out)[SidePayer]; p.Result != PostingFailed {
		t.Errorf("an unreadable customer: payer = %+v", p)
	}
}

// Review Focus 5.
func TestAPanicIsContained(t *testing.T) {
	f := newInvoiceFixture()
	f.books.panics = true
	out := f.rule.Paid(context.Background(), f.inv, "a", "r", at)
	if len(out) == 0 || out[0].Result != PostingFailed || !strings.Contains(out[0].Err.Error(), "panic") {
		t.Fatalf("out = %+v", out)
	}
}

func TestATestModeInvoicePostsToTestSpaces(t *testing.T) {
	f := newInvoiceFixture()
	f.inv.Livemode = false
	f.rule.Paid(context.Background(), f.inv, "a", "r", at)
	for pk := range f.books.recorded {
		if !strings.HasSuffix(pk, "#test") {
			t.Errorf("a test invoice posted to %s", pk)
		}
	}
	if len(f.books.recorded) != 2 {
		t.Fatalf("recorded = %v", f.books.recorded)
	}
}

func TestACreditNoteOnAPaidInvoicePostsInBothSpaces(t *testing.T) {
	f := newInvoiceFixture()
	f.rule.Paid(context.Background(), f.inv, "a", "r", at)
	cn := &billing.CreditNote{ID: "cn_1", InvoiceID: "in_1", Amount: 1990, CreatedAt: at}
	out := f.rule.Credited(context.Background(), f.inv, cn, "user:op", "r", at)
	wantResult(t, out, SideIssuer, PostingPosted, "")
	wantResult(t, out, SidePayer, PostingPosted, "")
	if f.books.credits[issuerPK] != 1990 || f.books.credits[payerPK] != 1990 {
		t.Fatalf("credits = %v", f.books.credits)
	}

	open := newInvoiceFixture()
	open.inv.Status = billing.InvoiceOpen
	if out := open.rule.Credited(context.Background(), open.inv, cn, "u", "r", at); out != nil {
		t.Errorf("a credit on an open invoice posted: %+v", out)
	}

	never := newInvoiceFixture() // paid, but never recorded (no receiving account then)
	out = never.rule.Credited(context.Background(), never.inv, cn, "u", "r", at)
	wantResult(t, out, SideIssuer, PostingSkipped, "invoice_not_recorded")
	wantResult(t, out, SidePayer, PostingSkipped, "invoice_not_recorded")
}

// ---- the replay queue (6.7 review, I1) ------------------------------------------

func TestACompletePostingLeavesTheQueue(t *testing.T) {
	f := newInvoiceFixture()
	f.rule.Paid(context.Background(), f.inv, "a", "r", at)
	if !f.queue.posted["in_1"] {
		t.Fatal("both spaces recorded, but the invoice stays queued")
	}
	g := newInvoiceFixture()
	g.orgs["merchant"] = &billing.Organization{ID: "merchant", Livemode: true, AccountOrganizationID: linkedOrg}
	g.inv.OrganizationID = "merchant" // payer side skipped for good: not tenant zero
	g.rule.Paid(context.Background(), g.inv, "a", "r", at)
	if !g.queue.posted["in_1"] {
		t.Fatal("a side skipped for good must not keep the invoice queued")
	}
}

func TestASideThatCanStillBeWrittenKeepsItQueued(t *testing.T) {
	for name, mutate := range map[string]func(*invoiceFixture){
		"a failed write":           func(f *invoiceFixture) { f.books.failPK = issuerPK },
		"no receiving account":     func(f *invoiceFixture) { f.books.noRecvPK = payerPK },
		"an issuer not linked yet": func(f *invoiceFixture) { f.orgs["ctech"].AccountOrganizationID = "" },
		"a panic":                  func(f *invoiceFixture) { f.books.panics = true },
	} {
		f := newInvoiceFixture()
		mutate(f)
		f.rule.Paid(context.Background(), f.inv, "a", "r", at)
		if f.queue.posted["in_1"] {
			t.Errorf("%s: taken off the queue", name)
		}
	}
}

func TestReplayPostsWhatIsPendingAndGivesUpOutsideTheWindow(t *testing.T) {
	f := newInvoiceFixture()
	old := *f.inv
	old.ID, old.PaidAt = "in_old", at.Add(-FinanceReplayWindow-time.Hour).Format(time.RFC3339)
	f.queue.postings = []billing.Invoice{old, *f.inv}
	res := f.rule.Replay(context.Background(), true, at)
	if res.GivenUp != 1 || res.Done != 1 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v", res)
	}
	if !f.queue.posted["in_old"] || !f.queue.posted["in_1"] {
		t.Fatalf("taken off = %v", f.queue.posted)
	}
	if _, ok := f.books.recorded[issuerPK]; !ok {
		t.Fatal("the pending invoice was not recorded")
	}
	if f.books.calls != 2 {
		t.Fatalf("%d writes: the invoice outside the window must not be posted", f.books.calls)
	}
}

func TestReplayCreditsAfterTheInvoiceAndKeepsWhatCannotLandYet(t *testing.T) {
	f := newInvoiceFixture()
	f.queue.invoices["in_1"] = f.inv
	cn := billing.CreditNote{ID: "cn_1", OrganizationID: "ctech", Livemode: true, InvoiceID: "in_1", Amount: 1990, CreatedAt: at}
	f.queue.credits = []billing.CreditNote{cn}
	f.rule.Replay(context.Background(), true, at) // nothing recorded yet: invoice_not_recorded
	if f.queue.credited["cn_1"] {
		t.Fatal("a credit whose invoice is not recorded yet left the queue")
	}
	f.queue.postings = []billing.Invoice{*f.inv}
	res := f.rule.Replay(context.Background(), true, at)
	if !f.queue.credited["cn_1"] || f.books.credits[issuerPK] != 1990 || len(res.Errors) != 0 {
		t.Fatalf("credited = %v, credits = %v, errors = %v", f.queue.credited, f.books.credits, res.Errors)
	}
}

func TestACreditBeyondTheBillIsSettledNotRetried(t *testing.T) {
	f := newInvoiceFixture()
	f.books.overPK = issuerPK
	f.rule.Paid(context.Background(), f.inv, "a", "r", at)
	cn := &billing.CreditNote{ID: "cn_1", OrganizationID: "ctech", Livemode: true, InvoiceID: "in_1", Amount: 1990, CreatedAt: at}
	out := f.rule.Credited(context.Background(), f.inv, cn, "u", "r", at)
	wantResult(t, out, SideIssuer, PostingSkipped, "credit_exceeds_bill")
	if !f.queue.credited["cn_1"] {
		t.Fatal("a credit the bill can never take stays queued forever")
	}
}
