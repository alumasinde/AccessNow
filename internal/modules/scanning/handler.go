package scanning

import (
	"net/http"
	"time"

	"accessnow/internal/platform/httpx"
	"accessnow/internal/platform/tenancy"
)

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// IssueToken hands back the only copy of the secret. A desk that loses it re-issues, which is
// cheaper than a stored secret every copy of the database can read.
func (h *Handler) IssueToken(w http.ResponseWriter, r *http.Request) {
	scope, passID, err := scopeAndID(r, "passID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req IssueTokenRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	issued, err := h.svc.Issue(r.Context(), scope, req.toRequest(passID))
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.Created(w, viewOfIssued(issued, time.Now().UTC()))
}

func (h *Handler) ListTokens(w http.ResponseWriter, r *http.Request) {
	scope, passID, err := scopeAndID(r, "passID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	rows, err := h.svc.Tokens(r.Context(), scope.TenantID, passID)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := viewsOfTokens(rows, time.Now().UTC())
	httpx.OK(w, map[string]any{"items": views, "count": len(views)})
}

func (h *Handler) RevokeToken(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r, "tokenID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req RevokeTokenRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	row, err := h.svc.Revoke(r.Context(), scope, id, req.Reason)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOfToken(row, time.Now().UTC()))
}

// Attempts is the supervisor's log. It answers "who was refused here last night, and why" with
// rows for the refusals that never became a movement.
func (h *Handler) Attempts(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter, page, err := ParseAttempts(r)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter.TenantID = scope.TenantID
	filter.Limit = page.Limit + 1
	filter.Before = page.Cursor

	rows, err := h.svc.Attempts(r.Context(), filter)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := viewsOfAttempts(rows)
	httpx.OK(w, httpx.Paginate(views, page.Limit, func(v AttemptView) httpx.Cursor {
		return httpx.Cursor{At: v.OccurredAt, ID: v.ID}
	}))
}

func (h *Handler) Attempt(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r, "attemptID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	row, err := h.svc.Attempt(r.Context(), scope.TenantID, id)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOfAttempt(row))
}

func scopeAndID(r *http.Request, param string) (tenancy.Scope, int64, error) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		return scope, 0, err
	}
	id, err := httpx.ID(r, param)
	return scope, id, err
}
