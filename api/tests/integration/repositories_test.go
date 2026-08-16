//go:build integration

package integration

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

func now() time.Time { return time.Date(2026, time.March, 10, 12, 0, 0, 0, time.UTC) }

func marchPeriod() billing.Period {
	return billing.Period{
		Start: brcal.New(2026, time.March, 1),
		End:   brcal.New(2026, time.April, 1),
	}
}

// newOrg provisions a fresh, charge-enabled organization per test, so tests
// cannot leak rows into each other through a shared tenant.
func newOrg(t *testing.T, livemode bool) *billing.Organization {
	t.Helper()
	repo := repositories.NewOrganizationRepository(testDB, testCfg)
	org := &billing.Organization{
		ID:           id.NewWithPrefix(id.PrefixOrganization),
		DisplayName:  "Test Org",
		Livemode:     livemode,
		PayoutStatus: billing.PayoutEnabled,
		// A distinct owner per organization: the owner is now a lookup key
		// (ADR 0011), and tests that share one would resolve to each other's
		// tenant — which is the exact bug this key must not have.
		OwnerUserID: "usr_" + id.New(),
	}
	if err := repo.Create(ctxT(t), org, now()); err != nil {
		t.Fatal(err)
	}
	return org
}

func newDraftInvoice(t *testing.T, org *billing.Organization, generationKey string) *billing.Invoice {
	t.Helper()
	repo := repositories.NewInvoiceRepository(testDB, testCfg)
	inv := &billing.Invoice{
		ID:             id.NewWithPrefix(id.PrefixInvoice),
		OrganizationID: org.ID,
		Livemode:       org.Livemode,
		CustomerID:     "cus_1",
		Status:         billing.InvoiceDraft,
		Period:         marchPeriod(),
		Currency:       billing.CurrencyBRL,
		Total:          4990,
		Subtotal:       4990,
	}
	items := []billing.InvoiceItem{{Description: "DF-e Basic", Period: marchPeriod(), Quantity: 1, UnitAmount: 4990, Amount: 4990}}
	if err := repo.Create(ctxT(t), inv, items, generationKey, now()); err != nil {
		t.Fatal(err)
	}
	return inv
}

// TestPeriodIndexKeySchema pins the shape every period query depends on. If the
// index is ever created with a different key, listings silently degrade to
// filters — and this is the test that says so instead of the queries just
// getting slower and more expensive.
func TestPeriodIndexKeySchema(t *testing.T) {
	out, err := testDB.DescribeTable(ctxT(t), &dynamodb.DescribeTableInput{
		TableName: aws.String(repositories.TableName(testCfg, repositories.TableInvoices)),
	})
	if err != nil {
		t.Fatal(err)
	}
	var period *types.GlobalSecondaryIndexDescription
	for i, gsi := range out.Table.GlobalSecondaryIndexes {
		if aws.ToString(gsi.IndexName) == repositories.IndexPeriod {
			period = &out.Table.GlobalSecondaryIndexes[i]
		}
	}
	if period == nil {
		t.Fatalf("%s index is missing", repositories.IndexPeriod)
	}

	var hash, ranges []string
	for _, k := range period.KeySchema {
		if k.KeyType == types.KeyTypeHash {
			hash = append(hash, aws.ToString(k.AttributeName))
		} else {
			ranges = append(ranges, aws.ToString(k.AttributeName))
		}
	}
	if fmt.Sprint(hash) != "[period_pk]" || fmt.Sprint(ranges) != "[period_sk]" {
		t.Fatalf("key schema = hash %v, range %v; want [period_pk] / [period_sk]", hash, ranges)
	}
}

func TestTenantAndModeIsolation(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewCustomerRepository(testDB, testCfg)

	orgA, orgB := newOrg(t, true), newOrg(t, true)
	sharedID := id.NewWithPrefix(id.PrefixCustomer)

	for _, org := range []*billing.Organization{orgA, orgB} {
		c := &billing.Customer{
			ID: sharedID, OrganizationID: org.ID, Livemode: true,
			Name: "Cliente " + org.ID, Email: "a@example.com",
		}
		if err := repo.Create(ctx, c, now()); err != nil {
			t.Fatalf("the same customer id must be usable in a different tenant: %v", err)
		}
	}

	a, err := repo.Get(ctx, orgA.ID, true, sharedID)
	if err != nil {
		t.Fatal(err)
	}
	if a.OrganizationID != orgA.ID {
		t.Fatalf("read crossed tenants: got %s", a.OrganizationID)
	}

	// Test mode is a different partition, not a flag: a live customer is simply
	// not there in test mode.
	if _, err := repo.Get(ctx, orgA.ID, false, sharedID); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("a live row must not be visible in test mode, got %v", err)
	}
}

