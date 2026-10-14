//go:build integration

package integration

import (
	"slices"
	"testing"

	"gopkg.aoctech.app/api-commons/byok"
)

// An operator refresh re-tests the stored credentials, so a connection made
// before the bank's product list grew learns what it can now do.
func TestRefreshLearnsWhatTheStoredCredentialsCanNowDo(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	before := env.connection(t)
	if slices.Contains(before.Capabilities, byok.CapabilityPayout) {
		t.Fatal("precondition")
	}
	env.fake.Capabilities = append(slices.Clone(env.fake.Capabilities), byok.CapabilityPayout)

	got, err := env.conns.Refresh(ctxT(t), env.sp, "inter", "operator:test", "req", now())
	if err != nil {
		t.Fatal(err)
	}
	after := env.connection(t)
	if !slices.Contains(after.Capabilities, byok.CapabilityPayout) || !slices.Contains(got.Capabilities, byok.CapabilityPayout) {
		t.Fatalf("capabilities %v", after.Capabilities)
	}
	if after.Version != before.Version+1 {
		t.Fatalf("sealed under the next version: %d -> %d", before.Version, after.Version)
	}
}

func TestRefreshOfAConnectionThatIsNotThereIsRefused(t *testing.T) {
	env := newCollectEnv(t, collectLive)
	if _, err := env.conns.Refresh(ctxT(t), env.sp, "other", "operator:test", "req", now()); err == nil {
		t.Fatal("no such connection")
	}
}
