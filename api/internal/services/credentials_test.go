package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/api-commons/oauthclients"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

var machine = []string{"billing:invoices:read", "billing:subscriptions:write", "billing:usage:write"}

type fakeClients struct {
	got   map[string]oauthclients.OAuthClient
	err   error
	asked int
}

func (f *fakeClients) Get(_ context.Context, id string) (oauthclients.OAuthClient, error) {
	f.asked++
	if f.err != nil {
		return oauthclients.OAuthClient{}, f.err
	}
	c, ok := f.got[id]
	if !ok {
		return oauthclients.OAuthClient{}, oauthclients.ErrNotFound
	}
	return c, nil
}

type fakeStore struct {
	here       *billing.APICredential // what Get finds in this tenant
	existing   *billing.APICredential
	resolveErr error
	connectErr error
	connected  []*billing.APICredential
}

func (f *fakeStore) Resolve(context.Context, string) (*billing.APICredential, error) {
	if f.resolveErr != nil {
		return nil, f.resolveErr
	}
	if f.existing == nil {
		return nil, repositories.ErrNotFound
	}
	return f.existing, nil
}

func (f *fakeStore) Get(_ context.Context, org string, livemode bool, id string) (*billing.APICredential, error) {
	if f.here != nil && f.here.OrganizationID == org && f.here.Livemode == livemode && f.here.ClientID == id {
		return f.here, nil
	}
	return nil, repositories.ErrNotFound
}

func (f *fakeStore) Connect(_ context.Context, c *billing.APICredential, _, _ string, _ time.Time) error {
	if f.connectErr != nil {
		return f.connectErr
	}
	f.connected = append(f.connected, c)
	return nil
}

func req(client string) ConnectRequest {
	return ConnectRequest{OrganizationID: "org_a", Livemode: false, ClientID: client, UserID: "usr_me", RequestID: "req", Now: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
}

func mine(scopes ...string) oauthclients.OAuthClient {
	return oauthclients.OAuthClient{ClientID: "c", OwnerUserID: "usr_me", AllowedScopes: scopes, Active: true}
}

func refusal(t *testing.T, err error) ConnectRefusal {
	t.Helper()
	var r *ConnectRefusedError
	if !errors.As(err, &r) {
		t.Fatalf("err = %v, want a refusal", err)
	}
	return r.Code
}

func TestTheConnectRulesInOrder(t *testing.T) {
	someoneElses := mine("billing:invoices:read")
	someoneElses.OwnerUserID = "usr_other"
	inactive := mine("billing:invoices:read")
	inactive.Active = false
	for _, tc := range []struct {
		name   string
		client *oauthclients.OAuthClient
		store  fakeStore
		want   ConnectRefusal
	}{
		{name: "unknown", client: nil, want: RefusedNotYours},
		{name: "someone else's", client: &someoneElses, want: RefusedNotYours},
		// Ownership first: a stranger never learns the client is in use.
		{name: "someone else's and connected elsewhere", client: &someoneElses,
			store: fakeStore{existing: &billing.APICredential{ClientID: "c", OrganizationID: "org_b"}}, want: RefusedNotYours},
		{name: "sign-in only", client: new(mine("openid", "profile")), want: RefusedNoBillingScope},
		{name: "no scopes", client: new(mine()), want: RefusedNoBillingScope},
		{name: "billing and openid", client: new(mine("billing:invoices:read", "openid")), want: RefusedNotMachineOnly},
		{name: "a user billing scope", client: new(mine("billing:my-invoices:read")), want: RefusedNotMachineOnly},
		{name: "another product's scope", client: new(mine("billing:invoices:read", "dfe:nfe:read")), want: RefusedNotMachineOnly},
		{name: "an internal scope", client: new(mine("billing:invoices:read", "internal:account:org-member")), want: RefusedNotMachineOnly},
		{name: "inactive", client: &inactive, want: RefusedNotMachineOnly},
		{name: "connected to another tenant", client: new(mine("billing:invoices:read")),
			store: fakeStore{existing: &billing.APICredential{ClientID: "c", OrganizationID: "org_b"}}, want: RefusedAlreadyConnected},
		{name: "connected to this tenant in the other mode", client: new(mine("billing:invoices:read")),
			store: fakeStore{existing: &billing.APICredential{ClientID: "c", OrganizationID: "org_a", Livemode: true}}, want: RefusedAlreadyConnected},
		{name: "switched off by billing", client: new(mine("billing:invoices:read")),
			store: fakeStore{resolveErr: billing.ErrCredentialInactive}, want: RefusedAlreadyConnected},
		{name: "lost the race", client: new(mine("billing:invoices:read")),
			store: fakeStore{connectErr: repositories.ErrCredentialTaken}, want: RefusedAlreadyConnected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clients := &fakeClients{got: map[string]oauthclients.OAuthClient{}}
			if tc.client != nil {
				clients.got["c"] = *tc.client
			}
			store := tc.store
			_, _, err := NewCredentialConnector(clients, &store, machine).Connect(context.Background(), req("c"))
			if got := refusal(t, err); got != tc.want {
				t.Fatalf("refusal = %s, want %s", got, tc.want)
			}
			if len(store.connected) != 0 {
				t.Fatal("a refused client was written")
			}
		})
	}
}

