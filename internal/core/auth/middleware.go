package auth

import (
	"net/http"
	"strings"

	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/httpx"
	"accessnow/internal/platform/logging"
	"accessnow/internal/platform/tenancy"
)

// Middleware turns a credential into a tenancy.Scope on the request context. Nothing
// downstream reads a tenant, gate or user off a query string or a body field.
type Middleware struct{ svc *Service }

func NewMiddleware(svc *Service) *Middleware { return &Middleware{svc: svc} }

func (m *Middleware) Session(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := tokenFrom(r)
		if token == "" {
			httpx.Fail(r.Context(), w, r, errs.Unauthenticated("no session presented"))
			return
		}
		scope, err := m.svc.Resolve(r.Context(), token)
		if err != nil {
			httpx.Fail(r.Context(), w, r, err)
			return
		}

		ctx := tenancy.Put(r.Context(), scope)
		attrs := []any{logging.Tenant(scope.TenantID), logging.User(scope.UserID)}
		if scope.GateID > 0 {
			attrs = append(attrs, logging.Gate(scope.GateID))
		}
		ctx = logging.With(ctx, logging.From(r.Context()).With(attrs...))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// tokenFrom accepts a cookie for browsers and a bearer token for API clients and the
// gate tablet. Only the hash of either is ever compared against the database.
func tokenFrom(r *http.Request) string {
	if cookie, err := r.Cookie(cookieName); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	if header := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(header), "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return ""
}
