package byokconn

import (
	"testing"
	"time"

	"gopkg.aoctech.app/api-commons/byok"
)

func TestCertificateWarningsFireOncePerThresholdInOrder(t *testing.T) {
	notAfter := time.Date(2027, 10, 12, 0, 0, 0, 0, time.UTC)
	c := &Connection{State: StateActive, CertNotAfter: notAfter}
	at := func(daysBefore int) time.Time { return notAfter.AddDate(0, 0, -daysBefore) }
	for _, step := range []struct {
		now  time.Time
		want string
		due  bool
	}{
		{at(120), "", false}, {at(90), "90", true}, {at(60), "", false}, {at(30), "30", true},
		{at(7), "7", true}, {at(1), "", false}, {notAfter, "expired", true}, {notAfter.Add(48 * time.Hour), "", false},
	} {
		got, due := c.CertWarning(step.now)
		if got != step.want || due != step.due {
			t.Fatalf("at %s: %q %v, want %q %v", step.now, got, due, step.want, step.due)
		}
		if due {
			c.MarkWarned(got)
		}
	}
}

func TestAJobThatMissedD90StillSendsD30Only(t *testing.T) {
	notAfter := time.Date(2027, 1, 31, 0, 0, 0, 0, time.UTC)
	c := &Connection{State: StateActive, CertNotAfter: notAfter}
	got, due := c.CertWarning(notAfter.AddDate(0, 0, -20))
	if got != "30" || !due {
		t.Fatalf("%q %v: the most recent threshold passed is the one sent", got, due)
	}
	c.MarkWarned(got)
	if !c.Warned90 {
		t.Fatal("marking 30 also marks 90, so it is never sent late")
	}
}

func TestExpireIfPastAndDegraded(t *testing.T) {
	c := &Connection{State: StateActive, CertNotAfter: time.Now().Add(-time.Minute), ConsecutiveFailures: 3}
	if !c.Degraded() {
		t.Fatal("three failures on an active connection is degraded")
	}
	if !c.ExpireIfPast(time.Now()) || c.State != StateExpired {
		t.Fatal("past notAfter becomes expired")
	}
	if c.Degraded() {
		t.Fatal("expired is not degraded, it is expired")
	}
	if (&Connection{}).ExpireIfPast(time.Now()) {
		t.Fatal("a provider without certificates never expires")
	}
}

func TestTheSandboxRefIsNamedSandbox(t *testing.T) {
	c := &Connection{Kind: KindSandboxShared, ID: "01J", Capabilities: []byok.Capability{byok.CapabilityStatement}}
	if c.Ref().ID != "sandbox" || c.Ref().Sealed != "" {
		t.Fatalf("%+v", c.Ref())
	}
	if !c.Has(byok.CapabilityStatement) || c.Has(byok.CapabilityCollection) {
		t.Fatal("Has reads Capabilities")
	}
}
