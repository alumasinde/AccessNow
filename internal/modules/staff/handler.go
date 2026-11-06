package staff

import (
	"net/http"

	"accessnow/internal/platform/httpx"
	"accessnow/internal/platform/tenancy"
)

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// There is no check-out or check-in endpoint here on purpose. An employee leaves through a
// barrier and comes back through one, and the barrier is the only thing that may say it did.

func (h *Handler) Request(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req OutpassRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filed, err := h.svc.Request(r.Context(), scope, req.toRequest())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.Created(w, viewOf(filed))
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

	outpasses, err := h.svc.List(r.Context(), filter)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := viewsOf(outpasses)
	httpx.OK(w, httpx.Paginate(views, page.Limit, func(v OutpassView) httpx.Cursor {
		return httpx.Cursor{At: v.CreatedAt, ID: v.ID}
	}))
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filed, err := h.svc.Get(r.Context(), scope.TenantID, id)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOf(filed))
}

func (h *Handler) Decide(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req DecisionRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filed, err := h.svc.Decide(r.Context(), scope, id, req.Action == "approve", req.Note)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOf(filed))
}

func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req NoteRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filed, err := h.svc.Cancel(r.Context(), scope, id, req.Note)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOf(filed))
}

func (h *Handler) Close(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req NoteRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filed, err := h.svc.Close(r.Context(), scope, id, req.Note)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOf(filed))
}

// Escalate is the sweep, exposed as a POST so a site can hang a cron job or a supervisor
// button on the same code without a scheduler in this process.
func (h *Handler) Escalate(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	moved, err := h.svc.Escalate(r.Context(), scope)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, moved)
}

// Outstanding is the roster of who is off the site right now.
func (h *Handler) Outstanding(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	page, err := httpx.ParsePage(r, defaultPage, 200)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	rows, err := h.svc.Outstanding(r.Context(), scope.TenantID, page.Limit)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, map[string]any{"items": rows, "count": len(rows)})
}

func (h *Handler) Reasons(w http.ResponseWriter, r *http.Request) {
	reasons, err := h.svc.Reasons(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, map[string]any{"items": viewsOfReasons(reasons), "count": len(reasons)})
}

func scopeAndID(r *http.Request) (tenancy.Scope, int64, error) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		return scope, 0, err
	}
	id, err := httpx.ID(r, "outpassID")
	return scope, id, err
}
