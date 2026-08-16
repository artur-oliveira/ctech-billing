package billing

import (
	"errors"
	"fmt"
	"maps"
	"unicode/utf8"
)

// Metadata is the opaque key/value map attachable to Customer, Subscription,
// Invoice, Product, Price, CheckoutSession and CreditNote (ADR 0008).
//
// **Opaque means opaque.** No business rule in this package reads a metadata
// key, and none ever may. The day a dunning decision reads
// metadata["skip_dunning"], this stops being a free field and becomes an
// informal schema with no validation, no migration and no tests. A value that
// changes behavior deserves a first-class field.
type Metadata map[string]string

// Limits from ADR 0008. They exist because metadata is propagated in every
// webhook payload, so an unbounded map is an unbounded outbound request body.
const (
	MetadataMaxKeys     = 50
	MetadataMaxKeyLen   = 40
	MetadataMaxValueLen = 500
)

// ErrMetadataInvalid is the sentinel for every metadata rule violation; the
// wrapped message names which rule and which key.
var ErrMetadataInvalid = errors.New("invalid metadata")

// Validate checks the size limits. Nil and empty metadata are valid.
//
// Lengths are counted in runes, not bytes: a merchant writing a 40-character
// key in Portuguese should not be rejected because of accents.
func (m Metadata) Validate() error {
	if len(m) > MetadataMaxKeys {
		return fmt.Errorf("%w: %d keys exceeds the limit of %d", ErrMetadataInvalid, len(m), MetadataMaxKeys)
	}
	for k, v := range m {
		if k == "" {
			return fmt.Errorf("%w: empty key", ErrMetadataInvalid)
		}
		if n := utf8.RuneCountInString(k); n > MetadataMaxKeyLen {
			return fmt.Errorf("%w: key %q is %d characters, limit is %d", ErrMetadataInvalid, k, n, MetadataMaxKeyLen)
		}
		if n := utf8.RuneCountInString(v); n > MetadataMaxValueLen {
			return fmt.Errorf("%w: value for key %q is %d characters, limit is %d", ErrMetadataInvalid, k, n, MetadataMaxValueLen)
		}
	}
	return nil
}

// Clone returns an independent copy, or nil for nil.
//
// This is what makes "copied, not referenced" (ADR 0008) real: an Invoice takes
// a Clone of the Subscription's metadata at generation time. If it shared the
// map, editing the subscription would rewrite the past of closed invoices.
func (m Metadata) Clone() Metadata {
	if m == nil {
		return nil
	}
	return maps.Clone(m)
}

// Apply returns m updated by patch, per-key rather than by blind merge:
// a key present in patch replaces its value, and a key whose patch value is the
// empty string is **removed**. Keys absent from patch are untouched.
//
// Deletion-by-empty-value is the convention integrators already know from other
// billing APIs. The alternative — a separate delete call — means a client that
// wants to clear a key has to make two requests and handle a partial failure.
//
// m is not modified; the result is validated before being returned, so a patch
// that would breach a limit fails without leaving a half-applied map behind.
func (m Metadata) Apply(patch Metadata) (Metadata, error) {
	if len(patch) == 0 {
		return m.Clone(), nil
	}
	out := m.Clone()
	if out == nil {
		out = make(Metadata, len(patch))
	}
	for k, v := range patch {
		if v == "" {
			delete(out, k)
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		out = nil
	}
	if err := out.Validate(); err != nil {
		return nil, err
	}
	return out, nil
}
