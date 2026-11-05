package auth

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"accessnow/internal/platform/router"
)

// Register mounts the session routes under /auth. The limiter for /login is handed in
// because it is the only endpoint somebody else can hammer, and a rate limit belongs with
// the surface that owns the public path.
func Register(r chi.Router, h *Handler, g router.Guard, login func(http.Handler) http.Handler) {
	r.With(login).Post("/login", h.Login)
	r.With(g.Session).Post("/logout", h.Logout)
	r.With(g.Session).Get("/me", h.Current)
}
