package staff

import (
	"context"
	"time"

	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/events"
	"accessnow/internal/platform/tenancy"
)

// OnMovement is the gate's reaction to a staff scan. An employee steps out through a
// barrier and comes back through one, and only the barrier may say either happened; the
// register follows the movement instead of competing with it.
//
// Every branch is a compare-and-set on the status this read found, which is what makes an
// at-least-once delivery safe: a replayed scan moves nothing and publishes nothing.
func (s *Service) OnMovement(ctx context.Context, e events.Event) error {
	if e.PassID == 0 || e.AggregateID == 0 || !e.Bool("opened") {
		return nil
	}
	filed, err := s.repo.ByPass(ctx, e.TenantID, e.PassID)
	if err != nil {
		if errs.CodeOf(err) == errs.CodeNotFound {
			return nil
		}
		return err
	}

	switch e.Str("direction") {
	case "in":
		return s.returned(ctx, e, filed)
	case "out":
		return s.departed(ctx, e, filed)
	default:
		return nil
	}
}

// departed starts the clock. Only an approved request can leave: a pending one never
// reached the barrier at all, and a returned or cancelled row is history - which is what
// stops one outpass being used to step out twice.
func (s *Service) departed(ctx context.Context, e events.Event, filed Outpass) error {
	if Status(filed.Status) != StatusApproved {
		return nil
	}
	return db.Transact(ctx, s.pool, func(c db.Conn) error {
		moved, err := s.repo.In(c).MarkOut(ctx, e.TenantID, filed.ID, e.GateID, e.AggregateID)
		if err != nil || !moved {
			return err
		}
		return s.publish(ctx, c, scopeOf(e), filed, "outpass.departed", map[string]any{
			"gate_id": e.GateID, "movement_id": e.AggregateID,
		})
	})
}

// returned stops the clock, measures how long the employee was actually gone and closes the
// pass behind it. The Core pass is still inside its window and still has an entry left, so
// without the close a returned outpass would open the barrier a second time.
func (s *Service) returned(ctx context.Context, e events.Event, filed Outpass) error {
	if !filed.Open() {
		return nil
	}
	now := time.Now().UTC()
	minutes := derefMinutes(filed.MinutesOut(now))
	over := filed.PastExpected(now)
	// A row the sweep had not reached yet is escalated by the return that finally closed it,
	// so a late arrival always leaves both a duration and an overdue notice behind.
	notYetSwept := over && filed.Status == string(StatusOut)

	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		moved, err := s.repo.In(c).MarkReturned(ctx, e.TenantID, filed.ID, e.GateID,
			e.AggregateID, minutes, over)
		if err != nil || !moved {
			return err
		}
		if err := s.publish(ctx, c, scopeOf(e), filed, "outpass.returned", map[string]any{
			"minutes_out": minutes, "over_duration": over, "by": "gate",
			"gate_id": e.GateID, "movement_id": e.AggregateID,
		}); err != nil {
			return err
		}
		if !notYetSwept {
			return nil
		}
		return s.publish(ctx, c, scopeOf(e), filed, "outpass.overdue", map[string]any{
			"minutes_out": derefMinutes(filed.MinutesLate(now)), "closed_on_return": true,
		})
	})
	if err != nil {
		return err
	}
	return s.finish(ctx, scopeOf(e), filed.PassID, "employee returned")
}

// publish writes the module's own event on the reaction's transaction, so the register row
// and the notice about it commit together or neither does.
func (s *Service) publish(ctx context.Context, c db.Conn, scope tenancy.Scope, filed Outpass,
	key string, extra map[string]any) error {
	payload := map[string]any{
		"pass_no": filed.PassNo, "staff_id": filed.StaffID, "staff_name": filed.StaffName,
		"employee_no": filed.EmployeeNo, "reason_key": filed.ReasonKey,
		"destination": filed.Destination,
	}
	for name, value := range extra {
		payload[name] = value
	}
	return s.publisher.Publish(ctx, c, events.Event{
		TenantID: scope.TenantID, EventKey: key, AggregateType: aggregateType,
		AggregateID: filed.ID, PassID: filed.PassID, SiteID: scope.SiteID, GateID: scope.GateID,
		ActorUserID: scope.UserID, Payload: payload,
	})
}

// scopeOf rebuilds the request identity a Core decision wants from the outbox row: the
// guard who scanned is the actor, and the gate he stood at is the one on record.
func scopeOf(e events.Event) tenancy.Scope {
	return tenancy.Scope{
		TenantID: e.TenantID, UserID: e.ActorUserID, SiteID: e.SiteID, GateID: e.GateID,
	}
}
