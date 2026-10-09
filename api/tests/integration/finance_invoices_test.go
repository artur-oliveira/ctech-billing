//go:build integration

package integration

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/provision"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// Spec § 3.8 and the 6.7 plan: a paid billing invoice is revenue in the issuing
// organization's space and an expense in the paying person's own space.

func provisionRepos() provision.Repos {
	return provision.Repos{
		Organizations: repositories.NewOrganizationRepository(testDB, testCfg),
		Credentials:   repositories.NewCredentialRepository(testDB, testCfg),
		Catalog:       repositories.NewCatalogRepository(testDB, testCfg),
		Webhooks:      repositories.NewWebhookRepository(testDB, testCfg),
	}
}

// An existing tenant (tenant zero is one in every environment) gains its link
// from the plan once; the same plan again changes nothing; a plan that names a
// different organization is refused and the stored link stays.
func TestProvisioningLinksAnExistingTenantOnce(t *testing.T) {
	ctx := ctxT(t)
	repos := provisionRepos()
	plan := &provision.Plan{Organization: provision.Organization{ID: "org_" + id.New(), DisplayName: "CTech"}}
	if _, err := provision.Apply(ctx, repos, plan, true, now()); err != nil {
		t.Fatal(err)
	}

	link := newSpaceOrgID()
	plan.Organization.AccountOrganizationID = link
	res, err := provision.Apply(ctx, repos, plan, true, now())
	if err != nil {
		t.Fatal(err)
	}
	if !contains(res.Created, "account organization link "+plan.Organization.ID) {
		t.Fatalf("created = %v", res.Created)
	}
	res, err = provision.Apply(ctx, repos, plan, true, now())
	if err != nil || !contains(res.Skipped, "account organization link "+plan.Organization.ID) {
		t.Fatalf("second apply: %v, skipped = %v", err, res.Skipped)
	}

	plan.Organization.AccountOrganizationID = newSpaceOrgID()
	if _, err := provision.Apply(ctx, repos, plan, true, now()); !errors.Is(err, repositories.ErrAccountOrganizationLinked) {
		t.Fatalf("a diverging link: err = %v", err)
	}
	org, err := repos.Organizations.Get(ctx, plan.Organization.ID, true)
	if err != nil || org.AccountOrganizationID != link {
		t.Fatalf("stored link = %q, %v; want %q", org.AccountOrganizationID, err, link)
	}
}

// invoiceSpace is a space with a bank account chosen as the default receiving
// account (F8), created the way a person would: opening the space seeds it.
func invoiceSpace(t *testing.T, owner string, live bool) space.ResolvedSpace {
	t.Helper()
	sp := jobSpace(t, owner, live)
	ledger := repositories.NewLedgerRepository(testDB, testCfg)
	ctx, at := context.Background(), time.Now()
	if err := ledger.EnsureReady(ctx, sp, at); err != nil {
		t.Fatal(err)
	}
	if err := ledger.CreateAccount(ctx, sp, finance.LedgerAccount{ID: "bank", Name: "Banco", Class: finance.ClassAsset}, at); err != nil {
		t.Fatal(err)
	}
	if err := ledger.SetDefaultReceivingAccount(ctx, sp, "bank", at); err != nil {
		t.Fatal(err)
	}
	return sp
}

func revenueFact(invoiceID string) repositories.InvoiceFact {
	return repositories.InvoiceFact{
		InvoiceID: invoiceID, Direction: finance.Receivable, Amount: 4990,
		Competence: brcal.New(2026, time.February, 1), Due: brcal.New(2026, time.March, 10),
		Paid: brcal.New(2026, time.March, 5), Description: "Fatura #12",
	}
}

