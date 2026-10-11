package billing

import (
	"errors"
	"net/netip"
	"strings"
	"testing"
)

func TestCheckWebhookURL(t *testing.T) {
	for _, tc := range []struct {
		url  string
		want error // nil, ErrInvalidEndpoint or ErrWebhookHostNotAllowed
	}{
		{"https://hooks.example.com/billing", nil},
		{"https://example.com:8443/x?y=1", nil},
		{"https://203.0.113.10/hook", nil},
		{"http://hooks.example.com/billing", ErrInvalidEndpoint},
		{"hooks.example.com/billing", ErrInvalidEndpoint},
		{"https://user:pw@hooks.example.com/", ErrInvalidEndpoint},
		{"https:///nohost", ErrInvalidEndpoint},
		{"https://127.0.0.1/hook", ErrWebhookHostNotAllowed},
		{"https://[::1]/hook", ErrWebhookHostNotAllowed},
		{"https://10.0.0.5/hook", ErrWebhookHostNotAllowed},
		{"https://169.254.169.254/latest/meta-data", ErrWebhookHostNotAllowed},
		{"https://100.64.1.1/hook", ErrWebhookHostNotAllowed},
		{"https://[::ffff:192.168.0.1]/hook", ErrWebhookHostNotAllowed},
		{"https://localhost/hook", ErrWebhookHostNotAllowed},
		{"https://api.localhost/hook", ErrWebhookHostNotAllowed},
		{"https://printer.local/hook", ErrWebhookHostNotAllowed},
		{"https://dfe.internal/hook", ErrWebhookHostNotAllowed},
		{"https://intranet/hook", ErrWebhookHostNotAllowed},
	} {
		err := CheckWebhookURL(tc.url)
		switch {
		case tc.want == nil && err != nil:
			t.Errorf("%s: %v, want accepted", tc.url, err)
		case tc.want != nil && !errors.Is(err, tc.want):
			t.Errorf("%s: %v, want %v", tc.url, err, tc.want)
		}
	}
}

func TestPublicAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"8.8.8.8": true, "2001:4860:4860::8888": true,
		"127.0.0.1": false, "10.1.2.3": false, "172.16.0.1": false, "192.168.1.1": false,
		"169.254.169.254": false, "100.64.0.1": false, "0.0.0.0": false, "::1": false,
		"fe80::1": false, "fc00::1": false, "224.0.0.1": false, "::ffff:10.0.0.1": false,
	} {
		if got := PublicAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("PublicAddr(%s) = %v, want %v", addr, got, want)
		}
	}
}

func TestNewWebhookSecretIsLongUniqueAndValid(t *testing.T) {
	a, b := NewWebhookSecret(), NewWebhookSecret()
	if a == b || !strings.HasPrefix(a, "whsec_") || len(a) != len("whsec_")+64 {
		t.Fatalf("secrets %q %q", a, b)
	}
	e := WebhookEndpoint{ID: "whe_1", OrganizationID: "org_1", URL: "https://example.com", Secret: a}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
}
