package billing

import (
	"errors"
	"testing"

	"gopkg.aoctech.app/api-commons/webhooks"
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

func TestANewSecretValidatesAsAnEndpointSecret(t *testing.T) {
	a := webhooks.NewSecret()
	e := WebhookEndpoint{ID: "whe_1", OrganizationID: "org_1", URL: "https://example.com", Secret: a}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
}
