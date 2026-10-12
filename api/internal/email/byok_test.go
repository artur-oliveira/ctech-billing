package email

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestByokNoticesArePortugueseWithoutEmDashesAndEscaped(t *testing.T) {
	for _, kind := range []string{"cert_90", "cert_30", "cert_7", "cert_expired", "degraded", "disconnected", "excess_payment", "webhook_overwritten"} {
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

func TestBrlFormatsCentavos(t *testing.T) {
	for in, want := range map[int64]string{0: "R$ 0,00", 5: "R$ 0,05", 123456: "R$ 1.234,56", 100000000: "R$ 1.000.000,00"} {
		if got := brl(in); got != want {
			t.Errorf("%d: %q", in, got)
		}
	}
}
