package staff

import (
	"github.com/go-chi/chi/v5"

	"accessnow/internal/platform/router"
)

// Register mounts the outpass surface. With staff_outpass switched off these paths answer
// 404, and an employee with an approved pass on their badge still walks out and back
// through the barrier: the reaction that keeps the register true needs no desk endpoint to
// be reachable, which is exactly what it is for a module a site may enable in one building
// and not in another.
func Register(r chi.Router, h *Handler, g router.Guard) {
	desk := g.Chain(g.Session, g.Module(ModuleKey))

	r.With(desk, g.Permission("outpass.create")).Post("/outpasses", h.Request)
	r.With(desk, g.Permission("outpass.view")).Get("/outpasses", h.List)
	// Static before the wildcard: chi prefers the exact segment, so these never swallow a
	// numeric id or get swallowed by one.
	r.With(desk, g.Permission("outpass.view")).Get("/outpasses/outstanding", h.Outstanding)
	r.With(desk, g.Permission("outpass.close")).Post("/outpasses/escalate", h.Escalate)
	r.With(desk, g.Permission("outpass.view")).Get("/outpasses/{outpassID}", h.Get)
	r.With(desk, g.Permission("outpass.approve")).Post("/outpasses/{outpassID}/decision", h.Decide)
	// Cancelling is the create side of the desk: whoever filed a step-out that has not
	// happened is allowed to take it back.
	r.With(desk, g.Permission("outpass.create")).Post("/outpasses/{outpassID}/cancel", h.Cancel)
	// Closing belongs to the supervisor's authority, not the clerk's: it is how an employee
	// who owes a return scan is written off, and the audit says who did it.
	r.With(desk, g.Permission("outpass.close")).Post("/outpasses/{outpassID}/close", h.Close)

	r.With(desk, g.Permission("outpass.view")).Get("/outpass-reasons", h.Reasons)
}
