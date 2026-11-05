package visitor

import (
	"context"

	"accessnow/internal/core/passes"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/tenancy"
)

// Decide answers a pending visit. Core owns the pass; the visit mirrors it afterwards, so
// a crash between the two leaves a decided pass and a pending visit the desk can settle
// again, never an approved visit with a rejected badge.
func (s *Service) Decide(ctx context.Context, scope tenancy.Scope, visitID int64, approve bool, note string) (Visit, error) {
	visit, err := s.repo.VisitByID(ctx, scope.TenantID, visitID)
	if err != nil {
		return Visit{}, err
	}
	if visit.Status != string(StatusPending) {
		return Visit{}, errs.Conflict("visit " + visit.PassNo + " is already " + visit.Status)
	}

	to, mirror, response := passes.StatusApproved, StatusApproved, "approved"
	if !approve {
		to, mirror, response = passes.StatusRejected, StatusRejected, "rejected"
	}
	if _, err := s.passes.Decide(ctx, scope, visit.PassID, to, note); err != nil {
		return Visit{}, err
	}

	if err := s.repo.MarkStatus(ctx, scope.TenantID, visitID, mirror); err != nil {
		return Visit{}, err
	}
	if err := s.repo.MarkHostResponse(ctx, scope.TenantID, visitID, response); err != nil {
		return Visit{}, err
	}
	return s.repo.VisitByID(ctx, scope.TenantID, visitID)
}

// Cancel withdraws a visit before anybody walks in. A pass that is already on site is
// closed at the gate, not here.
func (s *Service) Cancel(ctx context.Context, scope tenancy.Scope, visitID int64, reason string) (Visit, error) {
	visit, err := s.repo.VisitByID(ctx, scope.TenantID, visitID)
	if err != nil {
		return Visit{}, err
	}
	if visit.Status == string(StatusOnSite) {
		return Visit{}, errs.Conflict("visit is on site, close it at the gate")
	}
	if !visit.Open() {
		return Visit{}, errs.Conflict("visit is already " + visit.Status)
	}
	if _, err := s.passes.Decide(ctx, scope, visit.PassID, passes.StatusCancelled, reason); err != nil {
		return Visit{}, err
	}
	if err := s.repo.MarkStatus(ctx, scope.TenantID, visitID, StatusCancelled); err != nil {
		return Visit{}, err
	}
	return s.repo.VisitByID(ctx, scope.TenantID, visitID)
}
