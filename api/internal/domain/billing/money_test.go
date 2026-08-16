package billing

import "testing"

func TestMulDivRoundsHalfAwayFromZero(t *testing.T) {
	cases := []struct {
		name     string
		amount   Cents
		num, den int64
		want     Cents
	}{
		{"exact division", 10000, 1, 2, 5000},
		{"rounds half up", 100, 1, 8, 13},       // 12.5 -> 13
		{"rounds half up again", 300, 1, 8, 38}, // 37.5 -> 38
		{"rounds down below half", 100, 1, 3, 33},
		{"rounds up above half", 200, 1, 3, 67},
		{"negative rounds away from zero", -100, 1, 8, -13},
		{"negative below half", -100, 1, 3, -33},
		{"negative denominator", 100, 1, -8, -13},
		{"zero amount", 0, 15, 30, 0},
		{"whole amount", 9999, 30, 30, 9999},
		{"odd denominator never hits an exact half", 100, 1, 7, 14},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := MulDiv(tc.amount, tc.num, tc.den); got != tc.want {
				t.Fatalf("MulDiv(%d, %d, %d) = %d, want %d", tc.amount, tc.num, tc.den, got, tc.want)
			}
		})
	}
}

func TestMulDivIsSymmetric(t *testing.T) {
	// Crediting the same fraction that was charged must return exactly the amount
	// charged. Banker's rounding breaks this for half-cases; half-away-from-zero
	// does not, which is why it is the policy.
	for amount := Cents(1); amount <= 5000; amount += 7 {
		for _, den := range []int64{3, 7, 28, 30, 31, 365} {
			for num := int64(1); num < den; num += den / 3 {
				charged := MulDiv(amount, num, den)
				credited := MulDiv(-amount, num, den)
				if charged+credited != 0 {
					t.Fatalf("MulDiv(%d,%d,%d)=%d but MulDiv(%d,%d,%d)=%d",
						amount, num, den, charged, -amount, num, den, credited)
				}
			}
		}
	}
}

func TestMulDivPanicsOnZeroDenominator(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("MulDiv must panic on a zero denominator rather than return a wrong amount")
		}
	}()
	MulDiv(100, 1, 0)
}

func TestCentsString(t *testing.T) {
	cases := map[Cents]string{
		0:         "R$ 0,00",
		1:         "R$ 0,01",
		99:        "R$ 0,99",
		100:       "R$ 1,00",
		1234:      "R$ 12,34",
		100000:    "R$ 1.000,00",
		123456789: "R$ 1.234.567,89",
		-1234:     "-R$ 12,34",
	}
	for c, want := range cases {
		if got := c.String(); got != want {
			t.Errorf("Cents(%d).String() = %q, want %q", int64(c), got, want)
		}
	}
}

func TestMaxChargeCentsMatchesTheWalletCeiling(t *testing.T) {
	// ADR 0004: R$ 10.000,00. If this constant is edited, `max_charge_cents` for
	// the billing client in ctech-wallet's m2m-clients parameter must be edited in
	// the same change or the two repos silently disagree — and the disagreement
	// surfaces as a 422 on a customer's first charge, not as a failing test.
	if MaxChargeCents != 1_000_000 || MaxChargeCents.String() != "R$ 10.000,00" {
		t.Fatalf("MaxChargeCents = %d (%s), want 1000000 (R$ 10.000,00)", MaxChargeCents, MaxChargeCents)
	}
}
