package pendingpush

import (
	"context"
	"strings"
	"sync"
	"testing"

	"gopkg.aoctech.app/api-commons/pushsvc"
)

type memReminders struct {
	mu   sync.Mutex
	rows map[string]pushsvc.Reminder
}

func newMem() *memReminders { return &memReminders{rows: map[string]pushsvc.Reminder{}} }

func (m *memReminders) Put(_ context.Context, r pushsvc.Reminder) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[r.Sub+"/"+r.Kind] = r
	return nil
}

func (m *memReminders) Get(_ context.Context, sub, kind string) (pushsvc.Reminder, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[sub+"/"+kind]
	return r, ok, nil
}

func prefs(on bool, time string) Preferences {
	var p Preferences
	p.PendingReminder.Enabled, p.PendingReminder.Time = on, time
	return p
}

func TestGetDefaultsToDisabled(t *testing.T) {
	got, err := NewService(newMem()).Get(context.Background(), "u1")
	if err != nil || got != (Preferences{}) {
		t.Errorf("Get = %+v, %v; want everything off until the person chooses a time", got, err)
	}
}

func TestSetRequiresTimeWhenEnabled(t *testing.T) {
	_, err := NewService(newMem()).Set(context.Background(), "u1", prefs(true, ""))
	if err == nil || !strings.Contains(err.Error(), "horário") {
		t.Fatalf("err = %v, want a bad-request about the time", err)
	}
}

func TestSetRejectsBadTime(t *testing.T) {
	for _, bad := range []string{"24:00", "7:5", "noon", "12:60"} {
		if _, err := NewService(newMem()).Set(context.Background(), "u1", prefs(true, bad)); err == nil {
			t.Errorf("time %q accepted", bad)
		}
	}
}

func TestSetThenGetRoundTripsAndRoundsToTheSlot(t *testing.T) {
	svc := NewService(newMem())
	got, err := svc.Set(context.Background(), "u1", prefs(true, "08:07"))
	if err != nil || !got.PendingReminder.Enabled || got.PendingReminder.Time != "08:00" {
		t.Fatalf("Set = %+v, %v; the time shows the slot it was rounded to", got, err)
	}
	back, _ := svc.Get(context.Background(), "u1")
	if back != got {
		t.Errorf("Get = %+v, want %+v", back, got)
	}
}

func TestDisablingKeepsTheTime(t *testing.T) {
	svc := NewService(newMem())
	_, _ = svc.Set(context.Background(), "u1", prefs(true, "08:00"))
	got, _ := svc.Set(context.Background(), "u1", prefs(false, ""))
	if got.PendingReminder.Enabled || got.PendingReminder.Time != "08:00" {
		t.Errorf("got %+v; turning it off keeps the chosen time for when it is turned on again", got)
	}
}

func TestDisablingSomethingNeverSetStoresNothing(t *testing.T) {
	mem := newMem()
	if _, err := NewService(mem).Set(context.Background(), "u1", prefs(false, "")); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := mem.Get(context.Background(), "u1", Kind); ok {
		t.Error("an off reminder that was never on must not create a row")
	}
}
