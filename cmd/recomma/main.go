package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/recomma/recomma/cmd/recomma/internal/config"
	"github.com/recomma/recomma/internal/debugmode"
)

func fatal(msg string, err error) {
	slog.Error(msg, slog.String("error", err.Error()))
	os.Exit(1)
}

func main() {
	cfg := config.DefaultConfig()
	fs := config.NewConfigFlagSet(&cfg)

	if err := fs.Parse(os.Args[1:]); err != nil {
		fatal("parsing flags failed", err)
	}

	if err := config.ApplyEnvDefaults(fs, &cfg); err != nil {
		fatal("invalid parameters", err)
	}

	if err := config.ValidateConfig(cfg); err != nil {
		fatal("invalid configuration", err)
	}

	if cfg.Debug && !debugmode.Available() {
		fatal("debug mode unavailable", debugmode.ErrUnavailable)
	}

	appCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Create and initialize application
	app, err := NewApp(appCtx, AppOptions{Config: cfg})
	if err != nil {
		fatal("app init failed", err)
	}

	// Start HTTP server (before vault unsealing, for WebAuthn/vault UI)
	app.StartHTTPServer()

	// Wait for vault to be unsealed
	if err := app.WaitForVaultUnseal(appCtx); err != nil {
		if !errors.Is(err, context.Canceled) {
			app.Logger.Error("vault unseal failed", slog.String("error", err.Error()))
			os.Exit(1) // Non-zero exit for startup failures
		}
		os.Exit(0) // Clean shutdown on cancellation
	}

	// Start all services (workers, periodic tasks, etc.)
	if err := app.Start(appCtx); err != nil {
		fatal("app start failed", err)
	}

	// Block until shutdown signal
	<-appCtx.Done()

	slog.Info("shutdown requested")

	// Graceful shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	if err := app.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", slog.String("error", err.Error()))
		os.Exit(1)
	}

	slog.Debug("fully shutdown")
}
