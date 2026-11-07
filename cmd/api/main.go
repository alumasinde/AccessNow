// Command api runs the HTTP server.
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
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
	var loops sync.WaitGroup
	start := func(run func(context.Context)) {
		loops.Add(1)
		go func() {
			defer loops.Done()
			run(ctx)
		}()
	}
	start(deps.Backlog.Run)
	start(deps.Delivery.Run)
	// A scheduled export has no request to ride: the loop is what notices its moment passed.
	start(deps.ReportsLoop.Run)
	// Webhooks are a queue like notifications: the gate scan's event is written, and this loop
	// is what turns it into a request to a tenant's system.
	start(deps.WebhooksLoop.Run)
	// An HR or ERP sync is the same shape: the desk files a job, and this loop is what calls the
	// remote system, links what came back and records the outcome.
	start(deps.SyncLoop.Run)
	// Nightly rules are the one evaluation with no gate and no request to ride: the loop is
	// what walks the open register.
	start(deps.RulesLoop.Run)
	// Retention owners hear before their data becomes purgeable, even when no purge request
	// has been made yet.
	start(deps.ComplianceLoop.Run)

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
	// The loops stop on the same cancelled context; wait so none of them is mid-write when the
	// pool closes underneath it.
	done := make(chan struct{})
	go func() { loops.Wait(); close(done) }()
	select {
	case <-done:
	case <-shutdownCtx.Done():
		logger.Warn("background loops still running at shutdown deadline")
	}
	return pool.Close()
}
