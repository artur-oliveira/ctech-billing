//go:build integration

package integration

import (
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// Resolve runs on every M2M request. A credential this process changes is forgotten
// at once; one changed by another process is seen when the remembered answer
// expires. An inactive credential is never remembered.
func TestResolveRemembersACredentialAndForgetsItOnALocalWrite(t *testing.T) {
	ctx := ctxT(t)
	cfg := *testCfg
	cfg.GateMemoryTTL = 400 * time.Millisecond
	mine := repositories.NewCredentialRepository(testDB, &cfg)
	other := repositories.NewCredentialRepository(testDB, &cfg) // another process

	org := newOrg(t, true)
	clientID := "cli_" + id.New()
	cred := consoleCred(org, clientID)
	if err := mine.Connect(ctx, cred, "user:"+cred.ConnectedBy, "req_1", now()); err != nil {
		t.Fatal(err)
	}
	if _, err := mine.Resolve(ctx, clientID); err != nil {
		t.Fatal(err)
	}

	// Another process deactivates it. Inside the window this process still serves
	// the remembered credential, which is the staleness the setting accepts.
	got, err := other.Resolve(ctx, clientID)
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Deactivate(ctx, got, now()); err != nil {
		t.Fatal(err)
	}
	if _, err := mine.Resolve(ctx, clientID); err != nil {
		t.Fatalf("inside the window the remembered credential should still resolve, got %v", err)
	}
	time.Sleep(450 * time.Millisecond)
	if _, err := mine.Resolve(ctx, clientID); !errors.Is(err, billing.ErrCredentialInactive) {
		t.Fatalf("after the window a deactivated credential resolves as %v, want ErrCredentialInactive", err)
	}

	// A write by this process forgets at once: reconnect another client, resolve,
	// deactivate through the same repository, resolve again.
	second := consoleCred(org, "cli_"+id.New())
	if err := mine.Connect(ctx, second, "user:"+second.ConnectedBy, "req_2", now()); err != nil {
		t.Fatal(err)
	}
	fresh, err := mine.Resolve(ctx, second.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	if err := mine.Deactivate(ctx, fresh, now()); err != nil {
		t.Fatal(err)
	}
	if _, err := mine.Resolve(ctx, second.ClientID); !errors.Is(err, billing.ErrCredentialInactive) {
		t.Fatalf("a credential this process just deactivated resolves as %v, want ErrCredentialInactive at once", err)
	}
}
