package closure

import (
	"errors"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

func newRequest() *Closure {
	return New("clo_1", "acct-org-1", "usr_owner", t0)
}

func TestANewRequestWaitsForTheEmailLink(t *testing.T) {
	c := newRequest()
	if c.State != AwaitingConfirmation || !c.ConfirmBy.Equal(t0.Add(ConfirmWindow)) {
		t.Fatalf("state %s confirm_by %s", c.State, c.ConfirmBy)
	}
	if c.ReadOnly() {
		t.Fatal("nothing is read-only before the owner confirms")
	}
}

func TestConfirmStartsTheReadOnlyGrace(t *testing.T) {
	c := newRequest()
	if err := c.Confirm(t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if c.State != Closing || !c.ReadOnly() || !c.GraceUntil.Equal(t0.Add(time.Hour+GracePeriod)) {
		t.Fatalf("state %s grace %s", c.State, c.GraceUntil)
	}
	if err := c.Confirm(t0.Add(2 * time.Hour)); !errors.Is(err, ErrWrongState) {
		t.Fatalf("a second click: %v, want ErrWrongState", err)
	}
}

func TestConfirmAfterTheWindowExpires(t *testing.T) {
	c := newRequest()
	if err := c.Confirm(t0.Add(ConfirmWindow + time.Second)); !errors.Is(err, ErrExpired) {
		t.Fatalf("err = %v, want ErrExpired", err)
	}
	if c.State != Expired {
		t.Fatalf("state = %s", c.State)
	}
}

func TestCancelWorksUntilThePurgeStartsAndLiftsTheReadOnly(t *testing.T) {
	for _, state := range []State{Closing, Blocked, ReadyToPurge} {
		c := newRequest()
		c.State = state
		if err := c.Cancel(t0.Add(time.Hour)); err != nil {
			t.Fatalf("%s: %v", state, err)
		}
		if c.State != Cancelled || c.ReadOnly() {
			t.Fatalf("%s: after cancel %s readonly=%v", state, c.State, c.ReadOnly())
		}
	}
	for _, state := range []State{Purging, Closed, Cancelled, Expired} {
		c := newRequest()
		c.State = state
		if err := c.Cancel(t0); !errors.Is(err, ErrNotCancellable) {
			t.Fatalf("%s: err = %v, want ErrNotCancellable", state, err)
		}
	}
}

func TestEndOfGraceWithBlockersBlocksAndWithoutReadiesThePurge(t *testing.T) {
	c := newRequest()
	_ = c.Confirm(t0)
	if c.GraceOver(t0.Add(GracePeriod - time.Second)) {
		t.Fatal("grace is not over yet")
	}
	end := t0.Add(GracePeriod)
	c.EndGrace(end, []string{"billing.tenant_active_subscriptions"})
	if c.State != Blocked || len(c.Blockers) != 1 || !c.ReadOnly() {
		t.Fatalf("with a blocker: %+v", c)
	}
	c2 := newRequest()
	_ = c2.Confirm(t0)
	c2.EndGrace(end, nil)
	if c2.State != ReadyToPurge || !c2.ReadOnly() {
		t.Fatalf("without blockers: %+v", c2)
	}
}

func TestPurgeStatesAreTerminalOnce(t *testing.T) {
	c := newRequest()
	c.State = ReadyToPurge
	if err := c.StartPurge(t0); err != nil || c.State != Purging {
		t.Fatalf("start purge: %v %s", err, c.State)
	}
	c.Finish(t0.Add(time.Hour))
	if c.State != Closed || c.ClosedAt.IsZero() || c.ReadOnly() {
		t.Fatalf("closed: %+v (a closed tenant has no row left; the flag is moot)", c)
	}
	if err := c.StartPurge(t0); !errors.Is(err, ErrWrongState) {
		t.Fatalf("restart after closed: %v", err)
	}
}

func TestTokensAreMatchedByHash(t *testing.T) {
	raw, hash, err := NewToken()
	if err != nil || raw == "" || hash == "" || raw == hash {
		t.Fatalf("token %q hash %q err %v", raw, hash, err)
	}
	if !TokenMatches(raw, hash) || TokenMatches("other", hash) || TokenMatches(raw, "") {
		t.Fatal("token comparison is wrong")
	}
}

func TestThePhraseIsFixed(t *testing.T) {
	if Phrase != "ENCERRAR CONTA DE COBRANÇA" {
		t.Fatalf("phrase = %q", Phrase)
	}
}
