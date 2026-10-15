//go:build integration

package integration

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/id"
	"gopkg.aoctech.app/billing/api/internal/realtime"
	"gopkg.aoctech.app/billing/api/internal/realtime/wire"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

type userOf map[string]string

func (u userOf) Get(_ context.Context, _ string, _ bool, id string) (*billing.Customer, error) {
	return &billing.Customer{ID: id, UserID: u[id]}, nil
}

type captured struct {
	mu   sync.Mutex
	sent map[string][][]byte
}

func (c *captured) Broadcast(_ context.Context, key string, payload []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sent == nil {
		c.sent = map[string][][]byte{}
	}
	c.sent[key] = append(c.sent[key], payload)
}

type panicky struct{}

func (panicky) Publish(context.Context, ...*repositories.Change) { panic("the publisher is down") }

func realtimeInvoices(rt repositories.Realtime) *repositories.InvoiceRepository {
	return repositories.NewInvoiceRepository(testDB, testCfg).WithRealtime(rt)
}

func payAndPublish(t *testing.T, repo *repositories.InvoiceRepository, org *billing.Organization) *billing.Invoice {
	t.Helper()
	ctx := ctxT(t)
	inv := newDraftInvoice(t, org, "gen_"+org.ID)
	due := brcal.New(2026, time.March, 10)
	if _, err := repo.Finalize(ctx, inv, due, due, billing.CauseScheduler, "test", "req_1", now()); err != nil {
		t.Fatal(err)
	}
	// A second later: two transitions of one invoice are never in the same
	// millisecond (each is a DynamoDB transaction), and the id's time orders them.
	if _, err := repo.Transition(ctx, inv, billing.InvoicePaid, billing.CauseManualPayment, "test", "req_2", now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	return inv
}

func TestAPaidInvoiceWritesAndPublishesAChangeToTheTenantTheCheckoutAndTheUser(t *testing.T) {
	ctx := ctxT(t)
	org := newOrg(t, true)
	wire_ := &captured{}
	rt := repositories.Realtime{
		Emitter:   realtime.NewEmitter(userOf{"cus_1": "user-1"}),
		Publisher: realtime.NewPublisher(wire_, nil),
	}
	inv := payAndPublish(t, realtimeInvoices(rt), org)

	scopes := []string{realtime.TenantScope(org.ID, true), realtime.InvoiceScope(org.ID, true, inv.ID), realtime.UserScope("user-1")}
	log := repositories.NewRealtimeRepository(testDB, testCfg)
	cursor := id.NewAt(now().Add(-time.Hour)) // anything older than the writes
	got, _, err := log.Since(ctx, scopes, cursor, 50)
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, c := range got {
		types = append(types, c.Type)
	}
	// Finalized and then paid, in that order, each in every scope. (The draft was
	// created by a helper with no realtime, so there is no invoice.created.)
	if !slices.Equal(types, []string{"invoice.finalized", "invoice.paid"}) {
		t.Errorf("log types = %v, want [invoice.finalized invoice.paid]", types)
	}
	if last := got[len(got)-1]; last.Type != "invoice.paid" || last.Status != "PAID" || len(last.Scopes) != 3 {
		t.Errorf("last change = %+v", last)
	}

	for _, scope := range scopes {
		frames := wire_.sent[scope]
		if len(frames) != 2 {
			t.Errorf("%s received %d frames, want 2", scope, len(frames))
			continue
		}
		var f wire.ServerFrame
		if err := proto.Unmarshal(frames[len(frames)-1], &f); err != nil || f.GetChange().GetType() != "invoice.paid" || f.GetChange().GetEntityId() != inv.ID {
			t.Errorf("%s: last frame = %+v (%v)", scope, f.GetChange(), err)
		}
	}
}

func TestWithoutAnEmitterNothingIsWrittenAndNothingPublished(t *testing.T) {
	ctx := ctxT(t)
	org := newOrg(t, true)
	inv := payAndPublish(t, realtimeInvoices(repositories.Realtime{}), org)

	log := repositories.NewRealtimeRepository(testDB, testCfg)
	head, err := log.Head(ctx, []string{realtime.TenantScope(org.ID, true), realtime.InvoiceScope(org.ID, true, inv.ID)})
	if err != nil || head != "" {
		t.Fatalf("a change was written with the log off: head=%q err=%v", head, err)
	}
}

func TestAPublisherThatBreaksNeverFailsTheMutation(t *testing.T) {
	ctx := ctxT(t)
	org := newOrg(t, true)
	rt := repositories.Realtime{Emitter: realtime.NewEmitter(userOf{}), Publisher: panicky{}}
	inv := payAndPublish(t, realtimeInvoices(rt), org) // t.Fatal on any error

	fresh, err := repositories.NewInvoiceRepository(testDB, testCfg).Get(ctx, org.ID, true, inv.ID)
	if err != nil || fresh.Status != billing.InvoicePaid {
		t.Fatalf("invoice = %+v (%v), want PAID", fresh, err)
	}
	// And the change is in the log, so a reconnecting client still finds it.
	log := repositories.NewRealtimeRepository(testDB, testCfg)
	if head, _ := log.Head(ctx, []string{realtime.TenantScope(org.ID, true)}); head == "" {
		t.Fatal("the change was not written")
	}
}

func TestATestInvoiceNeverReachesALiveScope(t *testing.T) {
	ctx := ctxT(t)
	org := newOrg(t, false)
	rt := repositories.Realtime{Emitter: realtime.NewEmitter(userOf{"cus_1": "user-1"})}
	inv := payAndPublish(t, realtimeInvoices(rt), org)

	log := repositories.NewRealtimeRepository(testDB, testCfg)
	if head, _ := log.Head(ctx, []string{realtime.TenantScope(org.ID, true), realtime.InvoiceScope(org.ID, true, inv.ID)}); head != "" {
		t.Fatalf("a test-mode invoice was written to a live scope (head %q)", head)
	}
	if head, _ := log.Head(ctx, []string{realtime.TenantScope(org.ID, false)}); head == "" {
		t.Fatal("it was not written to its own test scope")
	}
}
