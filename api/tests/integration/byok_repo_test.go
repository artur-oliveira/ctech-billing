//go:build integration

package integration

import (
	"errors"
	"sync"
	"testing"

	"gopkg.aoctech.app/api-commons/byok"

	"gopkg.aoctech.app/billing/api/internal/domain/byokconn"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

func newConnection(org string, live bool) *byokconn.Connection {
	return &byokconn.Connection{AccountOrganizationID: org, Livemode: live, Provider: "inter", ID: id.New(),
		Kind: byokconn.KindMerchant, State: byokconn.StateActive, Sealed: "b1.AAAA", Version: 1,
		Capabilities: []byok.Capability{byok.CapabilityStatement}, SyncState: byokconn.SyncActive}
}

func TestAConnectionIsCreatedOncePerOrganizationModeAndProvider(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewByokRepository(testDB, testCfg)
	org := newSpaceOrgID()
	if err := repo.CreateConnection(ctx, newConnection(org, true), "u1", "req", now()); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateConnection(ctx, newConnection(org, true), "u1", "req", now()); !errors.Is(err, repositories.ErrConnectionExists) {
		t.Fatalf("second create: %v", err)
	}
	if err := repo.CreateConnection(ctx, newConnection(org, false), "u1", "req", now()); err != nil {
		t.Fatalf("test mode is another row: %v", err)
	}
	page, err := repo.ConnectionsForJob(ctx, true, nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, c := range page.Items {
		if c.AccountOrganizationID == org {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("the live job partition holds %d rows of org, want 1", found)
	}
}

func TestConcurrentSavesOfOneConnectionLetOneWin(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewByokRepository(testDB, testCfg)
	c := newConnection(newSpaceOrgID(), true)
	if err := repo.CreateConnection(ctx, c, "u", "r", now()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cp := *c
			cp.ConsecutiveFailures = i + 1
			errs[i] = repo.SaveConnection(ctx, &cp, "u", "r", "byok.health", now())
		}()
	}
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("exactly one save wins: %v / %v", errs[0], errs[1])
	}
	for _, err := range errs {
		if err != nil && !errors.Is(err, repositories.ErrConnectionChanged) {
			t.Fatalf("loser: %v", err)
		}
	}
}

func TestADisconnectedConnectionLeavesTheJobPartition(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewByokRepository(testDB, testCfg)
	c := newConnection(newSpaceOrgID(), false)
	_ = repo.CreateConnection(ctx, c, "u", "r", now())
	if err := repo.DeleteConnection(ctx, c, "u", "r", now()); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetConnection(ctx, c.AccountOrganizationID, false, "inter"); !errors.Is(err, repositories.ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
}
