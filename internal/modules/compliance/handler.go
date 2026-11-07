package compliance

import (
	"net/http"

	"accessnow/internal/platform/httpx"
	"accessnow/internal/platform/tenancy"
)

// Handler is the compliance desk.
type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) Policies(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	policies, err := h.svc.Policies(r.Context(), scope.TenantID)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, map[string]any{"items": policyViews(policies)})
}

// SavePolicy is an upsert by category: one policy per category is what the purge reads, and two
// rows would make which one applies a coin toss.
func (h *Handler) SavePolicy(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req PolicyRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	policy, err := h.svc.SavePolicy(r.Context(), scope, req)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, policyView(policy))
}

func (h *Handler) Holds(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	status, err := ParseHoldStatus(r)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	holds, err := h.svc.Holds(r.Context(), scope.TenantID, status)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, map[string]any{"items": holdViews(holds)})
}

func (h *Handler) PlaceHold(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req HoldRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	hold, err := h.svc.PlaceHold(r.Context(), scope, req)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.Created(w, holdView(hold))
}

func (h *Handler) ReleaseHold(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	id, err := httpx.ID(r, "holdID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req ReleaseRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	hold, err := h.svc.ReleaseHold(r.Context(), scope, id, req)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, holdView(hold))
}

// Verify walks the audit chain and records the finding.
func (h *Handler) Verify(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req VerifyRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	verification, err := h.svc.Verify(r.Context(), scope, req)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.Created(w, verificationView(verification))
}

func (h *Handler) Verifications(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	rows, err := h.svc.Verifications(r.Context(), scope.TenantID)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, map[string]any{"items": verificationViews(rows)})
}

// Purge runs a category's retention policy. The response says whether anything was actually
// removed, so a dry run and a real one are never confused for each other.
func (h *Handler) Purge(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req PurgeRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	run, err := h.svc.Purge(r.Context(), scope, req)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.Created(w, purgeView(run))
}

func (h *Handler) PurgeRuns(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	runs, err := h.svc.PurgeRuns(r.Context(), scope.TenantID)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, map[string]any{"items": purgeViews(runs)})
}
