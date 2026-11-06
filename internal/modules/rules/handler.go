package rules

import (
	"net/http"
	"time"

	"accessnow/internal/platform/httpx"
	"accessnow/internal/platform/tenancy"
)

// Handler is the desk surface for the rulebook. Nothing here decides at a barrier: the gate's
// answer comes from the guard, which reads the same repository through Core's own scan.
type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// File writes a new rule onto the books, and answers with the row as stored so the desk sees
// the defaults it did not type: the priority it was given, the period a counted rule opened on.
func (h *Handler) File(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req RuleRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	rule, err := h.svc.File(r.Context(), scope, req.toNew())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.Created(w, viewOf(rule, time.Now().UTC()))
}

// Save rewrites a rule in place. It is a whole rewrite, so the body is the same shape as the
// one that filed it; the version on the row goes up and every evaluation log keeps pointing
// at the same id.
func (h *Handler) Save(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	id, err := httpx.ID(r, "ruleID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req RuleRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	rule, err := h.svc.Save(r.Context(), scope, id, req.toNew())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOf(rule, time.Now().UTC()))
}

// List is the rulebook in the order the gate walks it, which is the only order that explains
// why one rule won.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter, page, err := ParseRules(r)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter.TenantID = scope.TenantID
	filter.Limit = page.Limit

	rules, err := h.svc.List(r.Context(), filter)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewsOf(rules))
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	id, err := httpx.ID(r, "ruleID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	rule, err := h.svc.Get(r.Context(), scope.TenantID, id)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOf(rule, time.Now().UTC()))
}

// Pause takes a rule off the books, or puts it back. The desk pauses rather than deletes
// because the evaluation log behind a rule has to keep naming something.
func (h *Handler) Pause(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	id, err := httpx.ID(r, "ruleID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req ActiveRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	rule, err := h.svc.Pause(r.Context(), scope, id, req.IsActive)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOf(rule, time.Now().UTC()))
}

// Retire takes a rule off the books for good, with the sentence that says why.
func (h *Handler) Retire(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	id, err := httpx.ID(r, "ruleID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req RetireRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	rule, err := h.svc.Retire(r.Context(), scope, id, req.Reason)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOf(rule, time.Now().UTC()))
}

// Summary is the desk screen's opening numbers.
func (h *Handler) Summary(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	summary, err := h.svc.Summary(r.Context(), scope.TenantID)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, summary)
}

// Evaluations is the feed: what the rulebook did, to which pass, and whether anybody waived
// it. Newest first, because the question it is opened with is about this morning.
func (h *Handler) Evaluations(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter, page, err := ParseEvaluations(r)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter.TenantID = scope.TenantID
	filter.Limit = page.Limit + 1

	rows, err := h.svc.Evaluations(r.Context(), filter)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := evaluationViewsOf(rows)
	httpx.OK(w, httpx.Paginate(views, page.Limit, func(v EvaluationView) httpx.Cursor {
		return httpx.Cursor{At: v.EvaluatedAt, ID: v.ID}
	}))
}

// Catalog is what a rule may be written about: the fields, the operators, the actions. It is
// served rather than hard-coded into a form so that a new fact the engine can read is one the
// desk finds by itself.
func (h *Handler) Catalog(w http.ResponseWriter, r *http.Request) {
	httpx.OK(w, CatalogueOf())
}
