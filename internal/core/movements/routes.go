package movements

import (
	"github.com/go-chi/chi/v5"

	"accessnow/internal/platform/router"
)

// Register mounts the gate surface inside the /gate subrouter the server owns: a narrower
// API for the tablet than the operator app, its own rate limit, and a device token that
// decides which gate a scan belongs to.
func Register(r chi.Router, h *Handler, g router.Guard) {
	gate := g.Chain(g.Session, g.Device)
	r.With(gate).Post("/scan", h.Scan)
	r.With(gate).Get("/on-site", h.OnSite)
	r.With(gate).Post("/sync", h.Sync)
}
