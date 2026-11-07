package access

import (
	"net/http"
	"strings"

	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/httpx"
	"accessnow/internal/platform/tenancy"
)

// Module answers the "a module can be off and the system still works" rule at the
// router: a disabled module has no surface at all, so no service down the chain needs
// an if-module-on branch.
func (s *Service) Module(moduleKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			scope, err := tenancy.Require(r.Context())
			if err != nil {
				httpx.Fail(r.Context(), w, r, err)
				return
			}
			enabled, err := s.Enabled(r.Context(), scope.TenantID, moduleKey)
			if err != nil {
				httpx.Fail(r.Context(), w, r, err)
				return
			}
			if !enabled {
				httpx.Fail(r.Context(), w, r, errs.ModuleDisabled(moduleKey))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func (s *Service) Permission(permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			scope, err := tenancy.Require(r.Context())
			if err != nil {
				httpx.Fail(r.Context(), w, r, err)
				return
			}
			if err := s.RequirePermission(r.Context(), scope, permission); err != nil {
				httpx.Fail(r.Context(), w, r, err)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ReadOnly enforces the auditor template at the router rather than trusting every route to
// omit its write verbs. It is a role check, not a permission check, and it runs only on a
// mutating method, so a read-only role costs nothing on the reads it exists for.
func (s *Service) ReadOnly() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet, http.MethodHead, http.MethodOptions:
				next.ServeHTTP(w, r)
				return
			}
			// Signing out is the one write an auditor must keep. It ends their own session
			// and changes no tenant record.
			if strings.HasSuffix(r.URL.Path, "/auth/logout") {
				next.ServeHTTP(w, r)
				return
			}
			scope, err := tenancy.Require(r.Context())
			if err != nil {
				httpx.Fail(r.Context(), w, r, err)
				return
			}
			auditor, err := s.repo.IsAuditor(r.Context(), scope.TenantID, scope.UserID)
			if err != nil {
				httpx.Fail(r.Context(), w, r, err)
				return
			}
			if auditor {
				httpx.Fail(r.Context(), w, r, errs.Forbidden("the auditor role is read only"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// GateWork keeps the gate picker and the guard screen away from roles that do not work
// a gate, which is what makes the picker skip for requesters, approvers and auditors.
func (s *Service) GateWork() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			scope, err := tenancy.Require(r.Context())
			if err != nil {
				httpx.Fail(r.Context(), w, r, err)
				return
			}
			works, err := s.DoesGateWork(r.Context(), scope.TenantID, scope.UserID)
			if err != nil {
				httpx.Fail(r.Context(), w, r, err)
				return
			}
			if !works {
				httpx.Fail(r.Context(), w, r, errs.Forbidden("this role does not work a gate"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
