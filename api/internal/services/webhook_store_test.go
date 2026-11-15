package services

import (
	"encoding/json"
	"gopkg.aoctech.app/api-commons/webhooks"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

func TestWebhookStoreMapsProvisionedToInternal(t *testing.T) {
	prov := toEndpoint(&billing.WebhookEndpoint{ID: "whe_dfe", URL: "https://x", Secret: "s", Status: billing.EndpointActive})
	if !prov.Internal || !prov.Active {
		t.Fatalf("provisioned = %+v", prov)
	}
	cust := toEndpoint(&billing.WebhookEndpoint{ID: "whe_1", AddedBy: "usr_1", Status: billing.EndpointDisabled})
	if cust.Internal || cust.Active {
		t.Fatalf("customer = %+v", cust)
	}
}

func TestWebhookStoreDueCarriesGroupRefAndPayload(t *testing.T) {
	ev := billing.Event{ID: "evt_1", Type: billing.EventInvoicePaid, OccurredAt: "2026-11-15T12:00:00Z", Entity: "invoice", EntityID: "in_1"}
	row := repositories.DueDelivery{
		Delivery: billing.Delivery{EventID: "evt_1", EndpointID: "whe_1", OrganizationID: "org_1", Livemode: true, Attempts: 2, NextAttemptAt: "2026-11-15T12:00:30Z"},
		Event:    ev,
	}
	due, err := toDue(row)
	if err != nil {
		t.Fatal(err)
	}
	if due.Delivery.Group != "org_1|true|whe_1" || due.Delivery.Attempts != 2 || due.Delivery.NextAttemptAt != row.Delivery.NextAttemptAt {
		t.Fatalf("delivery = %+v", due.Delivery)
	}
	if got, ok := due.Delivery.Ref.(billing.Delivery); !ok || got.OrganizationID != "org_1" || !got.Livemode {
		t.Fatalf("ref = %#v", due.Delivery.Ref)
	}
	want, _ := json.Marshal(ev.Payload())
	if string(due.Event.Body) != string(want) || due.Event.ID != "evt_1" || due.Event.Type != string(billing.EventInvoicePaid) {
		t.Fatalf("event = %+v", due.Event)
	}
}

func TestWebhookStoreAttemptKeepsEveryField(t *testing.T) {
	a := toAttempt(webhooksAttempt(3, 502, 41, "gateway page", "endpoint answered 502"))
	if a.Number != 3 || a.StatusCode != 502 || a.DurationMs != 41 || a.Response != "gateway page" || a.Error != "endpoint answered 502" {
		t.Fatalf("attempt = %+v", a)
	}
}

func webhooksAttempt(n, status int, ms int64, response, errText string) webhooks.Attempt {
	return webhooks.Attempt{Number: n, StatusCode: status, DurationMs: ms, Response: response, Error: errText}
}
