package finance

import (
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

func TestSplitInstallmentsPrintsLikeAnIssuer(t *testing.T) {
	parts, err := SplitInstallments(10000, 3)
	if err != nil {
		t.Fatal(err)
	}
	if parts[0] != 3334 || parts[1] != 3333 || parts[2] != 3333 {
		t.Fatalf("parts = %v, want [3334 3333 3333]", parts)
	}
}

func TestSplitInstallmentsAlwaysSumsToTheTotal(t *testing.T) {
	for _, total := range []billing.Cents{48, 99, 100, 101, 999, 12345, 120000, 99999999} {
		for n := 1; n <= MaxInstallments; n++ {
			if total < billing.Cents(n) {
				continue
			}
			parts, err := SplitInstallments(total, n)
			if err != nil {
				t.Fatalf("%d in %d: %v", total, n, err)
			}
			var sum billing.Cents
			for _, p := range parts {
				if p <= 0 {
					t.Fatalf("%d in %d: a non-positive part %v", total, n, parts)
				}
				sum += p
			}
			if sum != total {
				t.Fatalf("%d in %d: parts sum to %d", total, n, sum)
			}
		}
	}
}

func TestSplitInstallmentsRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		total billing.Cents
		n     int
	}{
		"zero installments":        {1000, 0},
		"49 installments":          {100000, 49},
		"less than a centavo each": {5, 12},
		"zero total":               {0, 1},
	} {
		if _, err := SplitInstallments(tc.total, tc.n); !errors.Is(err, ErrInvalidInstallments) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
}

func TestStatementFor(t *testing.T) {
	cases := []struct {
		name    string
		day     int
		closing int
		want    Month
	}{
		{"before closing", 10, 15, Month{2026, time.March}},
		{"on the closing day", 15, 15, Month{2026, time.March}},
		{"after closing", 16, 15, Month{2026, time.April}},
	}
	for _, tc := range cases {
		if got := StatementFor(d(2026, time.March, tc.day), tc.closing); got != tc.want {
			t.Fatalf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
	// Closing on the 31st clamps to 28/02: a purchase on 28/02 is on February's statement.
	if got := StatementFor(d(2026, time.February, 28), 31); got != (Month{2026, time.February}) {
		t.Fatalf("clamped closing day: %s", got)
	}
	// After closing in December rolls into next year.
	if got := StatementFor(d(2026, time.December, 20), 10); got != (Month{2027, time.January}) {
		t.Fatalf("year boundary: %s", got)
	}
}

func TestAllocateInstallments(t *testing.T) {
	plan, err := AllocateInstallments(120000, 12, d(2026, time.March, 20), 15)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 12 || plan[0].Statement != (Month{2026, time.April}) || plan[11].Statement != (Month{2027, time.March}) {
		t.Fatalf("plan = %+v", plan)
	}
	for i, inst := range plan {
		if inst.Number != i+1 || inst.Amount != 10000 {
			t.Fatalf("installment %d = %+v", i, inst)
		}
	}
	if _, err := AllocateInstallments(120000, 12, d(2026, time.March, 20), 0); !errors.Is(err, ErrInvalidInstallments) {
		t.Fatalf("closing day 0: err = %v", err)
	}
}

func TestAdvanceMovesOnlyFutureInstallments(t *testing.T) {
	plan, _ := AllocateInstallments(30000, 3, d(2026, time.March, 1), 15) // Mar, Apr, May
	got := Advance(plan, Month{2026, time.April})
	want := []Month{{2026, time.March}, {2026, time.April}, {2026, time.April}}
	for i := range want {
		if got[i].Statement != want[i] {
			t.Fatalf("got %+v", got)
		}
	}
	if plan[2].Statement != (Month{2026, time.May}) {
		t.Fatal("Advance changed its input")
	}
}

func TestRefundPlanCreditsWhatWasBilledAndRemovesTheRest(t *testing.T) {
	plan, _ := AllocateInstallments(30000, 3, d(2026, time.March, 1), 15) // Mar, Apr, May
	credit, removed := RefundPlan(plan, Month{2026, time.April})
	if credit != 10000 {
		t.Fatalf("credit = %d, want the March installment", credit)
	}
	if len(removed) != 2 || removed[0].Number != 2 || removed[1].Number != 3 {
		t.Fatalf("removed = %+v", removed)
	}
}
