package v1

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestADifferentPaidAmountNeedsACategoryForTheGap(t *testing.T) {
	if e := settleGapError(10000, 10000, ""); e != nil {
		t.Errorf("an equal amount needs no category: %v", e)
	}
	if e := settleGapError(10200, 10000, "interest"); e != nil {
		t.Errorf("a category was supplied: %v", e)
	}
	e := settleGapError(10200, 10000, "")
	if e == nil || e.Field != "difference_category_id" {
		t.Fatalf("got %+v, want a field error on difference_category_id", e)
	}
}

func TestPreviewCountMustBeBetween1And24(t *testing.T) {
	h := &financeHandlers{clock: time.Now}
	app := fiber.New()
	app.Post("/", h.previewRecurrence)
	for _, n := range []int{0, 25, -3} {
		body := fmt.Sprintf(`{"expression":{"kind":"day_of_month","day":10},"start":"2026-01-01","count":%d}`, n)
		resp, err := app.Test(httptest.NewRequest("POST", "/", strings.NewReader(body)))
		if err != nil || resp.StatusCode != 422 {
			t.Errorf("count %d: status %d %v, want 422", n, resp.StatusCode, err)
		}
	}
	ok := `{"expression":{"kind":"day_of_month","day":10},"start":"2026-01-01","count":3}`
	resp, err := app.Test(httptest.NewRequest("POST", "/", strings.NewReader(ok)))
	if err != nil || resp.StatusCode != 200 {
		t.Errorf("a valid preview answered %d %v", resp.StatusCode, err)
	}
}

func TestReportPeriodsAreBounded(t *testing.T) {
	for _, tc := range []struct {
		from, to string
		ok       bool
	}{
		{"2026-01", "2026-12", true},
		{"2026-01", "2027-12", true},  // 24 months
		{"2026-01", "2028-01", false}, // 25
		{"2026-05", "2026-04", false},
		{"2026-13", "2026-12", false},
		{"", "2026-12", false},
	} {
		_, _, err := parseMonthRange(tc.from, tc.to)
		if (err == nil) != tc.ok {
			t.Errorf("%s..%s: err=%v, want ok=%v", tc.from, tc.to, err, tc.ok)
		}
	}
}

func TestStatementPeriodIsBounded(t *testing.T) {
	for _, tc := range []struct {
		from, to string
		ok       bool
	}{
		{"2026-03-01", "2026-04-01", true},
		{"2026-01-01", "2027-01-02", true},  // 366 days
		{"2026-01-01", "2027-01-03", false}, // 367
		{"2026-04-01", "2026-04-01", false},
		{"2026-02-30", "2026-03-01", false},
	} {
		_, _, err := parseDateRange(tc.from, tc.to)
		if (err == nil) != tc.ok {
			t.Errorf("%s..%s: err=%v, want ok=%v", tc.from, tc.to, err, tc.ok)
		}
	}
}

func TestTransferValidationNamesTheFields(t *testing.T) {
	var req transferRequest
	if code := decodeProbe(t, `{"from_account_id":"a","to_account_id":"a","amount":0,"date":"x"}`, &req); code != 200 {
		t.Fatalf("decode %d", code)
	}
	errs := req.validate()
	want := map[string]bool{"to_account_id": true, "amount": true, "date": true}
	for _, e := range errs {
		delete(want, e.Field)
	}
	if len(want) != 0 {
		t.Fatalf("missing field errors: %v (got %+v)", want, errs)
	}
}
