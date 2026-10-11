package billing

import (
	"encoding/json"
	"testing"
)

func TestMerchantStatusFromTheTenantRowsAndTheApplication(t *testing.T) {
	test := &Organization{ID: "org_1", Livemode: false}
	live := &Organization{ID: "org_1", Livemode: true}
	suspended := &Organization{ID: "org_1", Livemode: true, SuspendedAt: "2026-10-10T12:00:00Z"}
	for _, c := range []struct {
		name       string
		test, live *Organization
		latest     ApplicationStatus
		want       MerchantStatus
	}{
		{"nothing", nil, nil, "", MerchantNone},
		{"applied, waiting", test, nil, ApplicationPending, MerchantTest},
		{"provisioned test tenant, no application", test, nil, "", MerchantTest},
		{"rejected keeps its test tenant", test, nil, ApplicationRejected, MerchantRejected},
		{"approved", test, live, ApplicationApproved, MerchantLive},
		{"provisioned live", nil, live, "", MerchantLive},
		{"suspended wins over everything", test, suspended, ApplicationApproved, MerchantSuspended},
		// A re-application after a rejection is pending again: test, not rejected.
		{"re-applied", test, nil, ApplicationPending, MerchantTest},
	} {
		if got := MerchantStatusOf(c.test, c.live, c.latest); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

// The wire values are a contract with the console and with parts 2 and 3.
func TestMerchantStatusWireValues(t *testing.T) {
	all := []MerchantStatus{MerchantNone, MerchantTest, MerchantLive, MerchantRejected, MerchantSuspended}
	raw, err := json.Marshal(all)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `["none","test","live","rejected","suspended"]` {
		t.Fatalf("wire values = %s", raw)
	}
	for _, s := range all {
		if !s.Valid() {
			t.Errorf("%q is not valid", s)
		}
	}
	if MerchantStatus("approved").Valid() || MerchantStatus("").Valid() {
		t.Fatal("an unknown status is valid")
	}
}
