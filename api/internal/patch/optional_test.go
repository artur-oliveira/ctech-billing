package patch

import (
	"encoding/json"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

type body struct {
	End  Optional[brcal.Date] `json:"end"`
	Note Optional[string]     `json:"note"`
	Day  Optional[int]        `json:"day"`
}

// A PATCH body has three states per field, and Go's *T has two: absent and
// null both decode to nil. Optional tells them apart.
func TestOptionalTellsAbsentFromNullFromAValue(t *testing.T) {
	var b body
	if err := json.Unmarshal([]byte(`{"end":null,"note":"x"}`), &b); err != nil {
		t.Fatal(err)
	}
	if !b.End.Present() || !b.End.IsNull() {
		t.Fatalf("end = %+v, want present and null", b.End)
	}
	if _, ok := b.End.Get(); ok {
		t.Fatal("a null end has a value")
	}
	if v, ok := b.Note.Get(); !ok || v != "x" || b.Note.IsNull() {
		t.Fatalf("note = %+v, want the value x", b.Note)
	}
	if b.Day.Present() || b.Day.IsNull() {
		t.Fatalf("day = %+v, want absent", b.Day)
	}
}

func TestOptionalDecodesItsValueThroughTheTypesOwnRules(t *testing.T) {
	var b body
	if err := json.Unmarshal([]byte(`{"end":"2026-05-10"}`), &b); err != nil {
		t.Fatal(err)
	}
	if v, ok := b.End.Get(); !ok || v != brcal.New(2026, time.May, 10) {
		t.Fatalf("end = %+v", b.End)
	}
	if err := json.Unmarshal([]byte(`{"end":"10/05/2026"}`), &b); err == nil {
		t.Fatal("a malformed date was accepted")
	}
}

func TestPtrIsNilUnlessThereIsAValue(t *testing.T) {
	var b body
	if err := json.Unmarshal([]byte(`{"day":null,"note":"y"}`), &b); err != nil {
		t.Fatal(err)
	}
	if b.Day.Ptr() != nil {
		t.Fatal("a null day has a pointer")
	}
	if p := b.Note.Ptr(); p == nil || *p != "y" {
		t.Fatalf("note ptr = %v", p)
	}
	if (Optional[int]{}).Ptr() != nil {
		t.Fatal("an absent day has a pointer")
	}
}

func TestOfAndNullBuildTheStatesForCallers(t *testing.T) {
	if v, ok := Of(3).Get(); !ok || v != 3 {
		t.Fatal("Of(3) has no value")
	}
	if n := Null[int](); !n.Present() || !n.IsNull() {
		t.Fatal("Null is not a present null")
	}
}
