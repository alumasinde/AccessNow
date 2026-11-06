package vehicle

import (
	"context"
	"strconv"
	"time"

	"accessnow/internal/core/passes"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/events"
	"accessnow/internal/platform/tenancy"
)

// OnMovement is the gate's reaction to a vehicle scan. The barrier is what puts a plate on
// the property and what takes it off, so the journey row follows the movement instead of
// competing with it - the same division of work the visitor and goods modules use, and the
// reason internal/server can register this against "movement.recorded" without this module
// importing the movements package.
//
// Every branch is a compare-and-set on the status this read found, which is what makes an
// at-least-once delivery safe: a replayed row moves nothing and publishes nothing.
func (s *Service) OnMovement(ctx context.Context, e events.Event) error {
	if e.PassID == 0 || e.AggregateID == 0 || e.Str("result") != "granted" {
		return nil
	}
	journey, err := s.repo.PassByPass(ctx, e.TenantID, e.PassID)
	if err != nil {
		if errs.CodeOf(err) == errs.CodeNotFound {
			return nil
		}
		return err
	}

	switch e.Str("direction") {
	case "in":
		return s.crossedIn(ctx, e, journey)
	case "out":
		if err := s.crossedOut(ctx, e, journey); err != nil {
			return err
		}
		return s.closeWhenUsed(ctx, scopeOf(e), journey.PassID)
	default:
		return nil
	}
}

// crossedIn starts a journey. It accepts a completed pass as well as an approved one: a
// fleet vehicle with entries still in its allowance comes back on the same pass, and the
// movements table keeps each crossing while this row tracks the one it is on now.
func (s *Service) crossedIn(ctx context.Context, e events.Event, p Pass) error {
	from := []Status{StatusApproved, StatusCompleted}
	if !holds(from, p.Status) {
		return nil
	}
	return db.Transact(ctx, s.pool, func(c db.Conn) error {
		moved, err := s.repo.In(c).MarkEntered(ctx, e.TenantID, p.ID, e.GateID, from)
		if err != nil || !moved {
			return err
		}
		return s.publish(ctx, c, e, p, "vehicle.entered", map[string]any{"gate_id": e.GateID})
	})
}

// crossedOut ends the journey and measures the dwell the vehicle actually used. The
// movement's own dwell is Core's answer for the pass; this one belongs to the plate, which
// is the number the site cares about when it wants its yard back.
func (s *Service) crossedOut(ctx context.Context, e events.Event, p Pass) error {
	if !p.OnSite() {
		return nil
	}
	now := time.Now().UTC()
	overstayed := p.PastDwell(now)
	dwell := derefMinutes(p.MinutesOnSite(now))

	return db.Transact(ctx, s.pool, func(c db.Conn) error {
		moved, err := s.repo.In(c).MarkExited(ctx, e.TenantID, p.ID, e.GateID, dwell, overstayed)
		if err != nil || !moved {
			return err
		}
		if err := s.publish(ctx, c, e, p, "vehicle.exited", map[string]any{
			"gate_id": e.GateID, "dwell_minutes": dwell,
		}); err != nil {
			return err
		}
		if !overstayed {
			return nil
		}
		// Same key the on-site sweep would raise, so whoever subscribes to overstays hears
		// it however the vehicle was found: still parked, or parked and then driven out.
		return s.publish(ctx, c, e, p, "vehicle.overstaying", map[string]any{"dwell_minutes": dwell})
	})
}

// closeWhenUsed finishes the Core pass once the tenant's entry allowance is spent. Core
// leaves a pass that has exited as active - used, still inside its window - and only the
// module that knows the kind of journey can say whether another entry is still owed. A
// delivery with one entry gets closed here, which is what turns its second scan red.
func (s *Service) closeWhenUsed(ctx context.Context, scope tenancy.Scope, passID int64) error {
	pass, err := s.passRepo.ByID(ctx, scope.TenantID, passID)
	if err != nil {
		return err
	}
	passType, err := s.passRepo.TypeByID(ctx, pass.PassTypeID)
	if err != nil {
		return err
	}
	if passType.DefaultMaxEntries == nil || pass.EntryCount < *passType.DefaultMaxEntries {
		return nil
	}
	if pass.Status == string(passes.StatusCompleted) {
		return nil
	}
	_, err = s.passes.Decide(ctx, scope, pass.ID, passes.StatusCompleted,
		"vehicle pass used up its "+strconv.Itoa(*passType.DefaultMaxEntries)+" entries")
	return err
}

// publish writes the module's own event on the reaction's transaction, so a journey row
// and the notice about it commit together or neither does.
func (s *Service) publish(ctx context.Context, c db.Conn, e events.Event, p Pass, key string, extra map[string]any) error {
	payload := map[string]any{
		"pass_no": p.PassNo, "plate": p.PlateNormalized, "driver": deref(p.DriverName),
		"movement_id": e.AggregateID,
	}
	for name, value := range extra {
		payload[name] = value
	}
	return s.publisher.Publish(ctx, c, events.Event{
		TenantID: e.TenantID, EventKey: key, AggregateType: "vehicle_pass",
		AggregateID: p.ID, PassID: p.PassID, SiteID: e.SiteID, GateID: e.GateID,
		ActorUserID: e.ActorUserID, Payload: payload,
	})
}

// scopeOf rebuilds the request identity a Core decision wants from the outbox row: the
// guard who scanned is the actor, and the gate he stood at is the one on record.
func scopeOf(e events.Event) tenancy.Scope {
	return tenancy.Scope{
		TenantID: e.TenantID, UserID: e.ActorUserID, SiteID: e.SiteID, GateID: e.GateID,
	}
}

func holds(from []Status, status string) bool {
	for _, candidate := range from {
		if string(candidate) == status {
			return true
		}
	}
	return false
}

func derefMinutes(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
