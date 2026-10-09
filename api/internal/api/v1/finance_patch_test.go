package v1

import (
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/problem"
)

// UX batch 4. Every finance PATCH follows one rule: an absent field keeps what
// is stored, an explicit null clears an optional field, a value replaces it.
// Null on a required field is a 422 naming it, never "unchanged".

var patchToday = brcal.New(2026, time.October, 9)

func fieldsOf(errs []problem.FieldError) map[string]string {
	out := map[string]string{}
	for _, e := range errs {
		out[e.Field] = e.Code
	}
	return out
}

func decodeInto[T any](t *testing.T, body string) T {
	t.Helper()
	var req T
	if got := decodeProbe(t, body, &req); got != 200 {
		t.Fatalf("%s decoded as %d", body, got)
	}
	return req
}

func TestARecurrencePatchClearsItsEndWithNull(t *testing.T) {
	req := decodeInto[patchRecurrenceRequest](t, `{"end":null}`)
	p, errs := req.edit(patchToday)
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if !p.End.IsNull() {
		t.Fatalf("end = %+v, want a clear", p.End)
	}
}

func TestARecurrencePatchWithoutEndKeepsIt(t *testing.T) {
	req := decodeInto[patchRecurrenceRequest](t, `{"amount":100}`)
	p, errs := req.edit(patchToday)
	if len(errs) != 0 || p.End.Present() {
		t.Fatalf("end = %+v errs = %v, want untouched", p.End, errs)
	}
	if p.Amount == nil || *p.Amount != 100 {
		t.Fatalf("amount = %v", p.Amount)
	}
}

func TestARecurrencePatchSetsAnEnd(t *testing.T) {
	req := decodeInto[patchRecurrenceRequest](t, `{"end":"2027-01-10"}`)
	p, errs := req.edit(patchToday)
	if v, ok := p.End.Get(); len(errs) != 0 || !ok || v != brcal.New(2027, time.January, 10) {
		t.Fatalf("end = %+v errs = %v", p.End, errs)
	}
}

func TestARecurrencePatchClearsItsDescriptionWithNull(t *testing.T) {
	req := decodeInto[patchRecurrenceRequest](t, `{"description":null}`)
	p, errs := req.edit(patchToday)
	if len(errs) != 0 || p.Description == nil || *p.Description != "" {
		t.Fatalf("description = %v errs = %v, want cleared", p.Description, errs)
	}
}

func TestARecurrencePatchRefusesNullOnARequiredField(t *testing.T) {
	req := decodeInto[patchRecurrenceRequest](t, `{"amount":null,"category_id":null,"account_id":null,"auto_settle":null}`)
	_, errs := req.edit(patchToday)
	got := fieldsOf(errs)
	for _, f := range []string{"amount", "category_id", "account_id", "auto_settle"} {
		if got[f] != "required" {
			t.Errorf("%s: code %q, want required (all: %v)", f, got[f], errs)
		}
	}
}

func TestABillPatchClearsItsDescriptionWithNull(t *testing.T) {
	req := decodeInto[patchBillRequest](t, `{"description":null}`)
	e, errs := req.edit(patchToday)
	if len(errs) != 0 || e.Description == nil || *e.Description != "" {
		t.Fatalf("description = %v errs = %v, want cleared", e.Description, errs)
	}
	if e.Amount != nil || e.Due != nil || e.CategoryID != nil || e.AccountID != nil || e.AutoSettle != nil {
		t.Fatalf("absent fields changed: %+v", e)
	}
}

func TestABillPatchRefusesNullOnARequiredField(t *testing.T) {
	req := decodeInto[patchBillRequest](t, `{"amount":null,"category_id":null,"account_id":null,"due_date":null,"auto_settle":null}`)
	_, errs := req.edit(patchToday)
	got := fieldsOf(errs)
	for _, f := range []string{"amount", "category_id", "account_id", "due_date", "auto_settle"} {
		if got[f] != "required" {
			t.Errorf("%s: code %q, want required (all: %v)", f, got[f], errs)
		}
	}
}

func TestACardPatchClearsBrandAndDigitsWithNullOrEmpty(t *testing.T) {
	for _, body := range []string{`{"brand":null,"last4":null}`, `{"brand":"","last4":""}`} {
		req := decodeInto[cardPatchRequest](t, body)
		p, errs := req.edit()
		if len(errs) != 0 {
			t.Fatalf("%s: errs = %v", body, errs)
		}
		if p.Brand == nil || *p.Brand != "" || p.Last4 == nil || *p.Last4 != "" {
			t.Fatalf("%s: brand %v last4 %v, want both cleared", body, p.Brand, p.Last4)
		}
	}
	req := decodeInto[cardPatchRequest](t, `{"due_day":12}`)
	p, _ := req.edit()
	if p.Brand != nil || p.Last4 != nil {
		t.Fatalf("absent brand/last4 changed: %+v", p)
	}
}

func TestACardPatchRefusesNullOnARequiredField(t *testing.T) {
	req := decodeInto[cardPatchRequest](t, `{"closing_day":null,"due_day":null,"paying_account_id":null}`)
	_, errs := req.edit()
	got := fieldsOf(errs)
	for _, f := range []string{"closing_day", "due_day", "paying_account_id"} {
		if got[f] != "required" {
			t.Errorf("%s: code %q, want required (all: %v)", f, got[f], errs)
		}
	}
}

func TestTheDefaultReceivingAccountIsClearedWithNullAndRequiredOtherwise(t *testing.T) {
	cleared := decodeInto[defaultReceivingRequest](t, `{"default_receiving_account_id":null}`)
	if id, clear, errs := cleared.edit(); len(errs) != 0 || !clear || id != "" {
		t.Fatalf("null: id %q clear %v errs %v", id, clear, errs)
	}
	set := decodeInto[defaultReceivingRequest](t, `{"default_receiving_account_id":"acc_01"}`)
	if id, clear, errs := set.edit(); len(errs) != 0 || clear || id != "acc_01" {
		t.Fatalf("value: id %q clear %v errs %v", id, clear, errs)
	}
	absent := decodeInto[defaultReceivingRequest](t, `{}`)
	if _, _, errs := absent.edit(); fieldsOf(errs)["default_receiving_account_id"] != "required" {
		t.Fatalf("absent: errs %v, want required", errs)
	}
}
