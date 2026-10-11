//go:build integration

package integration

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
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
