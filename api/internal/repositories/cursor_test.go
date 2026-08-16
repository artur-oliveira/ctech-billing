package repositories

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// A cursor is the only piece of the key layout that leaves the service, so the
// round trip has to be exact: a cursor that decodes to a *different* key silently
// restarts or skips a page, and neither looks like an error to the caller.
func TestCursorRoundTrip(t *testing.T) {
	key := map[string]types.AttributeValue{
		"pk":        &types.AttributeValueMemberS{Value: "org_1#live"},
		"sk":        &types.AttributeValueMemberS{Value: "INVOICE#in_1"},
		"period_pk": &types.AttributeValueMemberS{Value: "org_1#live#INVOICE"},
		"period_sk": &types.AttributeValueMemberS{Value: "2026#03#05#in_1"},
	}

	cursor := EncodeCursor(key)
	if cursor == "" {
		t.Fatal("a four-attribute string key must produce a cursor")
	}

	got, err := DecodeCursor(cursor)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(key) {
		t.Fatalf("decoded %d attributes, want %d", len(got), len(key))
	}
	for name, want := range key {
		v, ok := got[name].(*types.AttributeValueMemberS)
		if !ok {
			t.Fatalf("attribute %q did not survive as a string", name)
		}
		if v.Value != want.(*types.AttributeValueMemberS).Value {
			t.Errorf("attribute %q = %q, want %q", name, v.Value, want.(*types.AttributeValueMemberS).Value)
		}
	}
}

func TestEmptyCursorMeansFirstPage(t *testing.T) {
	if got := EncodeCursor(nil); got != "" {
		t.Errorf("a page with no continuation must produce no cursor, got %q", got)
	}
	key, err := DecodeCursor("")
	if err != nil {
		t.Fatalf("an absent cursor is not an error: %v", err)
	}
	if key != nil {
		t.Errorf("an absent cursor must start from the beginning, got %v", key)
	}
}

// A cursor is opaque, so a client that invents one gets a clean 400 rather than
// a query built from whatever the bytes decoded to.
func TestGarbageCursorIsRejected(t *testing.T) {
	for _, cursor := range []string{"not-base64!!", "bm90LWpzb24"} {
		if _, err := DecodeCursor(cursor); err == nil {
			t.Errorf("cursor %q was accepted", cursor)
		}
	}
}

// Non-string key attributes cannot be represented, and no cursor is better than
// a truncated one that silently restarts the listing.
func TestNonStringKeyProducesNoCursor(t *testing.T) {
	key := map[string]types.AttributeValue{
		"pk":  &types.AttributeValueMemberS{Value: "org_1#live"},
		"seq": &types.AttributeValueMemberN{Value: "42"},
	}
	if got := EncodeCursor(key); got != "" {
		t.Errorf("cursor = %q, want empty", got)
	}
}
