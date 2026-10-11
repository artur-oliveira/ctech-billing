package app

import (
	"os"
	"strings"
	"testing"
)

// Every binary that moves a subscription must keep the merchant suspension in
// step (spec § 4.3), so app builds the repository in exactly one place.
func TestTheSubscriptionRepositoryIsBuiltOnlyWithTheHook(t *testing.T) {
	src, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(src), "repositories.NewSubscriptionRepository("); n != 1 {
		t.Fatalf("app.go builds the subscription repository %d times; use newSubscriptions", n)
	}
	if !strings.Contains(string(src), "WithEntitlementHook(") {
		t.Fatal("newSubscriptions does not attach the hook")
	}
}
