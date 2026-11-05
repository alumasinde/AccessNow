package passes

import (
	"net/http"

	"accessnow/internal/platform/httpx"
	"accessnow/internal/platform/tenancy"
)

const (
	defaultPage = 25
	maxPage     = 100
)

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// List is the pass register: every kind from every module in one query, because the
// question an operator asks is "what is on site right now", not "what is a visit".
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter, err := ParseFilter(r)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	page, err := httpx.ParsePage(r, defaultPage, maxPage)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}

	// one row past the page tells us there is more, so has_more never needs a COUNT
	items, err := h.svc.List(r.Context(), scope.TenantID, filter, page.Limit+1, page.Cursor)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := make([]PassView, 0, len(items))
	for _, item := range items {
		views = append(views, View(item))
	}
	httpx.OK(w, httpx.Paginate(views, page.Limit, cursorOf))
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	passID, err := httpx.ID(r, "passID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	pass, err := h.svc.Get(r.Context(), scope.TenantID, passID)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, View(pass))
}

func cursorOf(view PassView) httpx.Cursor {
	return httpx.Cursor{At: view.CreatedAt, ID: view.ID}
}
