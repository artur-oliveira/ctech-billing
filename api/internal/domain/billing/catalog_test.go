package billing

import "testing"

func TestPriceLabelIsItsOwnNameOrTheProducts(t *testing.T) {
	if got := (&Price{}).Label("DF-e Pro"); got != "DF-e Pro" {
		t.Fatalf("no name: %q", got)
	}
	if got := (&Price{Name: "Sob demanda · NF-e"}).Label("DF-e"); got != "Sob demanda · NF-e" {
		t.Fatalf("named: %q", got)
	}
}
