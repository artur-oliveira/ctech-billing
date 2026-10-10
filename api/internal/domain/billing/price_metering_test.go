package billing

import (
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

func levelMeteredPrice() *Price {
	return &Price{
		ID: "p", Type: PriceMetered, Currency: CurrencyBRL, UnitAmount: 490,
		Recurrence: Recurrence{Interval: IntervalMonth, Count: 1}, Timing: BillArrears,
	}
}

func TestAMaxPriceNamesItsMeter(t *testing.T) {
	p := levelMeteredPrice()
	p.Aggregation = AggregationMax
	if err := p.Validate(); !errors.Is(err, ErrInvalidPrice) {
		t.Fatalf("max without a meter: %v", err)
	}
	p.Meter = "finance_spaces"
	p.IncludedQuantity = 1
	if err := p.Validate(); err != nil {
		t.Fatalf("a valid max price: %v", err)
	}
}

func TestMeteringFieldsBelongToMeteredPrices(t *testing.T) {
	for name, mut := range map[string]func(*Price){
		"aggregation": func(p *Price) { p.Aggregation = AggregationSum },
		"meter":       func(p *Price) { p.Meter = "x" },
		"included":    func(p *Price) { p.IncludedQuantity = 1 },
	} {
		p := &Price{ID: "p", Type: PriceFixed, Currency: CurrencyBRL, UnitAmount: 1990,
			Recurrence: Recurrence{Interval: IntervalMonth, Count: 1}, Timing: BillAdvance}
		mut(p)
		if err := p.Validate(); !errors.Is(err, ErrInvalidPrice) {
			t.Errorf("%s on a fixed price: %v", name, err)
		}
	}
}

func TestMeteringFieldsAreChecked(t *testing.T) {
	for name, mut := range map[string]func(*Price){
		"unknown aggregation": func(p *Price) { p.Aggregation = "avg" },
		"negative included":   func(p *Price) { p.IncludedQuantity = -1 },
		"meter with a hash":   func(p *Price) { p.Meter = "a#b" },
		"meter upper case":    func(p *Price) { p.Meter = "Spaces" },
	} {
		p := levelMeteredPrice()
		mut(p)
		if err := p.Validate(); !errors.Is(err, ErrInvalidPrice) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestIncludedUnitsAreNotCharged(t *testing.T) {
	p := levelMeteredPrice()
	p.IncludedQuantity = 1
	period := Period{Start: brcal.New(2026, time.October, 1), End: brcal.New(2026, time.November, 1)}
	for units, want := range map[int64]Cents{0: 0, 1: 0, 4: 3 * 490} {
		line := MeteredLine(p, "Espaços", period, units)
		if line.Amount != want || line.Quantity != int64(want/490) {
			t.Errorf("units %d: line = %+v, want amount %d", units, line, want)
		}
	}
}

// Spec test 6: every existing price has no included units, so its line is the
// one it always was.
func TestAPriceWithoutIncludedUnitsBillsEveryUnit(t *testing.T) {
	p := levelMeteredPrice()
	line := MeteredLine(p, "NF-e", Period{}, 7)
	if line.Quantity != 7 || line.Amount != 7*490 {
		t.Fatalf("line = %+v", line)
	}
}