// Review Focus 1.
func TestRecordingAnInvoiceTwiceWritesOneBill(t *testing.T) {
	sp := invoiceSpace(t, newSpaceOrgID(), true)
	bills := repositories.NewBillRepository(testDB, testCfg)
	ledger := repositories.NewLedgerRepository(testDB, testCfg)
	ctx := context.Background()

	b, created, err := bills.RecordInvoice(ctx, sp, revenueFact("in_1"), repositories.PostMeta{Actor: "service:ctech-wallet"}, time.Now())
	if err != nil || !created {
		t.Fatalf("first: created=%v err=%v", created, err)
	}
	if b.ID != repositories.InvoiceBillID(sp, "in_1") || b.Status != finance.BillPaid || b.Origin != finance.OriginBillingInvoice ||
		b.OriginRef != "in_1" || b.CategoryID != finance.CategorySubscriptionRevenue || b.AccountID != "bank" ||
		b.PaidDate != brcal.New(2026, time.March, 5) || len(b.TransactionIDs) != 2 {
		t.Fatalf("bill = %+v", b)
	}
	again, created, err := bills.RecordInvoice(ctx, sp, revenueFact("in_1"), repositories.PostMeta{Actor: "service:billing-reconciler"}, time.Now())
	if err != nil || created || again.ID != b.ID {
		t.Fatalf("replay: created=%v err=%v bill=%+v", created, err, again)
	}
	if got := balance(t, ledger, sp, "bank"); got != 4990 {
		t.Errorf("bank = %d, want 4990 once", got)
	}
	if got := balance(t, ledger, sp, finance.CategorySubscriptionRevenue); got != -4990 {
		t.Errorf("revenue = %d, want -4990 (a credit)", got)
	}
	if got := balance(t, ledger, sp, "sys-receivables"); got != 0 {
		t.Errorf("receivables = %d, want 0: recognised and received at once", got)
	}
	stored, err := bills.Get(ctx, sp, b.ID)
	if err != nil || stored.Status != finance.BillPaid || stored.Competence != brcal.New(2026, time.February, 1) {
		t.Fatalf("stored bill = %+v, %v", stored, err)
	}
	// The DRE reads competence: the revenue belongs to February, the cash to March.
	sums, err := ledger.Summaries(ctx, sp, finance.MonthOf(brcal.New(2026, time.February, 1)), finance.MonthOf(brcal.New(2026, time.March, 1)))
	if err != nil {
		t.Fatal(err)
	}
	var febRevenue billing.Cents
	for _, s := range sums {
		if s.AccountID == finance.CategorySubscriptionRevenue && s.Month == finance.MonthOf(brcal.New(2026, time.February, 1)) {
			febRevenue = s.Credits
		}
	}
	if febRevenue != 4990 {
		t.Errorf("February revenue = %d, want 4990 (competence = the period start): %+v", febRevenue, sums)
	}
	page, err := bills.ListOpen(ctx, sp, finance.Receivable, 10, nil)
	if err != nil || len(page.Items) != 0 {
		t.Errorf("a recorded invoice is not something to receive: %+v, %v", page, err)
	}
}

// Review Focus 1: the webhook and the reconciler arriving together.
func TestConcurrentRecordingsOfOneInvoiceWriteOneBill(t *testing.T) {
	sp := invoiceSpace(t, newSpaceOrgID(), true)
	bills := repositories.NewBillRepository(testDB, testCfg)
	var wg sync.WaitGroup
	var mu sync.Mutex
	createdN, errs := 0, []error{}
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, created, err := bills.RecordInvoice(context.Background(), sp, revenueFact("in_race"), repositories.PostMeta{}, time.Now())
			mu.Lock()
			defer mu.Unlock()
			if created {
				createdN++
			}
			if err != nil {
				errs = append(errs, err)
			}
		}()
	}
	wg.Wait()
	if createdN != 1 || len(errs) != 0 {
		t.Fatalf("created %d times, errors %v", createdN, errs)
	}
	if got := balance(t, repositories.NewLedgerRepository(testDB, testCfg), sp, "bank"); got != 4990 {
		t.Fatalf("bank = %d, want 4990 once", got)
	}
}

// Review Focus 3: a person who never opened Finanças has nothing created for them.
func TestASpaceWithNoReceivingAccountGetsNothing(t *testing.T) {
	sp := jobSpace(t, "USER#never-opened-"+id.New(), true)
	bills := repositories.NewBillRepository(testDB, testCfg)
	_, _, err := bills.RecordInvoice(context.Background(), sp, revenueFact("in_2"), repositories.PostMeta{}, time.Now())
	if !errors.Is(err, repositories.ErrNoReceivingAccount) {
		t.Fatalf("err = %v, want ErrNoReceivingAccount", err)
	}
	accts, err := repositories.NewLedgerRepository(testDB, testCfg).ListAccounts(context.Background(), sp)
	if err != nil || len(accts) != 0 {
		t.Fatalf("a space was created for a payer with no receiving account: %d accounts, %v", len(accts), err)
	}
}

