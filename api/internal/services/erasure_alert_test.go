package services

import (
	"context"
	"errors"
	"testing"

	"gopkg.aoctech.app/api-commons/alerts"
	"gopkg.aoctech.app/api-commons/erasure"
)

type captureAlerts struct{ got []alerts.Alert }

func (c *captureAlerts) Alert(_ context.Context, a alerts.Alert) { c.got = append(c.got, a) }

func TestAlertingPurge(t *testing.T) {
	boom := errors.New("boom")
	c := &captureAlerts{}
	fail := AlertingPurge(func(context.Context, erasure.Message) (erasure.Ack, error) { return erasure.Ack{}, boom }, c)
	if _, err := fail(context.Background(), erasure.Message{RequestID: "r1"}); !errors.Is(err, boom) {
		t.Fatalf("error must pass through, got %v", err)
	}
	if len(c.got) != 1 || c.got[0].Job != "erasure-purge-failed" {
		t.Fatalf("want one erasure alert, got %+v", c.got)
	}
	c.got = nil
	ok := AlertingPurge(func(context.Context, erasure.Message) (erasure.Ack, error) { return erasure.Ack{}, nil }, c)
	if _, err := ok(context.Background(), erasure.Message{}); err != nil || len(c.got) != 0 {
		t.Fatalf("success must not alert: err=%v alerts=%+v", err, c.got)
	}
}