func TestAMachineOnlyClientOfTheConnectorIsAdmitted(t *testing.T) {
	clients := &fakeClients{got: map[string]oauthclients.OAuthClient{"c": mine("billing:invoices:read", "billing:usage:write")}}
	store := &fakeStore{}
	cred, created, err := NewCredentialConnector(clients, store, machine).Connect(context.Background(), req("c"))
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	if cred.OrganizationID != "org_a" || cred.Livemode || !cred.Active || cred.ConnectedBy != "usr_me" ||
		cred.ConnectedAt != "2026-10-10T12:00:00Z" || len(store.connected) != 1 {
		t.Fatalf("cred = %+v", cred)
	}
}

// A double click: the same client, the same tenant and mode.
func TestReconnectingHereReturnsTheCredential(t *testing.T) {
	existing := &billing.APICredential{ClientID: "c", OrganizationID: "org_a", Livemode: false, Active: true, ConnectedBy: "usr_me"}
	clients := &fakeClients{got: map[string]oauthclients.OAuthClient{"c": mine("billing:invoices:read")}}
	store := &fakeStore{existing: existing}
	cred, created, err := NewCredentialConnector(clients, store, machine).Connect(context.Background(), req("c"))
	if err != nil || created || cred != existing || len(store.connected) != 0 {
		t.Fatalf("cred=%v created=%v err=%v", cred, created, err)
	}
}

func TestAnAccountOutageIsNotARefusal(t *testing.T) {
	clients := &fakeClients{err: errors.New("ctech-account answered 503")}
	_, _, err := NewCredentialConnector(clients, &fakeStore{}, machine).Connect(context.Background(), req("c"))
	if !errors.Is(err, ErrAccountUnavailable) {
		t.Fatalf("err = %v, want ErrAccountUnavailable", err)
	}
}

// Final review: a double click whose second request misses the first one's row
// in the (eventually consistent) lookup index loses on the guard; it is the same
// tenant's admission, so the answer is the credential, not a refusal.
func TestADoubleClickThatLosesOnTheGuardReturnsTheCredential(t *testing.T) {
	existing := &billing.APICredential{ClientID: "c", OrganizationID: "org_a", Livemode: false, Active: true, ConnectedBy: "usr_me"}
	clients := &fakeClients{got: map[string]oauthclients.OAuthClient{"c": mine("billing:invoices:read")}}
	store := &fakeStore{connectErr: repositories.ErrCredentialTaken, here: existing}
	cred, created, err := NewCredentialConnector(clients, store, machine).Connect(context.Background(), req("c"))
	if err != nil || created || cred != existing {
		t.Fatalf("cred=%v created=%v err=%v", cred, created, err)
	}
}
