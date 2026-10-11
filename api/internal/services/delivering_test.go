package services

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

// Review Focus 3: a merchant URL that resolves to a private address. The test
// server listens on 127.0.0.1, which is exactly such an address.
func TestAMerchantEndpointNeverDialsAPrivateAddress(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits.Add(1) }))
	t.Cleanup(srv.Close)
	d := NewDeliverer(nil)
	e := &billing.WebhookEndpoint{ID: "whe_1", URL: srv.URL, Secret: billing.NewWebhookSecret(), AddedBy: "usr_1"}
	_, err := d.post(context.Background(), e, &billing.Event{ID: "evt_1", Type: billing.EventPing}, time.Now())
	if !errors.Is(err, errPrivateAddress) {
		t.Fatalf("err = %v, want errPrivateAddress", err)
	}
	if hits.Load() != 0 {
		t.Fatal("the request reached the private address")
	}
}

// A provisioned endpoint (tenant zero's whe_dfe is on CTech's private network)
// keeps the plain client.
func TestAProvisionedEndpointKeepsThePlainClient(t *testing.T) {
	d := NewDeliverer(nil)
	if d.clientFor(&billing.WebhookEndpoint{ID: "whe_dfe"}) != d.client {
		t.Fatal("a provisioned endpoint got the guarded client")
	}
	if d.clientFor(&billing.WebhookEndpoint{ID: "whe_1", AddedBy: "usr_1"}) != d.merchant {
		t.Fatal("a merchant endpoint got the plain client")
	}
}

func TestAMerchantEndpointFollowsNoRedirect(t *testing.T) {
	c := newMerchantClient()
	if c.CheckRedirect == nil || !errors.Is(c.CheckRedirect(nil, nil), http.ErrUseLastResponse) {
		t.Fatal("the merchant client follows redirects")
	}
	if tr, ok := c.Transport.(*http.Transport); !ok || tr.Proxy != nil {
		t.Fatal("the merchant client may go through a proxy, which the dial guard would not see")
	}
}
