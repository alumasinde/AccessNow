package visitor

import (
	"context"

	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/events"
)

// OnMovement is the gate's reaction to a scan. The barrier, not the desk, is what says a
// visitor arrived, so the visit row follows the movement instead of competing with it:
// one writer, and a guard-only visit can no longer end with the desk and the roster
// disagreeing.
//
// internal/server registers this against "movement.recorded". A pass that is not a visit
// is a NotFound, which means the event belongs to another module and this one stays out
// of it.
func (s *Service) OnMovement(ctx context.Context, e events.Event) error {
	if e.PassID == 0 || e.AggregateID == 0 || e.Str("result") != "granted" {
		return nil
	}
	visit, err := s.repo.VisitByPass(ctx, e.TenantID, e.PassID)
	if err != nil {
		if errs.CodeOf(err) == errs.CodeNotFound {
			return nil
		}
		return err
	}

	switch e.Str("direction") {
	case "in":
		return s.repo.MarkCheckIn(ctx, e.TenantID, visit.ID, e.GateID, e.AggregateID)
	case "out":
		return s.repo.MarkCheckOut(ctx, e.TenantID, visit.ID, e.GateID, e.AggregateID)
	default:
		return nil
	}
}
