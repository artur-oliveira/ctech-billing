//go:build integration

package integration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/provision"
	"gopkg.aoctech.app/billing/api/internal/repositories"
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

func contains(xs []string, x string) bool {
	for _, s := range xs {
		if s == x {
			return true
		}
	}
	return false
}
