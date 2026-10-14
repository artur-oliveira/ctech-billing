package v1

import (
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

func fm(y int, m time.Month) finance.Month { return finance.Month{Year: y, Month: m} }

func monthlyOn(day int, id, cardID string, amount billing.Cents, installments int) repositories.DueRecurrence {
	return repositories.DueRecurrence{Recurrence: finance.Recurrence{
		ID: id, CardID: cardID, Amount: amount, Installments: installments, Description: id,
		Schedule: finance.Schedule{Expression: finance.DayOfMonth{Day: day}, Start: brcal.New(2026, time.January, 1), Adjust: finance.AdjustRollForward},
	}}
}

func total(items []cardForecastDTO) (sum billing.Cents) {
	for _, it := range items {
		sum += it.Amount
	}
	return sum
}

var today = brcal.New(2026, time.October, 14)

func TestForecastPlacesEachFutureOccurrenceOnItsStatement(t *testing.T) {
	got, err := cardForecast("card", 25, fm(2026, time.October), []repositories.DueRecurrence{monthlyOn(10, "rent", "card", 10000, 1)}, nil, today,
		[]finance.Month{fm(2026, time.October), fm(2026, time.November), fm(2026, time.December)})
	if err != nil {
		t.Fatal(err)
	}
	if n := len(got[fm(2026, time.October)]); n != 0 {
		t.Fatalf("October's 10th is behind today, got %d items", n)
	}
	for _, m := range []finance.Month{fm(2026, time.November), fm(2026, time.December)} {
		if items := got[m]; len(items) != 1 || items[0].Amount != 10000 || items[0].PendingConfirmation {
			t.Fatalf("%s = %+v, want one 10000 item", m, items)
		}
	}
}

func TestForecastSplitsInstallmentsAcrossStatements(t *testing.T) {
	// 9000 in 3: the November occurrence bills Nov, Dec, Jan; December's occurrence starts its own.
	got, err := cardForecast("card", 25, fm(2026, time.October), []repositories.DueRecurrence{monthlyOn(10, "gym", "card", 9000, 3)}, nil, today,
		[]finance.Month{fm(2026, time.November), fm(2026, time.December)})
	if err != nil {
		t.Fatal(err)
	}
	if total(got[fm(2026, time.November)]) != 3000 || total(got[fm(2026, time.December)]) != 6000 || len(got[fm(2026, time.December)]) != 2 {
		t.Fatalf("Nov %+v Dec %+v, want 3000 and 6000 (two items)", got[fm(2026, time.November)], got[fm(2026, time.December)])
	}
}

func TestForecastSkipsWhatIsNotThisCardsOrIsStopped(t *testing.T) {
	archived := monthlyOn(10, "old", "card", 5000, 1)
	archived.Recurrence.Archived = true
	got, err := cardForecast("card", 25, fm(2026, time.October), []repositories.DueRecurrence{
		monthlyOn(10, "other", "another-card", 7000, 1), monthlyOn(10, "account", "", 8000, 1), archived,
	}, nil, today, []finance.Month{fm(2026, time.November)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got[fm(2026, time.November)]) != 0 {
		t.Fatalf("got %+v, want nothing: another card's, an account's and an archived one", got[fm(2026, time.November)])
	}
}

func TestForecastSkipsOccurrencesTheJobAlreadyMade(t *testing.T) {
	made := monthlyOn(10, "rent", "card", 10000, 1)
	made.Cursor = brcal.New(2026, time.November, 10) // November's is a purchase already
	got, err := cardForecast("card", 25, fm(2026, time.October), []repositories.DueRecurrence{made}, nil, today,
		[]finance.Month{fm(2026, time.November), fm(2026, time.December)})
	if err != nil {
		t.Fatal(err)
	}
	if len(got[fm(2026, time.November)]) != 0 || len(got[fm(2026, time.December)]) != 1 {
		t.Fatalf("Nov %+v Dec %+v, want only December's", got[fm(2026, time.November)], got[fm(2026, time.December)])
	}
}

func TestForecastCountsAnOccurrenceAwaitingConfirmationAsAnEstimate(t *testing.T) {
	pending := []repositories.PendingOccurrence{
		{RecurrenceID: "power", CardID: "card", Nominal: brcal.New(2026, time.November, 5), Expected: 21000, Installments: 1, Description: "Energia"},
		{RecurrenceID: "elsewhere", CardID: "another-card", Nominal: brcal.New(2026, time.November, 5), Expected: 99999, Installments: 1},
	}
	got, err := cardForecast("card", 25, fm(2026, time.October), nil, pending, today, []finance.Month{fm(2026, time.November)})
	if err != nil {
		t.Fatal(err)
	}
	items := got[fm(2026, time.November)]
	if len(items) != 1 || items[0].Amount != 21000 || !items[0].PendingConfirmation {
		t.Fatalf("got %+v, want one pending 21000 item", items)
	}
}

func TestForecastForSeveralMonthsEqualsOneMonthAtATime(t *testing.T) {
	recs := []repositories.DueRecurrence{monthlyOn(10, "gym", "card", 9000, 3), monthlyOn(28, "tv", "card", 4000, 1)}
	months := []finance.Month{fm(2026, time.November), fm(2026, time.December), fm(2027, time.January)}
	all, err := cardForecast("card", 25, fm(2026, time.October), recs, nil, today, months)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range months {
		one, err := cardForecast("card", 25, fm(2026, time.October), recs, nil, today, []finance.Month{m})
		if err != nil {
			t.Fatal(err)
		}
		if total(one[m]) != total(all[m]) || len(one[m]) != len(all[m]) {
			t.Fatalf("%s: alone %+v, with the others %+v", m, one[m], all[m])
		}
	}
}

func TestForecastTotalsSumTheMonthAndSayWhenAnyPartIsAnEstimate(t *testing.T) {
	sum, estimated := forecastTotals([]cardForecastDTO{{Amount: 10000}, {Amount: 2500, PendingConfirmation: true}})
	if sum != 12500 || !estimated {
		t.Fatalf("got %d, %v, want 12500 and an estimate", sum, estimated)
	}
	sum, estimated = forecastTotals([]cardForecastDTO{{Amount: 10000}})
	if sum != 10000 || estimated {
		t.Fatalf("got %d, %v, want 10000 and no estimate", sum, estimated)
	}
	sum, estimated = forecastTotals(nil)
	if sum != 0 || estimated {
		t.Fatalf("nothing forecast = %d, %v", sum, estimated)
	}
}
