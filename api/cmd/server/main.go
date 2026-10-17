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
	server, err := app.Build(context.Background(), cfg, time.Now, app.WithByokConnections(&banks))
	if err != nil {
		slog.Error("startup", "error", err)
		os.Exit(1)
	}

	shutdownCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// The account-deletion consumer runs beside the API, in both processes of
	// every instance; SQS hands each message to one of them. A failure to start
	// it is logged, never fatal: the API keeps serving, and ctech-account alarms
	// on the missing ack after 48 h.
	consumer, err := app.BuildErasureConsumer(shutdownCtx, cfg, space.ForJob, bankConnections(banks))
	switch {
	case err != nil:
		slog.Error("account erasure consumer not started", "error", err)
	case consumer != nil:
		go func() { _ = consumer.Run(shutdownCtx) }()
	}

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
	}
}
