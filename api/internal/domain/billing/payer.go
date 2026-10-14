package billing

import "strings"

// Payer is who paid a collected charge, as the bank's statement line names it
// (BYOK spec 2026-10-14 § 6). It is evidence and a safety check, never the
// tomador of an NF: that is the customer's registered tax id.
type Payer struct {
	Name        string `dynamodbav:"name,omitempty"        json:"name,omitempty"`
	Document    string `dynamodbav:"document,omitempty"    json:"document,omitempty"`
	Institution string `dynamodbav:"institution,omitempty" json:"institution,omitempty"`
}

// PayerMatch says whether the payer is the customer.
type PayerMatch string

const (
	PayerMatchYes     PayerMatch = "match"
	PayerMatchNo      PayerMatch = "mismatch"
	PayerMatchUnknown PayerMatch = "unknown"
)

// minRevealedDigits is how many digits of a masked document must show for a
// comparison to mean anything.
const minRevealedDigits = 3

func digitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// MatchPayer compares the payer's document with the customer's tax id, digit
// for digit. A masked document ("***456789**") is compared on the digits it
// shows, and is unknown when too few show or its length does not fit. It never
// answers "match" on less than a full comparison.
func MatchPayer(customerTaxID string, p Payer) PayerMatch {
	want := digitsOnly(customerTaxID)
	if want == "" || strings.TrimSpace(p.Document) == "" {
		return PayerMatchUnknown
	}
	doc := strings.TrimSpace(p.Document)
	if !strings.ContainsRune(doc, '*') {
		got := digitsOnly(doc)
		switch {
		case got == "":
			return PayerMatchUnknown
		case got == want:
			return PayerMatchYes
		}
		return PayerMatchNo
	}
	runes := []rune(doc)
	if len(runes) != len(want) {
		return PayerMatchUnknown
	}
	shown := 0
	for i, r := range runes {
		if r < '0' || r > '9' {
			continue
		}
		shown++
		if byte(r) != want[i] {
			return PayerMatchNo
		}
	}
	if shown < minRevealedDigits {
		return PayerMatchUnknown
	}
	return PayerMatchYes
}
