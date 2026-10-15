package app

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/gofiber/fiber/v3"

	"gopkg.aoctech.app/api-commons/cache"
	"gopkg.aoctech.app/api-commons/drain"
	"gopkg.aoctech.app/api-commons/ws"

	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/middleware"
	"gopkg.aoctech.app/billing/api/internal/realtime"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// realtimeRuntime is what a process needs to tell the live screens: the emitter
// and the publisher every repository holds, and the registry that carries a
// published change to the connections of every instance.
type realtimeRuntime struct {
	registry  ws.Registry
	emitter   *realtime.Emitter
	publisher *realtime.Publisher
	metrics   *realtime.Metrics
}

var (
	rtMu    sync.Mutex
	rtState *realtimeRuntime
)

// enableRealtime wires the process for realtime when REALTIME_ENABLED is on, and
// does nothing otherwise. It is called from newDynamoDB, which every Build*
// calls before it builds a repository: the server, the sweep, the reconciler, the
// dunner and the BYOK jobs all write transitions, and each must announce them.
//
// The registry is Valkey when there is one (a change published by a job reaches
// the connections held by the server) and in-process otherwise (development: only
// this process's own connections hear it). A process that only publishes never
// starts the registry's subscription.
func enableRealtime(cfg *config.Config, db *dynamodb.Client) {
	rtMu.Lock()
	defer rtMu.Unlock()
	if !cfg.RealtimeEnabled {
		// Off means off, whatever an earlier wiring in this process left behind
		// (only a test binary builds more than one app).
		rtState = nil
		repositories.UseRealtime(repositories.Realtime{})
		return
	}
	if rtState != nil {
		return
	}
	var reg ws.Registry
	if rb, ok := newCache(cfg).(*cache.RedisBackend); ok {
		reg = ws.NewRedisRegistry(rb.Client())
	} else {
		slog.Warn("realtime: no Valkey, using an in-process registry (not shared across processes)")
		reg = ws.NewMemoryRegistry()
	}
	metrics := &realtime.Metrics{}
	emitter := realtime.NewEmitter(repositories.NewCustomerRepository(db, cfg))
	publisher := realtime.NewPublisher(reg, metrics)
	repositories.UseRealtime(repositories.Realtime{Emitter: emitter, Publisher: publisher})
	rtState = &realtimeRuntime{registry: reg, emitter: emitter, publisher: publisher, metrics: metrics}
}

// ResetRealtime drops the process-wide realtime wiring. Tests call it in a
// cleanup after enabling realtime, so what they turned on does not leak into the
// next test of the same binary.
func ResetRealtime() {
	rtMu.Lock()
	defer rtMu.Unlock()
	rtState = nil
	repositories.UseRealtime(repositories.Realtime{})
}

func currentRealtime() *realtimeRuntime {
	rtMu.Lock()
	defer rtMu.Unlock()
	return rtState
}

// realtimeServer builds the socket gateway and the changes route for the API
// process, starts what runs beside them (the registry's subscription, the
// fan-out health probe, the stats line) and ties their end to the server's:
// connections are told to reconnect elsewhere before the listener closes.
// Nil, nil when realtime is off.
func realtimeServer(
	ctx context.Context, cfg *config.Config, app *fiber.App, db *dynamodb.Client,
	verifier *middleware.Verifier, spaces *space.Resolver, orgs *repositories.OrganizationRepository,
	links *services.PayLink,
) (*realtime.Gateway, *realtime.ChangesHandler) {
	rt := currentRealtime()
	if rt == nil {
		return nil, nil
	}
	runCtx, stop := context.WithCancel(ctx)
	if err := rt.registry.Start(runCtx); err != nil {
		slog.Error("realtime: the registry did not start; connections will not hear other processes", "error", err)
	}
	hub := realtime.NewHub(rt.registry, realtime.DefaultLimits(), rt.metrics)
	health := realtime.NewFanoutHealth(rt.registry, 10*time.Second, rt.metrics)
	go health.Run(runCtx)
	go rt.metrics.Report(runCtx, time.Minute)

	log := repositories.NewRealtimeRepository(db, cfg)
	authorizers := map[string]realtime.Authorizer{
		realtime.KindConsole:  &realtime.ConsoleAuthorizer{Verifier: verifier, Spaces: spaces, Tenants: orgs},
		realtime.KindPortal:   &realtime.PortalAuthorizer{Verifier: verifier},
		realtime.KindCheckout: &realtime.CheckoutAuthorizer{Links: links, Lifetime: 35 * time.Minute},
	}
	tracker := &drain.Tracker{}
	gateway := &realtime.Gateway{
		Authorizers: authorizers, Hub: hub, Log: log, AllowedOrigins: cfg.RealtimeAllowedOrigins,
		Drain: tracker, Config: realtime.DefaultConfig(), Health: health,
	}
	changes := &realtime.ChangesHandler{Authorizers: authorizers, Log: log, Metrics: rt.metrics}

	app.Hooks().OnPreShutdown(func() error {
		// Before the listener closes: every socket gets a Bye with a retry-after.
		drainCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return tracker.Drain(drainCtx)
	})
	app.Hooks().OnPostShutdown(func(error) error {
		stop()
		stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return rt.registry.Stop(stopCtx)
	})
	return gateway, changes
}
