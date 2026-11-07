package compliance

import (
	"github.com/go-chi/chi/v5"

	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/router"
)

// Register mounts the compliance surface. With audit_compliance switched off these paths answer
// 404; Core's own audit trail keeps being written either way, because it is not a module.
func Register(r chi.Router, h *Handler, g router.Guard) {
	desk := g.Chain(g.Session, g.Module(ModuleKey))
	see := g.Permission("audit.view")
	verify := g.Permission("audit.verify")
	retain := g.Permission("retention.manage")
	hold := g.Permission("legal_hold.manage")

	r.With(desk, see).Get("/compliance/policies", h.Policies)
	r.With(desk, retain).Post("/compliance/policies", h.SavePolicy)

	r.With(desk, see).Get("/compliance/legal-holds", h.Holds)
	r.With(desk, hold).Post("/compliance/legal-holds", h.PlaceHold)
	r.With(desk, hold).Post("/compliance/legal-holds/{holdID}/release", h.ReleaseHold)

	r.With(desk, verify).Post("/compliance/audit/verify", h.Verify)
	r.With(desk, see).Get("/compliance/audit/verifications", h.Verifications)

	r.With(desk, retain).Post("/compliance/purges", h.Purge)
	r.With(desk, see).Get("/compliance/purges", h.PurgeRuns)
}

// errBadStatus is the one error ParseHoldStatus can raise, kept here so the DTO file does not
// import the error package just to name it.
func errBadStatus(status string) error {
	return errs.BadField("status", status+" is not a hold status")
}
