// Command api runs the HTTP server.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	// A slim container image has no zoneinfo, and every reader's clock would silently be UTC.
	_ "time/tzdata"

	"accessnow/internal/platform/config"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/logging"
	"accessnow/internal/server"
)

func main() {
	if err := run(); err != nil {
		slog.Error("stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load(ctx)
	if err != nil {
		return err
	}
	logger := logging.New(cfg.Environment)
	slog.SetDefault(logger)

	pool, err := db.Open(ctx, cfg.Database)
	if err != nil {
		return err
	}
	defer pool.Close()

	deps := server.Build(pool, cfg)
	// A gate scan relays its own events inside the request. A desk write has no scan coming,
	// so this loop is what delivers its events - and this is what gets the notices it filed
	// out to a gateway, since a message that waits for the next badge is a message late.
	go deps.Backlog.Run(ctx)
	go deps.Delivery.Run(ctx)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           server.New(pool, deps, cfg, logger),
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	logger.Info("listening", "addr", cfg.HTTPAddr, "env", cfg.Environment)

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	return pool.Close()
}
