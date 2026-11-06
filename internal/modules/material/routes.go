package material

import (
	"github.com/go-chi/chi/v5"

	"accessnow/internal/platform/router"
)

// Register mounts the goods surface. With material_logistics switched off these paths
// answer 404 and the gate keeps scanning passes: release and return are movements, and the
// reaction simply has no subscriber.
func Register(r chi.Router, h *Handler, g router.Guard) {
	desk := g.Chain(g.Session, g.Module(ModuleKey))

	r.With(desk, g.Permission("material.create")).Post("/materials", h.Declare)
	r.With(desk, g.Permission("material.view")).Get("/materials", h.List)
	// Static before the wildcard: chi prefers the exact segment, so this never swallows a
	// numeric id.
	r.With(desk, g.Permission("material.view")).Get("/materials/outstanding", h.Outstanding)
	r.With(desk, g.Permission("material.close")).Post("/materials/escalate", h.Escalate)
	r.With(desk, g.Permission("material.view")).Get("/materials/{materialID}", h.Get)
	r.With(desk, g.Permission("material.approve")).Post("/materials/{materialID}/decision", h.Decide)
	r.With(desk, g.Permission("material.verify_return")).Post("/materials/{materialID}/return", h.Return)
	// record_exception is the permission 0026 seeded for exactly this and no route ever
	// used: an exception nobody can name is the dispute.
	r.With(desk, g.Permission("material.record_exception")).Post("/materials/{materialID}/dispute", h.Dispute)
}
