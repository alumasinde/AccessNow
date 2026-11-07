package integrations

import (
	"net/http"

	"accessnow/internal/platform/httpx"
	"accessnow/internal/platform/tenancy"
)

func (h *Handler) Systems(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	systems, err := h.svc.Systems(r.Context(), scope.TenantID)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := make([]ExternalSystemView, 0, len(systems))
	for _, system := range systems {
		views = append(views, systemView(system))
	}
	httpx.OK(w, map[string]any{"items": views})
}

func (h *Handler) System(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	id, err := httpx.ID(r, "systemID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	system, err := h.svc.ExternalSystem(r.Context(), scope.TenantID, id)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, systemView(system))
}

func (h *Handler) CreateSystem(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req ExternalSystemRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	system, err := h.svc.CreateSystem(r.Context(), scope, req)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.Created(w, systemView(system))
}

func (h *Handler) UpdateSystem(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	id, err := httpx.ID(r, "systemID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req ExternalSystemRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	system, err := h.svc.UpdateSystem(r.Context(), scope, id, req)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, systemView(system))
}

func (h *Handler) SuspendSystem(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	id, err := httpx.ID(r, "systemID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	if err := h.svc.SuspendSystem(r.Context(), scope, id); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.NoContent(w)
}

func (h *Handler) StartSync(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	id, err := httpx.ID(r, "systemID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req SyncRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	job, err := h.svc.StartSync(r.Context(), scope, id, req)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.JSON(w, http.StatusAccepted, jobView(job))
}

func (h *Handler) SyncJobs(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	systemID, err := httpx.QueryID(r, "system_id")
	if err != nil && r.URL.Query().Get("system_id") != "" {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	jobs, err := h.svc.SyncJobs(r.Context(), scope.TenantID, systemID)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := make([]SyncJobView, 0, len(jobs))
	for _, job := range jobs {
		views = append(views, jobView(job))
	}
	httpx.OK(w, map[string]any{"items": views})
}

func (h *Handler) Links(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	systemID, err := httpx.QueryID(r, "system_id")
	if err != nil && r.URL.Query().Get("system_id") != "" {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	links, err := h.svc.Links(r.Context(), scope.TenantID, systemID,
		r.URL.Query().Get("entity_type"))
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := make([]EntityLinkView, 0, len(links))
	for _, link := range links {
		views = append(views, linkView(link))
	}
	httpx.OK(w, map[string]any{"items": views})
}

func (h *Handler) DeleteLink(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	id, err := httpx.ID(r, "linkID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	if err := h.svc.DeleteLink(r.Context(), scope, id); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.NoContent(w)
}
