package approval

import (
	"context"
)

// Guard is the answer Core waits for before it lets a desk approve a pass: is a chain of
// approvers still deciding this one?
//
// It is built empty and bound afterwards because the dependency runs the other way - the
// approval service needs Core's pass service to move a pass at the end of a chain, and Core
// needs this to refuse an approval in the middle of one. The binding happens while the
// object graph is still being assembled, before a single request is served.
type Guard struct{ svc *Service }

func NewGuard() *Guard { return &Guard{} }

// Bind hands the guard the service it is the front door for.
func (g *Guard) Bind(svc *Service) { g.svc = svc }

// Holds is the whole contract, and it answers false in the one case that matters most: a
// tenant with this module switched off is not held by a queue it cannot see. Their desk
// approves passes exactly as it did the day before the module was enabled.
func (g *Guard) Holds(ctx context.Context, tenantID, passID int64) (bool, error) {
	if g.svc == nil {
		return false, nil
	}
	on, err := g.svc.enabled(ctx, tenantID)
	if err != nil || !on {
		return false, err
	}
	return g.svc.repo.Holds(ctx, tenantID, passID)
}