// TestTaxIDIsEncryptedAtRest reads the raw item, not the repository's answer.
//
// Going through the repository would prove only that it round-trips, which a
// no-op "encryptor" also does. The claim being tested is about what somebody
// holding table access sees, so the test has to look at the same thing they
// would: the stored attribute.
func TestTaxIDIsEncryptedAtRest(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewCustomerRepository(testDB, testCfg)
	org := newOrg(t, true)

	const cpf = "52998224725"
	c := &billing.Customer{
		ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: org.ID, Livemode: true,
		Name: "Ana Ribeiro", Email: "ana@example.com", TaxID: cpf,
	}
	if err := repo.Create(ctx, c, now()); err != nil {
		t.Fatal(err)
	}
	// Create must not hand the caller back a ciphertext it cannot render.
	if c.TaxID != cpf {
		t.Fatalf("Create mutated the caller's tax id to %q", c.TaxID)
	}

	table := repositories.PhysicalName(testCfg.TablePrefix, repositories.TableCustomers)
	out, err := testDB.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(table),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: repositories.TenantPK(org.ID, true)},
			"sk": &types.AttributeValueMemberS{Value: repositories.CustomerSK(c.ID)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := out.Item["tax_id"].(*types.AttributeValueMemberS)
	if !ok {
		t.Fatalf("tax_id attribute is missing from the stored row")
	}
	if stored.Value == cpf {
		t.Fatalf("tax_id is stored in the clear")
	}
	if !strings.HasPrefix(stored.Value, "v1.") {
		t.Fatalf("tax_id = %q, want a v1. sealed value", stored.Value)
	}

	got, err := repo.Get(ctx, org.ID, true, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TaxID != cpf {
		t.Fatalf("read back %q, want %q", got.TaxID, cpf)
	}
}

// TestInvoiceNumberingIsGapless is the point of the optimistic counter. Under
// concurrency the numbers must be 1..N with no gap and no duplicate — a gap is
// a question an accountant asks years later, and by then it is unanswerable.
func TestInvoiceNumberingIsGapless(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewInvoiceRepository(testDB, testCfg)
	org := newOrg(t, true)

	const count = 8
	invoices := make([]*billing.Invoice, count)
	for i := range invoices {
		invoices[i] = newDraftInvoice(t, org, fmt.Sprintf("si_%d:2026-03-01", i))
	}

	var wg sync.WaitGroup
	errs := make([]error, count)
	for i, inv := range invoices {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = repo.Finalize(ctx, inv, brcal.New(2026, time.March, 10), brcal.New(2026, time.March, 20), "scheduler", "req_1", now())
		}()
	}
	wg.Wait()

	seen := map[int64]bool{}
	for i, err := range errs {
		if err != nil {
			t.Fatalf("finalize %d: %v", i, err)
		}
		n := invoices[i].Number
		if seen[n] {
			t.Fatalf("invoice number %d was issued twice", n)
		}
		seen[n] = true
	}
	for n := int64(1); n <= count; n++ {
		if !seen[n] {
			t.Fatalf("number %d is missing — numbering has a gap: %v", n, seen)
		}
	}
}

