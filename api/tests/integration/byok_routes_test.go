//go:build integration

package integration

import (
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/id"
)

func TestConnectionRoutesRefuseEveryoneButOwnersAndAdmins(t *testing.T) {
	e := newByokAPI(t)
	paths := []struct{ method, path, body string }{
		{"GET", "/v1.0/console/connections", ""}, {"GET", "/v1.0/console/connections/inter", ""},
		{"PUT", "/v1.0/console/connections/inter", "{}"}, {"POST", "/v1.0/console/connections/inter/test", ""},
		{"POST", "/v1.0/console/connections/inter/sync", ""}, {"PUT", "/v1.0/console/connections/inter/onboarding", `{"step":"validation"}`},
		{"DELETE", "/v1.0/console/connections/inter", ""},
	}
	for _, role := range []string{"member", "viewer"} {
		user := "user-" + role + "-" + id.New()
		e.account.set(e.accountOrg, user, "organization", role)
		for _, p := range paths {
			if r := e.consoleAsUser(t, user, p.method, p.path, "test", "org:"+e.accountOrg, p.body); r.status != 403 {
				t.Fatalf("%s %s %s: %d %s", role, p.method, p.path, r.status, r.body)
			}
		}
	}
	other := newSpaceOrgID()
	personalWS := newSpaceOrgID()
	e.account.set(personalWS, e.org.OwnerUserID, "personal", "owner")
	for _, p := range paths {
		for _, selector := range []string{"org:" + other, "personal", "org:" + personalWS} {
			if r := e.byokWrite(t, p.method, p.path, e.sessionToken(t, byokScopes...), "test", selector, p.body); r.status != 404 {
				t.Fatalf("%s %s %s: %d %s", selector, p.method, p.path, r.status, r.body)
			}
		}
	}
	if n := e.byokCalls.Load(); n != 0 {
		t.Fatalf("%d byok table calls before membership", n)
	}
}

func TestTestModeTogglesTheSandboxWithNoCredentials(t *testing.T) {
	e := newByokAPI(t)
	tok := e.sessionToken(t, byokScopes...)
	r := e.byokWrite(t, "PUT", "/v1.0/console/connections/inter", tok, "test", "org:"+e.accountOrg, "{}")
	var dto struct {
		Kind, State  string
		Capabilities []string
	}
	r.decode(t, &dto)
	if r.status != 201 || dto.Kind != "sandbox_shared" || dto.State != "active" || len(dto.Capabilities) != 2 {
		t.Fatalf("%d %+v", r.status, dto)
	}
	if r := e.byokWrite(t, "PUT", "/v1.0/console/connections/inter", tok, "live", "org:"+e.accountOrg, "{}"); r.status != 422 {
		t.Fatalf("live with no credentials: %d %s", r.status, r.body)
	}
}

func TestTheRawConnectionItemHoldsOnlyCiphertext(t *testing.T) {
	e := newByokAPI(t)
	r := e.connectLive(t, "s3cr3t-value")
	if r.status != 201 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	item := e.rawByokItem(t, e.accountOrg+"#live", "CONNECTION#inter")
	for k, v := range item {
		if s, ok := v.(*types.AttributeValueMemberS); ok && (strings.Contains(s.Value, "s3cr3t-value") || strings.Contains(s.Value, "BEGIN")) {
			t.Fatalf("attribute %s holds plaintext", k)
		}
	}
	if !strings.HasPrefix(item["sealed"].(*types.AttributeValueMemberS).Value, "b1.") {
		t.Fatal("sealed is a b1 bundle")
	}
	if strings.Contains(string(r.body), "s3cr3t-value") || strings.Contains(string(r.body), "sealed") {
		t.Fatal("the response carries no secret and no bundle")
	}
	if logs := e.capturedLogs(); strings.Contains(logs, "s3cr3t-value") || strings.Contains(logs, "BEGIN") {
		t.Fatal("the logs carry no secret")
	}
}

func TestUnknownProviderIs404(t *testing.T) {
	e := newByokAPI(t)
	r := e.consoleAs(t, "GET", "/v1.0/console/connections/other", e.sessionToken(t, byokScopes...), "test", "org:"+e.accountOrg, "")
	if r.status != 404 || !strings.Contains(string(r.body), `"resource_not_found"`) {
		t.Fatalf("%d %s", r.status, r.body)
	}
	r = e.consoleAs(t, "GET", "/v1.0/console/connections/inter", e.sessionToken(t, byokScopes...), "test", "org:"+e.accountOrg, "")
	if r.status != 404 || !strings.Contains(string(r.body), `"byok_not_connected"`) {
		t.Fatalf("a known provider never connected: %d %s", r.status, r.body)
	}
}

func TestClassifyRoute(t *testing.T) {
	e := newByokAPI(t)
	tok := e.sessionToken(t, byokScopes...)
	if r := e.byokWrite(t, "PUT", "/v1.0/console/connections/inter", tok, "test", "org:"+e.accountOrg, "{}"); r.status != 201 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	txID := e.postSandboxLine(t, 1500)
	r := e.byokWrite(t, "POST", "/v1.0/console/finance/unclassified/"+txID+"/classify", tok, "test", "org:"+e.accountOrg, `{"category_id":"cat-vendas"}`)
	if r.status != 200 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if r := e.byokWrite(t, "POST", "/v1.0/console/finance/unclassified/"+txID+"/classify", tok, "test", "org:"+e.accountOrg, `{"category_id":"cat-vendas","bill_id":"x"}`); r.status != 422 {
		t.Fatalf("exactly one target: %d %s", r.status, r.body)
	}
}

func TestOnboardingIsRecordedBeforeConnecting(t *testing.T) {
	e := newByokAPI(t)
	tok := e.sessionToken(t, byokScopes...)
	if r := e.byokWrite(t, "PUT", "/v1.0/console/connections/inter/onboarding", tok, "live", "org:"+e.accountOrg, `{"step":"validation"}`); r.status != 204 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	r := e.consoleAs(t, "GET", "/v1.0/console/connections/inter", tok, "live", "org:"+e.accountOrg, "")
	var dto struct {
		State          string `json:"state"`
		OnboardingStep string `json:"onboarding_step"`
	}
	r.decode(t, &dto)
	if r.status != 200 || dto.State != "onboarding" || dto.OnboardingStep != "validation" {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if r := e.connectLive(t, "after-onboarding"); r.status != 201 {
		t.Fatalf("connecting replaces the onboarding row: %d %s", r.status, r.body)
	}
}
