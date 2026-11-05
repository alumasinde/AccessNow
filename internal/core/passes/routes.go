package passes

import (
	"github.com/go-chi/chi/v5"

	"accessnow/internal/platform/router"
)

// Register mounts the read side of the spine. Decisions belong to the module that owns
// the pass, so they are reached through visit.approve, material.approve and friends.
func Register(r chi.Router, h *Handler, g router.Guard) {
	read := g.Chain(g.Session, g.Permission("core.pass.view"))
	r.With(read).Get("/passes", h.List)
	r.With(read).Get("/passes/{passID}", h.Get)
}
