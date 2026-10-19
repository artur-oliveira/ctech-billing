package services

import (
	"testing"
	"time"
)

// At most `per` holders of one tenant at once; another tenant is unaffected.
func TestOrgLimiterCapsOneTenantAndLeavesOthersAlone(t *testing.T) {
	l := newOrgLimiter(2)
	r1, r2 := l.acquire("org_a"), l.acquire("org_a")

	got := make(chan struct{})
	go func() { l.acquire("org_a")(); close(got) }()
	select {
	case <-got:
		t.Fatal("a third holder of one tenant got in past the cap")
	case <-time.After(50 * time.Millisecond):
	}

	other := make(chan struct{})
	go func() { l.acquire("org_b")(); close(other) }()
	select {
	case <-other:
	case <-time.After(time.Second):
		t.Fatal("another tenant was held up by org_a's cap")
	}

	r1()
	select {
	case <-got:
	case <-time.After(time.Second):
		t.Fatal("a release did not let the waiting holder in")
	}
	r2()
}

func TestOrgLimiterTreatsZeroAsOne(t *testing.T) {
	release := newOrgLimiter(0).acquire("org_a")
	release()
}
