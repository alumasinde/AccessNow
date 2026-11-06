package blocklist

import (
	"context"

	"accessnow/internal/core/movements"
)

// Guard is the object Core holds instead of an import: the barrier asks it the one question
// it cannot answer for itself, and never learns which module, table or list is behind the
// answer.
//
// It is built empty and bound afterwards because the dependency runs the other way - Core's
// movement service has to exist before this module can read its pass table, and Core needs
// this handle while it is being built. The binding happens while the object graph is still
// assembled, before a single request is served.
type Guard struct{ svc *Service }

func NewGuard() *Guard { return &Guard{} }

// Bind hands the guard the service it is the front door for.
func (g *Guard) Bind(svc *Service) { g.svc = svc }

// Screen answers with an empty verdict whenever the module is off or unbound, which is the
// case that matters most: a tenant that has not bought screening has every barrier working
// exactly as it did the day before.
func (g *Guard) Screen(ctx context.Context, q movements.ScreenQuery) (movements.Verdict, error) {
	if g.svc == nil {
		return movements.Verdict{}, nil
	}
	return g.svc.Screen(ctx, q)
}

// Note files the hit and raises the quiet alarm. Core logs its error rather than showing it:
// the gate has already answered.
func (g *Guard) Note(ctx context.Context, verdict movements.Verdict, ref movements.HitRef) error {
	if g.svc == nil {
		return nil
	}
	return g.svc.Note(ctx, verdict, ref)
}