// Review Focus 4.
func TestAnArchivedReceivingAccountIsNoReceivingAccount(t *testing.T) {
	sp := invoiceSpace(t, newSpaceOrgID(), true)
	ledger := repositories.NewLedgerRepository(testDB, testCfg)
	if err := ledger.ArchiveAccount(context.Background(), sp, "bank", time.Now()); err != nil {
		t.Fatal(err)
	}
	_, _, err := repositories.NewBillRepository(testDB, testCfg).RecordInvoice(context.Background(), sp, revenueFact("in_3"), repositories.PostMeta{}, time.Now())
	if !errors.Is(err, repositories.ErrNoReceivingAccount) {
		t.Fatalf("err = %v", err)
	}
}

// Review Focus 4: a space created before 6.7 (seed version 2, or never seeded)
// has neither billing category.
func TestASpaceSeededBeforeTheBillingCategoriesGetsThem(t *testing.T) {
	sp := jobSpace(t, newSpaceOrgID(), true)
	ledger := repositories.NewLedgerRepository(testDB, testCfg)
	ctx, at := context.Background(), time.Now()
	if err := ledger.EnsureSpace(ctx, sp, at); err != nil { // system accounts only, nothing seeded
		t.Fatal(err)
	}
	if err := ledger.CreateAccount(ctx, sp, finance.LedgerAccount{ID: "bank", Name: "Banco", Class: finance.ClassAsset}, at); err != nil {
		t.Fatal(err)
	}
	if err := ledger.SetDefaultReceivingAccount(ctx, sp, "bank", at); err != nil {
		t.Fatal(err)
	}
	f := revenueFact("in_4")
	f.Direction = finance.Payable
	b, created, err := repositories.NewBillRepository(testDB, testCfg).RecordInvoice(ctx, sp, f, repositories.PostMeta{}, at)
	if err != nil || !created || b.CategoryID != finance.CategoryCTechSubscriptions {
		t.Fatalf("bill = %+v created=%v err=%v", b, created, err)
	}
	if got := balance(t, ledger, sp, "bank"); got != -4990 {
		t.Errorf("bank = %d, want -4990 (paid out)", got)
	}
	if got := balance(t, ledger, sp, finance.CategoryCTechSubscriptions); got != 4990 {
		t.Errorf("expense = %d, want 4990", got)
	}
}

// Review Focus 4: the person's own category, named like the default, makes the
// seed skip the default; the posting rule still finds its category.
func TestATakenCategoryNameStillGetsTheSystemCategory(t *testing.T) {
	sp := jobSpace(t, newSpaceOrgID(), true)
	ledger := repositories.NewLedgerRepository(testDB, testCfg)
	ctx, at := context.Background(), time.Now()
	if err := ledger.EnsureSpace(ctx, sp, at); err != nil {
		t.Fatal(err)
	}
	for _, a := range []finance.LedgerAccount{
		{ID: "bank", Name: "Banco", Class: finance.ClassAsset},
		{ID: "mine", Name: "Assinaturas", Class: finance.ClassExpense, Group: finance.GroupOperatingExpenses},
	} {
		if err := ledger.CreateAccount(ctx, sp, a, at); err != nil {
			t.Fatal(err)
		}
	}
	if err := ledger.SetDefaultReceivingAccount(ctx, sp, "bank", at); err != nil {
		t.Fatal(err)
	}
	b, _, err := repositories.NewBillRepository(testDB, testCfg).RecordInvoice(ctx, sp, revenueFact("in_5"), repositories.PostMeta{}, at)
	if err != nil || b.CategoryID != finance.CategorySubscriptionRevenue {
		t.Fatalf("bill = %+v, %v", b, err)
	}
	if got := balance(t, ledger, sp, "mine"); got != 0 {
		t.Errorf("the person's own expense category moved: %d", got)
	}
}

