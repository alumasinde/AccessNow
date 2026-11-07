package rollcall

import (
	"github.com/go-chi/chi/v5"

	"accessnow/internal/platform/router"
)

// Register mounts the roll-call desk and the gate surface a muster point polls. With
// emergency_rollcall switched off these paths answer 404; Core's on-site roster stays available,
// which is the read a site needs even with the module closed.
func Register(r chi.Router, h *Handler, g router.Guard) {
	desk := g.Chain(g.Session, g.Module(ModuleKey))
	view := g.Permission("rollcall.view")

	r.With(desk, g.Permission("rollcall.start")).Post("/rollcall/incidents", h.Start)
	r.With(desk, view).Get("/rollcall/incidents", h.ListIncidents)
	// Static before the wildcard, so "open" is never read as an id.
	r.With(desk, view).Get("/rollcall/open", h.Open)
	r.With(desk, view).Get("/rollcall/incidents/{incidentID}", h.GetIncident)
	r.With(desk, view).Get("/rollcall/incidents/{incidentID}/roster", h.Roster)
	r.With(desk, view).Get("/rollcall/incidents/{incidentID}/marks", h.Marks)

	r.With(desk, g.Permission("rollcall.mark")).
		Post("/rollcall/incidents/{incidentID}/roster/{rosterID}/mark", h.Mark)
	r.With(desk, g.Permission("rollcall.mark")).
		Post("/rollcall/incidents/{incidentID}/remind", h.Remind)
	r.With(desk, g.Permission("rollcall.close")).
		Post("/rollcall/incidents/{incidentID}/close", h.Close)
	r.With(desk, g.Permission("rollcall.close")).
		Post("/rollcall/incidents/{incidentID}/cancel", h.Cancel)
}
