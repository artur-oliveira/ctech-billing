package repositories

import (
	"testing"
	"time"
)

func TestRetentionExpiry(t *testing.T) {
	now := time.Date(2026, time.March, 10, 12, 0, 0, 0, time.UTC)

	if RetentionPermanent.ExpiresAt(now) != nil {
		t.Fatal("a permanent record must carry no TTL at all")
	}

	cases := []struct {
		name string
		r    Retention
		want time.Time
	}{
		{"five years", RetentionFiveYears, now.AddDate(5, 0, 0)},
		{"twenty-four months", RetentionTwentyFourMonths, now.AddDate(0, 24, 0)},
		{"ninety days", RetentionNinetyDays, now.Add(90 * 24 * time.Hour)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.r.ExpiresAt(now)
			if got == nil {
				t.Fatal("expected a TTL")
			}
			if *got != tc.want.Unix() {
				t.Fatalf("got %d, want %d", *got, tc.want.Unix())
			}
		})
	}
}

// TestEvidenceOutlivesTheDocumentItExplains is the rule ADR 0009 states in
// words: a payment attempt or an audit entry must never expire before the
// invoice it explains. Since invoices never expire, both are simply long-lived —
// but if someone ever gives invoices a TTL, this test is where the contradiction
// surfaces.
func TestEvidenceOutlivesTheDocumentItExplains(t *testing.T) {
	now := time.Now()
	if RetentionInvoice.ExpiresAt(now) != nil {
		t.Fatal("invoices must not expire; evidence retention is derived from that")
	}
	for name, r := range map[string]Retention{
		"audit log":       RetentionAuditLog,
		"payment attempt": RetentionPaymentAttempt,
	} {
		if r.ExpiresAt(now) == nil {
			t.Fatalf("%s currently has no TTL — intentional?", name)
		}
		if r != RetentionFiveYears {
			t.Fatalf("%s must match the five-year legal floor", name)
		}
	}
}

func TestCommercialDocumentsAreNeverTTLed(t *testing.T) {
	// Purging a commercial document is a reviewed process, not an attribute
	// written years earlier by code nobody remembers.
	for name, r := range map[string]Retention{
		"invoice":         RetentionInvoice,
		"invoice item":    RetentionInvoiceItem,
		"credit note":     RetentionCreditNote,
		"subscription":    RetentionSubscription,
		"customer":        RetentionCustomer,
		"price":           RetentionPrice,
		"invoice counter": RetentionInvoiceCounter,
	} {
		if r.ExpiresAt(time.Now()) != nil {
			t.Errorf("%s must not carry a TTL", name)
		}
	}
}

func TestUnknownRetentionPanics(t *testing.T) {
	// Silence here would mean a new record type quietly acquiring unlimited
	// retention, which is the LGPD failure that is hardest to notice.
	defer func() {
		if recover() == nil {
			t.Fatal("an unknown retention class must panic, not default to keeping data forever")
		}
	}()
	Retention(99).ExpiresAt(time.Now())
}
