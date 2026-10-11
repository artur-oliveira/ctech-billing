//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/accountclient"
	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// merchantEnv is the whole service with a seeded tenant zero, CTech's
// organization (a reviewer is its admin) and an applicant organization with
// one company.
type merchantEnv struct {
	*apiEnv
	tz, ctechOrg, acct, reviewer, applicant string
}

func newMerchantEnv(t *testing.T) merchantEnv {
	t.Helper()
	tz, ctechOrg := seedTenantZero(t)
	e := newAPIWith(t, func(c *config.Config) { c.PortalOrganizationID = tz })
	m := merchantEnv{apiEnv: e, tz: tz, ctechOrg: ctechOrg, acct: newSpaceOrgID(),
		reviewer: "usr_" + id.New(), applicant: "usr_" + id.New()}
	e.account.name(ctechOrg, "CTech")
	e.account.set(ctechOrg, m.reviewer, "organization", "admin")
	e.account.name(m.acct, "Padaria Bom Dia")
	e.account.set(m.acct, m.applicant, "organization", "owner")
	e.account.company(m.acct, accountclient.Company{ID: "cmp_1", LegalName: "Padaria Bom Dia LTDA", TaxID: "12345678000190", TaxIDKind: "cnpj"})
	return m
}

var merchantScopes = append(append([]string{}, allConsoleScopes...), middleware.ScopeOrganizationWrite)

func (m merchantEnv) as(t *testing.T, sub, method, path, mode, org, body string) apiResponse {
	t.Helper()
	return m.consoleAs(t, method, path, m.token(t, sub, "sess_"+id.New(), merchantScopes...), mode, "org:"+org, body)
}

const applyBody = `{"company_id":"cmp_1","description":"Assinaturas de pão para empresas do bairro","website":"https://padaria.example","expected_monthly_volume":"up_to_10k","contact_email":"c@padaria.example"}`

func (m merchantEnv) apply(t *testing.T) string {
	t.Helper()
	r := m.as(t, m.applicant, http.MethodPost, "/v1.0/console/merchant/application", "test", m.acct, applyBody)
	if r.status != http.StatusCreated {
		t.Fatalf("apply: %d %s", r.status, r.body)
	}
	var b struct {
		ID string `json:"id"`
	}
	r.decode(t, &b)
	return b.ID
}

func statusIn(t *testing.T, m merchantEnv, sub, acct string) string {
	t.Helper()
	r := m.consoleAs(t, http.MethodGet, "/v1.0/console/session", m.token(t, sub, "sess_"+id.New(), merchantScopes...), "", "", "")
	var b struct {
		Organizations []struct {
			ID     string `json:"organization_id"`
			Status string `json:"merchant_status"`
		} `json:"organizations"`
	}
	r.decode(t, &b)
	for _, o := range b.Organizations {
		if o.ID == acct {
			return o.Status
		}
	}
	return "absent"
}

func TestTheApplicantGetsTestModeAtOnceAndLiveAfterApproval(t *testing.T) {
	m := newMerchantEnv(t)
	if s := statusIn(t, m, m.applicant, m.acct); s != "none" {
		t.Fatalf("before = %s", s)
	}
	appID := m.apply(t)
	if r := m.as(t, m.applicant, http.MethodPost, "/v1.0/console/merchant/application", "test", m.acct, applyBody); r.status != http.StatusOK ||
		!bytes.Contains(r.body, []byte(appID)) {
		t.Fatalf("second apply: %d %s", r.status, r.body)
	}
	if s := statusIn(t, m, m.applicant, m.acct); s != "test" {
		t.Fatalf("after applying = %s", s)
	}
	if r := m.as(t, m.applicant, http.MethodGet, "/v1.0/console/settings", "test", m.acct, ""); r.status != http.StatusOK {
		t.Fatalf("test mode: %d %s", r.status, r.body)
	}
	if r := m.as(t, m.applicant, http.MethodGet, "/v1.0/console/settings", "live", m.acct, ""); r.status != http.StatusNotFound ||
		!bytes.Contains(r.body, []byte(`"no_merchant"`)) {
		t.Fatalf("live before approval: %d %s", r.status, r.body)
	}

	list := m.as(t, m.reviewer, http.MethodGet, "/v1.0/console/applications?status=pending", "live", m.ctechOrg, "")
	if list.status != http.StatusOK || !bytes.Contains(list.body, []byte(appID)) || !bytes.Contains(list.body, []byte("Padaria Bom Dia LTDA")) {
		t.Fatalf("pending list: %d %s", list.status, list.body)
	}
	first := m.as(t, m.reviewer, http.MethodPost, "/v1.0/console/applications/"+appID+"/approve", "live", m.ctechOrg, "")
	second := m.as(t, m.reviewer, http.MethodPost, "/v1.0/console/applications/"+appID+"/approve", "live", m.ctechOrg, "")
	if first.status != http.StatusOK || !bytes.Equal(first.body, second.body) {
		t.Fatalf("approvals: %d %s / %d %s", first.status, first.body, second.status, second.body)
	}
	if s := statusIn(t, m, m.applicant, m.acct); s != "live" {
		t.Fatalf("after approval = %s", s)
	}
	if r := m.as(t, m.applicant, http.MethodGet, "/v1.0/console/settings", "live", m.acct, ""); r.status != http.StatusOK {
		t.Fatalf("live after approval: %d %s", r.status, r.body)
	}
}

