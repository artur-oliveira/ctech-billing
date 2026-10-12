package email

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestByokNoticesArePortugueseWithoutEmDashesAndEscaped(t *testing.T) {
	for _, kind := range []string{"cert_90", "cert_30", "cert_7", "cert_expired", "degraded", "disconnected"} {
		tx := &fakeTransport{}
		c := &Client{tx: tx}
		err := c.SendByokNotice(context.Background(), "a@example.com", ByokNotice{Kind: kind, Provider: "inter",
			OrganizationName: "<b>ACME</b>", ConsoleURL: "https://billing.test/console/connections/inter", NotAfter: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		if strings.ContainsAny(tx.out[0].subject+tx.out[0].body, "—–") {
			t.Fatalf("%s carries a dash: %s", kind, tx.out[0].subject)
		}
		if strings.Contains(tx.out[0].body, "<b>ACME") || !strings.Contains(tx.out[0].subject, "Inter") {
			t.Fatalf("%s: %s / %s", kind, tx.out[0].subject, tx.out[0].body)
		}
	}
	if err := (&Client{tx: &fakeTransport{}}).SendByokNotice(context.Background(), "a@example.com", ByokNotice{Kind: "nope"}); err == nil {
		t.Fatal("an unknown kind is refused")
	}
}
