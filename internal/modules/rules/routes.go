package rules

import (
	"github.com/go-chi/chi/v5"

	"accessnow/internal/platform/router"
)

// Register mounts the rulebook surface. With rules_policy switched off these paths answer 404
// and the gates are unchanged: the guard Core holds returns an empty Decision, so every scan
// is judged on the facts Core has always owned and no rule is walked.
//
// There is no endpoint here that asks what the rules say about a pass. That question is
// answered inside a scan, where the gate, the guard and the waiver already are - and a public
// "would this be refused" route would hand a tenant's restrictions to anybody with a session.
func Register(r chi.Router, h *Handler, g router.Guard) {
	desk := g.Chain(g.Session, g.Module(ModuleKey))
	see := g.Permission("rule.view")
	manage := g.Permission("rule.manage")

	r.With(desk, see).Get("/rules/catalog", h.Catalog)
	r.With(desk, see).Get("/rules/summary", h.Summary)
	r.With(desk, see).Get("/rules/evaluations", h.Evaluations)

	// Static before the wildcard, so "catalog" is never read as a rule id.
	r.With(desk, manage).Post("/rules", h.File)
	r.With(desk, see).Get("/rules", h.List)
	r.With(desk, see).Get("/rules/{ruleID}", h.Get)
	r.With(desk, manage).Put("/rules/{ruleID}", h.Save)
	r.With(desk, manage).Post("/rules/{ruleID}/active", h.Pause)
	r.With(desk, manage).Delete("/rules/{ruleID}", h.Retire)
}