// TestTransitionWritesAuditAtomically is the property § 18 asks for: a status
// that changed always has a record of who changed it.
func TestTransitionWritesAuditAtomically(t *testing.T) {
	ctx := ctxT(t)
	invoices := repositories.NewInvoiceRepository(testDB, testCfg)
	audits := repositories.NewAuditRepository(testDB, testCfg)
	org := newOrg(t, true)

	inv := newDraftInvoice(t, org, "si_audit:2026-03-01")
	if _, err := invoices.Finalize(ctx, inv, brcal.New(2026, time.March, 10), brcal.New(2026, time.March, 20), "scheduler", "req_a", now()); err != nil {
		t.Fatal(err)
	}
	if _, err := invoices.Transition(ctx, inv, billing.InvoicePaid, billing.CauseWalletWebhook, "wallet", "req_b", now()); err != nil {
		t.Fatal(err)
	}

	trail, err := audits.ListForEntity(ctx, org.ID, true, inv.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(trail) != 2 {
		t.Fatalf("expected 2 audit entries (finalized, paid), got %d: %+v", len(trail), trail)
	}
	byAction := map[string]billing.AuditLog{}
	for _, e := range trail {
		byAction[e.Action] = e
	}
	paid, ok := byAction[string(billing.EventInvoicePaid)]
	if !ok {
		t.Fatalf("no invoice.paid audit entry: %+v", trail)
	}
	if paid.Actor != "wallet" || paid.Cause != billing.CauseWalletWebhook {
		t.Fatalf("audit entry lost the actor or the cause: %+v", paid)
	}
	if paid.Before != string(billing.InvoiceOpen) || paid.After != string(billing.InvoicePaid) {
		t.Fatalf("audit entry lost the transition: %s -> %s", paid.Before, paid.After)
	}
}

// TestIllegalTransitionWritesNothing proves the domain rejects before DynamoDB
// is touched — no partial write, no orphan audit row.
func TestIllegalTransitionWritesNothing(t *testing.T) {
	ctx := ctxT(t)
	invoices := repositories.NewInvoiceRepository(testDB, testCfg)
	audits := repositories.NewAuditRepository(testDB, testCfg)
	org := newOrg(t, true)

	inv := newDraftInvoice(t, org, "si_illegal:2026-03-01")
	// DRAFT -> PAID is not an edge: an invoice must be finalized first.
	if _, err := invoices.Transition(ctx, inv, billing.InvoicePaid, billing.CauseWalletWebhook, "wallet", "req_x", now()); !errors.Is(err, billing.ErrInvalidTransition) {
		t.Fatalf("want ErrInvalidTransition, got %v", err)
	}
	if inv.Status != billing.InvoiceDraft {
		t.Fatalf("the rejected transition mutated the in-memory invoice: %s", inv.Status)
	}
	trail, err := audits.ListForEntity(ctx, org.ID, true, inv.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(trail) != 0 {
		t.Fatalf("a rejected transition wrote %d audit entries", len(trail))
	}
}

// TestConcurrentTransitionsLoseOne is the reason the update is conditional. Two
// callers acting on the same read must not both succeed — on an invoice that
// means a webhook marking PAID being silently overwritten by a stale void.
func TestConcurrentTransitionsLoseOne(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewInvoiceRepository(testDB, testCfg)
	org := newOrg(t, true)

	inv := newDraftInvoice(t, org, "si_race:2026-03-01")
	if _, err := repo.Finalize(ctx, inv, brcal.New(2026, time.March, 10), brcal.New(2026, time.March, 20), "scheduler", "req_1", now()); err != nil {
		t.Fatal(err)
	}

	// Two callers holding the same OPEN snapshot.
	first, second := *inv, *inv
	if _, err := repo.Transition(ctx, &first, billing.InvoicePaid, billing.CauseWalletWebhook, "wallet", "req_2", now()); err != nil {
		t.Fatal(err)
	}
	_, err := repo.Transition(ctx, &second, billing.InvoiceVoid, billing.CauseManual, "operator", "req_3", now())
	if !errors.Is(err, repositories.ErrConcurrentModification) {
		t.Fatalf("the stale writer must be rejected, got %v", err)
	}

	fresh, err := repo.Get(ctx, org.ID, true, inv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Status != billing.InvoicePaid {
		t.Fatalf("the paid invoice was overwritten: %s", fresh.Status)
	}
}

// TestGenerationKeyMakesTheSweepIdempotent: running the daily job twice must
// create one invoice, not two.
func TestGenerationKeyMakesTheSweepIdempotent(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewInvoiceRepository(testDB, testCfg)
	org := newOrg(t, true)

	key := billing.GenerationKey("sub_sweep", brcal.New(2026, time.March, 1))
	newDraftInvoice(t, org, key)

	second := &billing.Invoice{
		ID: id.NewWithPrefix(id.PrefixInvoice), OrganizationID: org.ID, Livemode: true,
		CustomerID: "cus_1", Status: billing.InvoiceDraft, Period: marchPeriod(),
		Currency: billing.CurrencyBRL, Total: 4990, Subtotal: 4990,
	}
	items := []billing.InvoiceItem{{Description: "DF-e Basic", Period: marchPeriod(), Quantity: 1, UnitAmount: 4990, Amount: 4990}}
	err := repo.Create(ctx, second, items, key, now())
	if !errors.Is(err, repositories.ErrAlreadyGenerated) {
		t.Fatalf("a repeated sweep must not create a second invoice, got %v", err)
	}
	if _, getErr := repo.Get(ctx, org.ID, true, second.ID); !errors.Is(getErr, repositories.ErrNotFound) {
		t.Fatal("the rejected invoice was partially written")
	}
}

func TestUsageIsIdempotent(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewUsageRepository(testDB, testCfg)
	org := newOrg(t, true)
	period := marchPeriod()

	record := func(key string, qty int64) *billing.UsageRecord {
		return &billing.UsageRecord{
			ID: id.NewWithPrefix(id.PrefixUsageRecord), OrganizationID: org.ID, Livemode: true,
			SubscriptionItemID: "si_usage", Quantity: qty,
			OccurredAt:     time.Date(2026, time.March, 5, 10, 0, 0, 0, time.UTC),
			IdempotencyKey: key,
		}
	}

	if err := repo.Append(ctx, record("evt-1", 10), period.Start, now()); err != nil {
		t.Fatal(err)
	}
	if err := repo.Append(ctx, record("evt-2", 5), period.Start, now()); err != nil {
		t.Fatal(err)
	}
	// The retry.
	if err := repo.Append(ctx, record("evt-1", 10), period.Start, now()); !errors.Is(err, repositories.ErrDuplicateUsage) {
		t.Fatalf("a retried report must be rejected, got %v", err)
	}

	records, err := repo.ListForPeriod(ctx, org.ID, true, "si_usage", period.Start)
	if err != nil {
		t.Fatal(err)
	}
	if total := billing.SumUsage(records, period); total != 15 {
		t.Fatalf("total = %d, want 15 (the retry must not be counted twice)", total)
	}
}

// TestScheduleIndexIsSparse: a canceled subscription must leave the sweep, not
// be filtered out of it every morning forever.
func TestScheduleIndexIsSparse(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewSubscriptionRepository(testDB, testCfg)
	org := newOrg(t, true)

	anchor := brcal.New(2026, time.March, 1)
	sub := &billing.Subscription{
		ID: id.NewWithPrefix(id.PrefixSubscription), OrganizationID: org.ID, Livemode: true,
		CustomerID: "cus_1", Status: billing.SubscriptionActive,
		Recurrence: billing.Recurrence{Interval: billing.IntervalMonth, Count: 1},
		Timing:     billing.BillAdvance, Anchor: anchor,
	}
	if err := repo.Create(ctx, sub, []billing.SubscriptionItem{{ID: id.NewWithPrefix(id.PrefixSubscriptionItm), OrganizationID: org.ID, Livemode: true, SubscriptionID: sub.ID, PriceID: "price_x", Quantity: 1}}, now()); err != nil {
		t.Fatal(err)
	}

	due := brcal.New(2026, time.April, 1) // the start of the next period
	page, err := repo.DueOn(ctx, true, due, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSubscription(page.Items, sub.ID) {
		t.Fatalf("an active subscription must appear in the sweep for %s", due)
	}

	if _, err := repo.Transition(ctx, sub, billing.SubscriptionCanceled, billing.CauseManual, "operator", "req_1", now()); err != nil {
		t.Fatal(err)
	}
	page, err = repo.DueOn(ctx, true, due, 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	if containsSubscription(page.Items, sub.ID) {
		t.Fatal("a canceled subscription is still in the sweep index")
	}
}

func TestRenewalMovesTheSweepForward(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewSubscriptionRepository(testDB, testCfg)
	org := newOrg(t, true)

	anchor := brcal.New(2026, time.January, 31)
	sub := &billing.Subscription{
		ID: id.NewWithPrefix(id.PrefixSubscription), OrganizationID: org.ID, Livemode: true,
		CustomerID: "cus_1", Status: billing.SubscriptionActive,
		Recurrence: billing.Recurrence{Interval: billing.IntervalMonth, Count: 1},
		Timing:     billing.BillAdvance, Anchor: anchor,
	}
	if err := repo.Create(ctx, sub, []billing.SubscriptionItem{{ID: id.NewWithPrefix(id.PrefixSubscriptionItm), OrganizationID: org.ID, Livemode: true, SubscriptionID: sub.ID, PriceID: "price_x", Quantity: 1}}, now()); err != nil {
		t.Fatal(err)
	}

	if _, err := repo.Transition(ctx, sub, billing.SubscriptionActive, billing.CauseRenewal, "scheduler", "req_1", now()); err != nil {
		t.Fatal(err)
	}
	if sub.PeriodIndex != 1 {
		t.Fatalf("period index = %d, want 1", sub.PeriodIndex)
	}

	// Anchored on the 31st, period 1 is 28 Feb and period 2 is 31 March — the
	// anchor must not have drifted to the 28th.
	page, err := repo.DueOn(ctx, true, brcal.New(2026, time.March, 31), 50, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSubscription(page.Items, sub.ID) {
		t.Fatal("after renewal the sweep must point at 2026-03-31")
	}

	reread, err := repo.Get(ctx, org.ID, true, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reread.PeriodIndex != 1 {
		t.Fatalf("the persisted period index is %d, want 1", reread.PeriodIndex)
	}
}

func TestPayoutGateIsAudited(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewOrganizationRepository(testDB, testCfg)
	audits := repositories.NewAuditRepository(testDB, testCfg)

	org := &billing.Organization{
		ID: id.NewWithPrefix(id.PrefixOrganization), DisplayName: "Gated", Livemode: true, OwnerUserID: "usr_1",
	}
	if err := repo.Create(ctx, org, now()); err != nil {
		t.Fatal(err)
	}
	// A new organization must not be able to collect money.
	if org.PayoutStatus != billing.PayoutNotConfigured {
		t.Fatalf("a new organization starts at %s, want not_configured", org.PayoutStatus)
	}
	if err := org.AuthorizeCharge(); !errors.Is(err, billing.ErrPayoutNotEnabled) {
		t.Fatalf("want ErrPayoutNotEnabled, got %v", err)
	}

	if err := repo.SetPayoutStatus(ctx, org, billing.PayoutEnabled, "artur", "req_1", now()); err != nil {
		t.Fatal(err)
	}
	stored, err := repo.Get(ctx, org.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if stored.PayoutStatus != billing.PayoutEnabled {
		t.Fatalf("payout status = %s", stored.PayoutStatus)
	}

	trail, err := audits.ListForEntity(ctx, org.ID, true, org.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(trail) != 1 || trail[0].Action != billing.AuditPayoutStatusChanged || trail[0].Actor != "artur" {
		t.Fatalf("the charge gate must leave an audited trail: %+v", trail)
	}
}

// TestRetentionIsWrittenOnCreation: the TTL attribute cannot be applied
// retroactively, so it has to be right on the first write.
func TestRetentionIsWrittenOnCreation(t *testing.T) {
	ctx := ctxT(t)
	invoices := repositories.NewInvoiceRepository(testDB, testCfg)
	org := newOrg(t, true)
	inv := newDraftInvoice(t, org, "si_ttl:2026-03-01")

	invItem := rawGet(t, repositories.TableInvoices, repositories.TenantPK(org.ID, true), "INVOICE#"+inv.ID)
	if _, hasTTL := invItem["ttl"]; hasTTL {
		t.Fatal("an invoice is a commercial document and must carry no TTL")
	}

	if _, err := invoices.Finalize(ctx, inv, brcal.New(2026, time.March, 10), brcal.New(2026, time.March, 20), "scheduler", "req_1", now()); err != nil {
		t.Fatal(err)
	}
	audits, err := repositories.NewAuditRepository(testDB, testCfg).ListForEntity(ctx, org.ID, true, inv.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 {
		t.Fatalf("expected one audit row, got %d", len(audits))
	}
	auditItem := rawGet(t, repositories.TableAudit, repositories.TenantPK(org.ID, true), "AUDIT#"+audits[0].ID)
	ttl, ok := auditItem["ttl"].(*types.AttributeValueMemberN)
	if !ok {
		t.Fatal("an audit row must carry a TTL")
	}
	var seconds int64
	fmt.Sscanf(ttl.Value, "%d", &seconds)
	wantAbout := now().AddDate(5, 0, 0).Unix()
	if diff := seconds - wantAbout; diff > 86400 || diff < -86400 {
		t.Fatalf("audit TTL is %d, want about %d (five years)", seconds, wantAbout)
	}
}

func rawGet(t *testing.T, table, pk, sk string) map[string]types.AttributeValue {
	t.Helper()
	out, err := testDB.GetItem(ctxT(t), &dynamodb.GetItemInput{
		TableName: aws.String(repositories.TableName(testCfg, table)),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: pk},
			"sk": &types.AttributeValueMemberS{Value: sk},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Item == nil {
		t.Fatalf("no row at %s / %s", pk, sk)
	}
	return out.Item
}

func containsSubscription(subs []billing.Subscription, id string) bool {
	for _, s := range subs {
		if s.ID == id {
			return true
		}
	}
	return false
}