func TestARejectionCarriesItsReasonAndAllowsApplyingAgain(t *testing.T) {
	m := newMerchantEnv(t)
	appID := m.apply(t)
	path := "/v1.0/console/applications/" + appID + "/reject"
	if r := m.as(t, m.reviewer, http.MethodPost, path, "live", m.ctechOrg, `{"reason":"curto"}`); r.status != http.StatusUnprocessableEntity ||
		!bytes.Contains(r.body, []byte(`"too_short"`)) {
		t.Fatalf("short reason: %d %s", r.status, r.body)
	}
	if r := m.as(t, m.reviewer, http.MethodPost, path, "live", m.ctechOrg, `{"reason":"O site não abre; envie de novo com o endereço certo."}`); r.status != http.StatusOK {
		t.Fatalf("reject: %d %s", r.status, r.body)
	}
	if s := statusIn(t, m, m.applicant, m.acct); s != "rejected" {
		t.Fatalf("status = %s", s)
	}
	r := m.as(t, m.applicant, http.MethodGet, "/v1.0/console/merchant/application", "test", m.acct, "")
	if r.status != http.StatusOK || !bytes.Contains(r.body, []byte("O site não abre")) {
		t.Fatalf("applicant view: %d %s", r.status, r.body)
	}
	if again := m.apply(t); again == appID {
		t.Fatal("re-application returned the rejected one")
	}
}

// Spec § 10.1, tenant-zero half: a review route from any other tenant is 404.
func TestReviewRoutesAreNotFoundFromAnyOtherTenant(t *testing.T) {
	m := newMerchantEnv(t)
	appID := m.apply(t)
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/v1.0/console/applications?status=pending"},
		{http.MethodPost, "/v1.0/console/applications/" + appID + "/approve"},
		{http.MethodPost, "/v1.0/console/applications/" + appID + "/reject"},
	} {
		r := m.as(t, m.applicant, c.method, c.path, "test", m.acct, `{"reason":"tentando aprovar a mim mesmo"}`)
		if r.status != http.StatusNotFound || !bytes.Contains(r.body, []byte(`"resource_not_found"`)) || bytes.Contains(r.body, []byte(appID)) {
			t.Fatalf("%s %s from the applicant's tenant: %d %s", c.method, c.path, r.status, r.body)
		}
	}
}

func TestMerchantRoutesRefuseEveryoneButOwnersAndAdmins(t *testing.T) {
	m := newMerchantEnv(t)
	member, viewer := "usr_"+id.New(), "usr_"+id.New()
	m.account.set(m.acct, member, "organization", "member")
	m.account.set(m.acct, viewer, "organization", "viewer")
	routes := []struct{ method, path string }{
		{http.MethodGet, "/v1.0/console/merchant/application"},
		{http.MethodPost, "/v1.0/console/merchant/application"},
		{http.MethodGet, "/v1.0/console/merchant/companies"},
	}
	for _, r := range routes {
		for _, sub := range []string{member, viewer} {
			if got := m.as(t, sub, r.method, r.path, "test", m.acct, applyBody); got.status != http.StatusForbidden ||
				!bytes.Contains(got.body, []byte(`"merchant_role_required"`)) {
				t.Errorf("%s %s as %s: %d %s", r.method, r.path, sub, got.status, got.body)
			}
		}
		tok := m.token(t, m.applicant, "sess_"+id.New(), merchantScopes...)
		if got := m.consoleAs(t, r.method, r.path, tok, "test", "personal", applyBody); got.status != http.StatusNotFound {
			t.Errorf("%s %s personal: %d", r.method, r.path, got.status)
		}
		if got := m.consoleAs(t, r.method, r.path, m.token(t, m.client, "", merchantScopes...), "test", "org:"+m.acct, applyBody); got.status != http.StatusForbidden {
			t.Errorf("%s %s m2m: %d", r.method, r.path, got.status)
		}
	}
	if _, err := orgRepo().GetByAccountOrganization(ctxT(t), m.acct, false); err == nil {
		t.Fatal("a refused request created a tenant")
	}
}

