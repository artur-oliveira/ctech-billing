package repositories

import (
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/id"
)

var changeNow = time.Date(2026, 10, 14, 15, 0, 0, 0, time.UTC)

func TestChangeWritesOneItemPerDistinctScopeAndFillsTheChangeInPlace(t *testing.T) {
	c := &Change{
		Type: "invoice.paid", Entity: "invoice", EntityID: "in_1", Status: "PAID",
		Scopes: []string{"tenant:o:live", "invoice:o:live:in_1", "tenant:o:live", "", "user:u1"},
	}
	items, err := changeWrites(Base{}, c, changeNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3 (a repeated and an empty scope are dropped)", len(items))
	}
	if c.ID == "" || c.Revision != uint64(changeNow.UnixMilli()) || !c.OccurredAt.Equal(changeNow) {
		t.Fatalf("the change was not filled in place: %+v", c)
	}
	if at, ok := id.Time(c.ID); !ok || !at.Equal(changeNow) {
		t.Fatalf("the id's time = %v, want the transaction's clock %v", at, changeNow)
	}
}

func TestChangeWritesNothingWithoutAScopeOrWithoutAChange(t *testing.T) {
	for name, c := range map[string]*Change{
		"nil":      nil,
		"no scope": {Type: "invoice.paid", Entity: "invoice", EntityID: "in_1"},
	} {
		if items, err := changeWrites(Base{}, c, changeNow); err != nil || items != nil {
			t.Errorf("%s: items=%v err=%v, want nothing", name, items, err)
		}
	}
}

func TestAChangeNeedsATypeAnEntityAndAnId(t *testing.T) {
	for name, c := range map[string]*Change{
		"no type":      {Entity: "invoice", EntityID: "in_1", Scopes: []string{"s"}},
		"no entity":    {Type: "invoice.paid", EntityID: "in_1", Scopes: []string{"s"}},
		"no entity id": {Type: "invoice.paid", Entity: "invoice", Scopes: []string{"s"}},
	} {
		if _, err := changeWrites(Base{}, c, changeNow); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestACursorOlderThanTheLogIsTooOldAndAGarbageOneIsBad(t *testing.T) {
	recent := id.NewAt(changeNow.Add(-47 * time.Hour))
	old := id.NewAt(changeNow.Add(-49 * time.Hour))
	if tooOld, err := CursorTooOld(recent, changeNow); err != nil || tooOld {
		t.Errorf("47 h: tooOld=%v err=%v", tooOld, err)
	}
	if tooOld, err := CursorTooOld(old, changeNow); err != nil || !tooOld {
		t.Errorf("49 h: tooOld=%v err=%v", tooOld, err)
	}
	if _, err := CursorTime("not-a-ulid"); err != ErrBadCursor {
		t.Errorf("garbage: %v", err)
	}
}

func TestTheFloorOfAMomentSortsBeforeEveryIdOfThatMoment(t *testing.T) {
	floor := id.FloorAt(changeNow)
	for i := 0; i < 50; i++ {
		if got := id.NewAt(changeNow); got <= floor {
			t.Fatalf("%s sorts at or before the floor %s", got, floor)
		}
	}
	if id.NewAt(changeNow.Add(-time.Millisecond)) >= floor {
		t.Fatal("an id from the previous millisecond sorts after the floor")
	}
}
