//go:build integration

package integration

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

const credsPath = "/v1.0/console/credentials"

type credentialBody struct {
	ClientID      string `json:"client_id"`
	Livemode      bool   `json:"livemode"`
	Active        bool   `json:"active"`
	Provisioned   bool   `json:"provisioned"`
	ConnectedBy   string `json:"connected_by"`
	ConnectedByMe bool   `json:"connected_by_me"`
	ConnectedAt   string `json:"connected_at"`
}

type credentialList struct {
	Data          []credentialBody `json:"data"`
	MachineScopes []string         `json:"machine_scopes"`
}

type problemBody struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
	Status int    `json:"status"`
}

func (e *apiEnv) writeToken(t *testing.T) string {
	t.Helper()
	return e.sessionToken(t, middleware.ScopeOrganizationRead, middleware.ScopeOrganizationWrite)
}

func (e *apiEnv) connect(t *testing.T, token, clientID string) apiResponse {
	t.Helper()
	return e.consoleWrite(t, http.MethodPost, credsPath, token, "live", `{"client_id":"`+clientID+`"}`)
}

// suspendLive sets suspended_at on e's live tenant, the attribute part 2 writes
// (spec § 6.2), directly: this plan tests what the console does with it.
func suspendLive(t *testing.T, org *billing.Organization) {
	t.Helper()
	_, err := testDB.UpdateItem(ctxT(t), &dynamodb.UpdateItemInput{
		TableName: aws.String(repositories.TableName(testCfg, repositories.TableOrganizations)),
		Key: map[string]types.AttributeValue{
			"pk": &types.AttributeValueMemberS{Value: repositories.TenantPK(org.ID, org.Livemode)},
			"sk": &types.AttributeValueMemberS{Value: repositories.OrganizationSK()},
		},
		UpdateExpression:          aws.String("SET suspended_at = :s"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":s": &types.AttributeValueMemberS{Value: "2026-10-10T12:00:00Z"}},
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestAConnectedClientActsForTheTenant(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	clientID := "cli_" + id.New()
	e.account.client(clientID, e.org.OwnerUserID, true, "billing:invoices:read", "billing:subscriptions:write")

	res := e.connect(t, tok, clientID)
	if res.status != http.StatusCreated {
		t.Fatalf("connect: %d %s", res.status, res.body)
	}
	var got credentialBody
	res.decode(t, &got)
	if got.ClientID != clientID || !got.Livemode || got.Provisioned || !got.ConnectedByMe || got.ConnectedAt == "" {
		t.Fatalf("connect body = %+v", got)
	}

	// The integration now resolves to this tenant.
	m2m := e.token(t, clientID, "", middleware.ScopeInvoicesRead)
	if r := e.do(t, http.MethodGet, "/v1.0/invoices", m2m, "", ""); r.status != http.StatusOK {
		t.Fatalf("M2M call with the connected client: %d %s", r.status, r.body)
	}

	var list credentialList
	e.console(t, credsPath, tok, "live").decode(t, &list)
	if len(list.MachineScopes) != 11 {
		t.Fatalf("machine_scopes = %v", list.MachineScopes)
	}
	found := false
	for _, c := range list.Data {
		if c.ClientID == clientID && c.ConnectedByMe {
			found = true
		}
	}
	if !found {
		t.Fatalf("list = %+v", list.Data)
	}

	// A double click answers 200 with the same credential.
	if again := e.connect(t, tok, clientID); again.status != http.StatusOK {
		t.Fatalf("reconnect: %d %s", again.status, again.body)
	}
}

// Spec § 10.4 and Review Focus 1.
func TestConnectRefusalsNeverSayWhereAClientIs(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	other := newOrg(t, true)
	creds := repositories.NewCredentialRepository(testDB, testCfg)

	strangers := "cli_" + id.New() // owned by someone else, connected to `other`
	e.account.client(strangers, "usr_"+id.New(), true, "billing:invoices:read")
	if err := creds.Create(ctxT(t), &billing.APICredential{ClientID: strangers, OrganizationID: other.ID, Livemode: true, Active: true}, now()); err != nil {
		t.Fatal(err)
	}
	unknown := "cli_" + id.New()

	var a, b problemBody
	ra, rb := e.connect(t, tok, strangers), e.connect(t, tok, unknown)
	ra.decode(t, &a)
	rb.decode(t, &b)
	if ra.status != 422 || a.Code != "client_not_yours" || a != b {
		t.Fatalf("stranger's = %d %+v, unknown = %d %+v; want the same client_not_yours", ra.status, a, rb.status, b)
	}

	mineElsewhere := "cli_" + id.New()
	e.account.client(mineElsewhere, e.org.OwnerUserID, true, "billing:invoices:read")
	if err := creds.Create(ctxT(t), &billing.APICredential{ClientID: mineElsewhere, OrganizationID: other.ID, Livemode: true, Active: true}, now()); err != nil {
		t.Fatal(err)
	}
	res := e.connect(t, tok, mineElsewhere)
	var p problemBody
	res.decode(t, &p)
	if res.status != 422 || p.Code != "client_already_connected" {
		t.Fatalf("already connected: %d %+v", res.status, p)
	}
	if bytes.Contains(res.body, []byte(other.ID)) || bytes.Contains(res.body, []byte(other.DisplayName)) {
		t.Fatalf("the refusal names the other tenant: %s", res.body)
	}
}

func TestClientsThatCannotGetAMachineTokenAreRefused(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	for _, tc := range []struct {
		name   string
		active bool
		scopes []string
		code   string
	}{
		{"sign-in only", true, []string{"openid", "profile"}, "client_has_no_billing_scope"},
		{"billing plus openid", true, []string{"billing:invoices:read", "openid"}, "client_not_machine_only"},
		{"a portal scope", true, []string{"billing:my-invoices:read"}, "client_not_machine_only"},
		{"the console write scope", true, []string{"billing:organization:write"}, "client_not_machine_only"},
		{"another product", true, []string{"billing:invoices:read", "dfe:nfe:read"}, "client_not_machine_only"},
		{"inactive", false, []string{"billing:invoices:read"}, "client_not_machine_only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clientID := "cli_" + id.New()
			e.account.client(clientID, e.org.OwnerUserID, tc.active, tc.scopes...)
			res := e.connect(t, tok, clientID)
			var p problemBody
			res.decode(t, &p)
			if res.status != 422 || p.Code != tc.code {
				t.Fatalf("%d %+v, want 422 %s", res.status, p, tc.code)
			}
		})
	}
}

func TestDisconnectStopsTheIntegrationAtOnce(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	clientID := "cli_" + id.New()
	e.account.client(clientID, e.org.OwnerUserID, true, "billing:invoices:read")
	if r := e.connect(t, tok, clientID); r.status != http.StatusCreated {
		t.Fatalf("connect: %d %s", r.status, r.body)
	}
	if r := e.consoleWrite(t, http.MethodDelete, credsPath+"/"+clientID, tok, "live", ""); r.status != http.StatusNoContent {
		t.Fatalf("disconnect: %d %s", r.status, r.body)
	}
	m2m := e.token(t, clientID, "", middleware.ScopeInvoicesRead)
	if r := e.do(t, http.MethodGet, "/v1.0/invoices", m2m, "", ""); r.status != http.StatusForbidden {
		t.Fatalf("M2M after disconnect: %d %s", r.status, r.body)
	}
}

func TestProvisionedCredentialsAreListedNeverRemoved(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	var list credentialList
	e.console(t, credsPath, tok, "live").decode(t, &list)
	var seeded *credentialBody
	for i := range list.Data {
		if list.Data[i].ClientID == e.client {
			seeded = &list.Data[i]
		}
	}
	if seeded == nil || !seeded.Provisioned || seeded.ConnectedByMe {
		t.Fatalf("seeded credential = %+v", seeded)
	}
	res := e.consoleWrite(t, http.MethodDelete, credsPath+"/"+e.client, tok, "live", "")
	var p problemBody
	res.decode(t, &p)
	if res.status != http.StatusConflict || p.Code != "credential_provisioned" {
		t.Fatalf("%d %+v", res.status, p)
	}
}

// Decision 1: a suspended live tenant may disconnect, never connect.
func TestASuspendedTenantCanOnlyShrinkItsCredentials(t *testing.T) {
	e := newAPI(t)
	tok := e.writeToken(t)
	connected := "cli_" + id.New()
	e.account.client(connected, e.org.OwnerUserID, true, "billing:invoices:read")
	if r := e.connect(t, tok, connected); r.status != http.StatusCreated {
		t.Fatal(r.status)
	}
	suspendLive(t, e.org)

	fresh := "cli_" + id.New()
	e.account.client(fresh, e.org.OwnerUserID, true, "billing:invoices:read")
	res := e.connect(t, tok, fresh)
	var p problemBody
	res.decode(t, &p)
	if res.status != http.StatusForbidden || p.Code != "tenant_suspended" {
		t.Fatalf("connect while suspended: %d %+v", res.status, p)
	}
	if r := e.console(t, credsPath, tok, "live"); r.status != http.StatusOK {
		t.Fatalf("list while suspended: %d", r.status)
	}
	if r := e.consoleWrite(t, http.MethodDelete, credsPath+"/"+connected, tok, "live", ""); r.status != http.StatusNoContent {
		t.Fatalf("disconnect while suspended: %d %s", r.status, r.body)
	}
}

func TestAnAccountOutageIsA503(t *testing.T) {
	e := newAPI(t)
	e.account.clientsDown = true
	res := e.connect(t, e.writeToken(t), "cli_"+id.New())
	var p problemBody
	res.decode(t, &p)
	if res.status != http.StatusServiceUnavailable || p.Code != "account_unavailable" {
		t.Fatalf("%d %+v", res.status, p)
	}
}

func TestConnectNeedsTheWriteScopeAndAWellFormedID(t *testing.T) {
	e := newAPI(t)
	readOnly := e.sessionToken(t, middleware.ScopeOrganizationRead)
	if r := e.connect(t, readOnly, "cli_x"); r.status != http.StatusForbidden {
		t.Fatalf("read-only token: %d", r.status)
	}
	for _, body := range []string{`{}`, `{"client_id":"a/b"}`, `{"client_id":"` + string(bytes.Repeat([]byte("a"), 129)) + `"}`} {
		r := e.consoleWrite(t, http.MethodPost, credsPath, e.writeToken(t), "live", body)
		if r.status != 422 {
			t.Fatalf("%s: %d %s", body, r.status, r.body)
		}
	}
}
