package scanning

import (
	"github.com/go-chi/chi/v5"

	"accessnow/internal/platform/router"
)

// Register mounts the scanning desk. These are the paths that mint and retire a badge; the
// barrier itself reads tokens on Core's /gate/scan, because the gate's session, device and
// direction are one decision and splitting it across two endpoints is how a guard ends up with
// two answers.
func Register(r chi.Router, h *Handler, g router.Guard) {
	desk := g.Chain(g.Session, g.Module(ModuleKey))
	view := g.Permission("scan.view_log")

	r.With(desk, view).Get("/passes/{passID}/tokens", h.ListTokens)
	r.With(desk, g.Permission("token.issue")).Post("/passes/{passID}/token", h.IssueToken)
	r.With(desk, g.Permission("token.revoke")).Post("/tokens/{tokenID}/revoke", h.RevokeToken)

	r.With(desk, view).Get("/scan-attempts", h.Attempts)
	r.With(desk, view).Get("/scan-attempts/{attemptID}", h.Attempt)
}
