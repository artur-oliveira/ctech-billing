package statement

import (
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
)

func day(d int) brcal.Date { return brcal.New(2026, time.March, d) }

func keysOf(k []Keyed) []string {
	out := make([]string, len(k))
	for i := range k {
		out[i] = k[i].Key
	}
	return out
}

func TestKeysAreTheSameForTheSameFile(t *testing.T) {
	lines := []Line{
		{Date: day(1), Amount: -500, Description: "Café", FITID: "A"},
		{Date: day(1), Amount: -500, Description: "Café"},
		{Date: day(1), Amount: -500, Description: "Café"},
	}
	a, b := keysOf(Keys("bank", lines)), keysOf(Keys("bank", lines))
	if a[0] != "F:A" {
		t.Fatalf("a unique FITID is its own key: %q", a[0])
	}
	if k := Keys("bank", lines); !strings.HasPrefix(k[0].Fallback, "H:") || k[1].Fallback != "" {
		t.Fatalf("a FITID line carries its content key as a fallback: %+v", k)
	}
	if a[1] == a[2] {
		t.Fatal("two identical lines without a FITID must be two keys (the ordinal)")
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("line %d: %q then %q", i, a[i], b[i])
		}
	}
	if other := keysOf(Keys("savings", lines)); other[1] == a[1] {
		t.Fatal("the account is part of the hash")
	}
}

// A bank that reuses one FITID for different lines must not collapse them.
func TestARepeatedFITIDFallsBackToTheHash(t *testing.T) {
	lines := []Line{
		{Date: day(1), Amount: -500, Description: "Café", FITID: "0"},
		{Date: day(2), Amount: -700, Description: "Pão", FITID: "0"},
	}
	k := keysOf(Keys("bank", lines))
	if k[0] == k[1] || k[0] == "F:0" || k[1] == "F:0" {
		t.Fatalf("keys = %v", k)
	}
}

// An export that overlaps an earlier one, with one more identical coffee, adds
// only that coffee.
func TestAnOverlappingExportAddsOnlyTheNewLine(t *testing.T) {
	coffee := Line{Date: day(1), Amount: -500, Description: "Café"}
	first := keysOf(Keys("bank", []Line{coffee}))
	second := keysOf(Keys("bank", []Line{{Date: day(1), Amount: -900, Description: "Almoço"}, coffee, coffee}))
	if second[1] != first[0] || second[2] == first[0] {
		t.Fatalf("first %v, second %v", first, second)
	}
}

func TestCandidates(t *testing.T) {
	bill := func(id string, dir finance.Direction, amount billing.Cents, due int, acct string) finance.Bill {
		return finance.Bill{ID: id, Direction: dir, Amount: amount, Due: day(due), AccountID: acct, Status: finance.BillForecast}
	}
	paid := bill("paid", finance.Payable, 1000, 10, "bank")
	paid.Status = finance.BillPaid
	bills := []finance.Bill{
		bill("far", finance.Payable, 1000, 16, "bank"),      // 6 days: out
		bill("late", finance.Payable, 1000, 13, "bank"),     // 3 days
		bill("early", finance.Payable, 1000, 7, "bank"),     // 3 days, earlier
		bill("exact", finance.Payable, 1000, 10, "bank"),    // 0 days
		bill("other", finance.Payable, 1000, 10, "savings"), // other account
		bill("amount", finance.Payable, 1001, 10, "bank"),   // other amount
		bill("in", finance.Receivable, 1000, 10, "bank"),    // other direction
		bill("edge", finance.Payable, 1000, 5, "bank"),      // exactly 5 days: in
		paid,
	}
	got := Candidates("bank", Line{Date: day(10), Amount: -1000}, bills)
	want := []string{"exact", "early", "late", "edge"}
	if len(got) != len(want) {
		t.Fatalf("got %d candidates: %+v", len(got), got)
	}
	for i := range want {
		if got[i].ID != want[i] {
			t.Errorf("candidate %d = %s, want %s", i, got[i].ID, want[i])
		}
	}
	if in := Candidates("bank", Line{Date: day(10), Amount: 1000}, bills); len(in) != 1 || in[0].ID != "in" {
		t.Fatalf("money in matches the receivable: %+v", in)
	}
}

func TestCandidatesAreBounded(t *testing.T) {
	var bills []finance.Bill
	for i := range 8 {
		bills = append(bills, finance.Bill{ID: string(rune('a' + i)), Direction: finance.Payable, Amount: 1, Due: day(10), AccountID: "b", Status: finance.BillForecast})
	}
	if got := Candidates("b", Line{Date: day(10), Amount: -1}, bills); len(got) != MaxCandidates {
		t.Fatalf("%d candidates", len(got))
	}
}

// A bill the recurrence's auto-settle already paid is offered to a line only
// in the exact case: same account and direction, the exact amount, paid within
// MatchWindow days of the line. A different amount (interest, a discount) is
// not offered; the person ignores the line.
func TestPaidCandidates(t *testing.T) {
	paid := func(id string, dir finance.Direction, amount billing.Cents, paidOn int, acct string) finance.Bill {
		return finance.Bill{ID: id, Direction: dir, Amount: amount, Due: day(1), PaidDate: day(paidOn), AccountID: acct, Status: finance.BillPaid}
	}
	open := paid("open", finance.Payable, 1000, 10, "bank")
	open.Status, open.PaidDate = finance.BillForecast, brcal.Date{}
	bills := []finance.Bill{
		paid("far", finance.Payable, 1000, 16, "bank"),      // 6 days: out
		paid("near", finance.Payable, 1000, 12, "bank"),     // 2 days
		paid("exact", finance.Payable, 1000, 10, "bank"),    // 0 days (due is far: the paid date decides)
		paid("other", finance.Payable, 1000, 10, "savings"), // other account
		paid("interest", finance.Payable, 1050, 10, "bank"), // other amount
		paid("in", finance.Receivable, 1000, 10, "bank"),    // other direction
		paid("edge", finance.Payable, 1000, 5, "bank"),      // exactly 5 days: in
		open, // not paid
	}
	got := PaidCandidates("bank", Line{Date: day(10), Amount: -1000}, bills)
	want := []string{"exact", "near", "edge"}
	if len(got) != len(want) {
		t.Fatalf("got %d candidates: %+v", len(got), got)
	}
	for i := range want {
		if got[i].ID != want[i] {
			t.Errorf("candidate %d = %s, want %s", i, got[i].ID, want[i])
		}
	}
	if in := PaidCandidates("bank", Line{Date: day(10), Amount: 1000}, bills); len(in) != 1 || in[0].ID != "in" {
		t.Fatalf("money in matches the paid receivable: %+v", in)
	}
}
