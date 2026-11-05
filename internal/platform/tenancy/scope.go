package tenancy

import (
	"context"

	"accessnow/internal/platform/errs"
)

// Scope is what one request knows about itself: which tenant, which user, which gate
// that user is standing at. Every repository call takes TenantID from here and no
// request body is ever trusted for it.
//
// SessionID is the login row in user_sessions; GateShiftID is the guard's open shift in
// gate_sessions. Different tables, and a movement that records the wrong one trips its
// foreign key.
type Scope struct {
	TenantID     int64
	UserID       int64
	SessionID    int64
	SiteID       int64
	GateID       int64
	DeviceID     int64
	GateShiftID  int64
	Timezone     string
	AuthMethod   string
	IsSuperAdmin bool
}

func (s Scope) HasGate() bool { return s.GateID > 0 }

type scopeKey struct{}

func Put(ctx context.Context, s Scope) context.Context {
	return context.WithValue(ctx, scopeKey{}, s)
}

func Get(ctx context.Context) (Scope, bool) {
	s, ok := ctx.Value(scopeKey{}).(Scope)
	return s, ok
}

// Require is what handlers call: a missing scope is a routing bug or a missing
// middleware, never a zero-tenant query.
func Require(ctx context.Context) (Scope, error) {
	s, ok := Get(ctx)
	if !ok || s.TenantID == 0 {
		return Scope{}, errs.Unauthenticated("request carries no tenant scope")
	}
	return s, nil
}
