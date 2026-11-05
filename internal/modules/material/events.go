package material

import (
	"context"

	"accessnow/internal/core/passes"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/events"
	"accessnow/internal/platform/tenancy"
)

// OnMovement is the gate's reaction to a goods scan: the barrier is what releases and what
// takes back, not the desk. internal/server registers it against "movement.recorded", so
// this module never imports Core's movements package.
//
// Each branch is keyed on the status the declaration is in, which makes the delivery
// safe to run twice: a replayed row finds nothing left to do.
func (s *Service) OnMovement(ctx context.Context, e events.Event) error {
	if e.PassID == 0 || e.AggregateID == 0 || e.Str("result") != "granted" {
		return nil
	}
	material, err := s.repo.MaterialByPass(ctx, e.TenantID, e.PassID)
	if err != nil {
		if errs.CodeOf(err) == errs.CodeNotFound {
			return nil
		}
		return err
	}

	direction := e.Str("direction")
	switch {
	case direction == "out" && material.Outward() && material.Status == string(StatusApproved):
		err = s.cross(ctx, e, material, true, "material.released")
	case direction == "in" && !material.Outward() && material.Status == string(StatusApproved):
		err = s.cross(ctx, e, material, false, "material.received")
	case direction == "in" && material.Outward() && isOut(material.Status):
		err = s.broughtBack(ctx, e, material)
	case direction == "out" && !material.Outward() && material.Status == string(StatusReleased):
		err = s.carriedOut(ctx, e, material)
	default:
		return nil
	}
	if err != nil {
		return err
	}

	// A pass is finished goods business once it has done its job: non-returnable outward
	// that left, inward that never comes back, and returnable that all came back.
	return s.finish(ctx, scopeOf(e), material.ID)
}

// cross moves an outward load out or an inward load in: the detail row gets its gate
// stamp, every declared line is counted as what actually crossed, and one event per line
// says which way it went.
func (s *Service) cross(ctx context.Context, e events.Event, m Material, outward bool, eventKey string) error {
	return s.transaction(ctx, func(c db.Conn) error {
		repo := s.repo.In(c)
		if err := repo.MarkReleased(ctx, e.TenantID, m.ID, e.GateID, StatusApproved); err != nil {
			return err
		}
		if err := repo.ReleaseLines(ctx, e.TenantID, m.ID, outward); err != nil {
			return err
		}
		name := "exit"
		if !outward {
			name = "entry"
		}
		if err := repo.LineEvents(ctx, e.TenantID, m.ID, name, outward,
			e.GateID, e.AggregateID, e.ActorUserID); err != nil {
			return err
		}
		return s.publish(ctx, c, e, m, eventKey)
	})
}

// broughtBack is the whole load coming back through the barrier at once.
func (s *Service) broughtBack(ctx context.Context, e events.Event, m Material) error {
	return s.transaction(ctx, func(c db.Conn) error {
		repo := s.repo.In(c)
		if err := repo.ReturnEverything(ctx, e.TenantID, m.ID, e.GateID, e.AggregateID, e.ActorUserID); err != nil {
			return err
		}
		return s.publish(ctx, c, e, m, "material.returned")
	})
}

// carriedOut is an inward declaration that has served its purpose: a personal item or a
// vehicle sample that came in with somebody and is now leaving with the same somebody.
// The guard who let it out is the verifier the compliance report asks for.
func (s *Service) carriedOut(ctx context.Context, e events.Event, m Material) error {
	return s.transaction(ctx, func(c db.Conn) error {
		repo := s.repo.In(c)
		if err := repo.MarkClosed(ctx, e.TenantID, m.ID, StatusClosed, e.ActorUserID); err != nil {
			return err
		}
		if err := repo.LineEvents(ctx, e.TenantID, m.ID, "verified", false,
			e.GateID, e.AggregateID, e.ActorUserID); err != nil {
			return err
		}
		return s.publish(ctx, c, e, m, "material.verified")
	})
}

func (s *Service) transaction(ctx context.Context, fn func(db.Conn) error) error {
	return db.Transact(ctx, s.pool, fn)
}

// finish closes the Core pass once the declaration is done with it. It re-reads the row
// because the gate reaction just changed it, and leaves anything still owed alone: a
// returnable load that has not come back, or a personal item that has yet to leave again,
// must stay a pass a barrier will honour.
func (s *Service) finish(ctx context.Context, scope tenancy.Scope, materialID int64) error {
	m, err := s.repo.MaterialByID(ctx, scope.TenantID, materialID)
	if err != nil {
		return err
	}
	if m.PassStatus == string(passes.StatusCompleted) || !m.Closed() {
		return nil
	}
	_, err = s.passes.Decide(ctx, scope, m.PassID, passes.StatusCompleted,
		m.Kind+" goods declaration closed")
	return err
}

func (s *Service) publish(ctx context.Context, c db.Conn, e events.Event, m Material, key string) error {
	payload := map[string]any{
		"pass_no": m.PassNo, "kind": m.Kind, "return_type": m.ReturnType,
		"declared_by": deref(m.DeclaredByName), "movement_id": e.AggregateID,
	}
	return s.publisher.Publish(ctx, c, events.Event{
		TenantID: e.TenantID, EventKey: key, AggregateType: "material_pass",
		AggregateID: m.ID, PassID: m.PassID, SiteID: e.SiteID, GateID: e.GateID,
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

func isOut(status string) bool {
	return status == string(StatusReleased) || status == string(StatusPartiallyReturned)
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
