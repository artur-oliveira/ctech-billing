package v1

import "gopkg.aoctech.app/api-commons/patch"

// The two shapes a PATCH field takes (finance spec § 6, UX batch 4).

// required is a field that must keep a value: null cannot clear it and is a 422
// naming the field. It answers the value and whether there is one to check.
func required[T any](c *checks, field string, o patch.Optional[T]) (T, bool) {
	if o.IsNull() {
		c.fail(field, "required", "required: it cannot be cleared")
	}
	return o.Get()
}

// clearable is an optional text field: null clears it, which for text is the
// empty string (stored as nothing). Absent stays nil: keep what is there. An
// explicit "" also clears, as it always has.
func clearable(o patch.Optional[string]) *string {
	if o.IsNull() {
		empty := ""
		return &empty
	}
	return o.Ptr()
}
