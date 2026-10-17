package email

import (
	"strings"
	"testing"
	"time"
)

func TestEveryClosureNoticeRendersAndEscapes(t *testing.T) {
	at := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	for _, kind := range []string{"confirm", "scheduled", "blocked", "cancelled"} {
		subject, body := closureHTML(ClosureNotice{Kind: kind, FirstName: `Ana<script>`, Link: "https://x/closure/confirm?request=r&token=t", GraceUntil: at,
			Blockers: []string{"billing.tenant_active_subscriptions", "billing.<other>"}})
		if subject == "" || body == "" {
			t.Fatalf("%s: empty", kind)
		}
		if strings.Contains(body, "<script>") || strings.Contains(body, "billing.<other>") {
			t.Errorf("%s: unescaped input in %s", kind, body)
		}
	}
	if s, _ := closureHTML(ClosureNotice{Kind: "nope"}); s != "" {
		t.Fatal("an unknown notice must not render")
	}
	_, scheduled := closureHTML(ClosureNotice{Kind: "scheduled", Link: "https://x", GraceUntil: at})
	if !strings.Contains(scheduled, "24/10/2026 às 09:00") {
		t.Errorf("the grace end must read in Brasília time: %s", scheduled)
	}
}
