package wire

import "testing"

// Change is a hint to read, not data (spec D3): a client that hears one asks the
// authorized REST API for the entity. Every field it has is on this list on
// purpose; adding one is a decision, and this test is where it gets made.
func TestChangeCarriesIdsAndStateOnly(t *testing.T) {
	allowed := map[string]bool{
		"id": true, "type": true, "entity": true, "entity_id": true,
		"status": true, "revision": true, "occurred_at_ms": true,
	}
	fields := (&Change{}).ProtoReflect().Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		if name := string(fields.Get(i).Name()); !allowed[name] {
			t.Errorf("Change.%s is not an id or a state word; the event must not carry data. Read spec D3 (docs/specs/2026-10-14-realtime.md) before adding it.", name)
		}
	}
	if fields.Len() != len(allowed) {
		t.Errorf("Change has %d fields, the allowed set has %d", fields.Len(), len(allowed))
	}
}
