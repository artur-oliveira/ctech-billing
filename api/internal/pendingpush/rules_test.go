package pendingpush

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/api-commons/pushsvc"
)

type fakeCounter struct {
	n   map[string]int
	err error
}

func (f fakeCounter) Pending(_ context.Context, sub string) (int, error) { return f.n[sub], f.err }

// 08:00 in São Paulo on 2026-11-18.
var morning = time.Date(2026, 11, 18, 11, 0, 0, 0, time.UTC)

func decide(t *testing.T, c Counter) *pushsvc.Job {
	t.Helper()
	job, err := NewRules(c).Decide(context.Background(), morning, pushsvc.Reminder{Sub: "u1", Kind: Kind, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return job
}

func TestDecideSendsTheCount(t *testing.T) {
	job := decide(t, fakeCounter{n: map[string]int{"u1": 3}})
	if job == nil || job.Title != "3 contas aguardando confirmação" || job.Recipients[0] != "u1" {
		t.Fatalf("job = %+v", job)
	}
	if job.Navigate != "/finance/inbox" || job.EventID != "pending:u1:2026-11-18" || job.Tag != "pending-2026-11-18" || job.Topic != "pending" {
		t.Errorf("job = %+v", job)
	}
}

func TestDecideSingular(t *testing.T) {
	if job := decide(t, fakeCounter{n: map[string]int{"u1": 1}}); job == nil || job.Title != "1 conta aguardando confirmação" {
		t.Errorf("job = %+v", job)
	}
}

func TestDecideNothingPendingSendsNothing(t *testing.T) {
	if job := decide(t, fakeCounter{}); job != nil {
		t.Errorf("job = %+v", job)
	}
}

func TestDecideCapsTheCount(t *testing.T) {
	job := decide(t, fakeCounter{n: map[string]int{"u1": 5000}})
	if job == nil || job.Title != "Mais de 99 contas aguardando confirmação" {
		t.Errorf("job = %+v", job)
	}
}

func TestDecideCarriesNoNamesOrAmounts(t *testing.T) {
	job := decide(t, fakeCounter{n: map[string]int{"u1": 12}})
	for _, text := range []string{job.Title, job.Body} {
		if strings.Contains(text, "R$") {
			t.Errorf("text carries an amount: %q", text)
		}
	}
}

func TestDecidePropagatesTheCounterError(t *testing.T) {
	_, err := NewRules(fakeCounter{err: errors.New("throttled")}).Decide(context.Background(), morning, pushsvc.Reminder{Sub: "u1", Kind: Kind})
	if err == nil {
		t.Error("a failed read must not look like 'nothing pending'")
	}
}

func TestDecideIgnoresAnotherKind(t *testing.T) {
	job, _ := NewRules(fakeCounter{n: map[string]int{"u1": 3}}).Decide(context.Background(), morning, pushsvc.Reminder{Sub: "u1", Kind: "other"})
	if job != nil {
		t.Errorf("job = %+v", job)
	}
}
