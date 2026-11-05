package visitor

import (
	"context"

	"accessnow/internal/core/movements"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/tenancy"
)

// CheckIn logs the arrival through Core, so the gate rules run the same whether the desk
// or a tablet pressed the button. A red answer comes back as an outcome, not an error:
// the visit stays pending and the reason is the guard's sentence.
func (s *Service) CheckIn(ctx context.Context, scope tenancy.Scope, visitID int64) (Visit, movements.Outcome, error) {
	visit, err := s.repo.VisitByID(ctx, scope.TenantID, visitID)
	if err != nil {
		return visit, movements.Outcome{}, err
	}
	if visit.Status != string(StatusApproved) {
		return visit, movements.Outcome{}, errs.Conflict("visit is " + visit.Status + ", not approved")
	}

	outcome, err := s.scans.Scan(ctx, scope, movements.Scan{
		PassID: visit.PassID, Direction: movements.DirectionIn, Source: movements.SourceManual,
	})
	if err != nil {
		return visit, outcome, err
	}
	if !outcome.Green() {
		return visit, outcome, nil
	}

	// Scan drains the outbox before it answers, so OnMovement has already put this visit
	// on site. Reading the row back is the whole job: one writer keeps the desk, the
	// roster and the gate on the same side of the fence.
	updated, err := s.repo.VisitByID(ctx, scope.TenantID, visitID)
	return updated, outcome, err
}

// CheckOut closes the visit on the way out. The exit movement is the one that pairs the
// entry, so dwell time and the roll-call roster both fall out of Core.
func (s *Service) CheckOut(ctx context.Context, scope tenancy.Scope, visitID int64) (Visit, movements.Outcome, error) {
	visit, err := s.repo.VisitByID(ctx, scope.TenantID, visitID)
	if err != nil {
		return visit, movements.Outcome{}, err
	}
	if visit.Status != string(StatusOnSite) {
		return visit, movements.Outcome{}, errs.Conflict("visit is " + visit.Status + ", not on site")
	}

	outcome, err := s.scans.Scan(ctx, scope, movements.Scan{
		PassID: visit.PassID, Direction: movements.DirectionOut, Source: movements.SourceManual,
	})
	if err != nil {
		return visit, outcome, err
	}
	if !outcome.Green() {
		return visit, outcome, nil
	}

	// The relay inside Scan closed the visit as it recorded the exit.
	updated, err := s.repo.VisitByID(ctx, scope.TenantID, visitID)
	return updated, outcome, err
}
