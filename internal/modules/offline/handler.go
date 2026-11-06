package offline

import (
	"net/http"

	"accessnow/internal/platform/httpx"
	"accessnow/internal/platform/tenancy"
)

// Handler is the two surfaces of offline gate mode: the cache a tablet pulls, and the desk
// screen that reads what the pulls and the replays came to. Neither of them decides anything
// about a person at a barrier - that answer is Core's, made when the queue replays.
type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Cache is the bundle a device is told to hold. The device claims the version it believes it
// holds; a claim that does not match the server's own stamp is not argued with, it is answered
// with the whole register.
func (h *Handler) Cache(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	claim, err := ParseCacheVersion(r)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	bundle, err := h.svc.Bundle(r.Context(), scope, claim)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, bundle.View())
}

// Devices is the fleet screen: what each tablet holds, when it last said anything, and how much
// it still owes.
func (h *Handler) Devices(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	fleet, err := h.svc.Devices(r.Context(), scope.TenantID)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, map[string]any{"items": healthViewsOf(fleet), "count": len(fleet)})
}

func (h *Handler) Batches(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter, page, err := ParseBatches(r)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter.TenantID = scope.TenantID
	filter.Limit = page.Limit + 1
	filter.Before = page.Cursor

	batches, err := h.svc.Batches(r.Context(), filter)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := batchViewsOf(batches)
	httpx.OK(w, httpx.Paginate(views, page.Limit, func(v BatchView) httpx.Cursor {
		return httpx.Cursor{At: v.StartedAt, ID: v.ID}
	}))
}

func (h *Handler) Batch(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	id, err := httpx.ID(r, "batchID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	batch, err := h.svc.Batch(r.Context(), scope.TenantID, id)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOf(batch))
}

// Conflicts is the queue a supervisor works through: rows a device and the server could not
// agree about, newest first, unresolved ones the reason the page exists.
func (h *Handler) Conflicts(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter, page, err := ParseConflicts(r)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter.TenantID = scope.TenantID
	filter.Limit = page.Limit + 1
	filter.Before = page.Cursor

	conflicts, err := h.svc.Conflicts(r.Context(), filter)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := conflictViewsOf(conflicts)
	httpx.OK(w, httpx.Paginate(views, page.Limit, func(v ConflictView) httpx.Cursor {
		return httpx.Cursor{At: v.DetectedAt, ID: v.ID}
	}))
}

func (h *Handler) Conflict(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	id, err := httpx.ID(r, "conflictID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	conflict, err := h.svc.Conflict(r.Context(), scope.TenantID, id)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, conflictViewOf(conflict))
}

// Resolve files a person's answer over the server's. It answers with the row rather than 204
// because the desk screen shows what the conflict was settled as, and by whom.
func (h *Handler) Resolve(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	id, err := httpx.ID(r, "conflictID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req ResolveRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	conflict, err := h.svc.Resolve(r.Context(), scope, id, Resolution(req.Resolution), req.Note)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, conflictViewOf(conflict))
}
