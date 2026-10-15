//go:build integration

package integration

import (
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/realtime"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// emitOnly is a Realtime that logs and publishes nothing but records through
// the real emitter: the changes are read back from the log.
func emitOnly() repositories.Realtime {
	return repositories.Realtime{Emitter: realtime.NewEmitter(userOf{})}
}

// logged is every change the scopes hold, oldest first.
func logged(t *testing.T, scopes ...string) []repositories.Change {
	t.Helper()
	got, _, err := repositories.NewRealtimeRepository(testDB, testCfg).
		Since(ctxT(t), scopes, id.NewAt(now().Add(-time.Hour)), 100)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func typesOf(cs []repositories.Change) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Type + ":" + c.Entity + ":" + c.Status
	}
	return out
}

func TestABankLineIsAnnouncedToItsSpaceAndToNoOther(t *testing.T) {
	ctx := ctxT(t)
	ledger, sp := byokSpace(t)
	ledger.WithRealtime(emitOnly())
	other, _ := space.ForJob(newSpaceOrgID(), true)

	line := repositories.BankLine{Provider: "inter", LineID: "RT1", Date: brcal.MustParse("2026-10-01"), Credit: true, Amount: 4200, Memo: "Pix"}
	txID, posted, err := ledger.PostBankLine(ctx, sp, "byok-inter-abc", line, repositories.PostMeta{Actor: "job"}, now())
	if err != nil || !posted {
		t.Fatalf("posted=%v err=%v", posted, err)
	}
	// The same line again is a replay: nothing new is announced.
	if _, posted, _ := ledger.PostBankLine(ctx, sp, "byok-inter-abc", line, repositories.PostMeta{Actor: "job"}, now()); posted {
		t.Fatal("a replayed line was posted twice")
	}

	mine := logged(t, realtime.SpaceScope(sp.PK()))
	if len(mine) != 1 || mine[0].Type != "statement.line_posted" || mine[0].EntityID != txID {
		t.Fatalf("my space: %v", typesOf(mine))
	}
	if theirs := logged(t, realtime.SpaceScope(other.PK())); len(theirs) != 0 {
		t.Fatalf("another space heard it: %v", typesOf(theirs))
	}
}

func TestClassifyingALineAnnouncesTheLineAndTheBillItSettled(t *testing.T) {
	ctx := ctxT(t)
	ledger, sp := byokSpace(t)
	bills := repositories.NewBillRepository(testDB, testCfg).WithRealtime(emitOnly())
	ledger.WithRealtime(emitOnly())
	d := brcal.MustParse("2026-10-05")

	payable, err := bills.Create(ctx, sp, finance.Bill{Direction: finance.Payable, Amount: 2500, AccountID: "byok-inter-abc",
		CategoryID: "cat-aluguel", Description: "Aluguel", Competence: d, Due: d, Origin: finance.OriginManual},
		repositories.PostMeta{Actor: "u"}, now())
	if err != nil {
		t.Fatal(err)
	}
	out, _, _ := ledger.PostBankLine(ctx, sp, "byok-inter-abc", repositories.BankLine{Provider: "inter", LineID: "RT-OUT", Date: d, Credit: false, Amount: 2500}, repositories.PostMeta{}, now())
	if _, err := ledger.Classify(ctx, bills, sp, out, repositories.Classification{BillID: payable.ID}, repositories.PostMeta{Actor: "u"}, now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}

	got := typesOf(logged(t, realtime.SpaceScope(sp.PK())))
	want := []string{
		"bill.updated:bill:forecast",                 // created
		"statement.line_posted:statement:",           // the bank line arrived
		"statement.line_posted:statement:classified", // and left "A classificar"
		"bill.updated:bill:settled",                  // settling the bill
	}
	if len(got) != len(want) {
		t.Fatalf("changes = %v, want %v", got, want)
	}
	for _, w := range want {
		found := false
		for _, g := range got {
			found = found || g == w
		}
		if !found {
			t.Errorf("missing %q in %v", w, got)
		}
	}
}

