//go:build integration

package integration

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/accountclient"
	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/email"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// appRepo is an application repository whose tenant zero is tz.
func appRepo(t *testing.T, tz string) *repositories.ApplicationRepository {
	t.Helper()
	cfg := *testCfg
	cfg.PortalOrganizationID = tz
	return repositories.NewApplicationRepository(testDB, &cfg)
}

func newApplication(acct string) *billing.Application {
	return &billing.Application{
		ID: id.NewWithPrefix(id.PrefixApplication), AccountOrganizationID: acct, OrganizationName: "Padaria Bom Dia",
		CompanyID: "cmp_1", CompanyLegalName: "Padaria Bom Dia LTDA", CompanyTaxID: "12345678000190",
		Description: "Assinaturas de pão para empresas do bairro", ContactEmail: "contato@padaria.example",
		AppliedBy: "usr_" + id.New(),
	}
}

func orgCustomer(tz, acct string) *billing.Customer {
	return &billing.Customer{
		ID: id.NewWithPrefix(id.PrefixCustomer), OrganizationID: tz, Livemode: true,
		ExternalRef: repositories.OrgRefPrefix + acct, Name: "Padaria Bom Dia", Email: "contato@padaria.example",
	}
}

func apply(t *testing.T, r *repositories.ApplicationRepository, tz, acct string, withCustomer bool) (*billing.Application, bool, error) {
	t.Helper()
	in := repositories.ApplyInput{Application: newApplication(acct), Actor: "user:x", RequestID: "req_1"}
	if withCustomer {
		in.Customer = orgCustomer(tz, acct)
	}
	return r.Apply(ctxT(t), in, now())
}

func TestApplyingCreatesTheTestTenantTheApplicationAndTheCustomerAtOnce(t *testing.T) {
	ctx := ctxT(t)
	tz, acct := "org_"+id.New(), newSpaceOrgID()
	r := appRepo(t, tz)

	app, created, err := apply(t, r, tz, acct, true)
	if err != nil || !created || app.Status != billing.ApplicationPending || app.TenantID == "" {
		t.Fatalf("apply = %+v, %v, %v", app, created, err)
	}
	test, err := orgRepo().GetByAccountOrganization(ctx, acct, false)
	if err != nil || test.ID != app.TenantID || test.PayoutStatus != billing.PayoutNotConfigured ||
		test.LegalName != "Padaria Bom Dia LTDA" || test.IssuerTaxID != "12345678000190" || test.DisplayName != "Padaria Bom Dia" {
		t.Fatalf("test tenant = %+v, %v", test, err)
	}
	if _, err := orgRepo().GetByAccountOrganization(ctx, acct, true); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("a live tenant exists before approval: %v", err)
	}
	if c, err := repositories.NewCustomerRepository(testDB, testCfg).GetByOrganization(ctx, tz, true, acct); err != nil || c.Email != "contato@padaria.example" {
		t.Fatalf("ORG_ customer = %+v, %v", c, err)
	}
	got, err := r.Get(ctx, app.ID)
	if err != nil || got.CompanyTaxID != "12345678000190" {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	// Sealed at rest, like a customer's document.
	raw := rawGet(t, repositories.TableOrganizations, repositories.TenantPK(tz, true), repositories.ApplicationSK(app.ID))
	if v := raw["company_tax_id"].(*types.AttributeValueMemberS).Value; v == "12345678000190" {
		t.Fatal("the company document is stored in the clear")
	}
	if st, err := r.LatestStatus(ctx, acct); err != nil || st != billing.ApplicationPending {
		t.Fatalf("LatestStatus = %q, %v", st, err)
	}
}

func TestASecondApplicationWhilePendingReturnsThePendingOne(t *testing.T) {
	tz, acct := "org_"+id.New(), newSpaceOrgID()
	r := appRepo(t, tz)
	first, _, err := apply(t, r, tz, acct, true)
	if err != nil {
		t.Fatal(err)
	}
	second, created, err := apply(t, r, tz, acct, false)
	if err != nil || created || second.ID != first.ID {
		t.Fatalf("second = %+v, created=%v, %v", second, created, err)
	}
}

