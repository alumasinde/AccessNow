package visitor

import (
	"github.com/go-chi/chi/v5"

	"accessnow/internal/platform/router"
)

// Register mounts the visitor surface. Every route is behind the module flag first: with
// visitor_management switched off these paths answer 404 and the rest of the tenant,
// gates and passes included, keeps working.
func Register(r chi.Router, h *Handler, g router.Guard) {
	desk := g.Chain(g.Session, g.Module(ModuleKey))

	r.With(desk, g.Permission("visit.create")).Post("/visits", h.Create)
	r.With(desk, g.Permission("visit.view")).Get("/visits", h.List)
	// Static before the wildcard, as in the goods surface: this is the sweep, not a visit id.
	r.With(desk, g.Permission("visit.cancel")).Post("/visits/close-out", h.CloseOut)
	r.With(desk, g.Permission("visit.view")).Get("/visits/{visitID}", h.Get)
	r.With(desk, g.Permission("visit.approve")).Post("/visits/{visitID}/decision", h.Decide)
	r.With(desk, g.Permission("visit.cancel")).Post("/visits/{visitID}/cancel", h.Cancel)
	r.With(desk, g.Permission("visit.update")).Post("/visits/{visitID}/check-in", h.CheckIn)
	r.With(desk, g.Permission("visit.update")).Post("/visits/{visitID}/check-out", h.CheckOut)
}
