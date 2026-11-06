package rules

import (
	"context"

	"accessnow/internal/core/movements"
)

// Guard is the object Core holds instead of an import: the barrier asks it what the tenant's
// rulebook says about a scan, and never learns which rules, conditions or counters are behind
// the answer.
//
// Like the screening guard it is built empty and bound afterwards, because the dependency runs
// the other way - this module reads Core's pass row, and Core needs the handle while it is
// still being assembled. The binding happens in the object graph, before a request is served.
type Guard struct{ svc *Service }

func NewGuard() *Guard { return &Guard{} }

// Bind hands the guard the service it is the front door for.
func (g *Guard) Bind(svc *Service) { g.svc = svc }

// Decide answers with an empty Decision whenever the module is off or unbound, which is the
// case that matters most: a tenant that has not written any rules down gets every gate working
// exactly as it did the day before.
func (g *Guard) Decide(ctx context.Context, q movements.PolicyQuery) (movements.Decision, error) {
	if g.svc == nil {
		return movements.Decision{}, nil
	}
	return g.svc.Decide(ctx, q)
}

// Note keeps the rulebook's own record of what it did. Core logs the error rather than showing
// it: the gate has already answered.
func (g *Guard) Note(ctx context.Context, q movements.PolicyQuery, d movements.Decision,
	ref movements.HitRef) error {
	if g.svc == nil {
		return nil
	}
	return g.svc.Note(ctx, q, d, ref)
}
