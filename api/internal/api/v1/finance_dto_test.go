package v1

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

func decodeProbe(t *testing.T, body string, dst any) int {
	t.Helper()
	app := fiber.New()
	status := 0
	app.Post("/", func(c fiber.Ctx) error {
		if p := decodeStrict(c, dst); p != nil {
			status = p.Status
			return p.Send(c)
		}
		status = 200
		return c.SendStatus(200)
	})
	if _, err := app.Test(httptest.NewRequest("POST", "/", strings.NewReader(body))); err != nil {
		t.Fatal(err)
	}
	return status
}

func TestAnUnknownFieldIsRefused(t *testing.T) {
	var req createBillRequest
	if got := decodeProbe(t, `{"direction":"payable","amount":100,"catagory_id":"rent"}`, &req); got != 400 {
		t.Fatalf("a misspelt field answered %d, want 400", got)
	}
	var ok createBillRequest
	if got := decodeProbe(t, `{"direction":"payable","amount":100,"category_id":"rent"}`, &ok); got != 200 || ok.CategoryID != "rent" {
		t.Fatalf("a valid body answered %d (%+v)", got, ok)
	}
	var trailing createBillRequest
	if got := decodeProbe(t, `{"amount":1} {"amount":2}`, &trailing); got != 400 {
		t.Fatalf("trailing JSON answered %d, want 400", got)
	}
}

func TestCreateBillValidationNamesTheFields(t *testing.T) {
	errs := createBillRequest{}.validate()
	fields := map[string]bool{}
	for _, e := range errs {
		fields[e.Field] = true
	}
	for _, want := range []string{"direction", "amount", "account_id", "category_id", "due_date"} {
		if !fields[want] {
			t.Errorf("no field error for %s (got %v)", want, errs)
		}
	}
}

func TestARecurrenceExpressionIsValidatedLikeAStoredOne(t *testing.T) {
	good := scheduleRequest{Expression: []byte(`{"kind":"day_of_month","day":10}`)}
	good.Start = parseDate(t, "2026-01-01")
	if _, errs := good.schedule(); len(errs) != 0 {
		t.Fatalf("a valid schedule was refused: %v", errs)
	}
	bad := scheduleRequest{Expression: []byte(`{"kind":"day_of_month","day":40}`)}
	bad.Start = parseDate(t, "2026-01-01")
	if _, errs := bad.schedule(); len(errs) == 0 {
		t.Fatal("day 40 was accepted")
	}
	excludeOnly := scheduleRequest{Expression: []byte(`{"kind":"months_of_year","months":[12]}`)}
	excludeOnly.Start = parseDate(t, "2026-01-01")
	if _, errs := excludeOnly.schedule(); len(errs) == 0 {
		t.Fatal("an exclusion-only kind was accepted as a schedule")
	}
}

func parseDate(t *testing.T, s string) brcal.Date {
	t.Helper()
	d, err := brcal.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
