//go:build integration

package integration

import (
	"errors"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/services"
)

// Tenant zero's rail is the operator's audited action (spec § 12.6): the
// console path refuses it, the operator path moves it and moves it back.
func TestTheOperatorMovesTenantZeroToByokAndBack(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	ctx := ctxT(t)
	if err := env.orgs.SetPayoutRail(ctx, env.tenantNow(t), billing.RailWallet, "", billing.PayoutEnabled, "test", "req", now()); err != nil {
		t.Fatal(err)
	}
	// This env's tenant plays tenant zero.
	setup := services.NewByokCollectionSetup(env.conns, env.repo, env.orgs, env.payments, env.byok, env.webhooks,
		"billing-byok-hooks-test.aoctech.app", env.tenant.ID)

	err := setup.UseForCollection(ctx, env.sp, env.tenantNow(t), "inter", billing.CollectionSettings{}, "user:test", "req", now())
	if !errors.Is(err, services.ErrTenantZeroRail) {
		t.Fatalf("the console path still refuses tenant zero: %v", err)
	}

	if err := setup.OperatorUseForCollection(ctx, env.sp, env.tenantNow(t), "inter", billing.CollectionSettings{}, "operator:test", "req", now()); err != nil {
		t.Fatal(err)
	}
	org := env.tenantNow(t)
	if org.Rail() != billing.RailBYOK || org.PayoutProvider != "inter" || org.PayoutStatus != billing.PayoutEnabled {
		t.Fatalf("moved to BYOK: %+v", org)
	}
	if env.connection(t).WebhookStatus == "" {
		t.Fatal("the notice is registered on the way")
	}

	if err := setup.OperatorLeaveCollection(ctx, env.tenantNow(t), "operator:test", "req", now()); err != nil {
		t.Fatal(err)
	}
	org = env.tenantNow(t)
	if org.Rail() != billing.RailWallet || org.PayoutStatus != billing.PayoutEnabled {
		t.Fatalf("back on the wallet rail, still collecting: %+v", org)
	}
}

func TestTheOperatorPathNeedsACollectionConnection(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	ctx := ctxT(t)
	setup := services.NewByokCollectionSetup(env.conns, env.repo, env.orgs, env.payments, env.byok, env.webhooks,
		"billing-byok-hooks-test.aoctech.app", env.tenant.ID)
	if _, err := env.repo.GetConnection(ctx, env.accountOrg, true, "other"); err == nil {
		t.Fatal("no such connection is expected here")
	}
	err := setup.OperatorUseForCollection(ctx, env.sp, env.tenantNow(t), "other", billing.CollectionSettings{}, "operator:test", "req", now())
	if err == nil {
		t.Fatal("a provider the organization never connected is refused")
	}
}
