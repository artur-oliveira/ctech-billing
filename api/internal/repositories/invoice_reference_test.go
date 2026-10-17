package repositories

import (
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

func TestInvoiceRowEntityFillsReference(t *testing.T) {
	cases := []struct{ name, stored, key, want string }{
		{"stored wins", "dep-1", "oneoff:other", "dep-1"},
		{"issued before the field existed", "", "oneoff:snd_abc", "snd_abc"},
		{"subscription invoice has none", "", "item_1:2026-10-01", ""},
		{"no key", "", "", ""},
	}
	for _, c := range cases {
		row := invoiceRow{Invoice: billing.Invoice{Reference: c.stored}, GenerationKey: c.key}
		if got := row.entity().Reference; got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}
