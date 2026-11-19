// Package pendingpush is Billing's scheduled Web Push: one reminder a day, at a time the person picks, when recurrences
// are waiting for their confirmation. Subscriptions, slots, reminders and the sender are go-common's pushsvc; this
// package decides who gets what and when (docs/specs/2026-11-18-pending-reminder-web-push.md).
package pendingpush

import (
	"context"

	"gopkg.aoctech.app/api-commons/problem"
	"gopkg.aoctech.app/api-commons/pushsvc"
)

// Kind is the one reminder kind of a Billing user.
const Kind = "pending"

// PendingPreference is the reminder: on or off, and the "HH:MM" (São Paulo) the person chose.
type PendingPreference struct {
	Enabled bool   `json:"enabled"`
	Time    string `json:"time"`
}

// Preferences are a person's reminders.
type Preferences struct {
	PendingReminder PendingPreference `json:"pending_reminder"`
}

// Reminders is what Service needs of pushsvc.ReminderStore.
type Reminders interface {
	Put(ctx context.Context, r pushsvc.Reminder) error
	Get(ctx context.Context, sub, kind string) (pushsvc.Reminder, bool, error)
}

// Service reads and writes a person's reminder preferences.
type Service struct{ reminders Reminders }

// NewService returns a Service over the reminder store.
func NewService(r Reminders) *Service { return &Service{reminders: r} }

// Get returns the preferences; a reminder never set reads as off.
func (s *Service) Get(ctx context.Context, sub string) (Preferences, error) {
	var p Preferences
	r, ok, err := s.reminders.Get(ctx, sub, Kind)
	if err != nil || !ok {
		return p, err
	}
	p.PendingReminder = PendingPreference{Enabled: r.Enabled, Time: pushsvc.FormatSlot(r.Slot)}
	return p, nil
}

// Set stores the preferences. Turning the reminder on needs a valid time; turning it off keeps the time. The time is
// rounded down to its 15-minute slot, and the answer shows the slot it landed in.
func (s *Service) Set(ctx context.Context, sub string, p Preferences) (Preferences, error) {
	current, had, err := s.reminders.Get(ctx, sub, Kind)
	if err != nil {
		return Preferences{}, err
	}
	slot := current.Slot
	if p.PendingReminder.Enabled || p.PendingReminder.Time != "" {
		parsed, perr := pushsvc.ParseHHMM(p.PendingReminder.Time)
		if perr != nil {
			return Preferences{}, problem.BadRequest("informe o horário do lembrete no formato HH:MM")
		}
		slot = parsed
	}
	if p.PendingReminder.Enabled || had {
		if err := s.reminders.Put(ctx, pushsvc.Reminder{Sub: sub, Kind: Kind, Enabled: p.PendingReminder.Enabled, Slot: slot}); err != nil {
			return Preferences{}, err
		}
	}
	return s.Get(ctx, sub)
}
