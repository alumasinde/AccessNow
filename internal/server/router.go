package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/go-chi/httprate"
	"github.com/jmoiron/sqlx"

	"accessnow/internal/core/auth"
	"accessnow/internal/core/gates"
	"accessnow/internal/core/movements"
	"accessnow/internal/core/passes"
	"accessnow/internal/modules/approval"
	"accessnow/internal/modules/blocklist"
	"accessnow/internal/modules/material"
	"accessnow/internal/modules/notifications"
	"accessnow/internal/modules/staff"
	"accessnow/internal/modules/vehicle"
	"accessnow/internal/modules/visitor"
	"accessnow/internal/platform/config"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/httpx"
	"accessnow/internal/platform/logging"
)

// New builds the handler: one router, two surfaces. /api/v1 is what a person in a browser
// or an app calls; /api/v1/gate is what the tablet calls, and it carries a device token
// instead of a picker for the gate.
func New(pool *sqlx.DB, deps *Deps, cfg config.Config, logger *slog.Logger) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(requestLog(logger))
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(cfg.HandlerTimeout))
	r.Use(cors.New(cors.Options{
		AllowedOrigins:   cfg.AllowedOrigins,
		AllowedMethods:   []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete},
		AllowedHeaders:   []string{"Accept", "Content-Type", "Content-Language", "Authorization", "X-Device-Token"},
		AllowCredentials: true,
		MaxAge:           300,
	}).Handler)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { httpx.OK(w, map[string]string{"status": "ok"}) })
	r.Get("/readyz", ready(pool))

	r.Route("/api/v1", func(r chi.Router) {
		r.Route("/auth", func(r chi.Router) {
			auth.Register(r, deps.Auth, deps.Guard,
				httprate.LimitBy(cfg.LoginAttempts, time.Minute, httprate.KeyByIP))
		})
		gates.Register(r, deps.Gates, deps.Guard)
		passes.Register(r, deps.Passes, deps.Guard)
		visitor.Register(r, deps.Visitor, deps.Guard)
		material.Register(r, deps.Material, deps.Guard)
		vehicle.Register(r, deps.Vehicle, deps.Guard)
		staff.Register(r, deps.Staff, deps.Guard)
		approval.Register(r, deps.Approval, deps.Guard)
		blocklist.Register(r, deps.Blocklist, deps.Guard)
		notifications.Register(r, deps.Notifications, deps.Guard)

		r.Route("/gate", func(r chi.Router) {
			r.Use(httprate.LimitBy(cfg.ScanRate, time.Minute, httprate.KeyByIP))
			movements.Register(r, deps.Movements, deps.Guard)
		})
	})

	r.NotFound(func(w http.ResponseWriter, r *http.Request) {
		httpx.Fail(r.Context(), w, r, notFound(r))
	})
	return r
}

// ready answers 200 only when the pool can talk to MySQL, so a load balancer takes the
// pod out of rotation instead of shedding sessions.
func ready(pool *sqlx.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := pool.PingContext(ctx); err != nil {
			httpx.JSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
			return
		}
		httpx.OK(w, map[string]string{"status": "ready"})
	}
}

func requestLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			started := time.Now()
			recorder := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			// The request's own facts, put where a service that never sees the
			// http.Request can still audit them.
			ctx := httpx.WithMeta(r.Context(), r, middleware.GetReqID(r.Context()))
			ctx = logging.With(ctx, logger)
			next.ServeHTTP(recorder, r.WithContext(ctx))

			logger.InfoContext(r.Context(), "request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", recorder.Status()),
				slog.Int("bytes", recorder.BytesWritten()),
				slog.Int64("took_ms", time.Since(started).Milliseconds()),
				slog.String("request_id", middleware.GetReqID(r.Context())),
			)
		})
	}
}

// notFound names the route that does not exist. A disabled module lands here too, which
// is the point: the surface is not there at all.
func notFound(r *http.Request) error {
	return errs.NotFound(r.Method + " " + r.URL.Path)
}
