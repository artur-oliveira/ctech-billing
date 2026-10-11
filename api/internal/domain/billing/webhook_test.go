package billing

import "testing"

func TestAPingGoesOnlyToItsOwnEndpoint(t *testing.T) {
	ping := &Event{ID: "evt_1", Type: EventPing, EndpointID: "whe_a", Entity: "webhook_endpoint", EntityID: "whe_a"}
	target := WebhookEndpoint{ID: "whe_a", Status: EndpointActive, Events: []EventType{EventInvoicePaid}}
	other := WebhookEndpoint{ID: "whe_b", Status: EndpointActive}
	disabled := WebhookEndpoint{ID: "whe_a", Status: EndpointDisabled}
	if !target.Wants(ping) {
		t.Fatal("the target's event filter kept its own ping out")
	}
	if other.Wants(ping) || disabled.Wants(ping) {
		t.Fatal("a ping reached another or a disabled endpoint")
	}
	// An ordinary event is unaffected by EndpointID routing.
	if !other.Wants(&Event{Type: EventInvoicePaid}) {
		t.Fatal("ordinary routing changed")
	}
}

func TestAnEndpointWithoutAnAuthorWasProvisioned(t *testing.T) {
	if !(&WebhookEndpoint{ID: "whe_dfe"}).Provisioned() || (&WebhookEndpoint{ID: "whe_1", AddedBy: "usr_1"}).Provisioned() {
		t.Fatal("Provisioned is wrong")
	}
}
