package v1

import (
	"strings"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/limits"
)

// Finanças: a card and any account the person owns can be renamed. The name is
// validated exactly as on create: required, trimmed, at most limits.AccountName.

func TestACardPatchRenamesWithATrimmedName(t *testing.T) {
	req := decodeInto[cardPatchRequest](t, `{"name":"  Nubank roxinho  "}`)
	p, errs := req.edit()
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	if p.Name == nil || *p.Name != "Nubank roxinho" {
		t.Fatalf("name = %v, want the trimmed name", p.Name)
	}
	absent := decodeInto[cardPatchRequest](t, `{"due_day":12}`)
	if p, _ := absent.edit(); p.Name != nil {
		t.Fatalf("an absent name changed: %q", *p.Name)
	}
}

func TestACardPatchRefusesAnEmptyNullOrLongName(t *testing.T) {
	for body, code := range map[string]string{
		`{"name":null}`:  "required",
		`{"name":""}`:    "required",
		`{"name":"   "}`: "required",
		`{"name":"` + strings.Repeat("a", limits.AccountName+1) + `"}`: "too_long",
	} {
		req := decodeInto[cardPatchRequest](t, body)
		_, errs := req.edit()
		if got := fieldsOf(errs)["name"]; got != code {
			t.Errorf("%.40s: code %q, want %q (all: %v)", body, got, code, errs)
		}
	}
}

func TestAnAccountPatchNeedsANameAndTrimsIt(t *testing.T) {
	req := decodeInto[accountPatchRequest](t, `{"name":"  Conta conjunta "}`)
	name, errs := req.edit()
	if len(errs) != 0 || name != "Conta conjunta" {
		t.Fatalf("name %q errs %v", name, errs)
	}
	for body, code := range map[string]string{
		`{}`:            "required",
		`{"name":null}`: "required",
		`{"name":" "}`:  "required",
		`{"name":"` + strings.Repeat("é", limits.AccountName+1) + `"}`: "too_long",
		`{"name":"a\u0007b"}`: "invalid_chars",
	} {
		req := decodeInto[accountPatchRequest](t, body)
		if _, errs := req.edit(); fieldsOf(errs)["name"] != code {
			t.Errorf("%.40s: errs %v, want %s", body, errs, code)
		}
	}
	var other accountPatchRequest
	if got := decodeProbe(t, `{"name":"x","class":"asset"}`, &other); got != 400 {
		t.Fatalf("an account patch with class decoded as %d, want 400 (names only)", got)
	}
}