func TestATestModeInvoiceNeverReachesTheLiveSpace(t *testing.T) {
	owner := newSpaceOrgID()
	live, test := invoiceSpace(t, owner, true), invoiceSpace(t, owner, false)
	if _, _, err := repositories.NewBillRepository(testDB, testCfg).RecordInvoice(context.Background(), test, revenueFact("in_6"), repositories.PostMeta{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	ledger := repositories.NewLedgerRepository(testDB, testCfg)
	if balance(t, ledger, live, "bank") != 0 || balance(t, ledger, test, "bank") != 4990 {
		t.Fatal("a test invoice crossed modes")
	}
}

// Review Focus 2: a partial credit, posted twice by mistake, credits once.
func TestACreditNoteIsRecordedOnce(t *testing.T) {
	sp := invoiceSpace(t, newSpaceOrgID(), true)
	bills := repositories.NewBillRepository(testDB, testCfg)
	ledger := repositories.NewLedgerRepository(testDB, testCfg)
	ctx := context.Background()
	if _, _, err := bills.RecordInvoice(ctx, sp, revenueFact("in_7"), repositories.PostMeta{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	day := brcal.New(2026, time.March, 20)
	for i, want := range []bool{true, false} {
		posted, err := bills.RecordInvoiceCredit(ctx, sp, "in_7", "cn_1", 1990, day, repositories.PostMeta{Actor: "user:op"}, time.Now())
		if err != nil || posted != want {
			t.Fatalf("attempt %d: posted=%v err=%v", i, posted, err)
		}
	}
	if got := balance(t, ledger, sp, "bank"); got != 3000 {
		t.Errorf("bank = %d, want 3000 (4990 received, 1990 credited back once)", got)
	}
	if got := balance(t, ledger, sp, finance.CategorySubscriptionRevenue); got != -3000 {
		t.Errorf("revenue = %d, want -3000", got)
	}
	if got := balance(t, ledger, sp, "sys-receivables"); got != 0 {
		t.Errorf("receivables = %d: a credit on a paid bill leaves nothing to receive", got)
	}
	b, err := bills.Get(ctx, sp, repositories.InvoiceBillID(sp, "in_7"))
	if err != nil || b.Status != finance.BillPaid || len(b.TransactionIDs) != 2 {
		t.Fatalf("the credit must not change the bill (Desfazer pagamento reverses the settlement): %+v, %v", b, err)
	}
	if _, err := bills.RecordInvoiceCredit(ctx, sp, "in_7", "cn_2", 4991, day, repositories.PostMeta{}, time.Now()); !errors.Is(err, finance.ErrInvalidTransaction) {
		t.Errorf("a credit above the bill: err = %v", err)
	}
}

// Review Focus 2: an invoice that was never recorded in a space (it had no
// receiving account) has nothing there to credit.
func TestACreditForAnInvoiceNeverRecordedWritesNothing(t *testing.T) {
	sp := invoiceSpace(t, newSpaceOrgID(), true)
	_, err := repositories.NewBillRepository(testDB, testCfg).RecordInvoiceCredit(context.Background(), sp, "in_never", "cn_x", 100, brcal.New(2026, time.March, 1), repositories.PostMeta{}, time.Now())
	if !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if balance(t, repositories.NewLedgerRepository(testDB, testCfg), sp, "bank") != 0 {
		t.Fatal("a credit with no bill moved cash")
	}
}

// A bill the person reopened in Finanças (Desfazer pagamento) is no longer a
// payment to take back.
func TestACreditOnAReopenedBillIsRefused(t *testing.T) {
	sp := invoiceSpace(t, newSpaceOrgID(), true)
	bills := repositories.NewBillRepository(testDB, testCfg)
	ctx := context.Background()
	b, _, err := bills.RecordInvoice(ctx, sp, revenueFact("in_8"), repositories.PostMeta{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bills.Unsettle(ctx, sp, b.ID, brcal.New(2026, time.March, 6), repositories.PostMeta{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := bills.RecordInvoiceCredit(ctx, sp, "in_8", "cn_3", 100, brcal.New(2026, time.March, 7), repositories.PostMeta{}, time.Now()); !errors.Is(err, finance.ErrBillState) {
		t.Fatalf("err = %v, want ErrBillState", err)
	}
}

// ---- end to end: settlement and credit notes reach both ledgers ----------------

// financePayEnv is a checkout environment whose tenant zero is linked to a
// ctech-account organization with a finance space (bank as the receiving
// account), and whose paying customer has (or has not) opened Finanças.
type financePayEnv struct {
	*payEnv
	issuer, payer space.ResolvedSpace
	ledger        *repositories.LedgerRepository
	bills         *repositories.BillRepository
	finance       *services.FinanceInvoices
}

func newFinancePayEnv(t *testing.T, payerHasSpace bool) *financePayEnv {
	t.Helper()
	e := newPayEnv(t)
	orgs := repositories.NewOrganizationRepository(testDB, testCfg)
	link := newSpaceOrgID()
	if err := orgs.LinkAccountOrganization(ctxT(t), e.org, link, "test", "req_setup", now()); err != nil {
		t.Fatal(err)
	}
	f := &financePayEnv{
		payEnv: e,
		issuer: invoiceSpace(t, link, true),
		payer:  jobSpace(t, "USER#"+e.userID, true),
		ledger: repositories.NewLedgerRepository(testDB, testCfg),
		bills:  repositories.NewBillRepository(testDB, testCfg),
	}
	if payerHasSpace {
		f.payer = invoiceSpace(t, "USER#"+e.userID, true)
	}
	// The reconciler's collector and replay pass, wired the way cmd/reconcile
	// wires them.
	f.finance = services.NewFinanceInvoices(f.bills, orgs, repositories.NewCustomerRepository(testDB, testCfg),
		repositories.NewInvoiceRepository(testDB, testCfg), repositories.NewCreditNoteRepository(testDB, testCfg), e.org.ID)
	e.collector.WithFinance(f.finance)
	return f
}

// pendingPosting reports whether the invoice is still queued for its finance
// posting; pendingCredit the same for a credit note.
func (f *financePayEnv) pendingPosting(t *testing.T, invoiceID string) bool {
	t.Helper()
	var start map[string]types.AttributeValue
	for {
		page, err := repositories.NewInvoiceRepository(testDB, testCfg).PendingFinancePostings(ctxT(t), true, 100, start)
		if err != nil {
			t.Fatal(err)
		}
		for _, inv := range page.Items {
			if inv.ID == invoiceID {
				return true
			}
		}
		if page.LastEvaluatedKey == nil {
			return false
		}
		start = page.LastEvaluatedKey
	}
}

func (f *financePayEnv) pendingCredit(t *testing.T, invoiceID string) bool {
	t.Helper()
	var start map[string]types.AttributeValue
	for {
		page, err := repositories.NewCreditNoteRepository(testDB, testCfg).PendingFinanceCredits(ctxT(t), true, 100, start)
		if err != nil {
			t.Fatal(err)
		}
		for _, cn := range page.Items {
			if cn.InvoiceID == invoiceID {
				return true
			}
		}
		if page.LastEvaluatedKey == nil {
			return false
		}
		start = page.LastEvaluatedKey
	}
}

// I1 (review): a paid invoice whose posting is complete leaves nothing queued.
func TestAPostingDoneAtOnceLeavesNothingPending(t *testing.T) {
	f := newFinancePayEnv(t, true)
	inv, _ := f.payByWebhook(t)
	if f.pendingPosting(t, inv.ID) {
		t.Fatal("a fully recorded invoice is still queued")
	}
}

// I1 (review): a posting that could not be written — here the payer had no
// receiving account yet — stays queued, and the replay pass writes it once the
// space can take it, without writing the issuer's twice.
func TestAPostingThatCouldNotBeWrittenIsReplayed(t *testing.T) {
	f := newFinancePayEnv(t, false)
	inv, _ := f.payByWebhook(t)
	if !f.pendingPosting(t, inv.ID) {
		t.Fatal("the payer's posting was skipped and nothing remembers it")
	}
	f.payer = invoiceSpace(t, "USER#"+f.userID, true) // a week later the person opens Finanças
	later := now().Add(7 * 24 * time.Hour)
	for i := 0; i < 2; i++ {
		if res := f.finance.Replay(ctxT(t), true, later); len(res.Errors) != 0 {
			t.Fatalf("replay %d: %v", i, res.Errors)
		}
	}
	if got := f.bal(t, f.payer, "bank"); got != -inv.Total {
		t.Errorf("payer bank = %d, want %d", got, -inv.Total)
	}
	if got := f.bal(t, f.issuer, "bank"); got != inv.Total {
		t.Errorf("issuer bank = %d, want %d once", got, inv.Total)
	}
	if f.pendingPosting(t, inv.ID) {
		t.Error("still queued after the replay recorded it")
	}
}

// I1 (review): the replay is bounded, so a space that never sets a receiving
// account is not retried forever.
func TestReplayGivesUpAfterTheWindow(t *testing.T) {
	f := newFinancePayEnv(t, false)
	inv, _ := f.payByWebhook(t)
	f.finance.Replay(ctxT(t), true, now().Add(services.FinanceReplayWindow+time.Hour))
	if f.pendingPosting(t, inv.ID) {
		t.Fatal("an invoice paid outside the window is still queued")
	}
	accts, err := f.ledger.ListAccounts(ctxT(t), f.payer)
	if err != nil || len(accts) != 0 {
		t.Fatalf("giving up wrote into the payer's space: %d accounts, %v", len(accts), err)
	}
}

// I1 (review): a credit note that could not reach a space is replayed too,
// after the invoice it credits.
func TestACreditThatCouldNotBeWrittenIsReplayed(t *testing.T) {
	f := newFinancePayEnv(t, false)
	inv, _ := f.payByWebhook(t)
	token := f.sessionToken(t, middleware.ScopeInvoicesWrite, middleware.ScopeInvoicesRead)
	if res := f.consolePost(t, "/v1.0/console/invoices/"+inv.ID+"/credit-notes", token, "live",
		`{"amount":1990,"reason":"cobrança em duplicidade"}`); res.status != http.StatusCreated {
		t.Fatalf("credit note: %d %s", res.status, res.body)
	}
	if !f.pendingCredit(t, inv.ID) {
		t.Fatal("the payer's credit was skipped and nothing remembers it")
	}
	f.payer = invoiceSpace(t, "USER#"+f.userID, true)
	if res := f.finance.Replay(ctxT(t), true, now().Add(24*time.Hour)); len(res.Errors) != 0 {
		t.Fatalf("replay: %v", res.Errors)
	}
	kept := inv.Total - 1990
	if got := f.bal(t, f.payer, "bank"); got != -kept {
		t.Errorf("payer bank = %d, want %d", got, -kept)
	}
	if got := f.bal(t, f.issuer, "bank"); got != kept {
		t.Errorf("issuer bank = %d, want %d (credited once)", got, kept)
	}
	if f.pendingCredit(t, inv.ID) {
		t.Error("the credit is still queued after the replay recorded it")
	}
}

// payByWebhook opens the invoice, pays it in the fake wallet and delivers the
// wallet's webhook, as production does.
func (f *financePayEnv) payByWebhook(t *testing.T) (*billing.Invoice, string) {
	t.Helper()
	inv := f.openInvoice(t)
	charge := f.openCharge(t, inv)
	f.wallet.settle(charge, int64(inv.Total))
	if res := f.notify(t, charge); res.status != http.StatusOK {
		t.Fatalf("webhook: %d", res.status)
	}
	if got := f.invoiceStatus(t, inv.ID); got != billing.InvoicePaid {
		t.Fatalf("invoice is %s, want PAID", got)
	}
	return inv, charge
}

func (f *financePayEnv) bal(t *testing.T, sp space.ResolvedSpace, account string) billing.Cents {
	t.Helper()
	return balance(t, f.ledger, sp, account)
}

func TestAPaidInvoiceIsRevenueForTheIssuerAndAnExpenseForThePayer(t *testing.T) {
	f := newFinancePayEnv(t, true)
	inv, _ := f.payByWebhook(t)

	if got := f.bal(t, f.issuer, "bank"); got != inv.Total {
		t.Errorf("issuer bank = %d, want %d", got, inv.Total)
	}
	if got := f.bal(t, f.issuer, finance.CategorySubscriptionRevenue); got != -inv.Total {
		t.Errorf("issuer revenue = %d, want %d", got, -inv.Total)
	}
	if got := f.bal(t, f.payer, "bank"); got != -inv.Total {
		t.Errorf("payer bank = %d, want %d", got, -inv.Total)
	}
	if got := f.bal(t, f.payer, finance.CategoryCTechSubscriptions); got != inv.Total {
		t.Errorf("payer expense = %d, want %d", got, inv.Total)
	}
	for _, sp := range []space.ResolvedSpace{f.issuer, f.payer} {
		b, err := f.bills.Get(ctxT(t), sp, repositories.InvoiceBillID(sp, inv.ID))
		if err != nil || b.Status != finance.BillPaid || b.Origin != finance.OriginBillingInvoice || b.OriginRef != inv.ID ||
			b.Competence != inv.Period.Start {
			t.Errorf("%s: bill = %+v, %v", sp.PK(), b, err)
		}
	}
}

// Review Focus 1: the webhook delivered twice, then the reconciler confirming
// the same charge — one bill per space, money counted once.
func TestAReplayedSettlementRecordsTheInvoiceOnce(t *testing.T) {
	f := newFinancePayEnv(t, true)
	inv, charge := f.payByWebhook(t)
	if res := f.notify(t, charge); res.status != http.StatusOK {
		t.Fatalf("second webhook: %d", res.status)
	}
	if err := f.collector.Confirm(ctxT(t), true, charge, billing.CauseReconciliation, "req_reconcile", now()); err != nil {
		t.Fatal(err)
	}
	if got := f.bal(t, f.issuer, "bank"); got != inv.Total {
		t.Errorf("issuer bank = %d, want %d once", got, inv.Total)
	}
	if got := f.bal(t, f.payer, "bank"); got != -inv.Total {
		t.Errorf("payer bank = %d, want %d once", got, -inv.Total)
	}
	b, err := f.bills.Get(ctxT(t), f.issuer, repositories.InvoiceBillID(f.issuer, inv.ID))
	if err != nil || len(b.TransactionIDs) != 2 {
		t.Fatalf("issuer bill = %+v, %v", b, err)
	}
}

// Review Focus 3: the payer never opened Finanças. The invoice is paid, the
// issuer has its revenue, and nothing was created for the payer.
func TestAPayerWithNoFinanceSpaceIsLeftAlone(t *testing.T) {
	f := newFinancePayEnv(t, false)
	inv, _ := f.payByWebhook(t)
	if got := f.bal(t, f.issuer, "bank"); got != inv.Total {
		t.Errorf("issuer bank = %d, want %d", got, inv.Total)
	}
	accts, err := f.ledger.ListAccounts(ctxT(t), f.payer)
	if err != nil || len(accts) != 0 {
		t.Fatalf("a space was created for the payer: %d accounts, %v", len(accts), err)
	}
}

type panickingPoster struct{}

func (panickingPoster) Paid(context.Context, *billing.Invoice, string, string, time.Time) []services.Posting {
	panic("finance is down")
}

// Review Focus 5: whatever the finance side does, the money that arrived is
// recorded and the confirmation succeeds.
func TestAFailingFinancePostingNeverFailsTheSettlement(t *testing.T) {
	f := newFinancePayEnv(t, true)
	f.collector.WithFinance(panickingPoster{})
	inv := f.openInvoice(t)
	charge := f.openCharge(t, inv)
	f.wallet.settle(charge, int64(inv.Total))
	if err := f.collector.Confirm(ctxT(t), true, charge, billing.CauseReconciliation, "req_reconcile", now()); err != nil {
		t.Fatalf("the settlement failed because of finance: %v", err)
	}
	if got := f.invoiceStatus(t, inv.ID); got != billing.InvoicePaid {
		t.Fatalf("invoice is %s, want PAID", got)
	}
}

// Review Focus 2: a partial credit note issued in the console on the paid
// invoice takes its amount back out of both ledgers.
func TestACreditNoteOnAPaidInvoiceTakesTheAmountBackInBothSpaces(t *testing.T) {
	f := newFinancePayEnv(t, true)
	inv, _ := f.payByWebhook(t)
	token := f.sessionToken(t, middleware.ScopeInvoicesWrite, middleware.ScopeInvoicesRead)
	res := f.consolePost(t, "/v1.0/console/invoices/"+inv.ID+"/credit-notes", token, "live",
		`{"amount":1990,"reason":"cobrança em duplicidade","refunded_externally":true}`)
	if res.status != http.StatusCreated {
		t.Fatalf("credit note: %d %s", res.status, res.body)
	}
	kept := inv.Total - 1990
	if got := f.bal(t, f.issuer, "bank"); got != kept {
		t.Errorf("issuer bank = %d, want %d", got, kept)
	}
	if got := f.bal(t, f.issuer, finance.CategorySubscriptionRevenue); got != -kept {
		t.Errorf("issuer revenue = %d, want %d", got, -kept)
	}
	if got := f.bal(t, f.payer, "bank"); got != -kept {
		t.Errorf("payer bank = %d, want %d", got, -kept)
	}
	if got := f.bal(t, f.payer, finance.CategoryCTechSubscriptions); got != kept {
		t.Errorf("payer expense = %d, want %d", got, kept)
	}
}

// race runs fn n times at once and returns how many returned nil.
func race(n int, fn func(i int) error) (ok int, errs []error) {
	var wg sync.WaitGroup
	var mu sync.Mutex
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			err := fn(i)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				ok++
			} else {
				errs = append(errs, err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	return ok, errs
}

// I2 (review): two operators crediting R$ 30,00 each on a R$ 49,90 paid invoice
// at the same moment. Both read "nothing credited yet"; only one may write.
// Pre-dates 6.7 (the guard checked the status only), and 6.7 turned the second
// note into money taken out of two ledgers.
func TestConcurrentCreditNotesCannotSumPastTheInvoice(t *testing.T) {
	ctx := ctxT(t)
	org := newOrg(t, true)
	invoices := repositories.NewInvoiceRepository(testDB, testCfg)
	inv := newDraftInvoice(t, org, "gen_"+id.New())
	due := brcal.New(2026, time.March, 10)
	if _, err := invoices.Finalize(ctx, inv, due, due, billing.CauseScheduler, "scheduler", "req", now()); err != nil {
		t.Fatal(err)
	}
	if _, err := invoices.Transition(ctx, inv, billing.InvoicePaid, billing.CauseReconciliation, "reconciler", "req", now()); err != nil {
		t.Fatal(err)
	}
	credits := repositories.NewCreditNoteRepository(testDB, testCfg)
	ok, _ := race(6, func(i int) error {
		snapshot := *inv
		return credits.Issue(ctx, &billing.CreditNote{
			ID: id.NewWithPrefix(id.PrefixCreditNote), InvoiceID: inv.ID, Amount: 3000, Reason: "duplicada",
		}, &snapshot, "user:op", "req", now())
	})
	total, err := credits.TotalCredited(ctx, org.ID, true, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ok != 1 || total != 3000 {
		t.Fatalf("%d notes issued, %d credited on a total of %d", ok, total, inv.Total)
	}
	// A later note within what is left still goes through.
	if err := credits.Issue(ctx, &billing.CreditNote{ID: id.NewWithPrefix(id.PrefixCreditNote), InvoiceID: inv.ID, Amount: 1990, Reason: "resto"}, inv, "user:op", "req", now()); err != nil {
		t.Fatalf("the remaining 1990: %v", err)
	}
}

// I2 (review): the finance side guards the sum too, so whatever reaches it,
// the credits on one bill never exceed the bill.
func TestConcurrentFinanceCreditsCannotSumPastTheBill(t *testing.T) {
	sp := invoiceSpace(t, newSpaceOrgID(), true)
	bills := repositories.NewBillRepository(testDB, testCfg)
	ctx := context.Background()
	if _, _, err := bills.RecordInvoice(ctx, sp, revenueFact("in_sum"), repositories.PostMeta{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	posted := 0
	var mu sync.Mutex
	_, errs := race(6, func(i int) error {
		p, err := bills.RecordInvoiceCredit(ctx, sp, "in_sum", "cn_sum_"+string(rune('a'+i)), 3000, brcal.New(2026, time.March, 20), repositories.PostMeta{}, time.Now())
		if p {
			mu.Lock()
			posted++
			mu.Unlock()
		}
		return err
	})
	if posted != 1 {
		t.Fatalf("%d credits of 3000 posted on a bill of 4990 (errors %v)", posted, errs)
	}
	for _, err := range errs {
		if !errors.Is(err, finance.ErrInvalidTransaction) {
			t.Errorf("a refused credit: err = %v, want ErrInvalidTransaction", err)
		}
	}
	if got := balance(t, repositories.NewLedgerRepository(testDB, testCfg), sp, "bank"); got != 1990 {
		t.Fatalf("bank = %d, want 1990", got)
	}
}

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}
