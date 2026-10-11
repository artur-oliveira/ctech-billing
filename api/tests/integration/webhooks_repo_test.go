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

// merchantEndpoint points at a reserved .invalid host on purpose: the delivery
// queue is shared by every test, and httptest's TLS client sends any request
// for example.com to its own server, so a ping queued here for an example.com
// URL would land on another test's consumer.
func merchantEndpoint(org *billing.Organization) *billing.WebhookEndpoint {
	return &billing.WebhookEndpoint{
		ID: id.NewWithPrefix(id.PrefixWebhookEndpoint), OrganizationID: org.ID, Livemode: org.Livemode,
		URL: "https://hooks.invalid/billing", Secret: billing.NewWebhookSecret(),
		Status: billing.EndpointActive, AddedBy: "usr_1", AddedAt: "2026-10-10T12:00:00Z",
	}
}

func TestAddCountsEveryEndpointOfTheTenantAndMode(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewWebhookRepository(testDB, testCfg)
	org := newOrg(t, true)
	// A provisioned one counts too (scope decision 8).
	if err := repo.CreateEndpoint(ctx, &billing.WebhookEndpoint{
		ID: "whe_" + id.New(), OrganizationID: org.ID, Livemode: true,
		URL: "https://dfe.invalid/hook", Secret: billing.NewWebhookSecret(),
	}, now()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if err := repo.AddEndpoint(ctx, merchantEndpoint(org), 5, "user:usr_1", "req", now()); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	if err := repo.AddEndpoint(ctx, merchantEndpoint(org), 5, "user:usr_1", "req", now()); !errors.Is(err, repositories.ErrEndpointLimit) {
		t.Fatalf("sixth: %v, want ErrEndpointLimit", err)
	}
	// The other mode is another tenant.
	test := *org
	test.Livemode = false
	if err := repo.AddEndpoint(ctx, merchantEndpoint(&test), 5, "user:usr_1", "req", now()); err != nil {
		t.Fatalf("test mode: %v", err)
	}
}

// Review Focus 4.
func TestConcurrentAddsNeverPassTheLimit(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewWebhookRepository(testDB, testCfg)
	org := newOrg(t, true)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := repo.AddEndpoint(ctx, merchantEndpoint(org), 5, "user:usr_1", "req", now())
			if err != nil && !errors.Is(err, repositories.ErrEndpointLimit) && !errors.Is(err, repositories.ErrEndpointsChanged) {
				t.Errorf("unexpected: %v", err)
			}
		}()
	}
	wg.Wait()
	list, err := repo.ListEndpoints(ctx, org.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) > 5 || len(list) == 0 {
		t.Fatalf("%d endpoints after 8 concurrent adds", len(list))
	}
}

func TestRemoveFreesASlotAndRefusesProvisioned(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewWebhookRepository(testDB, testCfg)
	org := newOrg(t, true)
	var added []*billing.WebhookEndpoint
	for i := 0; i < 5; i++ {
		e := merchantEndpoint(org)
		if err := repo.AddEndpoint(ctx, e, 5, "user:usr_1", "req", now()); err != nil {
			t.Fatal(err)
		}
		added = append(added, e)
	}
	if err := repo.RemoveEndpoint(ctx, added[0], "user:usr_1", "req", now()); err != nil {
		t.Fatal(err)
	}
	if err := repo.AddEndpoint(ctx, merchantEndpoint(org), 5, "user:usr_1", "req", now()); err != nil {
		t.Fatalf("add after remove: %v", err)
	}
	provisioned := &billing.WebhookEndpoint{ID: "whe_" + id.New(), OrganizationID: org.ID, Livemode: true}
	if err := repo.RemoveEndpoint(ctx, provisioned, "user:usr_1", "req", now()); !errors.Is(err, repositories.ErrEndpointProvisioned) {
		t.Fatalf("provisioned: %v", err)
	}
}

func TestRotateReplacesTheSealedSecret(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewWebhookRepository(testDB, testCfg)
	org := newOrg(t, true)
	e := merchantEndpoint(org)
	if err := repo.AddEndpoint(ctx, e, 5, "user:usr_1", "req", now()); err != nil {
		t.Fatal(err)
	}
	next := billing.NewWebhookSecret()
	if err := repo.RotateEndpointSecret(ctx, e, next, "user:usr_1", "req", now()); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetEndpoint(ctx, org.ID, true, e.ID)
	if err != nil || got.Secret != next {
		t.Fatalf("after rotate: %v, secret changed=%v", err, got != nil && got.Secret == next)
	}
}

func TestAPingIsQueuedForItsEndpoint(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewWebhookRepository(testDB, testCfg)
	org := newOrg(t, true)
	e := merchantEndpoint(org)
	if err := repo.AddEndpoint(ctx, e, 5, "user:usr_1", "req", now()); err != nil {
		t.Fatal(err)
	}
	ev, err := repo.EmitPing(ctx, e, now())
	if err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetEvent(ctx, org.ID, true, ev.ID)
	if err != nil || got.Type != billing.EventPing || got.EndpointID != e.ID || got.EntityID != e.ID {
		t.Fatalf("event = %+v, %v", got, err)
	}
}
