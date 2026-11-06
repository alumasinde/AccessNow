package notifications

import (
	"github.com/go-chi/chi/v5"

	"accessnow/internal/platform/router"
)

// Register mounts the notice surface. With notifications switched off these paths answer 404
// and the rest of the system is untouched: passes are still issued, chains still run, and the
// events this module would have reacted to still sit on the outbox for whoever else cares.
//
// The inbox belongs to anybody who can sign in - notification.view is granted to every role
// that receives a notice, including guard and host - while the routing and the wording belong
// to whoever configures them.
func Register(r chi.Router, h *Handler, g router.Guard) {
	desk := g.Chain(g.Session, g.Module(ModuleKey))
	see := g.Permission("notification.view")
	configure := g.Permission("notification.configure")
	resend := g.Permission("notification.resend")

	// The inbox first, and always scoped to the signed-in user: these four paths are the only
	// ones a guard on a gate tablet calls.
	r.With(desk, see).Get("/notifications", h.Inbox)
	r.With(desk, see).Get("/notifications/unread", h.UnreadOnly)
	r.With(desk, see).Post("/notifications/read-all", h.ReadAll)
	r.With(desk, see).Post("/notifications/{notificationID}/read", h.Read)

	// What the system can send, and what this tenant decided about it.
	r.With(desk, see).Get("/notifications/events", h.Events)
	r.With(desk, see).Get("/notifications/routes", h.ListRoutes)
	r.With(desk, configure).Post("/notifications/routes", h.SaveRoute)
	r.With(desk, configure).Delete("/notifications/routes/{routeID}", h.DeleteRoute)

	// Wording: the tenant's own rows and the system defaults they sit on top of.
	r.With(desk, see).Get("/notifications/templates", h.Templates)
	r.With(desk, configure).Put("/notifications/templates", h.SaveTemplate)
	r.With(desk, configure).Delete("/notifications/templates/{templateID}", h.DeleteTemplate)

	// The queue, counted and listed, and the two buttons that move it now rather than soon.
	r.With(desk, configure).Get("/notifications/outbox", h.Outbox)
	r.With(desk, configure).Get("/notifications/ledger", h.Ledger)
	r.With(desk, resend).Post("/notifications/deliver", h.Sweep)
	r.With(desk, resend).Post("/notifications/{notificationID}/resend", h.Resend)
}
