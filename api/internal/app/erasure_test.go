package app

import "testing"

func TestRevocationURLDropsTheLogicalDB(t *testing.T) {
	for in, want := range map[string]string{
		"redis://valkey.internal:6379/3":          "redis://valkey.internal:6379",
		"rediss://:secret@valkey.internal:6379/3": "rediss://:secret@valkey.internal:6379",
		"redis://valkey.internal:6379":            "redis://valkey.internal:6379",
	} {
		got, err := revocationURL(in)
		if err != nil || got != want {
			t.Errorf("revocationURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := revocationURL("valkey.internal"); err == nil {
		t.Error("a URL with no host must be refused, not turned into a DB 0 connection to nowhere")
	}
}

func TestPortalInvoicesURLIsTheCheckoutsOrigin(t *testing.T) {
	for in, want := range map[string]string{
		"https://billing.aoctech.app/checkout":     "https://billing.aoctech.app/invoices",
		"https://billing-dev.aoctech.app/checkout": "https://billing-dev.aoctech.app/invoices",
		"": "",
	} {
		if got := portalInvoicesURL(in); got != want {
			t.Errorf("portalInvoicesURL(%q) = %q, want %q", in, got, want)
		}
	}
}
