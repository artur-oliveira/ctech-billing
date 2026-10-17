//go:build integration

package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/api-commons/erasure"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/domain/finance/statement"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/problem"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// lockSubject applies a user.locked for sub, exactly as the saga consumer does.
func lockSubject(t *testing.T, sub string) {
	t.Helper()
	store := erasure.NewStore(testDB, testCfg.TablePrefix, 0)
	if _, err := store.Apply(ctxT(t), erasure.SubKey(sub), erasure.Message{
		Version: erasure.Version, Type: erasure.TypeLocked, RequestID: "req_" + id.New(), Sub: sub,
		Scope: erasure.ScopeAccount, Services: []string{"billing"}, IssuedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
}

// Review Focus 3.
func TestALockedPersonIsRefusedWithATokenThatIsStillValid(t *testing.T) {
	e := newPortal(t)
	e.withPortal(t)
	token := e.portalToken(t, middleware.ScopeMySubscriptionsRead)
	if res := e.console(t, "/v1.0/portal/session", token, ""); res.status != http.StatusOK {
		t.Fatalf("before the lock: %d %s", res.status, res.body)
	}

	lockSubject(t, e.userID)

	res := e.console(t, "/v1.0/portal/session", token, "")
	if res.status != http.StatusForbidden || problemType(t, res.body) != problem.TypeAccountLocked {
		t.Fatalf("after the lock: %d %s, want 403 %s", res.status, res.body, problem.TypeAccountLocked)
	}
	// A service token is not a person: a user's erasure does not lock the M2M surface.
	m2m := e.token(t, e.client, "", middleware.ScopeProductsRead)
	if res := e.do(t, http.MethodGet, "/v1.0/products", m2m, "", ""); res.status != http.StatusOK {
		t.Fatalf("M2M after a user's lock: %d %s", res.status, res.body)
	}
}

func newSubscriptionWithMetadata(t *testing.T, org *billing.Organization, customerID, priceID string) *billing.Subscription {
	t.Helper()
	sub := &billing.Subscription{
		ID: id.NewWithPrefix(id.PrefixSubscription), OrganizationID: org.ID, Livemode: org.Livemode,
		CustomerID: customerID, Status: billing.SubscriptionActive,
		Recurrence: billing.Recurrence{Interval: billing.IntervalMonth, Count: 1},
		Timing:     billing.BillAdvance,
		Anchor:     brcal.New(2026, time.March, 1),
		Metadata:   billing.Metadata{"crm_contact": "pessoa@example.com"},
	}
	item := billing.SubscriptionItem{
		ID: id.NewWithPrefix(id.PrefixSubscriptionItm), OrganizationID: org.ID, Livemode: org.Livemode,
		SubscriptionID: sub.ID, PriceID: priceID, Quantity: 1,
	}
	if err := repositories.NewSubscriptionRepository(testDB, testCfg).
		Create(ctxT(t), sub, []billing.SubscriptionItem{item}, now()); err != nil {
		t.Fatal(err)
	}
	return sub
}

func TestListByCustomerReadsOneCustomerOnlyAndMetadataClears(t *testing.T) {
	ctx := ctxT(t)
	e := newPortal(t)
	other := &billing.Customer{
		ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: e.org.ID, Livemode: true,
		Name: "Outra Pessoa", Email: "outra@example.com",
	}
	if err := repositories.NewCustomerRepository(testDB, testCfg).Create(ctx, other, "test", "req_setup", now()); err != nil {
		t.Fatal(err)
	}
	mine := newSubscriptionWithMetadata(t, e.org, e.customer.ID, e.priceID)
	newSubscriptionIn(t, e.org, other.ID, e.priceID, billing.SubscriptionActive)
	newSubscriptionInvoice(t, e.org, mine)
	newInvoiceFor(t, e.org, e.customer.ID)
	newInvoiceFor(t, e.org, other.ID)

	invoices, err := repositories.NewInvoiceRepository(testDB, testCfg).ListByCustomer(ctx, e.org.ID, true, e.customer.ID, 0)
	if err != nil || len(invoices) != 2 {
		t.Fatalf("invoices = %d, %v; want this customer's 2 and none of the other's", len(invoices), err)
	}
	for _, inv := range invoices {
		if inv.CustomerID != e.customer.ID {
			t.Fatalf("invoice %s belongs to %s", inv.ID, inv.CustomerID)
		}
	}

	subs := repositories.NewSubscriptionRepository(testDB, testCfg)
	got, err := subs.ListByCustomer(ctx, e.org.ID, true, e.customer.ID, 0)
	if err != nil || len(got) != 1 || got[0].ID != mine.ID || len(got[0].Metadata) == 0 {
		t.Fatalf("subscriptions = %+v, %v; want only %s, with its metadata", got, err, mine.ID)
	}
	if err := subs.ClearMetadata(ctx, &got[0], now()); err != nil {
		t.Fatal(err)
	}
	after, err := subs.Get(ctx, e.org.ID, true, mine.ID)
	if err != nil || len(after.Metadata) != 0 {
		t.Fatalf("metadata after clearing = %v, %v", after.Metadata, err)
	}
}

// R22: the indexes are sparse. A row written before customer-index existed and
// not backfilled is invisible to ListByCustomer, so an open invoice on it would
// not block an erasure. This test pins that fact; the control is the backfill
// gate in Task 11, not code.
func TestAnInvoiceWithoutItsCustomerKeyIsInvisibleToTheIndex(t *testing.T) {
	ctx := ctxT(t)
	e := newPortal(t)
	inv := newInvoiceFor(t, e.org, e.customer.ID)
	removeAttr(t, "invoices", repositories.TenantPK(e.org.ID, true), repositories.InvoiceSK(inv.ID), "customer_pk")

	got, err := repositories.NewInvoiceRepository(testDB, testCfg).ListByCustomer(ctx, e.org.ID, true, e.customer.ID, 0)
	if err != nil || len(got) != 0 {
		t.Fatalf("invoices = %d, %v; a row without customer_pk must not appear (that is what the backfill fixes)", len(got), err)
	}
}

// removeAttr drops one attribute from an item, to reproduce a row written before
// an index's key existed.
func removeAttr(t *testing.T, logicalTable, pk, sk, attr string) {
	t.Helper()
	if _, err := testDB.UpdateItem(ctxT(t), &dynamodb.UpdateItemInput{
		TableName: aws.String(repositories.TableName(testCfg, logicalTable)),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: pk},
			"sk": &types.AttributeValueMemberS{Value: sk},
		},
		UpdateExpression:         aws.String("REMOVE #a"),
		ExpressionAttributeNames: map[string]string{"#a": attr},
	}); err != nil {
		t.Fatal(err)
	}
}

