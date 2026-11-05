package material

import (
	"net/http"

	"accessnow/internal/platform/httpx"
	"accessnow/internal/platform/tenancy"
)

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) Declare(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req DeclareRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	material, err := h.svc.Declare(r.Context(), scope, req.toDeclare())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.Created(w, viewOf(material))
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

	materials, err := h.svc.List(r.Context(), filter)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := make([]MaterialView, 0, len(materials))
	for _, material := range materials {
		views = append(views, viewOf(material))
	}
	httpx.OK(w, httpx.Paginate(views, page.Limit, func(v MaterialView) httpx.Cursor {
		return httpx.Cursor{At: v.CreatedAt, ID: v.ID}
	}))
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	materialID, err := httpx.ID(r, "materialID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	material, items, err := h.svc.Get(r.Context(), scope.TenantID, materialID)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	view := viewOf(material)
	view.Items = viewsOf(items)
	httpx.OK(w, view)
}

func (h *Handler) Decide(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	materialID, err := httpx.ID(r, "materialID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req DecisionRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	material, err := h.svc.Decide(r.Context(), scope, materialID, req.Action == "approve", req.Note)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOf(material))
}

// Return is the desk half of the return story: the guard counted the load, somebody with
// a keyboard says which lines came back and which are still owed.
func (h *Handler) Return(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	materialID, err := httpx.ID(r, "materialID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req ReturnRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	material, err := h.svc.Return(r.Context(), scope, materialID, req.toLines(), req.Note)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOf(material))
}

// Outstanding answers the dashboard question "what is still out there". It is a list of
// lines, not passes, because a line is the thing a tenant chases.
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