// Spec § 10.2: two concurrent applications make one tenant and one pending application.
func TestConcurrentApplicationsMakeOneTenantAndOnePendingApplication(t *testing.T) {
	ctx := ctxT(t)
	tz, acct := "org_"+id.New(), newSpaceOrgID()
	r := appRepo(t, tz)
	var wg sync.WaitGroup
	ids := make([]string, 5)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			app, _, err := apply(t, r, tz, acct, true)
			switch {
			case err == nil:
				ids[i] = app.ID
			case errors.Is(err, repositories.ErrTransactionConflict), errors.Is(err, repositories.ErrConcurrentModification):
				// A lost race the service retries; never a second row.
			default:
				t.Errorf("apply %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	cur, err := r.Current(ctx, acct)
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range ids {
		if got != "" && got != cur.ID {
			t.Fatalf("an apply answered %s, current is %s", got, cur.ID)
		}
	}
	if _, err := orgRepo().GetByAccountOrganization(ctx, acct, false); err != nil {
		t.Fatalf("one test tenant, readable without ambiguity: %v", err)
	}
	page, err := r.List(ctx, billing.ApplicationPending, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, a := range page.Items {
		if a.AccountOrganizationID == acct {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d pending applications for the organization, want 1", n)
	}
}

func TestReapplyingAfterARejectionKeepsTheTenantAndTheHistory(t *testing.T) {
	ctx := ctxT(t)
	tz, acct := "org_"+id.New(), newSpaceOrgID()
	r := appRepo(t, tz)
	first, _, _ := apply(t, r, tz, acct, true)
	if err := r.Decide(ctx, first, billing.ApplicationRejected, "", "Site fora do ar", "user:rev", "req_2", now()); err != nil {
		t.Fatal(err)
	}
	if st, _ := r.LatestStatus(ctx, acct); st != billing.ApplicationRejected {
		t.Fatalf("status after rejection = %q", st)
	}
	second, created, err := apply(t, r, tz, acct, false)
	if err != nil || !created || second.ID == first.ID || second.TenantID != first.TenantID {
		t.Fatalf("re-apply = %+v, %v, %v", second, created, err)
	}
	old, err := r.Get(ctx, first.ID)
	if err != nil || old.Status != billing.ApplicationRejected || old.RejectionReason != "Site fora do ar" {
		t.Fatalf("history = %+v, %v", old, err)
	}
	rejected, _ := r.List(ctx, billing.ApplicationRejected, 100, nil)
	found := false
	for _, a := range rejected.Items {
		found = found || a.ID == first.ID
	}
	if !found {
		t.Fatal("the rejected application left the rejected list")
	}
}

func TestADecisionIsConditionalOnPending(t *testing.T) {
	ctx := ctxT(t)
	tz, acct := "org_"+id.New(), newSpaceOrgID()
	r := appRepo(t, tz)
	app, _, _ := apply(t, r, tz, acct, true)
	if err := r.Decide(ctx, app, billing.ApplicationApproved, "sub_1", "", "user:rev", "req_2", now()); err != nil {
		t.Fatal(err)
	}
	stale := *app
	stale.Status = billing.ApplicationPending
	if err := r.Decide(ctx, &stale, billing.ApplicationRejected, "", "tarde demais", "user:rev2", "req_3", now()); !errors.Is(err, billing.ErrApplicationNotPending) {
		t.Fatalf("deciding an approved application: %v", err)
	}
	got, _ := r.Get(ctx, app.ID)
	if got.Status != billing.ApplicationApproved || got.SubscriptionID != "sub_1" || got.DecidedBy != "user:rev" {
		t.Fatalf("after = %+v", got)
	}
}

func TestTheDecisionLeaseAdmitsOneAtATime(t *testing.T) {
	ctx := ctxT(t)
	r := appRepo(t, "org_"+id.New())
	release, err := r.Lock(ctx, "apl_x", "a", now(), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Lock(ctx, "apl_x", "b", now(), 30*time.Second); !errors.Is(err, repositories.ErrApplicationBusy) {
		t.Fatalf("second lock: %v", err)
	}
	release()
	if _, err := r.Lock(ctx, "apl_x", "c", now(), 30*time.Second); err != nil {
		t.Fatalf("after release: %v", err)
	}
}

func TestApplyingWithoutATenantZeroIsRefused(t *testing.T) {
	r := appRepo(t, "")
	if _, _, err := apply(t, r, "", newSpaceOrgID(), false); !errors.Is(err, repositories.ErrNoTenantZero) {
		t.Fatalf("err = %v", err)
	}
}

// A provisioned test tenant is reused: no second row on the ACCOUNT_ORG key.
func TestApplyReusesAProvisionedTestTenant(t *testing.T) {
	ctx := ctxT(t)
	tz, acct := "org_"+id.New(), newSpaceOrgID()
	existing := newLinkedOrg(t, false, acct)
	r := appRepo(t, tz)
	in := repositories.ApplyInput{Application: newApplication(acct), ExistingTestTenant: existing, Customer: orgCustomer(tz, acct), Actor: "user:x"}
	app, _, err := r.Apply(ctx, in, now())
	if err != nil || app.TenantID != existing.ID {
		t.Fatalf("apply = %+v, %v", app, err)
	}
	if got, err := orgRepo().GetByAccountOrganization(ctx, acct, false); err != nil || got.ID != existing.ID {
		t.Fatalf("lookup = %+v, %v", got, err)
	}
}

type fakeCompanies map[string][]accountclient.Company // org -> companies

func (f fakeCompanies) Companies(_ context.Context, org string) (accountclient.CompanyList, error) {
	list, ok := f[org]
	if !ok {
		return accountclient.CompanyList{}, accountclient.ErrOrganizationNotFound
	}
	return accountclient.CompanyList{Companies: list}, nil
}

func (f fakeCompanies) Company(_ context.Context, org, id string) (*accountclient.Company, error) {
	for _, c := range f[org] {
		if c.ID == id {
			return &c, nil
		}
	}
	return nil, accountclient.ErrCompanyNotFound
}

type outbox struct {
	mu       sync.Mutex
	received []string
	decided  []email.ApplicationDecided
}

func (o *outbox) SendApplicationReceived(_ context.Context, to string, _ email.ApplicationReceived) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.received = append(o.received, to)
	return nil
}

func (o *outbox) SendApplicationDecided(_ context.Context, _ string, n email.ApplicationDecided) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.decided = append(o.decided, n)
	return nil
}

type applicationsFixture struct {
	tz, acct string
	svc      *services.Applications
	subs     *repositories.SubscriptionRepository
	mail     *outbox
}

func newApplicationsFixture(t *testing.T) applicationsFixture {
	t.Helper()
	tz, _ := seedTenantZero(t)
	acct := newSpaceOrgID()
	subs := hookedSubs(tz)
	cat := repositories.NewCatalogRepository(testDB, testCfg)
	inv := services.NewInvoicer(subs, repositories.NewInvoiceRepository(testDB, testCfg), cat, repositories.NewUsageRepository(testDB, testCfg))
	box := &outbox{}
	svc := services.NewApplications(appRepo(t, tz), orgRepo(), repositories.NewCustomerRepository(testDB, testCfg), subs,
		services.NewSubscriber(subs, cat, inv),
		fakeCompanies{acct: {
			{ID: "cmp_1", LegalName: "Padaria Bom Dia LTDA", TaxID: "12345678000190", TaxIDKind: "cnpj"},
			{ID: "cmp_2", LegalName: "Bom Dia Cafés LTDA", TaxID: "98765432000110", TaxIDKind: "cnpj"},
		}}, tz).
		WithMail(box, "time@ctech.example")
	return applicationsFixture{tz: tz, acct: acct, svc: svc, subs: subs, mail: box}
}

func (f applicationsFixture) apply(t *testing.T) *billing.Application {
	t.Helper()
	app, _, err := f.svc.Apply(ctxT(t), services.ApplyInput{
		AccountOrganizationID: f.acct, OrganizationName: "Padaria Bom Dia", UserID: "usr_a", CompanyID: "cmp_1",
		Description: "Assinaturas de pão para empresas", ContactEmail: "c@padaria.example", RequestID: "req_1",
	}, now())
	if err != nil {
		t.Fatal(err)
	}
	return app
}

func TestApplyNeedsOneOfTheOrganizationsCompanies(t *testing.T) {
	f := newApplicationsFixture(t)
	_, _, err := f.svc.Apply(ctxT(t), services.ApplyInput{AccountOrganizationID: f.acct, OrganizationName: "P",
		UserID: "u", CompanyID: "cmp_someone_elses", Description: "x", ContactEmail: "c@x.example"}, now())
	if !errors.Is(err, services.ErrCompanyNotInOrganization) {
		t.Fatalf("err = %v", err)
	}
	if _, err := orgRepo().GetByAccountOrganization(ctxT(t), f.acct, false); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatal("a refused application created a tenant")
	}
}

func TestApplyNotifiesCTechOnceAndCreatesTheCustomerOnce(t *testing.T) {
	f := newApplicationsFixture(t)
	first := f.apply(t)
	again := f.apply(t)
	if again.ID != first.ID || len(f.mail.received) != 1 || f.mail.received[0] != "time@ctech.example" {
		t.Fatalf("again=%s first=%s notices=%v", again.ID, first.ID, f.mail.received)
	}
}

// Spec § 10.2: after approval the live tenant exists once; the subscription is
// prod_billing at R$ 0 in tenant zero, live.
func TestApprovingSubscribesTheOrganizationAndCreatesTheLiveTenantOnce(t *testing.T) {
	ctx := ctxT(t)
	f := newApplicationsFixture(t)
	app := f.apply(t)
	got, err := f.svc.Approve(ctx, app.ID, "user:rev", "req_2", now())
	if err != nil || got.Status != billing.ApplicationApproved || got.SubscriptionID == "" {
		t.Fatalf("approve = %+v, %v", got, err)
	}
	again, err := f.svc.Approve(ctx, app.ID, "user:rev", "req_3", now())
	if err != nil || again.SubscriptionID != got.SubscriptionID || again.DecidedAt != got.DecidedAt {
		t.Fatalf("second approval changed something: %+v, %v", again, err)
	}
	live, err := orgRepo().GetByAccountOrganization(ctx, f.acct, true)
	if err != nil || live.ID != app.TenantID || live.PayoutStatus != billing.PayoutNotConfigured || live.LegalName != "Padaria Bom Dia LTDA" {
		t.Fatalf("live = %+v, %v", live, err)
	}
	sub, err := f.subs.Get(ctx, f.tz, true, got.SubscriptionID)
	if err != nil || sub.OwnerKey != billing.MerchantOwnerKey || sub.Status != billing.SubscriptionActive {
		t.Fatalf("subscription = %+v, %v", sub, err)
	}
	if len(f.mail.decided) != 1 || !f.mail.decided[0].Approved {
		t.Fatalf("decision mail = %+v", f.mail.decided)
	}
}

// Review Focus 3.
func TestConcurrentApprovalsMakeOneSubscriptionAndOneLiveTenant(t *testing.T) {
	ctx := ctxT(t)
	f := newApplicationsFixture(t)
	app := f.apply(t)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.svc.Approve(ctx, app.ID, "user:rev", "req", now()); err != nil && !errors.Is(err, repositories.ErrApplicationBusy) {
				t.Errorf("approve: %v", err)
			}
		}()
	}
	wg.Wait()
	if _, err := f.svc.Approve(ctx, app.ID, "user:rev", "req", now()); err != nil {
		t.Fatal(err)
	}
	customer, _ := repositories.NewCustomerRepository(testDB, testCfg).GetByOrganization(ctx, f.tz, true, f.acct)
	subs, _ := f.subs.ListByCustomer(ctx, f.tz, true, customer.ID, 100)
	n := 0
	for _, s := range subs {
		if s.OwnerKey == billing.MerchantOwnerKey {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d prod_billing subscriptions, want 1", n)
	}
	if _, err := orgRepo().GetByAccountOrganization(ctx, f.acct, true); err != nil {
		t.Fatalf("one live tenant, readable without ambiguity: %v", err)
	}
}

func TestRejectingKeepsTheTestTenantAndAllowsANewApplication(t *testing.T) {
	ctx := ctxT(t)
	f := newApplicationsFixture(t)
	app := f.apply(t)
	got, err := f.svc.Reject(ctx, app.ID, "Site fora do ar", "user:rev", "req", now())
	if err != nil || got.Status != billing.ApplicationRejected {
		t.Fatalf("reject = %+v, %v", got, err)
	}
	if _, err := f.svc.Approve(ctx, app.ID, "user:rev", "req", now()); !errors.Is(err, billing.ErrApplicationNotPending) {
		t.Fatalf("approving a rejected application: %v", err)
	}
	if _, err := orgRepo().GetByAccountOrganization(ctx, f.acct, false); err != nil {
		t.Fatalf("test tenant after rejection: %v", err)
	}
	if again := f.apply(t); again.ID == app.ID || again.Status != billing.ApplicationPending {
		t.Fatalf("re-application = %+v", again)
	}
	if len(f.mail.decided) != 1 || f.mail.decided[0].Approved || f.mail.decided[0].Reason != "Site fora do ar" {
		t.Fatalf("decision mail = %+v", f.mail.decided)
	}
}

// Review Focus 1.
func TestAProvisionedTestTenantIsReusedNotDuplicated(t *testing.T) {
	f := newApplicationsFixture(t)
	existing := newLinkedOrg(t, false, f.acct)
	if app := f.apply(t); app.TenantID != existing.ID {
		t.Fatalf("tenant = %s, want the provisioned %s", app.TenantID, existing.ID)
	}
}

func TestAnOrganizationAlreadyLiveCannotApply(t *testing.T) {
	f := newApplicationsFixture(t)
	newLinkedOrg(t, true, f.acct)
	_, _, err := f.svc.Apply(ctxT(t), services.ApplyInput{AccountOrganizationID: f.acct, OrganizationName: "P",
		UserID: "u", CompanyID: "cmp_1", Description: "x", ContactEmail: "c@x.example"}, now())
	if !errors.Is(err, billing.ErrAlreadyMerchant) {
		t.Fatalf("err = %v", err)
	}
}

// ctech-account not knowing the organization is an answer, not an outage: it
// must not read as companies_unavailable.
func TestCompaniesOfAnUnknownOrganizationIsNotAnOutage(t *testing.T) {
	f := newApplicationsFixture(t)
	if _, err := f.svc.Companies(ctxT(t), newSpaceOrgID()); !errors.Is(err, accountclient.ErrOrganizationNotFound) ||
		errors.Is(err, services.ErrCompaniesUnavailable) {
		t.Fatalf("err = %v", err)
	}
	list, err := f.svc.Companies(ctxT(t), f.acct)
	if err != nil || len(list.Companies) != 2 {
		t.Fatalf("list = %+v, %v", list, err)
	}
}

// A reviewer rejecting while another approves: one decision wins and the
// other is refused or answers the same outcome; never both.
func TestRejectWaitsForAnApprovalInProgress(t *testing.T) {
	ctx := ctxT(t)
	f := newApplicationsFixture(t)
	app := f.apply(t)
	release, err := appRepo(t, f.tz).Lock(ctx, app.ID, "someone", now(), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := f.svc.Reject(ctx, app.ID, "Site fora do ar", "user:rev", "req", now()); !errors.Is(err, repositories.ErrApplicationBusy) {
		t.Fatalf("reject during a decision: %v", err)
	}
}

// The company a reviewer approves is the one the live tenant invoices as
// (spec § 4.1), even when the test tenant was made by an earlier application
// for another company, or provisioned by hand.
func TestTheApprovedCompanyIsTheLiveTenantsIssuer(t *testing.T) {
	ctx := ctxT(t)
	f := newApplicationsFixture(t)
	first := f.apply(t)
	if _, err := f.svc.Reject(ctx, first.ID, "Empresa errada, use a outra", "user:rev", "req", now()); err != nil {
		t.Fatal(err)
	}
	second, _, err := f.svc.Apply(ctx, services.ApplyInput{
		AccountOrganizationID: f.acct, OrganizationName: "Padaria Bom Dia", UserID: "usr_a", CompanyID: "cmp_2",
		Description: "Assinaturas de pão para empresas", ContactEmail: "c@padaria.example", RequestID: "req_2",
	}, now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Approve(ctx, second.ID, "user:rev", "req_3", now()); err != nil {
		t.Fatal(err)
	}
	live, err := orgRepo().GetByAccountOrganization(ctx, f.acct, true)
	if err != nil || live.LegalName != "Bom Dia Cafés LTDA" || live.IssuerTaxID != "98765432000110" {
		t.Fatalf("live issuer = %q %q, %v; want the approved company", live.LegalName, live.IssuerTaxID, err)
	}
}
