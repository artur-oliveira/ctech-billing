// Package settlement carries "this invoice was paid" between instances.
//
// The payment screen needs to know the moment a PIX charge settles, and the
// instance holding that browser connection is almost never the instance the
// wallet webhook arrived at. Before this, the screen re-read the invoice every
// two seconds for up to thirty-five minutes — correct on any number of
// instances, and a DynamoDB read per viewer per two seconds for the entire
// length of a PIX window.
//
// So: a notification when the settlement happens, and the re-read kept as a
// **slow** safety net rather than the mechanism. Pub/sub is fire-and-forget —
// a subscriber that reconnects a millisecond late misses the message and there
// is no replay — so a design that trusted it alone would occasionally leave
// somebody watching a spinner for a payment that already went through. The
// notification makes the common case instant; the re-read makes every case
// correct.
package settlement

import (
	"context"
	"log/slog"

	"github.com/valkey-io/valkey-go"
)

// Bus is what the payment screen and the collector talk through.
//
// An interface because the deployment without Valkey is a real one — a single
// instance, or a local run — and it must degrade to the re-read rather than
// fail. A nil Bus is valid and means exactly that.
type Bus interface {
	// Settled announces that an invoice reached a terminal, paid state.
	Settled(ctx context.Context, invoiceID string)
	// Watch returns a channel that receives once when the invoice settles.
	// Cancelling ctx releases everything the subscription holds.
	Watch(ctx context.Context, invoiceID string) <-chan struct{}
}

func channel(invoiceID string) string { return "billing:settled:" + invoiceID }

// ValkeyBus is the shared-instance implementation.
type ValkeyBus struct {
	client valkey.Client
}

// NewValkeyBus connects to the Valkey the service already uses for its JWKS
// cache. It is the same server and a different keyspace concern — pub/sub
// channels are not part of the logical database, so this shares nothing with
// the cache but the connection URL.
func NewValkeyBus(url string) (*ValkeyBus, error) {
	opt, err := valkey.ParseURL(url)
	if err != nil {
		return nil, err
	}
	client, err := valkey.NewClient(opt)
	if err != nil {
		return nil, err
	}
	return &ValkeyBus{client: client}, nil
}

// Settled publishes the notification.
//
// Failures are logged and swallowed. This is called from the path that has just
// marked an invoice PAID in DynamoDB — the money is settled and the record is
// written — and failing that operation because a notification could not be sent
// would turn a slow screen into a lost payment.
func (b *ValkeyBus) Settled(ctx context.Context, invoiceID string) {
	cmd := b.client.B().Publish().Channel(channel(invoiceID)).Message("1").Build()
	if err := b.client.Do(ctx, cmd).Error(); err != nil {
		slog.Warn("could not publish settlement", "invoice", invoiceID, "error", err)
	}
}

// Watch subscribes until the invoice settles or ctx ends.
//
// ponytail: one dedicated Valkey connection per watching browser. That is fine
// at the volume this service sees — concurrent watchers are people with a QR
// code open right now — and it is the ceiling to raise first if it ever is not:
// the upgrade is one process-wide pattern subscription fanning out to an
// in-memory map of waiters.
func (b *ValkeyBus) Watch(ctx context.Context, invoiceID string) <-chan struct{} {
	// Buffered so the publisher's callback never blocks on a reader that has
	// already walked away.
	out := make(chan struct{}, 1)

	go func() {
		defer close(out)
		// A dedicated connection, not one borrowed from the multiplexed pool: a
		// long-lived Receive on a shared connection is the bug ctech-go-common's
		// ws registry documents, where the subscription registers server-side and
		// the callback never fires.
		client, release := b.client.Dedicate()
		defer release()

		cmd := client.B().Subscribe().Channel(channel(invoiceID)).Build()
		err := client.Receive(ctx, cmd, func(valkey.PubSubMessage) {
			select {
			case out <- struct{}{}:
			default:
			}
		})
		// A cancelled context is the normal way this ends: the reader got its
		// answer, or the stream timed out.
		if err != nil && ctx.Err() == nil {
			slog.Warn("settlement subscription ended", "invoice", invoiceID, "error", err)
		}
	}()

	return out
}

// Close releases the connection pool.
func (b *ValkeyBus) Close() { b.client.Close() }
