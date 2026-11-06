package material

import (
	"context"

	"accessnow/internal/platform/audit"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/tenancy"
)

// Escalation counts what the sweep moved. The lines are counted separately from the passes
// because a supervisor chases a line, and the number on the dashboard is the number of
// things owed.
type Escalation struct {
	Overdue int `json:"overdue"`
	Lines   int `json:"lines"`
}

// Escalate is the sweep: a returnable load past the date the desk wrote is overdue, and the
// lines it is made of are owed individually. Nothing in this system runs a clock of its own,
// so this is the endpoint a site's cron calls and the button a supervisor presses before the
// returns report - and both are safe to repeat, because every pass is a compare-and-set on
// the status the sweep read, and a pass another run already moved is counted by nobody.
func (s *Service) Escalate(ctx context.Context, scope tenancy.Scope) (Escalation, error) {
	overdue, err := s.repo.DueOverdue(ctx, scope.TenantID)
	if err != nil {
		return Escalation{}, err
	}
	out := Escalation{}
	for _, m := range overdue {
		var took bool
		var lines int
		err := db.Transact(ctx, s.pool, func(c db.Conn) error {
			repo := s.repo.In(c)
			ok, err := repo.MarkOverdue(ctx, scope.TenantID, m.ID, Status(m.Status))
			if err != nil || !ok {
				return err
			}
			// The lines are stamped in the same transaction as the status, so a report can
			// never count a line as owed on a pass that is not itself marked that way.
			n, err := repo.MarkLinesOverdue(ctx, scope.TenantID, m.ID)
			if err != nil {
				return err
			}
			if err := s.announce(ctx, c, scope, m, "material.overdue", nil); err != nil {
				return err
			}
			took, lines = true, n
			return nil
		})
		if err != nil {
			return out, err
		}
		if took {
			out.Overdue++
			out.Lines += lines
		}
	}
	return out, nil
}

// Dispute is the desk writing down that the record of what came back is contested. It is the
// only writer of the disputed status, and the reason is mandatory for the same reason it is
// on a revoke: a dispute nobody can explain is an accusation, not a record.
//
// Only a declaration that has actually crossed a barrier can be disputed. A pending one is
// answered rather than argued with, and a rejected one never went anywhere.
func (s *Service) Dispute(ctx context.Context, scope tenancy.Scope, materialID int64,
	reason string) (Material, error) {
	m, err := s.repo.MaterialByID(ctx, scope.TenantID, materialID)
	if err != nil {
		return Material{}, err
	}
	if !disputable(m.Status) {
		return Material{}, errs.Conflict("goods pass " + m.PassNo + " is " + m.Status +
			", and nothing has crossed a barrier to dispute")
	}

	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		// The guard is the status this read found, so a return scanned while the desk was
		// typing is reported to the clerk as a conflict instead of being argued with.
		ok, err := repo.MarkDisputed(ctx, scope.TenantID, materialID, Status(m.Status))
		if err != nil || !ok {
			return err
		}
		if err := s.announce(ctx, c, scope, m, "material.disputed",
			map[string]any{"reason": reason}); err != nil {
			return err
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "material.disputed",
			SubjectType: "material_pass", SubjectID: m.ID, PassID: m.PassID, SiteID: scope.SiteID,
			Reason: reason,
			Before: map[string]any{"status": m.Status},
			After:  map[string]any{"status": string(StatusDisputed)},
		})
	})
	if err != nil {
		return Material{}, err
	}
	return s.repo.MaterialByID(ctx, scope.TenantID, materialID)
}

// disputable is the argument a goods record can be wrong about: the load has crossed a
// barrier. Returned and closed count, because the commonest dispute is that the count on
// the way back in was wrong.
func disputable(status string) bool {
	switch Status(status) {
	case StatusReleased, StatusPartiallyReturned, StatusOverdue, StatusReturned, StatusClosed:
		return true
	default:
		return false
	}
}

// announce writes one of this module's own events on the caller's transaction, for the two
// paths that have no gate movement behind them. The payload is the shape the gate reaction
// already publishes, so one template serves both.
func (s *Service) announce(ctx context.Context, c db.Conn, scope tenancy.Scope, m Material,
	key string, extra map[string]any) error {
	payload := map[string]any{
		"pass_no": m.PassNo, "kind": m.Kind, "return_type": m.ReturnType,
		"declared_by": deref(m.DeclaredByName), "lines": m.LineCount,
	}
	for name, value := range extra {
		payload[name] = value
	}
	return s.publisher.Publish(ctx, c, event(scope, key, m.ID, m.PassID, payload))
}
