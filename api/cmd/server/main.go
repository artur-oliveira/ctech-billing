package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata" // billing decides "today" in America/Sao_Paulo, on any host

	"gopkg.aoctech.app/api-commons/supervisor"

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/services"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// bankConnections is the console's connection service, or a stand-in with none
// when BYOK is not configured (a nil *ByokConnections inside the interface would
// panic on the first purge).
func bankConnections(c *services.ByokConnections) services.BankConnections {
	if c == nil {
		return services.NoBankConnections{}
	}
	return c
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration", "error", err)
		os.Exit(1)
	}

	var banks *services.ByokConnections
	var closures *services.TenantClosures
	server, err := app.Build(context.Background(), cfg, time.Now, app.WithByokConnections(&banks), app.WithClosures(&closures))
	if err != nil {
		slog.Error("startup", "error", err)
		os.Exit(1)
	}

	shutdownCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Background workers run under one supervisor: a panic or error restarts the
	// worker with backoff instead of ending it (or the process), and shutdown
	// waits for them to return. No Locker: the erasure consumer is fed by SQS and
	// the closure worker is idempotent, so every instance runs both.
	sup := supervisor.New(shutdownCtx, supervisor.Config{})

	// The account-deletion consumer runs beside the API, in both processes of
	// every instance; SQS hands each message to one of them. A failure to start
	// it is logged, never fatal: the API keeps serving, and ctech-account alarms
	// on the missing ack after 48 h.
	consumer, err := app.BuildErasureConsumer(shutdownCtx, cfg, space.ForJob, bankConnections(banks))
	switch {
	case err != nil:
		slog.Error("account erasure consumer not started", "error", err)
	case consumer != nil:
		_ = sup.Run(shutdownCtx, "erasure-consumer", consumer.Run)
	}

	_ = sup.Run(shutdownCtx, "closures", app.RunClosureWorker(closures)) // a no-op when the feature is off

	addr := fmt.Sprintf(":%d", cfg.Port)
	slog.Info("billing api listening", "addr", addr, "env", cfg.Env, "version", cfg.AppVersion)

	listenErr := make(chan error, 1)
	go func() { listenErr <- server.Listen(addr) }()

	select {
	case err := <-listenErr:
		if err == nil {
			return
		}
		slog.Error("listen", "error", err)
		os.Exit(1)
	case <-shutdownCtx.Done():
		slog.Info("billing api draining")
		if err := server.ShutdownWithTimeout(15 * time.Second); err != nil {
			slog.Error("shutdown", "error", err)
			os.Exit(1)
		}
		workersCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := sup.Shutdown(workersCtx); err != nil {
			// The process is exiting anyway; a late worker is not a failed stop.
			slog.Warn("workers did not stop in time", "error", err)
		}
	}
}