var seedNow = time.Date(2026, 3, 20, 12, 0, 0, 0, time.UTC)

// seedFinance writes a space as a person uses it, across EVERY space-keyed
// table: the chart, a transfer, a bill, a recurrence, a card with a purchase,
// an import with its lines and locks, a BYOK connection with a hook, an audit
// row and a cached idempotent response. A new space-keyed table must be added
// here, which assertSpaceGone then checks.
func seedFinance(t *testing.T, sp space.ResolvedSpace) {
	t.Helper()
	ctx := context.Background()
	ledger := repositories.NewLedgerRepository(testDB, testCfg)
	if err := ledger.EnsureSpace(ctx, sp, seedNow); err != nil {
		t.Fatal(err)
	}
	for _, a := range []finance.LedgerAccount{
		{ID: "bank", Name: "Banco", Class: finance.ClassAsset},
		{ID: "cash", Name: "Caixa", Class: finance.ClassAsset},
		{ID: "rent", Name: "Aluguel", Class: finance.ClassExpense, Group: finance.GroupOperatingExpenses},
	} {
		if err := ledger.CreateAccount(ctx, sp, a, seedNow); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ledger.Post(ctx, sp, transfer(t, "bank", "cash", 1000, brcal.New(2026, time.March, 2)),
		repositories.PostMeta{Actor: "u"}, seedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := repositories.NewBillRepository(testDB, testCfg).Create(ctx, sp, finance.Bill{
		Direction: finance.Payable, Amount: 5000, AccountID: "bank", CategoryID: "rent", Description: "Aluguel",
		Competence: brcal.New(2026, time.March, 1), Due: brcal.New(2026, time.March, 10), Origin: finance.OriginManual,
	}, repositories.PostMeta{Actor: "u"}, seedNow); err != nil {
		t.Fatal(err)
	}
	if _, err := repositories.NewRecurrenceRepository(testDB, testCfg).Create(ctx, sp, finance.Recurrence{
		Direction: finance.Payable, Amount: 150000, CategoryID: "rent", AccountID: "bank", Description: "Aluguel",
		Schedule: finance.Schedule{Expression: finance.DayOfMonth{Day: 10}, Start: brcal.New(2026, time.January, 1), Adjust: finance.AdjustNone},
	}, repositories.PostMeta{}, seedNow); err != nil {
		t.Fatal(err)
	}
	seedCardWithPurchase(t, sp)       // cards: S and S#CARD#{id}
	seedImportWithLines(t, sp)        // imports: S (IMPORT#, FITID#, CSVMAP#) and S#IMPORT#{id}
	seedByokConnectionWithHook(t, sp) // byok: S (CONNECTION#, SEALED#) and HOOK#{id}
	idem := repositories.NewIdempotencyRepository(testDB, testCfg)
	key := "k-" + id.New()
	if _, err := idem.Claim(ctx, sp.Owner(), sp.Livemode(), key, "h", "/x", seedNow); err != nil {
		t.Fatal(err)
	}
	if err := idem.Complete(ctx, sp.Owner(), sp.Livemode(),
		repositories.IdempotencyRecord{Key: key, RequestHash: "h", Status: 201, Response: "{}", Route: "/x"}, seedNow); err != nil {
		t.Fatal(err)
	}
	recordSeed(t, sp)
}

func TestPurgeSpaceErasesEveryTableAndIsSafeToRepeat(t *testing.T) {
	ctx := context.Background()
	purger := repositories.NewSpacePurger(testDB, testCfg)
	sp := jobSpace(t, "USER#"+id.New(), true)
	other := jobSpace(t, "USER#"+id.New(), true)
	seedFinance(t, sp)
	seedFinance(t, other)

	first, err := purger.PurgeSpace(ctx, sp)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"ledger_accounts", "ledger_transactions", "bills", "recurrences", "cards", "imports", "byok", "audit", "idempotency"} {
		if first[table] == 0 {
			t.Errorf("first run deleted nothing from %s; the seed or the purge skips it", table)
		}
	}
	assertSpaceGone(t, sp)

	second, err := purger.PurgeSpace(ctx, sp)
	if err != nil {
		t.Fatal(err)
	}
	for table, n := range second {
		if n != 0 {
			t.Errorf("second run deleted %d rows from %s; the purge is not idempotent", n, table)
		}
	}
	assertSpaceUntouched(t, other) // a different person's space is exactly as seeded
}

// The purge deletes every child partition before the rows that name it, so a
// run that dies leaves the parents in place and the next run finds what is left.
// Here an earlier run got through the entries and the card partitions and died
// before the imports and hooks.
func TestPurgeResumesAfterAnInterruptedRun(t *testing.T) {
	ctx := context.Background()
	sp := jobSpace(t, "USER#"+id.New(), true)
	seedFinance(t, sp)
	for k := range seededPartitions[sp.PK()] {
		if strings.Contains(k[1], "#ACCOUNT#") || strings.Contains(k[1], "#CARD#") {
			deleteRows(t, k[0], k[1])
		}
	}

	if _, err := repositories.NewSpacePurger(testDB, testCfg).PurgeSpace(ctx, sp); err != nil {
		t.Fatal(err)
	}
	assertSpaceGone(t, sp) // includes the S#IMPORT# and HOOK# partitions
}

// seedCardWithPurchase: the card row and its liability account in S, the
// purchase and statement rows in S#CARD#{id}.
func seedCardWithPurchase(t *testing.T, sp space.ResolvedSpace) {
	t.Helper()
	ctx := context.Background()
	cards := repositories.NewCardRepository(testDB, testCfg)
	card, err := cards.CreateCard(ctx, sp, "Visa", finance.Card{ClosingDay: 3, DueDay: 10, PayingAccountID: "bank"}, seedNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cards.AddPurchase(ctx, sp, card.ID, repositories.Purchase{
		Description: "Geladeira", CategoryID: "rent", Date: brcal.New(2026, time.March, 5), Total: 120000,
		Installments: make([]finance.Installment, 3),
	}, repositories.PostMeta{Actor: "u"}, seedNow); err != nil {
		t.Fatal(err)
	}
}

// seedImportWithLines: the import header, FITID locks and CSV mapping in S, the
// parsed lines in S#IMPORT#{id}.
func seedImportWithLines(t *testing.T, sp space.ResolvedSpace) {
	t.Helper()
	ctx := context.Background()
	imports := repositories.NewImportRepository(testDB, testCfg)
	if _, err := imports.Import(ctx, sp, "bank", statement.FormatOFX, oneLineOFX(t), "", seedNow); err != nil {
		t.Fatal(err)
	}
	if err := imports.PutMapping(ctx, sp, "bank", statement.Mapping{Delimiter: ";", Decimal: ",", DateFormat: statement.DateDMY, SkipRows: 1, Date: 1, Description: 2, Amount: 3}, seedNow); err != nil {
		t.Fatal(err)
	}
}

// seedByokConnectionWithHook: the connection and sealed rows in S, the webhook
// hook in HOOK#{id}, which only the connection's hook_id points to.
func seedByokConnectionWithHook(t *testing.T, sp space.ResolvedSpace) {
	t.Helper()
	ctx := context.Background()
	repo := repositories.NewByokRepository(testDB, testCfg)
	hook := id.New()
	for _, hookID := range []string{hook, hook + repositories.PayoutHookSuffix} {
		if err := repo.PutHook(ctx, &repositories.Hook{
			ID: hookID, Provider: "inter", AccountOrganizationID: sp.Owner(), Livemode: sp.Livemode(), SecretHash: "h",
		}, seedNow); err != nil {
			t.Fatal(err)
		}
	}
	conn := &byokconn.Connection{
		AccountOrganizationID: sp.Owner(), Livemode: sp.Livemode(), Provider: "inter", ID: id.New(),
		Kind: byokconn.KindMerchant, State: byokconn.StateActive, SyncState: byokconn.SyncActive, Version: 1,
		Sealed: "b1.fake", AccountNumber: "1234567", HookID: hook,
	}
	if err := repo.CreateConnection(ctx, conn, "u", "r", seedNow); err != nil {
		t.Fatal(err)
	}
}

// seededPartitions remembers, per space, every (table, partition) seedFinance
// created and how many rows each held, because the child partitions' ids are
// only discoverable while their parents still exist.
var seededPartitions = map[string]map[[2]string]int{}

func itemsUnder(t *testing.T, logical, pk string) []map[string]types.AttributeValue {
	t.Helper()
	var out []map[string]types.AttributeValue
	var start map[string]types.AttributeValue
	for {
		res, err := testDB.Query(ctxT(t), &dynamodb.QueryInput{
			TableName:                 aws.String(repositories.TableName(testCfg, logical)),
			KeyConditionExpression:    aws.String("pk = :pk"),
			ExpressionAttributeValues: map[string]types.AttributeValue{":pk": &types.AttributeValueMemberS{Value: pk}},
			ConsistentRead:            aws.Bool(true),
			ExclusiveStartKey:         start,
		})
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, res.Items...)
		if len(res.LastEvaluatedKey) == 0 {
			return out
		}
		start = res.LastEvaluatedKey
	}
}

func rowsUnder(t *testing.T, logical, pk string) int { return len(itemsUnder(t, logical, pk)) }

func strAttr(it map[string]types.AttributeValue, name string) string {
	if v, ok := it[name].(*types.AttributeValueMemberS); ok {
		return v.Value
	}
	return ""
}

// recordSeed lists the space's partitions (the S partition of every space-keyed
// table plus its account, card, import and hook children) with their row counts.
func recordSeed(t *testing.T, sp space.ResolvedSpace) {
	t.Helper()
	s := sp.PK()
	parts := map[[2]string]int{}
	add := func(table, pk string) { parts[[2]string{table, pk}] = rowsUnder(t, table, pk) }
	for _, tb := range []string{"ledger_accounts", "ledger_transactions", "bills", "recurrences", "cards", "imports", "byok", "audit", "idempotency"} {
		add(tb, s)
	}
	for _, it := range itemsUnder(t, "ledger_accounts", s) {
		if a, ok := strings.CutPrefix(strAttr(it, "sk"), "ACCOUNT#"); ok {
			add("ledger_transactions", repositories.LedgerEntryPK(sp, a))
		}
	}
	for _, it := range itemsUnder(t, "cards", s) {
		if c, ok := strings.CutPrefix(strAttr(it, "sk"), "CARD#"); ok {
			add("cards", repositories.CardPK(sp, c))
		}
	}
	for _, it := range itemsUnder(t, "imports", s) {
		if i, ok := strings.CutPrefix(strAttr(it, "sk"), "IMPORT#"); ok {
			add("imports", repositories.ImportPK(sp, i))
		}
	}
	for _, it := range itemsUnder(t, "byok", s) {
		if h := strAttr(it, "hook_id"); h != "" {
			add("byok", "HOOK#"+h)
			add("byok", "HOOK#"+h+repositories.PayoutHookSuffix)
		}
	}
	for k, n := range parts {
		// An account nobody posted to has no entries; every other partition is
		// seeded on purpose.
		if n == 0 && !strings.Contains(k[1], "#ACCOUNT#") {
			t.Fatalf("seed left %s %s empty: the seed no longer covers it", k[0], k[1])
		}
	}
	seededPartitions[s] = parts
}

func assertSpaceGone(t *testing.T, sp space.ResolvedSpace) {
	t.Helper()
	parts, ok := seededPartitions[sp.PK()]
	if !ok {
		t.Fatal("space was not seeded with seedFinance")
	}
	for k := range parts {
		if n := rowsUnder(t, k[0], k[1]); n != 0 {
			t.Errorf("%s %s still holds %d rows", k[0], k[1], n)
		}
	}
}

func assertSpaceUntouched(t *testing.T, sp space.ResolvedSpace) {
	t.Helper()
	for k, want := range seededPartitions[sp.PK()] {
		if n := rowsUnder(t, k[0], k[1]); n != want {
			t.Errorf("%s %s holds %d rows, seeded %d", k[0], k[1], n, want)
		}
	}
}

func deleteRowsWithPrefix(t *testing.T, logical, pk, skPrefix string) {
	t.Helper()
	for _, it := range itemsUnder(t, logical, pk) {
		if !strings.HasPrefix(strAttr(it, "sk"), skPrefix) {
			continue
		}
		if _, err := testDB.DeleteItem(ctxT(t), &dynamodb.DeleteItemInput{
			TableName: aws.String(repositories.TableName(testCfg, logical)),
			Key:       map[string]types.AttributeValue{"pk": it["pk"], "sk": it["sk"]},
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func deleteRows(t *testing.T, logical, pk string) { deleteRowsWithPrefix(t, logical, pk, "") }

func TestEraseRefDeletesHistoryAndLatestOfThatRefOnly(t *testing.T) {
	ctx := ctxT(t)
	e := newPortal(t)
	levels := repositories.NewLevelRepository(testDB, testCfg)
	mine, other := "USER_"+id.New(), "USER_"+id.New()
	for _, ref := range []string{mine, other} {
		for _, meter := range []string{"finance_spaces", "finance_people"} {
			if err := levels.Append(ctx, &billing.LevelRecord{
				OrganizationID: e.org.ID, Livemode: true, CustomerRef: ref, Meter: meter, Value: 2,
				OccurredAt: seedNow, IdempotencyKey: "k-" + id.New(),
			}, seedNow); err != nil {
				t.Fatal(err)
			}
		}
	}
	n, err := levels.EraseRef(ctx, e.org.ID, true, mine, []string{"finance_spaces", "finance_people", "never_reported"})
	if err != nil || n != 4 { // one history + one latest row per reported meter
		t.Fatalf("EraseRef = %d, %v; want 4", n, err)
	}
	if l, err := levels.Latest(ctx, e.org.ID, true, mine, "finance_spaces"); err == nil && l != nil {
		t.Fatalf("latest of the erased ref survives: %+v", l)
	}
	if l, err := levels.Latest(ctx, e.org.ID, true, other, "finance_spaces"); err != nil || l == nil {
		t.Fatalf("another ref's latest was touched: %+v, %v", l, err)
	}
	if n, err := levels.EraseRef(ctx, e.org.ID, true, mine, []string{"finance_spaces"}); err != nil || n != 0 {
		t.Fatalf("second run = %d, %v; want 0", n, err)
	}
}
