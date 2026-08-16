package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
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
	if err := server.Listen(addr); err != nil {
		slog.Error("listen", "error", err)
		os.Exit(1)
	}
}
