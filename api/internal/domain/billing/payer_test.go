package billing

import "testing"

func TestMatchPayer(t *testing.T) {
	cases := []struct {
		name, tax string
		p         Payer
		want      PayerMatch
	}{
		{"cpf equal", "12345678909", Payer{Document: "12345678909"}, PayerMatchYes},
		{"cpf equal with punctuation", "123.456.789-09", Payer{Document: "12345678909"}, PayerMatchYes},
		{"cpf different", "12345678909", Payer{Document: "98765432100"}, PayerMatchNo},
		{"cnpj payer, cpf customer", "12345678909", Payer{Document: "12345678000195"}, PayerMatchNo},
		{"cnpj equal", "12.345.678/0001-95", Payer{Document: "12345678000195"}, PayerMatchYes},
		{"masked, revealed digits equal", "12345678909", Payer{Document: "***456789**"}, PayerMatchYes},
		{"masked, a revealed digit differs", "12345678909", Payer{Document: "***456780**"}, PayerMatchNo},
		{"masked, too few digits shown", "12345678909", Payer{Document: "*********09"}, PayerMatchUnknown},
		{"nothing revealed", "12345678909", Payer{Document: "***********"}, PayerMatchUnknown},
		{"no payer document", "12345678909", Payer{Name: "Fulano"}, PayerMatchUnknown},
		{"customer has no tax id", "", Payer{Document: "12345678909"}, PayerMatchUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MatchPayer(tc.tax, tc.p); got != tc.want {
				t.Fatalf("got %s want %s", got, tc.want)
			}
		})
	}
}
