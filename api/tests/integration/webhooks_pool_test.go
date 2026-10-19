//go:build integration

package integration

import (
	"context"
	"net/http"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/repositories"
)

// Four endpoints of four tenants that answer slowly and badly, each with a backlog
// of due deliveries, next to four that answer at once. The pass:
//   - attempts each dead endpoint ONCE (the rest of its due rows move down the
//     queue), so a dead endpoint costs one timeout, not one per queued delivery;
//   - runs the endpoints at the same time, so the four 1.5 s timeouts overlap
//     (serial: 6 s) and the healthy endpoints never wait behind them.
func TestDeadEndpointsCostOneAttemptEachAndOverlap(t *testing.T) {
	ctx := ctxT(t)
	repo := repositories.NewWebhookRepository(testDB, testCfg)
	step := repositories.PingInterval + time.Second

	var slow, fast []*consumer
	for i := 0; i < 4; i++ {
		c := newConsumer(t, http.StatusInternalServerError)
		c.setDelay(1500 * time.Millisecond)
		ep := registerEndpoint(t, newOrg(t, true), c, "")
		for j := 0; j < 3; j++ {
			if _, err := repo.EmitPing(ctx, ep, now().Add(time.Duration(j)*step)); err != nil {
				t.Fatal(err)
			}
		}
		slow = append(slow, c)
	}
	for i := 0; i < 4; i++ {
		c := newConsumer(t, http.StatusOK)
		ep := registerEndpoint(t, newOrg(t, true), c, "")
		if _, err := repo.EmitPing(ctx, ep, now()); err != nil {
			t.Fatal(err)
		}
		fast = append(fast, c)
	}

	d := newDeliverer(t, slow[0]).WithConcurrency(8)
	started := time.Now()
	d.FanOutAndDeliver(context.Background(), true, 10_000, now().Add(4*step))
	elapsed := time.Since(started)

	for i, c := range fast {
		if len(c.deliveries()) != 1 {
			t.Fatalf("healthy endpoint %d got %d deliveries, want 1", i, len(c.deliveries()))
		}
	}
	for i, c := range slow {
		if got := len(c.deliveries()); got != 1 {
			t.Fatalf("dead endpoint %d was attempted %d times in one pass, want 1", i, got)
		}
	}
	if elapsed > 3*time.Second {
		t.Fatalf("the pass took %s: the four 1.5 s endpoints did not overlap", elapsed)
	}
}
