//go:build integration

package integration

import (
	"errors"
	"sync"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

func consoleCred(org *billing.Organization, clientID string) *billing.APICredential {
	return &billing.APICredential{
		ClientID: clientID, OrganizationID: org.ID, Livemode: org.Livemode, Active: true,
		ConnectedBy: "usr_" + id.New(), ConnectedAt: now().UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
}

func TestAConnectedCredentialResolvesListsAndIsAudited(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewCredentialRepository(testDB, testCfg)
	org := newOrg(t, true)
	clientID := "cli_" + id.New()
	cred := consoleCred(org, clientID)
	if err := repo.Connect(ctx, cred, "user:"+cred.ConnectedBy, "req_1", now()); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Resolve(ctx, clientID)
	if err != nil || got.OrganizationID != org.ID || got.ConnectedBy != cred.ConnectedBy {
		t.Fatalf("Resolve = %+v, %v", got, err)
	}
	list, err := repo.ListForTenant(ctx, org.ID, true)
	if err != nil || len(list) != 1 || list[0].ClientID != clientID {
		t.Fatalf("ListForTenant = %+v, %v", list, err)
	}
	audit := repositories.NewAuditRepository(testDB, testCfg)
	entries, err := audit.ListForEntity(ctx, org.ID, true, clientID, 10)
	if err != nil || len(entries) != 1 || entries[0].Action != billing.AuditCredentialConnected {
		t.Fatalf("audit = %+v, %v", entries, err)
	}
}

// Review Focus 2: the owner administers two organizations and connects one
// client to both at the same moment.
func TestTwoTenantsConnectingOneClientAtOnceAdmitOne(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewCredentialRepository(testDB, testCfg)
	a, b := newOrg(t, true), newOrg(t, true)
	clientID := "cli_" + id.New()
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, org := range []*billing.Organization{a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = repo.Connect(ctx, consoleCred(org, clientID), "user:u", "req", now())
		}()
	}
	wg.Wait()
	ok, taken := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, repositories.ErrCredentialTaken):
			taken++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if ok != 1 || taken != 1 {
		t.Fatalf("ok=%d taken=%d, want 1 and 1", ok, taken)
	}
}

func TestDisconnectFreesTheClientForAnotherTenant(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewCredentialRepository(testDB, testCfg)
	a, b := newOrg(t, true), newOrg(t, true)
	clientID := "cli_" + id.New()
	cred := consoleCred(a, clientID)
	if err := repo.Connect(ctx, cred, "user:u", "req", now()); err != nil {
		t.Fatal(err)
	}
	if err := repo.Disconnect(ctx, cred, "user:u", "req", now()); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Resolve(ctx, clientID); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("Resolve after disconnect: %v", err)
	}
	if err := repo.Connect(ctx, consoleCred(b, clientID), "user:u", "req", now()); err != nil {
		t.Fatalf("reconnect elsewhere: %v", err)
	}
}

func TestAProvisionedCredentialIsNeverDisconnected(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewCredentialRepository(testDB, testCfg)
	org := newOrg(t, true)
	cred := &billing.APICredential{ClientID: "cli_" + id.New(), OrganizationID: org.ID, Livemode: true, Active: true}
	if err := repo.Create(ctx, cred, now()); err != nil {
		t.Fatal(err)
	}
	if err := repo.Disconnect(ctx, cred, "user:u", "req", now()); !errors.Is(err, repositories.ErrCredentialProvisioned) {
		t.Fatalf("err = %v, want ErrCredentialProvisioned", err)
	}
	if _, err := repo.Resolve(ctx, cred.ClientID); err != nil {
		t.Fatalf("the provisioned credential is gone: %v", err)
	}
}

// The seed path writes the guard too: a console connect cannot slip a second
// row in behind a seeded client.
func TestCreateClaimsTheClientToo(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewCredentialRepository(testDB, testCfg)
	a, b := newOrg(t, true), newOrg(t, true)
	clientID := "cli_" + id.New()
	if err := repo.Create(ctx, &billing.APICredential{ClientID: clientID, OrganizationID: a.ID, Livemode: true, Active: true}, now()); err != nil {
		t.Fatal(err)
	}
	if err := repo.Connect(ctx, consoleCred(b, clientID), "user:u", "req", now()); !errors.Is(err, repositories.ErrCredentialTaken) {
		t.Fatalf("err = %v, want ErrCredentialTaken", err)
	}
}
