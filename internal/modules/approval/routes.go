package approval

import (
	"github.com/go-chi/chi/v5"

	"accessnow/internal/platform/router"
)

// Register mounts the workflow surface. With approval_workflow switched off these paths
// answer 404 and the rest of the system keeps working: passes are still issued, still
// decided by the desk, and still scanned at the barrier, because nothing outside this
// module needs it to be reachable - the guard that holds a pass returns false the moment
// the switch is off, which is exactly what makes the module safe to enable per site.
func Register(r chi.Router, h *Handler, g router.Guard) {
	desk := g.Chain(g.Session, g.Module(ModuleKey))
	see := g.Permission("approval.view")
	// decide is the supervisor's authority: it answers an ask, restarts a chain whose paper
	// changed, and files a chain for a pass that was issued before this module existed.
	act := g.Permission("approval.decide")
	design := g.Permission("approval_flow.manage")
	cover := g.Permission("approval.delegate")

	// Statics before the wildcard: chi prefers the exact segment, so none of these is
	// swallowed by /approvals/{requestID} and none swallows a numeric id.
	r.With(desk, see).Get("/approvals/inbox", h.Inbox)
	r.With(desk, act).Post("/approvals/escalate", h.Escalate)
	r.With(desk, see).Get("/approvals", h.List)

	r.With(desk, see).Get("/approvals/passes/{passID}/chain", h.GetByPass)
	r.With(desk, act).Post("/approvals/passes/{passID}/chain", h.StartChain)
	r.With(desk, act).Post("/approvals/passes/{passID}/reset", h.Reset)
	r.With(desk, act).Post("/approvals/steps/{stepID}/decision", h.Decide)

	r.With(desk, see).Get("/approvals/flows", h.ListFlows)
	r.With(desk, design).Post("/approvals/flows", h.SaveFlow(true))
	r.With(desk, see).Get("/approvals/flows/{flowID}", h.GetFlow)
	r.With(desk, design).Put("/approvals/flows/{flowID}", h.SaveFlow(false))
	r.With(desk, design).Delete("/approvals/flows/{flowID}", h.DeleteFlow)

	r.With(desk, see).Get("/approvals/delegations", h.ListDelegations)
	r.With(desk, cover).Post("/approvals/delegations", h.SaveDelegation)
	r.With(desk, cover).Post("/approvals/delegations/{delegationID}/end", h.EndDelegation)

	// Last, because it is the widest: everything above is a shape this cannot match.
	r.With(desk, see).Get("/approvals/{requestID}", h.Get)
	// Cancelling belongs to the person who filed the pass, so it is not gated above approve.
	// Service-side identity does the real checking here.
	r.With(desk, see).Post("/approvals/{requestID}/cancel", h.Cancel)
}
