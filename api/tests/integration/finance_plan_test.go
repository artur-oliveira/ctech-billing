//go:build integration

package integration

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/provision"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// seedTenantZero applies tenants/ctech.json in live mode under a fresh tenant
// id AND a fresh account organization: since part 1, two tenants may not share
// an ACCOUNT_ORG key, and every test seeds its own tenant zero.
func seedTenantZero(t *testing.T) (tenant, accountOrg string) {
	t.Helper()
	raw, err := os.ReadFile("../../tenants/ctech.json")
	if err != nil {
		t.Fatal(err)
	}
	tenant, accountOrg = "org_"+id.New(), newSpaceOrgID()
	doc := strings.Replace(string(raw), `"id": "ctech"`, `"id": "`+tenant+`"`, 1)
	doc = strings.Replace(doc, `"01a04ed6-1af9-745e-bcf7-d3b66fe52321"`, `"`+accountOrg+`"`, 1)
	plan, err := provision.Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	// Credentials are global by client id: drop them, a test needs none.
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
	return tenant, accountOrg
}

func seedCTechCatalogue(t *testing.T) string {
	t.Helper()
	tenant, _ := seedTenantZero(t)
	return tenant
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
	// Review finding: one subscription too, not only one customer — two would
	// bill the person twice every month.
	customer, err := repositories.NewCustomerRepository(testDB, testCfg).GetByUser(ctxT(t), tenant, true, user)
	if err != nil {
		t.Fatal(err)
	}
	subs, err := repositories.NewSubscriptionRepository(testDB, testCfg).ListByCustomer(ctxT(t), tenant, true, customer.ID, 10)
	if err != nil || len(subs) != 1 {
		t.Fatalf("%d subscriptions for one person (err %v)", len(subs), err)
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
	// Review finding: a subset of a plan (one on-demand price buys unlimited
	// spaces and meters only people) or a mix of two plans is not a plan.
	for _, ids := range [][]string{{"price_dfe_pro_monthly"}, {"price_finance_free"}, {}, {"price_nope"},
		{"price_finance_ondemand_people"}, {"price_finance_ondemand_spaces"},
		{"price_finance_basic_monthly", "price_finance_pro_monthly"},
		{"price_finance_basic_monthly", "price_finance_basic_monthly"}} {
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

type planEnv struct {
	*apiEnv
	account *fakeAccount
	tenant  string
}

func newPlanEnv(t *testing.T) planEnv {
	t.Helper()
	tenant := seedCTechCatalogue(t)
	acct := &fakeAccount{members: map[string][2]string{}, names: map[string]string{}, counts: map[string][2]int{}}
	srv := acct.serve(t)
	e := newAPIWith(t, func(c *config.Config) {
		c.PortalOrganizationID = tenant
		c.AccountBaseURL, c.AccountTokenURL = srv.URL, srv.URL+"/token"
		c.AccountClientID, c.AccountClientSecret = "billing-test", "secret"
	})
	return planEnv{apiEnv: e, account: acct, tenant: tenant}
}

func (p planEnv) call(t *testing.T, token, selector, method, path, body string) apiResponse {
	t.Helper()
	return spacesEnv{apiEnv: p.apiEnv, account: p.account}.in(t, token, selector, method, path, body)
}

type planBody struct {
	Subscription *struct {
		Status   string   `json:"status"`
		PriceIDs []string `json:"price_ids"`
	} `json:"subscription"`
	OpenInvoice *struct {
		ID          string `json:"id"`
		CheckoutURL string `json:"checkout_url"`
	} `json:"open_invoice"`
	Prices []struct {
		ID string `json:"id"`
	} `json:"prices"`
	Usage struct {
		Unavailable bool `json:"unavailable"`
		Spaces      []struct {
			ID                 string `json:"id"`
			People             int    `json:"people"`
			PendingInvitations int    `json:"pending_invitations"`
		} `json:"spaces"`
	} `json:"usage"`
	Invoice *struct {
		ID string `json:"id"`
	} `json:"invoice"`
}

// Spec test 7.
func TestSubscribingFromThePlanScreenCreatesTheCustomerOnce(t *testing.T) {
	p := newPlanEnv(t)
	sub := "usr_" + id.New()
	tok := p.token(t, sub, "sess_"+id.New(), middleware.ScopeFinanceRead, middleware.ScopeFinanceWrite)
	for i := 0; i < 2; i++ {
		r := p.call(t, tok, "personal", http.MethodPost, "/plan", `{"price_ids":["price_finance_basic_monthly"],"name":"Ana"}`)
		if r.status != http.StatusOK {
			t.Fatalf("choose %d: %d %s", i, r.status, r.body)
		}
	}
	var b planBody
	p.call(t, tok, "personal", http.MethodGet, "/plan", "").decode(t, &b)
	if b.Subscription == nil || b.Subscription.Status != "INCOMPLETE" || b.OpenInvoice == nil {
		t.Fatalf("state = %+v", b)
	}
	if _, err := repositories.NewCustomerRepository(testDB, testCfg).GetByExternalRef(ctxT(t), p.tenant, true, "USER_"+sub); err != nil {
		t.Fatal(err)
	}
}

func TestPlanUsageComesFromOwnedPersonalSpaces(t *testing.T) {
	p := newPlanEnv(t)
	sub := "usr_" + id.New()
	owned, shared, org := newSpaceOrgID(), newSpaceOrgID(), newSpaceOrgID()
	p.account.set(owned, sub, "personal", "owner")
	p.account.count(owned, 3, 1)
	p.account.set(shared, sub, "personal", "member")
	p.account.set(org, sub, "organization", "owner")
	var b planBody
	p.call(t, p.token(t, sub, "s", middleware.ScopeFinanceRead), "personal", http.MethodGet, "/plan", "").decode(t, &b)
	if b.Usage.Unavailable || len(b.Usage.Spaces) != 1 || b.Usage.Spaces[0].ID != owned ||
		b.Usage.Spaces[0].People != 3 || b.Usage.Spaces[0].PendingInvitations != 1 {
		t.Fatalf("usage = %+v", b.Usage)
	}
	if len(b.Prices) != 5 {
		t.Fatalf("%d finance prices", len(b.Prices))
	}
}

// Review Focus 2.
func TestUsageUnavailableStillAnswers(t *testing.T) {
	p := newPlanEnv(t)
	sub := "usr_" + id.New()
	ws := newSpaceOrgID()
	p.account.set(ws, sub, "personal", "owner") // no counts: an older ctech-account
	var b planBody
	r := p.call(t, p.token(t, sub, "s", middleware.ScopeFinanceRead), "personal", http.MethodGet, "/plan", "")
	r.decode(t, &b)
	if r.status != 200 || !b.Usage.Unavailable || len(b.Prices) != 5 {
		t.Fatalf("%d %+v", r.status, b)
	}
}

// Review Focus 5.
func TestThePlanIsPessoalOnly(t *testing.T) {
	p := newPlanEnv(t)
	sub := "usr_" + id.New()
	ws, org := newSpaceOrgID(), newSpaceOrgID()
	p.account.set(ws, sub, "personal", "owner")
	p.account.set(org, sub, "organization", "owner")
	tok := p.token(t, sub, "s", middleware.ScopeFinanceRead, middleware.ScopeFinanceWrite)
	for _, sel := range []string{"org:" + ws, "org:" + org, "org:" + newSpaceOrgID()} {
		if r := p.call(t, tok, sel, http.MethodGet, "/plan", ""); r.status != http.StatusNotFound {
			t.Errorf("%s: %d %s", sel, r.status, r.body)
		}
	}
}

func TestAPendingFirstInvoiceIsA409WithItsLink(t *testing.T) {
	p := newPlanEnv(t)
	sub := "usr_" + id.New()
	tok := p.token(t, sub, "s", middleware.ScopeFinanceRead, middleware.ScopeFinanceWrite)
	p.call(t, tok, "personal", http.MethodPost, "/plan", `{"price_ids":["price_finance_basic_monthly"],"name":"Ana"}`)
	r := p.call(t, tok, "personal", http.MethodPost, "/plan", `{"price_ids":["price_finance_pro_monthly"],"name":"Ana"}`)
	if r.status != http.StatusConflict || !strings.Contains(string(r.body), "plan_payment_pending") || !strings.Contains(string(r.body), "invoice_id") {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

// With the wallet configured, choosing a paid plan answers the invoice's
// checkout link — what the screen redirects to.
func TestTheChoiceCarriesTheCheckoutURL(t *testing.T) {
	tenant := seedCTechCatalogue(t)
	acct := &fakeAccount{members: map[string][2]string{}, names: map[string]string{}, counts: map[string][2]int{}}
	srv := acct.serve(t)
	fake := newFakeWallet(t)
	e := newAPIWith(t, func(c *config.Config) {
		c.PortalOrganizationID = tenant
		c.AccountBaseURL, c.AccountTokenURL = srv.URL, srv.URL+"/token"
		c.AccountClientID, c.AccountClientSecret = "billing-test", "secret"
		c.WalletBaseURL, c.WalletTokenURL = fake.server.URL, fake.server.URL+"/token"
		c.WalletClientID, c.WalletClientSecret = "billing", "secret"
		c.WalletWebhookSecret = testWalletSecret
		c.CheckoutLinkSecret = testLinkSecret
		c.CheckoutBaseURL = "https://pay.test/c"
	})
	p := planEnv{apiEnv: e, account: acct, tenant: tenant}
	tok := p.token(t, "usr_"+id.New(), "sess_"+id.New(), middleware.ScopeFinanceRead, middleware.ScopeFinanceWrite)
	r := p.call(t, tok, "personal", http.MethodPost, "/plan", `{"price_ids":["price_finance_basic_monthly"],"name":"Ana"}`)
	if r.status != http.StatusOK || !strings.Contains(string(r.body), `"checkout_url":"https://pay.test/c`) {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

// Review finding: the per-customer lookup was one filtered Query with Limit 100,
// which DynamoDB applies before the filter — a subscriber past the first page of
// the tenant's rows was invisible (Free on screen and in entitlements, and a
// second paid subscription on the next choice).
func TestASubscriberPastTheFirstPageIsFound(t *testing.T) {
	ctx := ctxT(t)
	f := newCatalog(t, "finance")
	price := f.price(t, f.product.ID, billing.PriceFixed, billing.BillAdvance, 0, billing.IntervalMonth)
	for i := 0; i < 60; i++ { // 60 subscriptions + 60 item rows under the same prefix
		if _, _, err := f.subber.Subscribe(ctx, services.SubscribeInput{OrganizationID: f.org.ID, Livemode: true,
			CustomerID: "cus_" + id.New(), Items: []services.SubscribeItem{{PriceID: price.ID}}, Actor: "t"}, now()); err != nil {
			t.Fatal(err)
		}
	}
	target := "cus_" + id.New()
	if _, _, err := f.subber.Subscribe(ctx, services.SubscribeInput{OrganizationID: f.org.ID, Livemode: true,
		CustomerID: target, Items: []services.SubscribeItem{{PriceID: price.ID}}, Actor: "t"}, now()); err != nil {
		t.Fatal(err)
	}
	got, err := f.subs.ListByCustomer(ctx, f.org.ID, true, target, 100)
	if err != nil || len(got) != 1 {
		t.Fatalf("found %d, err %v", len(got), err)
	}
}

// Review finding: the projection read only the newest in-TTL report before the
// period, while the close also falls back to the no-TTL latest level. A level
// unchanged for longer than the TTL projected 0 and was billed in full.
func TestTheProjectionCarriesALevelOlderThanItsTTL(t *testing.T) {
	tenant := seedCTechCatalogue(t)
	plan, levels := newFinancePlan(t, tenant)
	user := "usr_" + id.New()
	old := now().AddDate(-2, 0, 0)
	key := "old-" + id.New()
	if err := levels.Append(ctxT(t), &billing.LevelRecord{OrganizationID: tenant, Livemode: true, CustomerRef: "USER_" + user,
		Meter: "finance_spaces", Value: 3, OccurredAt: old, IdempotencyKey: key}, now()); err != nil {
		t.Fatal(err)
	}
	usageBase := repositories.NewBase(testDB, testCfg, repositories.TableUsage)
	if _, err := usageBase.DeleteItem(ctxT(t), repositories.LevelPK(tenant, true, "USER_"+user, "finance_spaces"), repositories.LevelSK(old, key)); err != nil {
		t.Fatal(err)
	}
	st, err := plan.State(ctxT(t), user, now())
	if err != nil {
		t.Fatal(err)
	}
	if st.Levels["finance_spaces"].Current != 3 {
		t.Fatalf("levels = %+v", st.Levels)
	}
}
