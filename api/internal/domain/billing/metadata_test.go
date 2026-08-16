package billing

import (
	"errors"
	"strings"
	"testing"
)

func TestMetadataValidate(t *testing.T) {
	if err := Metadata(nil).Validate(); err != nil {
		t.Fatalf("nil metadata must be valid: %v", err)
	}
	if err := (Metadata{"nfe": "12345"}).Validate(); err != nil {
		t.Fatalf("ordinary metadata must be valid: %v", err)
	}

	tooMany := Metadata{}
	for i := range MetadataMaxKeys + 1 {
		tooMany[string(rune('a'+i%26))+strings.Repeat("x", i)] = "v"
	}
	if err := tooMany.Validate(); !errors.Is(err, ErrMetadataInvalid) {
		t.Fatalf("too many keys must be rejected, got %v", err)
	}

	longKey := Metadata{strings.Repeat("k", MetadataMaxKeyLen+1): "v"}
	if err := longKey.Validate(); !errors.Is(err, ErrMetadataInvalid) {
		t.Fatalf("over-long key must be rejected, got %v", err)
	}

	longValue := Metadata{"k": strings.Repeat("v", MetadataMaxValueLen+1)}
	if err := longValue.Validate(); !errors.Is(err, ErrMetadataInvalid) {
		t.Fatalf("over-long value must be rejected, got %v", err)
	}

	if err := (Metadata{"": "v"}).Validate(); !errors.Is(err, ErrMetadataInvalid) {
		t.Fatal("empty key must be rejected")
	}
}

func TestMetadataLimitsCountRunesNotBytes(t *testing.T) {
	// "ç" is two bytes. A 40-character Portuguese key must not be rejected for
	// being 45 bytes long.
	key := strings.Repeat("ç", MetadataMaxKeyLen)
	if err := (Metadata{key: "v"}).Validate(); err != nil {
		t.Fatalf("a %d-rune key must be accepted: %v", MetadataMaxKeyLen, err)
	}
	if err := (Metadata{strings.Repeat("ç", MetadataMaxKeyLen+1): "v"}).Validate(); err == nil {
		t.Fatal("a key one rune over the limit must be rejected")
	}
}

func TestMetadataCloneIsIndependent(t *testing.T) {
	// This is what makes "copied, not referenced" real: editing the subscription
	// after an invoice was generated must not rewrite the invoice's history.
	sub := Metadata{"plan": "dfe-basic"}
	invoice := sub.Clone()
	sub["plan"] = "dfe-pro"
	if invoice["plan"] != "dfe-basic" {
		t.Fatalf("clone followed the original: %q", invoice["plan"])
	}
	if Metadata(nil).Clone() != nil {
		t.Fatal("cloning nil must stay nil")
	}
}

func TestMetadataApply(t *testing.T) {
	base := Metadata{"a": "1", "b": "2"}

	got, err := base.Apply(Metadata{"b": "22", "c": "3"})
	if err != nil {
		t.Fatal(err)
	}
	want := Metadata{"a": "1", "b": "22", "c": "3"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("key %q: got %q, want %q", k, got[k], v)
		}
	}

	// The original must be untouched — Apply is not an in-place mutation.
	if base["b"] != "2" || len(base) != 2 {
		t.Fatalf("Apply mutated the receiver: %v", base)
	}

	// An empty value deletes the key.
	got, err = base.Apply(Metadata{"a": ""})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["a"]; ok {
		t.Fatalf("empty value must delete the key, got %v", got)
	}
	if got["b"] != "2" {
		t.Fatal("deleting one key must not touch the others")
	}

	// Deleting the last key yields nil rather than an empty map, so the attribute
	// is omitted rather than stored as {}.
	got, err = Metadata{"only": "x"}.Apply(Metadata{"only": ""})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("emptying metadata must yield nil, got %v", got)
	}

	// Applying to nil metadata creates it.
	got, err = Metadata(nil).Apply(Metadata{"a": "1"})
	if err != nil || got["a"] != "1" {
		t.Fatalf("apply on nil: got %v, err %v", got, err)
	}

	// An empty patch is a no-op that still returns an independent copy.
	got, err = base.Apply(nil)
	if err != nil {
		t.Fatal(err)
	}
	got["a"] = "changed"
	if base["a"] != "1" {
		t.Fatal("no-op Apply returned an aliased map")
	}
}

func TestMetadataApplyRejectsPatchThatBreachesLimits(t *testing.T) {
	base := Metadata{"a": "1"}
	_, err := base.Apply(Metadata{"b": strings.Repeat("x", MetadataMaxValueLen+1)})
	if !errors.Is(err, ErrMetadataInvalid) {
		t.Fatalf("want ErrMetadataInvalid, got %v", err)
	}
	if len(base) != 1 || base["a"] != "1" {
		t.Fatalf("a rejected patch must leave the receiver untouched, got %v", base)
	}
}
