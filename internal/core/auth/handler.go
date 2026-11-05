package auth

import (
	"net/http"
	"time"

	"accessnow/internal/platform/config"
	"accessnow/internal/platform/httpx"
	"accessnow/internal/platform/logging"
	"accessnow/internal/platform/tenancy"
)

type Handler struct {
	svc    *Service
	secure bool
	ttl    time.Duration
}

func NewHandler(svc *Service, c config.Config) *Handler {
	return &Handler{svc: svc, secure: c.CookieSecure, ttl: c.SessionTTL}
}

func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var req LoginRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}

	result, err := h.svc.Login(r.Context(), LoginInput{
		TenantSlug: req.Tenant, Identifier: req.Identifier, Password: req.Password,
		IPAddress: clientIP(r), UserAgent: r.UserAgent(),
	})
	if err != nil {
		logging.From(r.Context()).WarnContext(r.Context(), "login refused", "tenant", req.Tenant)
		httpx.Fail(r.Context(), w, r, err)
		return
	}

	cookie := Cookie(h.ttl, h.secure)
	cookie.Value = result.Token
	http.SetCookie(w, cookie)

	httpx.Created(w, SessionResponse{
		Token: result.Token, ExpiresAt: result.ExpiresAt,
		MustChangePassword: result.MustChangePassword, User: view(result.User),
		ActiveSiteID: result.Scope.SiteID,
	})
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	if err := h.svc.Logout(r.Context(), scope); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	cookie := Cookie(h.ttl, h.secure)
	cookie.Value, cookie.MaxAge = "", -1
	http.SetCookie(w, cookie)
	httpx.NoContent(w)
}

func (h *Handler) Current(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	user, err := h.svc.Current(r.Context(), scope)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, map[string]any{"user": view(user), "scope": scopeViewOf(scope)})
}

type scopeView struct {
	TenantID int64 `json:"tenant_id"`
	UserID   int64 `json:"user_id"`
	SiteID   int64 `json:"site_id,omitempty"`
	GateID   int64 `json:"gate_id,omitempty"`
	DeviceID int64 `json:"device_id,omitempty"`
}

func scopeViewOf(s tenancy.Scope) scopeView {
	return scopeView{TenantID: s.TenantID, UserID: s.UserID, SiteID: s.SiteID, GateID: s.GateID, DeviceID: s.DeviceID}
}

func clientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		return first(forwarded)
	}
	return trimHost(r.RemoteAddr)
}
