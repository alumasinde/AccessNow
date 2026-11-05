package gates

import (
	"github.com/go-chi/chi/v5"

	"accessnow/internal/platform/router"
)

// Register mounts Core's gate surface. Gates are never behind a module flag: every
// tenant has at least one, even with all fifteen modules off.
func Register(r chi.Router, h *Handler, g router.Guard) {
	guarded := g.Chain(g.Session, g.GateWork)
	r.With(guarded).Get("/gates", h.List)
	r.With(guarded).Get("/gates/active", h.Current)
	r.With(guarded).Put("/gates/active", h.Select)
	r.With(guarded).Post("/gates/shift/end", h.EndShift)
}
