package repositories

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/finance/statement"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// Every method refuses before touching a table; a nil client would panic.
func TestEveryImportMethodChecksTheSpaceAndTheVerb(t *testing.T) {
	r := &ImportRepository{}
	ctx, now := context.Background(), time.Now()
	full, _ := space.ForJob("USER#u1", true)
	viewer := space.Narrow(full, space.Read)
	importer := space.Narrow(full, space.Read|space.Import) // may upload, may not move cash
	var zero space.ResolvedSpace

	calls := map[string]struct {
		run  func(sp space.ResolvedSpace) error
		deny []space.ResolvedSpace
	}{
		"Import": {func(sp space.ResolvedSpace) error {
			_, err := r.Import(ctx, sp, "bank", statement.FormatOFX, statement.Parsed{}, "", now)
			return err
		}, []space.ResolvedSpace{zero, viewer}},
		"List": {func(sp space.ResolvedSpace) error { _, err := r.List(ctx, sp, "", now); return err }, []space.ResolvedSpace{zero}},
		"Get":  {func(sp space.ResolvedSpace) error { _, _, err := r.Get(ctx, sp, "i", now); return err }, []space.ResolvedSpace{zero}},
		"Match": {func(sp space.ResolvedSpace) error {
			_, _, err := r.Match(ctx, sp, "i", 1, "b", "", PostMeta{}, now)
			return err
		}, []space.ResolvedSpace{zero, viewer, importer}},
		"Create": {func(sp space.ResolvedSpace) error {
			_, _, err := r.Create(ctx, sp, "i", 1, "food", "", PostMeta{}, now)
			return err
		}, []space.ResolvedSpace{zero, viewer, importer}},
		// Linking posts nothing, but it binds a bill: import and write.
		"Link": {func(sp space.ResolvedSpace) error {
			_, _, err := r.Link(ctx, sp, "i", 1, "b", now)
			return err
		}, []space.ResolvedSpace{zero, viewer, importer}},
		"AutoPaid": {func(sp space.ResolvedSpace) error {
			_, err := r.AutoPaid(ctx, sp, "bank", []ImportLine{{N: 1}})
			return err
		}, []space.ResolvedSpace{zero}},
		"Ignore":     {func(sp space.ResolvedSpace) error { _, err := r.Ignore(ctx, sp, "i", 1, now); return err }, []space.ResolvedSpace{zero, viewer}},
		"Reopen":     {func(sp space.ResolvedSpace) error { _, err := r.Reopen(ctx, sp, "i", 1, now); return err }, []space.ResolvedSpace{zero, viewer}},
		"GetMapping": {func(sp space.ResolvedSpace) error { _, err := r.GetMapping(ctx, sp, "bank"); return err }, []space.ResolvedSpace{zero}},
		"PutMapping": {func(sp space.ResolvedSpace) error {
			return r.PutMapping(ctx, sp, "bank", statement.Mapping{}, now)
		}, []space.ResolvedSpace{zero, viewer}},
	}
	for name, c := range calls {
		for i, sp := range c.deny {
			want := space.ErrDenied
			if sp.IsZero() {
				want = space.ErrNoSpace
			}
			if err := c.run(sp); !errors.Is(err, want) {
				t.Errorf("%s with space %d: %v, want %v", name, i, err, want)
			}
		}
	}
}

func TestImportKeys(t *testing.T) {
	sp, _ := space.ForJob("USER#u1", true)
	if got := ImportPK(sp, "I1"); got != sp.PK()+"#IMPORT#I1" {
		t.Fatalf("ImportPK = %q", got)
	}
	if got := ImportLineSK(7); got != "LINE#00007" {
		t.Fatalf("ImportLineSK = %q", got)
	}
	if got := ImportLockSK("bank", "F:123"); got != "FITID#bank#F:123" {
		t.Fatalf("ImportLockSK = %q", got)
	}
	if RetentionImportLock.ExpiresAt(time.Now()) != nil {
		t.Fatal("a FITID# lock must never expire (ADR 0026)")
	}
	if RetentionImportLine.ExpiresAt(time.Now()) == nil {
		t.Fatal("import lines expire after 90 days")
	}
}

func TestAlive(t *testing.T) {
	now := time.Unix(1000, 0)
	past, future := int64(999), int64(1001)
	if alive(&past, now) || !alive(&future, now) || !alive(nil, now) {
		t.Fatal("an expired row is gone for a reader even before DynamoDB deletes it")
	}
}
