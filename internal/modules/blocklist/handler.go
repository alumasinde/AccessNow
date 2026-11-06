package blocklist

import (
	"net/http"

	"accessnow/internal/platform/httpx"
	"accessnow/internal/platform/tenancy"
)

// Handler is the desk surface for the lists. Nothing here decides at a barrier: the gate's
// answer comes from the guard, which is handed the same repository through Core's own scan.
type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Add files a listing. 201 with the row as it was stored, so the desk sees the normalised
// value the gate will actually compare against.
func (h *Handler) Add(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req EntryRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	entry, err := h.svc.Add(r.Context(), scope, req.toNew())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.Created(w, viewOf(entry))
}

// List is the register: newest first, with the retired entries left out unless the caller
// asks for the history too.
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

	entries, err := h.svc.List(r.Context(), filter)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := viewsOf(entries)
	httpx.OK(w, httpx.Paginate(views, page.Limit, func(v EntryView) httpx.Cursor {
		return httpx.Cursor{At: v.AddedAt, ID: v.ID}
	}))
}

func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	id, err := httpx.ID(r, "entryID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	entry, err := h.svc.Get(r.Context(), scope.TenantID, id)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOf(entry))
}

// Revoke takes a listing off the list. It answers with the row rather than 204 because the
// desk screen shows the reason and the time it stopped biting.
func (h *Handler) Revoke(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	id, err := httpx.ID(r, "entryID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req RevokeRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	entry, err := h.svc.Revoke(r.Context(), scope, id, req.Reason)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOf(entry))
}

// ListHits is the feed a supervisor opens in the morning: what the lists caught, what the
// gate did about it, and which of them nobody has explained yet.
func (h *Handler) ListHits(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter, page, err := ParseHits(r)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter.TenantID = scope.TenantID
	filter.Limit = page.Limit + 1
	filter.Before = page.Cursor

	hits, err := h.svc.Hits(r.Context(), filter)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := hitViewsOf(hits)
	httpx.OK(w, httpx.Paginate(views, page.Limit, func(v HitView) httpx.Cursor {
		return httpx.Cursor{At: v.OccurredAt, ID: v.ID}
	}))
}

func (h *Handler) GetHit(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	id, err := httpx.ID(r, "hitID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	hit, err := h.svc.Hit(r.Context(), scope.TenantID, id)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, hitViewOf(hit))
}

// Resolve closes one hit. It is deliberately not the listing: saying a hit was the wrong
// person must not take the ban off the right one.
func (h *Handler) Resolve(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	id, err := httpx.ID(r, "hitID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req ResolveRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	hit, err := h.svc.Resolve(r.Context(), scope, id, req.Resolution)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, hitViewOf(hit))
}

// Summary is the two counts the list screen leads with, plus the open hits.
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
