package contractor

import (
	"net/http"
	"time"

	"accessnow/internal/platform/httpx"
	"accessnow/internal/platform/tenancy"
)

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// There is no check-in or check-out endpoint here. A contractor walks in through a barrier and
// Core records the movement; this desk issues the permission and answers for it at the gate.

func (h *Handler) CreateOrganization(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req OrganizationRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	created, err := h.svc.CreateOrganization(r.Context(), scope, req.toNew())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.Created(w, viewOfOrganization(created))
}

func (h *Handler) ListOrganizations(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter, page, err := ParseOrganizations(r)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter.TenantID = scope.TenantID
	filter.Limit = page.Limit + 1
	filter.Before = page.Cursor

	rows, err := h.svc.Organizations(r.Context(), filter)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := viewsOfOrganizations(rows)
	httpx.OK(w, httpx.Paginate(views, page.Limit, func(v OrganizationView) httpx.Cursor {
		return httpx.Cursor{At: v.CreatedAt, ID: v.ID}
	}))
}

func (h *Handler) GetOrganization(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r, "organizationID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	row, err := h.svc.Organization(r.Context(), scope.TenantID, id)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOfOrganization(row))
}

func (h *Handler) OrganizationStatus(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r, "organizationID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req StatusRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	row, err := h.svc.SetOrganizationStatus(r.Context(), scope, id, req.Status)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOfOrganization(row))
}

func (h *Handler) CreateContractor(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req ContractorRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	created, err := h.svc.CreateContractor(r.Context(), scope, req.toNew())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.Created(w, viewOfContractor(created))
}

func (h *Handler) ListContractors(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter, page, err := ParseContractors(r)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter.TenantID = scope.TenantID
	filter.Limit = page.Limit + 1
	filter.Before = page.Cursor

	rows, err := h.svc.Contractors(r.Context(), filter)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := viewsOfContractors(rows)
	httpx.OK(w, httpx.Paginate(views, page.Limit, func(v ContractorView) httpx.Cursor {
		return httpx.Cursor{At: v.CreatedAt, ID: v.ID}
	}))
}

func (h *Handler) GetContractor(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r, "contractorID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	row, err := h.svc.Contractor(r.Context(), scope.TenantID, id)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOfContractor(row))
}

func (h *Handler) ContractorStatus(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r, "contractorID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req StatusRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	row, err := h.svc.SetContractorStatus(r.Context(), scope, id, req.Status)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOfContractor(row))
}

func (h *Handler) AddDocument(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r, "contractorID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req DocumentRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	created, err := h.svc.AddDocument(r.Context(), scope, req.toNew(id))
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.Created(w, viewOfDocument(created, LocalDay(time.Now().UTC(), scope.Timezone)))
}

func (h *Handler) ListDocuments(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r, "contractorID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	rows, err := h.svc.Documents(r.Context(), scope.TenantID, id)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	today := LocalDay(time.Now().UTC(), scope.Timezone)
	httpx.OK(w, map[string]any{"items": viewsOfDocuments(rows, today), "count": len(rows)})
}

func (h *Handler) DecideDocument(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r, "documentID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req DocumentDecisionRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	row, err := h.svc.DecideDocument(r.Context(), scope, id, req.Action == "verify", req.Note)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOfDocument(row, LocalDay(time.Now().UTC(), scope.Timezone)))
}

func (h *Handler) DocumentTypes(w http.ResponseWriter, r *http.Request) {
	rows, err := h.svc.DocumentTypes(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, map[string]any{"items": viewsOfDocumentTypes(rows), "count": len(rows)})
}

// ExpiringDocuments is the chase list: paperwork that is still good today and runs out inside
// the window the desk asked about.
func (h *Handler) ExpiringDocuments(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	within := 30
	if raw := r.URL.Query().Get("within_days"); raw != "" {
		days, err := httpx.QueryID(r, "within_days")
		if err != nil {
			httpx.Fail(r.Context(), w, r, err)
			return
		}
		within = int(days)
	}
	page, err := httpx.ParsePage(r, defaultPage, 200)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	rows, err := h.svc.Expiring(r.Context(), scope.TenantID, scope.Timezone, within, page.Limit)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	today := LocalDay(time.Now().UTC(), scope.Timezone)
	httpx.OK(w, map[string]any{"items": viewsOfDocuments(rows, today), "count": len(rows)})
}

func (h *Handler) CreatePass(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req ContractorPassRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filed, err := h.svc.Pass(r.Context(), scope, req.toRequest())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.Created(w, viewOfPass(filed, LocalDay(time.Now().UTC(), scope.Timezone)))
}

func (h *Handler) ListPasses(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter, page, err := ParsePasses(r)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter.TenantID = scope.TenantID
	filter.Limit = page.Limit + 1
	filter.Before = page.Cursor

	rows, err := h.svc.List(r.Context(), filter)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	today := LocalDay(time.Now().UTC(), scope.Timezone)
	views := viewsOfPasses(rows, today)
	httpx.OK(w, httpx.Paginate(views, page.Limit, func(v PassView) httpx.Cursor {
		return httpx.Cursor{At: v.CreatedAt, ID: v.ID}
	}))
}

func (h *Handler) GetPass(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r, "contractorPassID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filed, err := h.svc.Get(r.Context(), scope.TenantID, id)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOfPass(filed, LocalDay(time.Now().UTC(), scope.Timezone)))
}

func (h *Handler) DecidePass(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r, "contractorPassID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req PassDecisionRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filed, err := h.svc.DecidePass(r.Context(), scope, id, req.Action == "approve", req.Note)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOfPass(filed, LocalDay(time.Now().UTC(), scope.Timezone)))
}

func (h *Handler) RevokePass(w http.ResponseWriter, r *http.Request) {
	scope, id, req, ok := noteRequest(r, w, "contractorPassID")
	if !ok {
		return
	}
	filed, err := h.svc.Revoke(r.Context(), scope, id, req.Reason)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOfPass(filed, LocalDay(time.Now().UTC(), scope.Timezone)))
}

func (h *Handler) BlockPass(w http.ResponseWriter, r *http.Request) {
	scope, id, req, ok := noteRequest(r, w, "contractorPassID")
	if !ok {
		return
	}
	filed, err := h.svc.Block(r.Context(), scope, id, req.Reason)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOfPass(filed, LocalDay(time.Now().UTC(), scope.Timezone)))
}

func (h *Handler) UnblockPass(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r, "contractorPassID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filed, err := h.svc.Unblock(r.Context(), scope, id)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOfPass(filed, LocalDay(time.Now().UTC(), scope.Timezone)))
}

// Sweep is the nightly document check, exposed as a POST so a cron job and a supervisor's
// button run the same code without a scheduler in this process.
func (h *Handler) Sweep(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	result, err := h.svc.Sweep(r.Context(), scope)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, result)
}

func scopeAndID(r *http.Request, param string) (tenancy.Scope, int64, error) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		return scope, 0, err
	}
	id, err := httpx.ID(r, param)
	return scope, id, err
}

// noteRequest decodes the reason a revoke or block has to carry. It answers the response itself
// when the body is bad, so the two callers stay three lines long.
func noteRequest(r *http.Request, w http.ResponseWriter, param string) (tenancy.Scope, int64, NoteRequest, bool) {
	scope, id, err := scopeAndID(r, param)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return tenancy.Scope{}, 0, NoteRequest{}, false
	}
	var req NoteRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return tenancy.Scope{}, 0, NoteRequest{}, false
	}
	return scope, id, req, true
}
