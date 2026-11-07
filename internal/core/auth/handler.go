package auth

import (
	"context"
	"net/http"
	"time"

	"accessnow/internal/platform/config"
	"accessnow/internal/platform/httpx"
	"accessnow/internal/platform/logging"
	"accessnow/internal/platform/tenancy"
)

type Handler struct {
	svc    *Service
	access Access
	secure bool
	ttl    time.Duration
}

// Access is the read side of the guard bundle: the session hands the UI the same facts
// every protected route is judged on, rather than the UI inventing its own role table.
type Access interface {
	Permissions(ctx context.Context, tenantID, userID int64) ([]string, error)
	Modules(ctx context.Context, tenantID int64) (map[string]bool, error)
	DoesGateWork(ctx context.Context, tenantID, userID int64) (bool, error)
	Auditor(ctx context.Context, tenantID, userID int64) (bool, error)
}

func NewHandler(svc *Service, c config.Config, access Access) *Handler {
	return &Handler{svc: svc, access: access, secure: c.CookieSecure, ttl: c.SessionTTL}
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

	tenant, site, permissions, modules, capabilities, err := h.bootstrap(r.Context(), result.Scope, result.User)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.Created(w, SessionResponse{
		Token: result.Token, ExpiresAt: result.ExpiresAt,
		MustChangePassword: result.MustChangePassword, User: view(result.User),
		Tenant: tenant, Site: site, Permissions: permissions, Modules: modules,
		Capabilities: capabilities, ActiveSiteID: result.Scope.SiteID,
		ActiveGateID: result.Scope.GateID,
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
	tenant, site, permissions, modules, capabilities, err := h.bootstrap(r.Context(), scope, user)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, CurrentSession{
		User: view(user), Scope: scopeViewOf(scope), Tenant: tenant, Site: site,
		Permissions: permissions, Modules: modules, Capabilities: capabilities,
	})
}

// bootstrap reads all of the session's presentation facts once so login and refresh
// cannot drift apart. Authorization still happens per route in the middleware.
func (h *Handler) bootstrap(ctx context.Context, scope tenancy.Scope, user User) (TenantView, *SiteView, []string, map[string]bool, Capabilities, error) {
	tenant, err := h.svc.TenantByID(ctx, scope.TenantID)
	if err != nil {
		return TenantView{}, nil, nil, nil, Capabilities{}, err
	}
	site, err := h.svc.SiteByID(ctx, scope.TenantID, scope.SiteID)
	if err != nil {
		return TenantView{}, nil, nil, nil, Capabilities{}, err
	}
	permissions, err := h.access.Permissions(ctx, scope.TenantID, scope.UserID)
	if err != nil {
		return TenantView{}, nil, nil, nil, Capabilities{}, err
	}
	modules, err := h.access.Modules(ctx, scope.TenantID)
	if err != nil {
		return TenantView{}, nil, nil, nil, Capabilities{}, err
	}
	gateWork, err := h.access.DoesGateWork(ctx, scope.TenantID, scope.UserID)
	if err != nil {
		return TenantView{}, nil, nil, nil, Capabilities{}, err
	}
	auditor, err := h.access.Auditor(ctx, scope.TenantID, scope.UserID)
	if err != nil {
		return TenantView{}, nil, nil, nil, Capabilities{}, err
	}

	var siteView *SiteView
	if site != nil {
		siteView = &SiteView{ID: site.ID, Name: site.Name, Timezone: site.Timezone}
	}
	return TenantView{ID: tenant.ID, Name: tenant.Name, Timezone: tenant.Timezone}, siteView,
		permissions, modules, Capabilities{
			SuperAdmin: user.IsSuperAdmin || scope.IsSuperAdmin,
			GateWork:   gateWork,
			ReadOnly:   auditor,
		}, nil
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

// clientIP trusts only r.RemoteAddr. When TRUST_PROXY_HEADERS is on, chi's RealIP middleware has
// already rewritten it from the proxy's header; reading X-Forwarded-For here as well would let
// any client write its own address into the audit trail.
func clientIP(r *http.Request) string {
	return trimHost(r.RemoteAddr)
}
