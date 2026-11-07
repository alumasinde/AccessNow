package rollcall

import (
	"context"

	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/tenancy"
)

// Open is the incident a muster point is currently working from. A site with no alarm answers
// not-found, which is the answer the gate screen uses to draw nothing.
func (s *Service) Open(ctx context.Context, scope tenancy.Scope) (Incident, error) {
	if scope.SiteID == 0 {
		return Incident{}, errs.Forbidden("no site on this session")
	}
	return s.repo.OpenAt(ctx, scope.TenantID, scope.SiteID)
}

func (s *Service) Incident(ctx context.Context, tenantID, id int64) (Incident, error) {
	return s.repo.ByID(ctx, tenantID, id)
}

func (s *Service) Incidents(ctx context.Context, filter IncidentFilter) ([]Incident, error) {
	return s.repo.Incidents(ctx, filter)
}

func (s *Service) Roster(ctx context.Context, filter RosterFilter) ([]Person, error) {
	return s.repo.Roster(ctx, filter)
}

func (s *Service) Marks(ctx context.Context, tenantID, incidentID, rosterID int64,
	limit int) ([]Mark, error) {
	return s.repo.Marks(ctx, tenantID, incidentID, rosterID, limit)
}
