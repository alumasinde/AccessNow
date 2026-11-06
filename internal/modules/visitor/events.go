package visitor

import (
	"context"

	"accessnow/internal/platform/db"
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
	if e.PassID == 0 || e.AggregateID == 0 || !e.Bool("opened") {
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
		return s.arrived(ctx, e, visit)
	case "out":
		return s.left(ctx, e, visit)
	default:
		return nil
	}
}

// arrived stamps the visit on site and says so. The two happen in one transaction so a
// host is never told a guest arrived on a row that did not move, and the notice is published
// only when this delivery is the one that moved it - the reaction is delivered at least once.
func (s *Service) arrived(ctx context.Context, e events.Event, visit Visit) error {
	return db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		moved, err := repo.MarkCheckIn(ctx, e.TenantID, visit.ID, e.GateID, e.AggregateID)
		if err != nil || !moved {
			return err
		}
		if err := repo.MarkArrival(ctx, e.TenantID, visit.VisitorID); err != nil {
			return err
		}
		return s.publish(ctx, c, e, visit, "visit.checked_in", map[string]any{
			"status": string(StatusOnSite), "gate_id": e.GateID, "movement_id": e.AggregateID,
		})
	})
}

// left is the same pair on the way out. The pass itself is Core's row to close and the
// movement already did that; this is the visit's own record of the departure and the
// announcement that goes with it.
func (s *Service) left(ctx context.Context, e events.Event, visit Visit) error {
	return db.Transact(ctx, s.pool, func(c db.Conn) error {
		moved, err := s.repo.In(c).MarkCheckOut(ctx, e.TenantID, visit.ID, e.GateID, e.AggregateID)
		if err != nil || !moved {
			return err
		}
		return s.publish(ctx, c, e, visit, "visit.checked_out", map[string]any{
			"status": string(StatusCompleted), "gate_id": e.GateID, "movement_id": e.AggregateID,
		})
	})
}

// publish writes one of the visit module's own events on the reaction's transaction. The
// payload names only what the gate knows, because the notice is rendered from the pass row
// and an invented field is a blank in somebody's email.
func (s *Service) publish(ctx context.Context, c db.Conn, e events.Event, visit Visit,
	key string, extra map[string]any) error {
	payload := map[string]any{
		"visitor": visit.VisitorName(), "pass_no": visit.PassNo, "purpose": visit.Purpose,
		"host_user_id": visit.HostUserID,
	}
	for name, value := range extra {
		payload[name] = value
	}
	return s.publisher.Publish(ctx, c, events.Event{
		TenantID: e.TenantID, EventKey: key, AggregateType: "visit", AggregateID: visit.ID,
		PassID: visit.PassID, SiteID: e.SiteID, GateID: e.GateID, ActorUserID: e.ActorUserID,
		Payload: payload,
	})
}
