// Package main is the entrypoint for the fdh portal API.
//
// The portal API is a read-only HTTP service that exposes the FDH skill
// registry over JSON. It reads from the same `pkg/registry` library the
// CLI uses, so there is exactly one source of truth for the registry's
// shape across CLI and portal.
//
// The full HTTP contract is defined in internal/portalapi/openapi.yaml.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/forge/fdh/internal/portalapi"
)

var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	cfg, err := portalapi.LoadConfig()
	if err != nil {
		logger.Error("invalid configuration", "err", err)
		os.Exit(2)
	}

	logger.Info("starting fdh-portal-api",
		"version", version,
		"commit", commit,
		"build_date", buildDate,
		"addr", cfg.Addr,
		"registry_source", cfg.RegistrySource(),
	)

	srv, err := portalapi.New(cfg, portalapi.BuildInfo{
		Version: version, Commit: commit, BuildDate: buildDate,
	})
	if err != nil {
		logger.Error("failed to construct server", "err", err)
		os.Exit(1)
	}

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Refresh + signal handling.
	go srv.RunRefreshLoop(ctx)

	// SIGHUP triggers an immediate refresh.
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	go func() {
		for range hup {
			if err := srv.Refresh(ctx); err != nil {
				logger.Warn("SIGHUP refresh failed", "err", err)
			} else {
				logger.Info("SIGHUP refresh complete")
			}
		}
	}()

	// SIGINT/SIGTERM trigger graceful shutdown.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-stop
		logger.Info("shutdown signal received")
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer shutdownCancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("graceful shutdown failed", "err", err)
		}
		// Flush telemetry (drain emitter, shut down OTLP providers).
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Warn("telemetry shutdown failed", "err", err)
		}
	}()

	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped unexpectedly", "err", err)
		os.Exit(1)
	}
	logger.Info("server stopped cleanly")
	_ = fmt.Sprintf // keep fmt for future use
}
