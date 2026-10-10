//go:build integration

package integration

import (
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/provision"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// seedCTechCatalogue applies tenants/ctech.json under a fresh organization id.
func seedCTechCatalogue(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("../../tenants/ctech.json")
	if err != nil {
		t.Fatal(err)
	}
	orgID := "org_" + id.New()
	doc := strings.Replace(string(raw), `"id": "ctech"`, `"id": "`+orgID+`"`, 1)
	// Credentials are global by client id: drop them, a test needs none.
	plan, err := provision.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	plan.Credentials = nil
	plan.Endpoints = nil
	repos := provision.Repos{
		Organizations: repositories.NewOrganizationRepository(testDB, testCfg),
		Credentials:   repositories.NewCredentialRepository(testDB, testCfg),
		Catalog:       repositories.NewCatalogRepository(testDB, testCfg),
		Webhooks:      repositories.NewWebhookRepository(testDB, testCfg),
	}
	if _, err := provision.Apply(ctxT(t), repos, plan, true, now()); err != nil {
		t.Fatal(err)
	}
	return orgID
}

func newFinancePlan(t *testing.T, tenant string) (*services.FinancePlan, *repositories.LevelRepository) {
	t.Helper()
	subs := repositories.NewSubscriptionRepository(testDB, testCfg)
	inv := repositories.NewInvoiceRepository(testDB, testCfg)
	cat := repositories.NewCatalogRepository(testDB, testCfg)
	customers := repositories.NewCustomerRepository(testDB, testCfg)
	levels := repositories.NewLevelRepository(testDB, testCfg)
	invoicer := services.NewInvoicer(subs, inv, cat, repositories.NewUsageRepository(testDB, testCfg)).WithLevels(levels, customers)
	return services.NewFinancePlan(customers, subs, inv, cat, levels, services.NewSubscriber(subs, cat, invoicer), tenant), levels
}

func TestChoosingBasicCreatesTheUserCustomerAndAnInvoice(t *testing.T) {
	tenant := seedCTechCatalogue(t)
	plan, _ := newFinancePlan(t, tenant)
	user := "usr_" + id.New()
	sub, inv, err := plan.Choose(ctxT(t), services.ChooseInput{UserID: user, Name: "Ana", PriceIDs: []string{"price_finance_basic_monthly"}}, now())
	if err != nil {
		t.Fatal(err)
	}
	if sub.Status != billing.SubscriptionIncomplete || inv == nil || inv.Total != 1990 {
		t.Fatalf("sub %+v inv %+v", sub, inv)
	}
	c, err := repositories.NewCustomerRepository(testDB, testCfg).GetByUser(ctxT(t), tenant, true, user)
	if err != nil || c.ExternalRef != "USER_"+user {
		t.Fatalf("customer = %+v, %v", c, err)
	}
}

// Review Focus 1.
func TestTwoFirstChoicesMakeOneCustomer(t *testing.T) {
	tenant := seedCTechCatalogue(t)
	plan, _ := newFinancePlan(t, tenant)
	user := "usr_" + id.New()
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = plan.Choose(ctxT(t), services.ChooseInput{UserID: user, Name: "Ana", PriceIDs: []string{"price_finance_pro_monthly"}}, now())
		}()
	}
	wg.Wait()
	page, err := repositories.NewCustomerRepository(testDB, testCfg).List(ctxT(t), tenant, true, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range page.Items {
		if c.UserID == user {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d customers for one person", n)
	}
}

// Review Focus 4.
func TestAnUnpaidFirstInvoiceIsPaidBeforeChanging(t *testing.T) {
	tenant := seedCTechCatalogue(t)
	plan, _ := newFinancePlan(t, tenant)
	user := "usr_" + id.New()
	_, first, err := plan.Choose(ctxT(t), services.ChooseInput{UserID: user, Name: "Ana", PriceIDs: []string{"price_finance_basic_monthly"}}, now())
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = plan.Choose(ctxT(t), services.ChooseInput{UserID: user, Name: "Ana", PriceIDs: []string{"price_finance_pro_monthly"}}, now())
	var pending *services.PlanPaymentPending
	if !errors.As(err, &pending) || pending.Invoice.ID != first.ID {
		t.Fatalf("err = %v", err)
	}
}

func TestOnDemandStartsActiveAndProjectsFromLevels(t *testing.T) {
	tenant := seedCTechCatalogue(t)
	plan, levels := newFinancePlan(t, tenant)
	user := "usr_" + id.New()
	if _, _, err := plan.Choose(ctxT(t), services.ChooseInput{UserID: user, Name: "Ana",
		PriceIDs: []string{"price_finance_ondemand_spaces", "price_finance_ondemand_people"}}, now()); err != nil {
		t.Fatal(err)
	}
	for meter, v := range map[string]int64{"finance_spaces": 5, "finance_people": 4} {
		if err := levels.Append(ctxT(t), &billing.LevelRecord{OrganizationID: tenant, Livemode: true, CustomerRef: "USER_" + user,
			Meter: meter, Value: v, OccurredAt: now().Add(-time.Hour), IdempotencyKey: id.New()}, now()); err != nil {
			t.Fatal(err)
		}
	}
	st, err := plan.State(ctxT(t), user, now())
	if err != nil {
		t.Fatal(err)
	}
	// (5-1)×490 + (4-1)×290 = 1960 + 870
	if st.Subscription == nil || st.Subscription.Status != billing.SubscriptionActive || st.Projected != 2830 {
		t.Fatalf("state = %+v", st)
	}
	if st.Levels["finance_spaces"].Peak != 5 {
		t.Fatalf("levels = %+v", st.Levels)
	}
}

// Decision 5a, through the service: a paid Basic switching to Sob demanda
// keeps Basic until the period ends.
func TestBasicToOnDemandIsScheduledForThePeriodEnd(t *testing.T) {
	tenant := seedCTechCatalogue(t)
	plan, _ := newFinancePlan(t, tenant)
	user := "usr_" + id.New()
	sub, first, err := plan.Choose(ctxT(t), services.ChooseInput{UserID: user, Name: "Ana", PriceIDs: []string{"price_finance_basic_monthly"}}, now())
	if err != nil {
		t.Fatal(err)
	}
	payInvoice(t, tenant, first) // mark PAID → ACTIVE, the way checkout_test.go settles one
	_, inv, err := plan.Choose(ctxT(t), services.ChooseInput{UserID: user, Name: "Ana",
		PriceIDs: []string{"price_finance_ondemand_spaces", "price_finance_ondemand_people"}}, now())
	if err != nil || inv != nil {
		t.Fatalf("inv %+v err %v", inv, err)
	}
	st, _ := plan.State(ctxT(t), user, now())
	if len(st.PriceIDs) != 1 || st.PriceIDs[0] != "price_finance_basic_monthly" || st.PendingChange == nil || st.PendingChange.At != sub.CurrentPeriod().End {
		t.Fatalf("state = %+v", st)
	}
	// Choosing Basic again drops the scheduled change.
	if _, _, err := plan.Choose(ctxT(t), services.ChooseInput{UserID: user, Name: "Ana", PriceIDs: []string{"price_finance_basic_monthly"}}, now()); err != nil {
		t.Fatal(err)
	}
	if st, _ := plan.State(ctxT(t), user, now()); st.PendingChange != nil {
		t.Fatal("the scheduled change survived choosing the current plan")
	}
}

func TestOnDemandToBasicIsImmediate(t *testing.T) {
	tenant := seedCTechCatalogue(t)
	plan, _ := newFinancePlan(t, tenant)
	user := "usr_" + id.New()
	_, _, _ = plan.Choose(ctxT(t), services.ChooseInput{UserID: user, Name: "A", PriceIDs: []string{"price_finance_ondemand_spaces", "price_finance_ondemand_people"}}, now())
	_, inv, err := plan.Choose(ctxT(t), services.ChooseInput{UserID: user, Name: "A", PriceIDs: []string{"price_finance_basic_monthly"}}, now())
	if err != nil || inv == nil || inv.Total <= 0 {
		t.Fatalf("an immediate prorated Basic invoice, got %+v %v", inv, err)
	}
	st, _ := plan.State(ctxT(t), user, now())
	if len(st.PriceIDs) != 1 || st.PriceIDs[0] != "price_finance_basic_monthly" || st.Subscription.Timing != billing.BillAdvance {
		t.Fatalf("state = %+v", st)
	}
}

func TestOnlyFinancePlanPricesAreAccepted(t *testing.T) {
	tenant := seedCTechCatalogue(t)
	plan, _ := newFinancePlan(t, tenant)
	for _, ids := range [][]string{{"price_dfe_pro_monthly"}, {"price_finance_free"}, {}, {"price_nope"}} {
		if _, _, err := plan.Choose(ctxT(t), services.ChooseInput{UserID: "usr_" + id.New(), Name: "A", PriceIDs: ids}, now()); !errors.Is(err, services.ErrNotAPlanPrice) {
			t.Errorf("%v: %v", ids, err)
		}
	}
}

func TestACustomerUnderAnotherRefIsRefused(t *testing.T) {
	tenant := seedCTechCatalogue(t)
	plan, _ := newFinancePlan(t, tenant)
	user := "usr_" + id.New()
	if err := repositories.NewCustomerRepository(testDB, testCfg).Create(ctxT(t), &billing.Customer{
		ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: tenant, Livemode: true, Name: "Ana", UserID: user, ExternalRef: "crm_42",
	}, "test", "r", now()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := plan.Choose(ctxT(t), services.ChooseInput{UserID: user, Name: "Ana", PriceIDs: []string{"price_finance_basic_monthly"}}, now()); !errors.Is(err, services.ErrCustomerRefMismatch) {
		t.Fatalf("err = %v", err)
	}
}

func TestCancelGoesBackToFreeAtPeriodEnd(t *testing.T) {
	tenant := seedCTechCatalogue(t)
	plan, _ := newFinancePlan(t, tenant)
	user := "usr_" + id.New()
	if _, err := plan.Cancel(ctxT(t), user, "r", now()); !errors.Is(err, services.ErrNoFinancePlan) {
		t.Fatalf("nothing to cancel: %v", err)
	}
	_, _, _ = plan.Choose(ctxT(t), services.ChooseInput{UserID: user, Name: "A", PriceIDs: []string{"price_finance_ondemand_spaces", "price_finance_ondemand_people"}}, now())
	sub, err := plan.Cancel(ctxT(t), user, "r", now())
	if err != nil || !sub.CancelAtPeriodEnd {
		t.Fatalf("sub = %+v, %v", sub, err)
	}
}

// payInvoice settles a first invoice the short way: the invoice PAID and its
// INCOMPLETE subscription ACTIVE, which is what the test needs (not the PIX path).
func payInvoice(t *testing.T, tenant string, inv *billing.Invoice) {
	t.Helper()
	ctx := ctxT(t)
	if inv == nil {
		t.Fatal("no invoice to pay")
	}
	if _, err := repositories.NewInvoiceRepository(testDB, testCfg).Transition(ctx, inv, billing.InvoicePaid, billing.CauseManualPayment, "test", "r", now()); err != nil {
		t.Fatal(err)
	}
	subs := repositories.NewSubscriptionRepository(testDB, testCfg)
	sub, err := subs.Get(ctx, tenant, true, inv.SubscriptionID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := subs.Transition(ctx, sub, billing.SubscriptionActive, billing.CauseInvoicePaid, "test", "r", now()); err != nil {
		t.Fatal(err)
	}
}
