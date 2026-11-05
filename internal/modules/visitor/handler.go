package visitor

import (
	"context"
	"net/http"

	"accessnow/internal/core/movements"
	"accessnow/internal/platform/httpx"
	"accessnow/internal/platform/tenancy"
)

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req CreateRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	visit, err := h.svc.Create(r.Context(), scope, req.toCreate())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.Created(w, viewOf(visit))
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter, page, err := ParseList(r)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter.TenantID = scope.TenantID
	filter.Limit = page.Limit + 1
	filter.Before = page.Cursor

	visits, err := h.svc.List(r.Context(), filter)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := make([]VisitView, 0, len(visits))
	for _, visit := range visits {
		views = append(views, viewOf(visit))
	}
	httpx.OK(w, httpx.Paginate(views, page.Limit, func(v VisitView) httpx.Cursor {
		return httpx.Cursor{At: v.CreatedAt, ID: v.ID}
	}))
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	visitID, err := httpx.ID(r, "visitID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	visit, person, err := h.svc.Get(r.Context(), scope.TenantID, visitID)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, map[string]any{
		"visit":   viewOf(visit),
		"visitor": viewOfVisitor(person),
	})
}

func (h *Handler) Decide(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	visitID, err := httpx.ID(r, "visitID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req DecisionRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	visit, err := h.svc.Decide(r.Context(), scope, visitID, req.Action == "approve", req.Note)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOf(visit))
}

func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	visitID, err := httpx.ID(r, "visitID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req CancelRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	visit, err := h.svc.Cancel(r.Context(), scope, visitID, req.Reason)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOf(visit))
}

// CheckIn answers the desk button. The gate decision rides along, because a red answer is
// still a correct answer for the person at the counter.
func (h *Handler) CheckIn(w http.ResponseWriter, r *http.Request) {
	h.arrive(w, r, h.svc.CheckIn)
}

func (h *Handler) CheckOut(w http.ResponseWriter, r *http.Request) {
	h.arrive(w, r, h.svc.CheckOut)
}

func (h *Handler) arrive(w http.ResponseWriter, r *http.Request,
	do func(context.Context, tenancy.Scope, int64) (Visit, movements.Outcome, error)) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	visitID, err := httpx.ID(r, "visitID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	visit, outcome, err := do(r.Context(), scope, visitID)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, map[string]any{"visit": viewOf(visit), "gate": outcome.View()})
}
