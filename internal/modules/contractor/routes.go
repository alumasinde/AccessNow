package contractor

import (
	"github.com/go-chi/chi/v5"

	"accessnow/internal/platform/router"
)

// Register mounts the contractor desk. With contractor_access switched off these paths answer
// 404; a pass already issued stays on Core's spine, and the barrier keeps honouring it until
// the range runs out, which is what a module switch means everywhere else in this system.
func Register(r chi.Router, h *Handler, g router.Guard) {
	desk := g.Chain(g.Session, g.Module(ModuleKey))
	view := g.Permission("contractor.view")
	manage := g.Permission("contractor.manage")

	r.With(desk, manage).Post("/contractor-organizations", h.CreateOrganization)
	r.With(desk, view).Get("/contractor-organizations", h.ListOrganizations)
	r.With(desk, view).Get("/contractor-organizations/{organizationID}", h.GetOrganization)
	r.With(desk, manage).Post("/contractor-organizations/{organizationID}/status", h.OrganizationStatus)

	r.With(desk, manage).Post("/contractors", h.CreateContractor)
	r.With(desk, view).Get("/contractors", h.ListContractors)
	r.With(desk, view).Get("/contractors/{contractorID}", h.GetContractor)
	r.With(desk, manage).Post("/contractors/{contractorID}/status", h.ContractorStatus)

	r.With(desk, view).Get("/contractors/{contractorID}/documents", h.ListDocuments)
	r.With(desk, g.Permission("contractor_document.upload")).
		Post("/contractors/{contractorID}/documents", h.AddDocument)
	// Verifying a permit is a desk decision, not the clerk's: it is what lets somebody on site.
	r.With(desk, g.Permission("contractor_document.verify")).
		Post("/contractor-documents/{documentID}/decision", h.DecideDocument)
	r.With(desk, view).Get("/document-types", h.DocumentTypes)
	r.With(desk, view).Get("/contractor-documents/expiring", h.ExpiringDocuments)

	r.With(desk, g.Permission("contractor_pass.create")).Post("/contractor-passes", h.CreatePass)
	r.With(desk, view).Get("/contractor-passes", h.ListPasses)
	// Static before the wildcard, so these never get read as an id.
	r.With(desk, manage).Post("/contractor-passes/sweep", h.Sweep)
	r.With(desk, view).Get("/contractor-passes/{contractorPassID}", h.GetPass)
	r.With(desk, g.Permission("contractor_pass.approve")).
		Post("/contractor-passes/{contractorPassID}/decision", h.DecidePass)
	r.With(desk, g.Permission("contractor_pass.revoke")).
		Post("/contractor-passes/{contractorPassID}/revoke", h.RevokePass)
	r.With(desk, manage).Post("/contractor-passes/{contractorPassID}/block", h.BlockPass)
	r.With(desk, manage).Post("/contractor-passes/{contractorPassID}/unblock", h.UnblockPass)
}
