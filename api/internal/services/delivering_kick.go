package services

import (
	"context"
	"sync"
	"time"
)

// passer is the two passes of the outbound webhook pipeline.
type passer interface {
	FanOut(ctx context.Context, livemode bool, limit int, now time.Time) PassResult
	Deliver(ctx context.Context, livemode bool, limit int, now time.Time) PassResult
}

const (
	kickBatch   = 50
	kickTimeout = 30 * time.Second
)

// DeliveryKick runs the fan-out and delivery passes for one mode right now, in
// the background, instead of waiting for the once-a-minute delivery job. A paid
// invoice is the event a person is watching a screen for (a wallet deposit), and
// a minute's wait for the timer is most of what they would feel.
//
// It is the job's own code, so a delivery attempted twice (the kick and the timer
// meeting) is the at-least-once the receivers already handle: the event id is
// in a header and the wallet's credit is idempotent. Kicks while one is running
// for the same mode coalesce into one more pass, so a burst of payments is not a
// burst of passes.
type DeliveryKick struct {
	passes passer
	clock  func() time.Time

	mu      sync.Mutex
	running [2]bool
	again   [2]bool
}

func NewDeliveryKick(p passer) *DeliveryKick {
	return &DeliveryKick{passes: p, clock: time.Now}
}

// Kick starts a pass for the mode and returns at once.
func (k *DeliveryKick) Kick(livemode bool) {
	i := 0
	if livemode {
		i = 1
	}
	k.mu.Lock()
	if k.running[i] {
		k.again[i] = true
		k.mu.Unlock()
		return
	}
	k.running[i] = true
	k.mu.Unlock()
	go k.loop(i, livemode)
}

func (k *DeliveryKick) loop(i int, livemode bool) {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), kickTimeout)
		now := k.clock()
		k.passes.FanOut(ctx, livemode, kickBatch, now)
		k.passes.Deliver(ctx, livemode, kickBatch, k.clock())
		cancel()

		k.mu.Lock()
		if !k.again[i] {
			k.running[i] = false
			k.mu.Unlock()
			return
		}
		k.again[i] = false
		k.mu.Unlock()
	}
}
