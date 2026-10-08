package finance

import (
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

func rent() Recurrence {
	return Recurrence{
		ID: "r1", Direction: Payable, Amount: 150000, CategoryID: "rent", AccountID: "bank", Description: "Aluguel",
		Schedule: Schedule{Expression: DayOfMonth{Day: 10}, Start: d(2026, time.January, 1), Adjust: AdjustRollForward},
	}
}

func TestHorizonIsTheCurrentMonthAndTheNext(t *testing.T) {
	from, to := Horizon(d(2026, time.December, 15))
	if from != d(2026, time.December, 1) || to != d(2027, time.January, 31) {
		t.Fatalf("horizon = %s .. %s", from, to)
	}
	if _, to := Horizon(d(2026, time.January, 31)); to != d(2026, time.February, 28) {
		t.Fatalf("horizon end in a short month = %s", to)
	}
}

func TestRecurrenceValidate(t *testing.T) {
	if err := rent().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Recurrence){
		"no amount":   func(r *Recurrence) { r.Amount = 0 },
		"no category": func(r *Recurrence) { r.CategoryID = "" },
		"no account":  func(r *Recurrence) { r.AccountID = "" },
		"bad dir":     func(r *Recurrence) { r.Direction = "x" },
		"no schedule": func(r *Recurrence) { r.Schedule = Schedule{} },
	} {
		r := rent()
		mutate(&r)
		if err := r.Validate(); !errors.Is(err, ErrInvalidRecurrence) {
			t.Errorf("%s: err = %v, want ErrInvalidRecurrence", name, err)
		}
	}
}

func TestMaterialiseFillsTheHorizonOnce(t *testing.T) {
	r := rent()
	today := d(2026, time.March, 20)
	drafts, err := r.Materialise(brcal.Date{}, today)
	if err != nil {
		t.Fatal(err)
	}
	var noms []brcal.Date
	for _, dr := range drafts {
		noms = append(noms, dr.Nominal)
	}
	// A brand-new recurrence started in the past is caught up from Start.
	assertDates(t, noms, d(2026, time.January, 10), d(2026, time.February, 10), d(2026, time.March, 10), d(2026, time.April, 10))

	again, _ := r.Materialise(d(2026, time.April, 10), today)
	if len(again) != 0 {
		t.Fatalf("a second pass with the cursor at the last occurrence produced %d drafts", len(again))
	}
}

func TestMaterialisedDraftsCarryTheirIdentityAndTheRolledDueDate(t *testing.T) {
	r := rent()
	r.Schedule.Expression = DayOfMonth{Day: 31}
	r.Schedule.Start = d(2026, time.January, 1)
	drafts, _ := r.Materialise(brcal.Date{}, d(2026, time.January, 15))
	jan := drafts[0]
	// 31/01/2026 is a Saturday: due rolls to Monday 02/02, nominal stays in January.
	if jan.Nominal != d(2026, time.January, 31) || jan.Bill.Due != d(2026, time.February, 2) || jan.Bill.Competence != jan.Nominal {
		t.Fatalf("draft = nominal %s competence %s due %s", jan.Nominal, jan.Bill.Competence, jan.Bill.Due)
	}
	if jan.Bill.Origin != OriginRecurrence || jan.Bill.OriginRef != "r1@2026-01-31" || jan.Bill.Status != BillForecast {
		t.Fatalf("draft bill = %+v", jan.Bill)
	}
	if err := jan.Bill.Validate(); err != nil {
		t.Fatalf("a materialised bill must validate (the service only assigns the id): %v", err)
	}
}

// Review Focus 4.
func TestMaterialisationAcrossTheMonthBoundary(t *testing.T) {
	r := rent()
	r.Schedule.Expression = DayOfMonth{Day: 31}
	r.Schedule.Adjust = AdjustNone
	// Day 30/Jan: the horizon is January + February.
	d1, _ := r.Materialise(brcal.Date{}, d(2026, time.January, 30))
	last := d1[len(d1)-1].Nominal
	if last != d(2026, time.February, 28) {
		t.Fatalf("last in the horizon = %s, want 28/02 (clamped)", last)
	}
	// Day 1/Feb: the horizon is February + March. The cursor is 28/02, so only 31/03 is new.
	d2, _ := r.Materialise(last, d(2026, time.February, 1))
	if len(d2) != 1 || d2[0].Nominal != d(2026, time.March, 31) {
		t.Fatalf("next pass = %+v, want exactly 31/03", d2)
	}
}

func TestMaterialiseStopsAtTheEndDate(t *testing.T) {
	r := rent()
	r.Schedule.End = d(2026, time.February, 10) // inclusive
	drafts, _ := r.Materialise(brcal.Date{}, d(2026, time.March, 1))
	if n := len(drafts); n != 2 {
		t.Fatalf("got %d drafts, want January and February only", n)
	}
	if _, ok := r.NextMaterialiseDate(d(2026, time.February, 10)); ok {
		t.Fatal("a finished recurrence still has a next materialisation date")
	}
}

func TestNextMaterialiseDateIsWhenTheNextOccurrenceEntersTheHorizon(t *testing.T) {
	r := rent()
	// After 10/04 the next nominal is 10/05; it enters the horizon on 1/04.
	next, ok := r.NextMaterialiseDate(d(2026, time.April, 10))
	if !ok || next != d(2026, time.April, 1) {
		t.Fatalf("next = %s, %v; want 2026-04-01", next, ok)
	}
}

func TestPreviewListsTheNextOccurrences(t *testing.T) {
	s := rent().Schedule
	got, err := Preview(s, d(2026, time.March, 11), 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Nominal != d(2026, time.April, 10) || got[2].Nominal != d(2026, time.June, 10) {
		t.Fatalf("preview = %+v", got)
	}
	for _, n := range []int{0, 25, -1} {
		if _, err := Preview(s, d(2026, time.March, 11), n); !errors.Is(err, ErrInvalidRecurrence) {
			t.Errorf("n=%d: err = %v, want ErrInvalidRecurrence", n, err)
		}
	}
}
