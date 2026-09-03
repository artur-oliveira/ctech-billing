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
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration", "error", err)
		os.Exit(1)
	}

	server, err := app.Build(context.Background(), cfg, time.Now)
	if err != nil {
		slog.Error("startup", "error", err)
		os.Exit(1)
	}

	addr := fmt.Sprintf(":%d", cfg.Port)
	slog.Info("billing api listening", "addr", addr, "env", cfg.Env, "version", cfg.AppVersion)

	listenErr := make(chan error, 1)
	go func() { listenErr <- server.Listen(addr) }()

	shutdownCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
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
