package billing

import (
	"encoding/json"
	"testing"
)

func TestMerchantStatusFromTheTenantRows(t *testing.T) {
	test := &Organization{ID: "org_1", Livemode: false}
	live := &Organization{ID: "org_1", Livemode: true}
	for _, c := range []struct {
		name       string
		test, live *Organization
		want       MerchantStatus
	}{
		{"no tenant", nil, nil, MerchantNone},
		{"test only: applied, waiting", test, nil, MerchantTest},
		{"live exists: approved", test, live, MerchantLive},
		// A live row without a test row does not happen through the product,
		// but a provisioned tenant can be live-only: it is still live.
		{"live only", nil, live, MerchantLive},
	} {
		if got := MerchantStatusOf(c.test, c.live); got != c.want {
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