func TestABillCreatedAndCanceledIsAnnouncedBothTimes(t *testing.T) {
	ctx := ctxT(t)
	_, sp := byokSpace(t)
	bills := repositories.NewBillRepository(testDB, testCfg).WithRealtime(emitOnly())
	d := brcal.MustParse("2026-10-05")
	b, err := bills.Create(ctx, sp, finance.Bill{Direction: finance.Payable, Amount: 300, AccountID: "byok-inter-abc",
		CategoryID: "cat-aluguel", Description: "Luz", Competence: d, Due: d, Origin: finance.OriginManual},
		repositories.PostMeta{Actor: "u"}, now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bills.Cancel(ctx, sp, b.ID, d, repositories.PostMeta{Actor: "u"}, now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	got := logged(t, realtime.SpaceScope(sp.PK()))
	if len(got) != 2 || got[0].EntityID != b.ID || got[1].Status != "canceled" {
		t.Fatalf("changes = %v", typesOf(got))
	}
}

func TestACustomerCreatedAndAnonymizedIsAnnouncedToTheTenantOnly(t *testing.T) {
	ctx := ctxT(t)
	org := newOrg(t, true)
	customers := repositories.NewCustomerRepository(testDB, testCfg).WithRealtime(emitOnly())
	c := &billing.Customer{ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: org.ID, Livemode: true, Name: "Fulano", Email: "f@example.com"}
	if err := customers.Create(ctx, c, "test", "req", now()); err != nil {
		t.Fatal(err)
	}
	if err := customers.Anonymize(ctx, c, "test", "req", now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	got := logged(t, realtime.TenantScope(org.ID, true))
	if len(got) != 2 || got[0].Type != "customer.updated" || got[1].Status != "anonymized" {
		t.Fatalf("changes = %v", typesOf(got))
	}
	if leak := logged(t, realtime.TenantScope(org.ID, false)); len(leak) != 0 {
		t.Fatalf("the test-mode tenant heard a live customer: %v", typesOf(leak))
	}
}

// The hourly health save is a plain put and is deliberately silent; a connect and
// an audited change are announced to the account organization's space.
func TestAConnectionIsAnnouncedWhenItChangesButNotOnTheHourlySave(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewByokRepository(testDB, testCfg).WithRealtime(emitOnly())
	org := newSpaceOrgID()
	c := newConnection(org, true)
	if err := repo.CreateConnection(ctx, c, "u", "r", now()); err != nil {
		t.Fatal(err)
	}
	scope := realtime.SpaceScope(org + "#live")
	if got := logged(t, scope); len(got) != 1 || got[0].Type != "connection.updated" || got[0].Entity != "connection" {
		t.Fatalf("after connect: %v", typesOf(got))
	}

	if err := repo.SaveConnection(ctx, c, "job", "r", "", now().Add(time.Second)); err != nil { // the hourly save
		t.Fatal(err)
	}
	if got := logged(t, scope); len(got) != 1 {
		t.Fatalf("the hourly save was announced: %v", typesOf(got))
	}

	if err := repo.SaveConnection(ctx, c, "u", "r", "byok.health", now().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := logged(t, scope); len(got) != 2 {
		t.Fatalf("an audited change was not announced: %v", typesOf(got))
	}
	if err := repo.DeleteConnection(ctx, c, "u", "r", now().Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := logged(t, scope); len(got) != 3 {
		t.Fatalf("a disconnect was not announced: %v", typesOf(got))
	}
}

func TestAPayerRecordedAndAPayoutSettledAreAnnounced(t *testing.T) {
	ctx := ctxT(t)
	org := newOrg(t, true)
	payments := repositories.NewPaymentRepository(testDB, testCfg).WithRealtime(emitOnly())

	a := &billing.PaymentAttempt{ID: id.NewWithPrefix(id.PrefixPaymentAttempt), OrganizationID: org.ID, Livemode: true,
		InvoiceID: "in_payer", AttemptNumber: 1, Status: billing.AttemptSucceeded, Method: billing.MethodManual, Amount: 100}
	if err := payments.CreateAttempt(ctx, a, now()); err != nil {
		t.Fatal(err)
	}
	if wrote, err := payments.SetPayerOnce(ctx, a, billing.Payer{Name: "Fulano", Document: "39053344705"}, billing.PayerMatch("statement"), now().Add(time.Second)); err != nil || !wrote {
		t.Fatalf("payer: wrote=%v err=%v", wrote, err)
	}
	// A second payer changes nothing and announces nothing.
	if wrote, _ := payments.SetPayerOnce(ctx, a, billing.Payer{Name: "Outro"}, billing.PayerMatch("statement"), now().Add(2*time.Second)); wrote {
		t.Fatal("a second payer was recorded")
	}
	got := logged(t, realtime.InvoiceScope(org.ID, true, "in_payer"))
	if len(got) != 1 || got[0].Type != "invoice.payer_recorded" || got[0].EntityID != "in_payer" {
		t.Fatalf("payer changes = %v", typesOf(got))
	}
	for _, c := range got {
		if len(c.Scopes) == 0 {
			t.Fatal("a change with no scope")
		}
	}

	p := &billing.Payout{ID: "po_" + id.New(), OrganizationID: org.ID, Livemode: true, Reference: "ref-" + id.New(), Status: billing.PayoutProcessing}
	if _, _, err := payments.CreatePayout(ctx, p, now()); err != nil {
		t.Fatal(err)
	}
	if err := payments.SettlePayout(ctx, p, billing.PayoutDone, "pay_1", "", now().Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	tenant := logged(t, realtime.TenantScope(org.ID, true))
	if len(tenant) < 1 || tenant[len(tenant)-1].Type != "payout.updated" || tenant[len(tenant)-1].Status != string(billing.PayoutDone) {
		t.Fatalf("tenant changes = %v", typesOf(tenant))
	}
}
