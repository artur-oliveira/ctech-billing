package realtime

import "testing"

func TestScopeKeysHaveExactlyTheDocumentedShapes(t *testing.T) {
	cases := []struct{ got, want string }{
		{TenantScope("org1", true), "tenant:org1:live"},
		{TenantScope("org1", false), "tenant:org1:test"},
		{SpaceScope("01a04ed6-1af9-745e-bcf7-d3b66fe52321#live"), "space:01a04ed6-1af9-745e-bcf7-d3b66fe52321#live"},
		{UserScope("user-1"), "user:user-1"},
		{InvoiceScope("org1", true, "in_1"), "invoice:org1:live:in_1"},
		{InvoiceScope("org1", false, "in_1"), "invoice:org1:test:in_1"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got %q, want %q", c.got, c.want)
		}
	}
}

func TestTheModeIsPartOfEveryKeyThatHasOne(t *testing.T) {
	if TenantScope("o", true) == TenantScope("o", false) {
		t.Error("a live and a test tenant scope are the same key")
	}
	if InvoiceScope("o", true, "i") == InvoiceScope("o", false, "i") {
		t.Error("a live and a test invoice scope are the same key")
	}
}

func TestKindRoundTrips(t *testing.T) {
	for scope, want := range map[string]string{
		TenantScope("o", true):       KindTenant,
		SpaceScope("o#live"):         KindSpace,
		UserScope("u"):               KindUser,
		InvoiceScope("o", true, "i"): KindInvoice,
		"nonsense":                   "",
		"":                           "",
	} {
		if got := Kind(scope); got != want {
			t.Errorf("Kind(%q) = %q, want %q", scope, got, want)
		}
	}
}
