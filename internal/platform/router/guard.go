// Package router carries the guard bundle every module mounts behind. It is a leaf so
// core, modules and the server can all name it without importing each other.
package router

import "net/http"

type Guard struct {
	// Session resolves the cookie or bearer token into a tenant scope.
	Session func(http.Handler) http.Handler
	// Module refuses the whole surface when the tenant has not bought it.
	Module func(moduleKey string) func(http.Handler) http.Handler
	// Permission refuses by capability.
	Permission func(permission string) func(http.Handler) http.Handler
	// GateWork refuses roles that do not stand at a gate.
	GateWork func(http.Handler) http.Handler
	// Device binds a request on the gate surface to an enrolled tablet and its gate.
	Device func(http.Handler) http.Handler
}

// Chain composes guards outside-in: the first listed runs first.
func (g Guard) Chain(middleware ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		for i := len(middleware) - 1; i >= 0; i-- {
			if middleware[i] != nil {
				next = middleware[i](next)
			}
		}
		return next
	}
}
