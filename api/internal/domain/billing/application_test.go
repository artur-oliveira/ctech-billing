package billing

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestApplicationWireValues(t *testing.T) {
	raw, _ := json.Marshal([]ApplicationStatus{ApplicationPending, ApplicationApproved, ApplicationRejected})
	if string(raw) != `["pending","approved","rejected"]` {
		t.Fatalf("statuses = %s", raw)
	}
	raw, _ = json.Marshal([]MonthlyVolume{VolumeUpTo10k, Volume10kTo50k, Volume50kTo200k, VolumeOver200k})
	if string(raw) != `["up_to_10k","10k_to_50k","50k_to_200k","over_200k"]` {
		t.Fatalf("volumes = %s", raw)
	}
	for _, s := range []ApplicationStatus{ApplicationPending, ApplicationApproved, ApplicationRejected} {
		if !s.Valid() {
			t.Errorf("%q invalid", s)
		}
	}
	if ApplicationStatus("").Valid() || ApplicationStatus("PENDING").Valid() {
		t.Fatal("unknown status accepted")
	}
	if !MonthlyVolume("").Valid() {
		t.Fatal("the volume is optional: empty must be valid")
	}
	if MonthlyVolume("até R$ 10 mil").Valid() {
		t.Fatal("a label is not a wire value")
	}
}

// The company document is never on the wire from the domain struct: the DTO
// decides what a screen sees.
func TestApplicationNeverSerializesTheDocument(t *testing.T) {
	raw, _ := json.Marshal(Application{ID: "apl_1", CompanyTaxID: "12345678000190"})
	if strings.Contains(string(raw), "12345678000190") {
		t.Fatalf("tax id leaked: %s", raw)
	}
}

func TestOnlyALiveTenantCanBeSuspended(t *testing.T) {
	if (&Organization{Livemode: true}).Suspended() {
		t.Fatal("no suspended_at is not suspended")
	}
	if !(&Organization{Livemode: true, SuspendedAt: "2026-10-10T12:00:00Z"}).Suspended() {
		t.Fatal("a live tenant with suspended_at is suspended")
	}
	// Test mode is never suspended (spec § 4.3), whatever a row says.
	if (&Organization{Livemode: false, SuspendedAt: "2026-10-10T12:00:00Z"}).Suspended() {
		t.Fatal("a test tenant reads as suspended")
	}
}
