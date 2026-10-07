package finance

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

func TestExpressionRoundTrip(t *testing.T) {
	for _, e := range []Expression{
		DayOfMonth{Day: 10},
		WorkdayOfMonth{N: 5},
		WorkdayOfMonth{N: -1},
		NthWeekdayOfMonth{Weekday: time.Sunday, N: 2}, // Sunday is 0: must survive omitempty
		Weekly{Weekday: time.Friday, Every: 2, Anchor: d(2026, time.January, 2)},
		Yearly{Month: time.March, Day: 15},
		Difference{
			Include: DayOfMonth{Day: 10},
			Exclude: Difference{
				Include: WorkdayOfMonth{N: 1},
				Exclude: Dates{Days: []brcal.Date{d(2026, time.March, 2)}},
			},
		},
		Difference{Include: DayOfMonth{Day: 10}, Exclude: MonthsOfYear{Months: []time.Month{time.December}}},
	} {
		b, err := MarshalExpression(e)
		if err != nil {
			t.Fatalf("%#v: marshal: %v", e, err)
		}
		got, err := ParseExpression(b)
		if err != nil {
			t.Fatalf("%s: parse: %v", b, err)
		}
		if !reflect.DeepEqual(got, e) {
			t.Fatalf("round trip of %s: got %#v, want %#v", b, got, e)
		}
	}
}

func TestParseExpressionRefuses(t *testing.T) {
	deep := `{"kind":"day_of_month","day":1}`
	for range maxExpressionDepth {
		deep = `{"kind":"difference","include":` + deep + `,"exclude":{"kind":"dates"}}`
	}
	for name, in := range map[string]string{
		"not json":                      `{`,
		"unknown kind":                  `{"kind":"lunar"}`,
		"day 0":                         `{"kind":"day_of_month","day":0}`,
		"day 32":                        `{"kind":"day_of_month","day":32}`,
		"business day 0":                `{"kind":"workday_of_month","n":0}`,
		"business day 24":               `{"kind":"workday_of_month","n":24}`,
		"fifth weekday":                 `{"kind":"nth_weekday_of_month","weekday":1,"n":5}`,
		"weekday missing":               `{"kind":"nth_weekday_of_month","n":1}`,
		"weekday 7":                     `{"kind":"nth_weekday_of_month","weekday":7,"n":1}`,
		"every 0":                       `{"kind":"weekly","weekday":5,"every":0,"anchor":"2026-01-02"}`,
		"anchor on another weekday":     `{"kind":"weekly","weekday":1,"every":1,"anchor":"2026-01-02"}`,
		"anchor missing":                `{"kind":"weekly","weekday":5,"every":1}`,
		"30 February":                   `{"kind":"yearly","month":2,"day":30}`,
		"month 13":                      `{"kind":"yearly","month":13,"day":1}`,
		"difference without exclude":    `{"kind":"difference","include":{"kind":"day_of_month","day":1}}`,
		"exclusion kind as the include": `{"kind":"difference","include":{"kind":"dates"},"exclude":{"kind":"dates"}}`,
		"too deep":                      deep,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseExpression([]byte(in)); !errors.Is(err, ErrInvalidExpression) {
				t.Fatalf("err = %v, want ErrInvalidExpression", err)
			}
		})
	}
}

func TestParseScheduleRefusesAnExclusionOnlyKind(t *testing.T) {
	for _, in := range []string{`{"kind":"dates","dates":["2026-03-02"]}`, `{"kind":"months_of_year","months":[12]}`} {
		if _, err := ParseSchedule([]byte(in)); !errors.Is(err, ErrInvalidExpression) {
			t.Fatalf("%s: err = %v, want ErrInvalidExpression", in, err)
		}
	}
	if _, err := ParseSchedule([]byte(`{"kind":"day_of_month","day":10}`)); err != nil {
		t.Fatal(err)
	}
}
