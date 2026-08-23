package v1

import (
	"bufio"
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
)

// The stream's job is to end. Every one of these checks is about a condition
// that must stop the wait, because the failure this route can produce is a
// screen that spins forever telling somebody nothing.

func TestIsSettledEndsTheWaitOnEveryPaidShape(t *testing.T) {
	cases := []struct {
		name string
		inv  *billing.Invoice
		want bool
	}{
		{"open with a balance", &billing.Invoice{Status: billing.InvoiceOpen, Total: 11300}, false},
		{"marked paid", &billing.Invoice{Status: billing.InvoicePaid, Total: 11300, AmountPaid: 11300}, true},
		// Settled by the arithmetic before the status catches up. The screen must
		// not keep waiting on an invoice that owes nothing.
		{"open but fully covered", &billing.Invoice{Status: billing.InvoiceOpen, Total: 11300, AmountPaid: 11300}, true},
		{"overpaid", &billing.Invoice{Status: billing.InvoiceOpen, Total: 11300, AmountPaid: 12000}, true},
		{"partially paid", &billing.Invoice{Status: billing.InvoiceOpen, Total: 11300, AmountPaid: 5000}, false},
	}

	for _, tc := range cases {
		if got := isSettled(tc.inv); got != tc.want {
			t.Errorf("%s: isSettled = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestWriteEventIsWellFormedSSE(t *testing.T) {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)
	writeEvent(context.Background(), w, "paid")

	got := buf.String()
	// A frame the browser will not parse is worse than no frame: the connection
	// stays open and the reader is told nothing.
	if !strings.HasPrefix(got, "event: paid\n") {
		t.Errorf("missing event name, got %q", got)
	}
	if !strings.HasSuffix(got, "\n\n") {
		t.Errorf("frame must end with a blank line, got %q", got)
	}
}

func TestBeatStaysQuietUntilTheHeartbeatIsDue(t *testing.T) {
	var buf bytes.Buffer
	w := bufio.NewWriter(&buf)

	if !beat(context.Background(), w, new(time.Now())) {
		t.Fatal("beat reported the client gone on a healthy writer")
	}
	if buf.Len() != 0 {
		t.Errorf("wrote a heartbeat before it was due: %q", buf.String())
	}

	stale := time.Now().Add(-2 * eventsHeartbeat)
	before := stale
	if !beat(context.Background(), w, &stale) {
		t.Fatal("beat reported the client gone on a healthy writer")
	}
	if !strings.HasPrefix(buf.String(), ":") {
		t.Errorf("heartbeat must be an SSE comment, got %q", buf.String())
	}
	if !stale.After(before) {
		t.Error("beat did not advance the last-beat timestamp, so it will fire every loop")
	}
}