func TestApplyValidatesEveryFieldAtOnce(t *testing.T) {
	m := newMerchantEnv(t)
	r := m.as(t, m.applicant, http.MethodPost, "/v1.0/console/merchant/application", "test", m.acct,
		`{"description":"curta","website":"http://x.example","expected_monthly_volume":"muito","contact_email":"nope"}`)
	if r.status != http.StatusUnprocessableEntity {
		t.Fatalf("%d %s", r.status, r.body)
	}
	var b struct {
		Errors []struct{ Field, Code string } `json:"errors"`
	}
	_ = json.Unmarshal(r.body, &b)
	got := map[string]string{}
	for _, e := range b.Errors {
		got[e.Field] = e.Code
	}
	want := map[string]string{"company_id": "required", "description": "too_short", "website": "invalid_url",
		"expected_monthly_volume": "unsupported_value", "contact_email": "invalid_email"}
	for f, c := range want {
		if got[f] != c {
			t.Errorf("%s = %q, want %q (all: %v)", f, got[f], c, got)
		}
	}
	if r := m.as(t, m.applicant, http.MethodPost, "/v1.0/console/merchant/application", "test", m.acct,
		`{"company_id":"cmp_not_ours","description":"Assinaturas de pão para empresas do bairro","contact_email":"c@x.example"}`); r.status != http.StatusUnprocessableEntity ||
		!bytes.Contains(r.body, []byte(`"not_found"`)) {
		t.Fatalf("someone else's company: %d %s", r.status, r.body)
	}
}

// Review Focus 4.
func TestApplyingWhileCtechAccountIsDownWritesNothing(t *testing.T) {
	m := newMerchantEnv(t)
	m.account.mu.Lock()
	m.account.companiesDown = true
	m.account.mu.Unlock()
	r := m.as(t, m.applicant, http.MethodPost, "/v1.0/console/merchant/application", "test", m.acct, applyBody)
	if r.status != http.StatusServiceUnavailable || !bytes.Contains(r.body, []byte(`"companies_unavailable"`)) {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if _, err := orgRepo().GetByAccountOrganization(ctxT(t), m.acct, false); err == nil {
		t.Fatal("a tenant was created while ctech-account was down")
	}
	if _, err := repositories.NewCustomerRepository(testDB, testCfg).GetByOrganization(ctxT(t), m.tz, true, m.acct); err == nil {
		t.Fatal("a customer was created while ctech-account was down")
	}
}

func TestApplyingNeedsTheWriteScope(t *testing.T) {
	m := newMerchantEnv(t)
	tok := m.token(t, m.applicant, "sess_"+id.New(), allConsoleScopes...)
	if r := m.consoleAs(t, http.MethodPost, "/v1.0/console/merchant/application", tok, "test", "org:"+m.acct, applyBody); r.status != http.StatusForbidden {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

// The picker says when ctech-account cut the list (it returns at most 200 by
// legal name), so the form never presents a cut list as the whole one.
func TestTheCompanyPickerSaysWhenTheListWasCut(t *testing.T) {
	m := newMerchantEnv(t)
	r := m.as(t, m.applicant, http.MethodGet, "/v1.0/console/merchant/companies", "test", m.acct, "")
	if r.status != http.StatusOK || !bytes.Contains(r.body, []byte(`"truncated":false`)) || !bytes.Contains(r.body, []byte(`"cmp_1"`)) {
		t.Fatalf("companies: %d %s", r.status, r.body)
	}
	m.account.mu.Lock()
	m.account.companiesTruncated = true
	m.account.mu.Unlock()
	if r := m.as(t, m.applicant, http.MethodGet, "/v1.0/console/merchant/companies", "test", m.acct, ""); r.status != http.StatusOK ||
		!bytes.Contains(r.body, []byte(`"truncated":true`)) {
		t.Fatalf("cut list: %d %s", r.status, r.body)
	}
}

// ctech-account answering 404 for the organization is "not found", never the
// 503 that would tell the person to try again.
func TestCompaniesOfAnOrganizationCtechAccountDoesNotKnowIsNotFound(t *testing.T) {
	m := newMerchantEnv(t)
	m.account.mu.Lock()
	m.account.companiesUnknown = true
	m.account.mu.Unlock()
	r := m.as(t, m.applicant, http.MethodGet, "/v1.0/console/merchant/companies", "test", m.acct, "")
	if r.status != http.StatusNotFound || !bytes.Contains(r.body, []byte(`"space_not_found"`)) {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

// An organization CTech provisioned by hand (a test tenant, no application) is
// not "under review": the session says it never applied, so the console offers
// the application instead of a review nobody is doing.
func TestTheSessionTellsAProvisionedTestTenantFromAPendingApplication(t *testing.T) {
	m := newMerchantEnv(t)
	newLinkedOrg(t, false, m.acct)
	appStatus := func() (string, string) {
		r := m.consoleAs(t, http.MethodGet, "/v1.0/console/session", m.token(t, m.applicant, "sess_"+id.New(), merchantScopes...), "", "", "")
		var b struct {
			Organizations []struct {
				ID          string `json:"organization_id"`
				Status      string `json:"merchant_status"`
				Application string `json:"application_status"`
			} `json:"organizations"`
		}
		r.decode(t, &b)
		for _, o := range b.Organizations {
			if o.ID == m.acct {
				return o.Status, o.Application
			}
		}
		return "absent", ""
	}
	if s, a := appStatus(); s != "test" || a != "" {
		t.Fatalf("provisioned, never applied: %s / %q", s, a)
	}
	m.apply(t)
	if s, a := appStatus(); s != "test" || a != "pending" {
		t.Fatalf("after applying: %s / %q", s, a)
	}
}
