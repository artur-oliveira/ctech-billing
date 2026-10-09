package problem

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/domain/finance/statement"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

func decode(t *testing.T, p *Problem) map[string]any {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestEveryProblemHasACode(t *testing.T) {
	cases := map[string]*Problem{
		"bad_request":           BadRequest("x"),
		"not_found":             NotFound("x"),
		"internal_server_error": Internal("x"),
		"validation_error":      Validation(nil),
		"idempotency_conflict":  New(409, TypeIdempotencyConflict, "t", "d"),
		"no_billing_account":    New(403, TypeNoBillingAccount, "t", "d"),
		"explicit":              BadRequest("x").WithCode("explicit"),
	}
	for want, p := range cases {
		if got := decode(t, p)["code"]; got != want {
			t.Errorf("code = %v, want %q", got, want)
		}
	}
}

func TestValidationShape(t *testing.T) {
	p := Validation([]FieldError{Field("name", "too_long", "at most 10 characters", "max", 10)})
	m := decode(t, p)
	errs := m["errors"].([]any)
	if len(errs) != 1 {
		t.Fatalf("errors = %v", errs)
	}
	e := errs[0].(map[string]any)
	if e["field"] != "name" || e["code"] != "too_long" || e["message"] == "" {
		t.Errorf("field error = %v", e)
	}
	if e["params"].(map[string]any)["max"] != float64(10) {
		t.Errorf("params = %v", e["params"])
	}
}

func TestFromErrorIsEnglishAndCoded(t *testing.T) {
	for _, err := range []error{
		repositories.ErrNotFound, repositories.ErrCardMoved, repositories.ErrOpeningExists,
		fmt.Errorf("%w: x", finance.ErrInvalidBill), errors.New("boom"),
	} {
		p := FromError(err)
		if p.Code == "" || p.Title == "" {
			t.Errorf("%v: code %q title %q", err, p.Code, p.Title)
		}
		for _, r := range p.Detail {
			if r > 127 {
				t.Errorf("%v: detail %q is not plain English", err, p.Detail)
				break
			}
		}
	}
	if got := FromError(errors.New("boom")); got.Code != "internal_server_error" || got.Status != 500 {
		t.Errorf("unknown error = %+v", got)
	}
}

func TestImportErrorsHaveTheirCodes(t *testing.T) {
	for err, want := range map[error]string{
		repositories.ErrLineResolved:                             "line_already_reconciled",
		repositories.ErrLineMismatch:                             "line_bill_mismatch",
		statement.ErrCardStatement:                               "statement_card_not_supported",
		statement.ErrManyAccounts:                                "statement_many_accounts",
		statement.ErrCurrency:                                    "statement_currency",
		statement.ErrTooLarge:                                    "statement_too_large",
		statement.ErrTooMany:                                     "statement_too_large",
		statement.ErrEmpty:                                       "statement_empty",
		fmt.Errorf("%w: no <OFX>", statement.ErrUnreadable):      "statement_unreadable",
		statement.ErrNoMapping:                                   "csv_mapping_required",
		fmt.Errorf("%w: delimiter", statement.ErrInvalidMapping): "invalid_csv_mapping",
	} {
		if p := FromError(err); p.Code != want || p.Status >= 500 {
			t.Errorf("%v: %d %q, want %q", err, p.Status, p.Code, want)
		}
	}
}
