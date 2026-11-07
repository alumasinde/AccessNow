package contractor

import (
	"context"
	"time"
)

// Get is one register row, with the rota attached: a pass that shows its windows is a pass a
// supervisor can reason about without a second call.
func (s *Service) Get(ctx context.Context, tenantID, id int64) (Pass, error) {
	filed, err := s.repo.PassByID(ctx, tenantID, id)
	if err != nil {
		return Pass{}, err
	}
	windows, err := s.repo.Windows(ctx, tenantID, id)
	if err != nil {
		return Pass{}, err
	}
	filed.Windows = windows
	return filed, nil
}

func (s *Service) List(ctx context.Context, filter PassFilter) ([]Pass, error) {
	if filter.Limit <= 0 {
		filter.Limit = defaultPage
	}
	return s.repo.Passes(ctx, filter)
}

func (s *Service) Passes(ctx context.Context, tenantID, contractorID int64, limit int) ([]Pass, error) {
	if limit <= 0 {
		limit = defaultPage
	}
	return s.repo.Passes(ctx, PassFilter{TenantID: tenantID, ContractorID: contractorID, Limit: limit})
}

func (s *Service) Organization(ctx context.Context, tenantID, id int64) (Organization, error) {
	return s.repo.OrganizationByID(ctx, tenantID, id)
}

func (s *Service) Organizations(ctx context.Context, filter OrganizationFilter) ([]Organization, error) {
	if filter.Limit <= 0 {
		filter.Limit = defaultPage
	}
	return s.repo.Organizations(ctx, filter)
}

func (s *Service) Contractor(ctx context.Context, tenantID, id int64) (Contractor, error) {
	return s.repo.ContractorByID(ctx, tenantID, id)
}

func (s *Service) Contractors(ctx context.Context, filter ContractorFilter) ([]Contractor, error) {
	if filter.Limit <= 0 {
		filter.Limit = defaultPage
	}
	return s.repo.Contractors(ctx, filter)
}

func (s *Service) Documents(ctx context.Context, tenantID, contractorID int64) ([]Document, error) {
	if _, err := s.repo.ContractorByID(ctx, tenantID, contractorID); err != nil {
		return nil, err
	}
	return s.repo.Documents(ctx, tenantID, contractorID, "")
}

func (s *Service) DocumentTypes(ctx context.Context) ([]DocumentType, error) {
	return s.repo.DocumentTypes(ctx)
}

// Expiring is the desk's "what needs chasing" list, counted from today on the site's clock.
func (s *Service) Expiring(ctx context.Context, tenantID int64, timezone string, withinDays, limit int) ([]Document, error) {
	if withinDays <= 0 {
		withinDays = 30
	}
	if withinDays > expiryHorizon {
		withinDays = expiryHorizon
	}
	if limit <= 0 {
		limit = defaultPage
	}
	today := LocalDay(time.Now().UTC(), timezone)
	return s.repo.ExpiringDocuments(ctx, tenantID, today, today.AddDate(0, 0, withinDays), limit)
}

// expiryHorizon is how far ahead the desk may look. Two years is longer than any document the
// catalogue seeds, and a bound keeps a hand-typed ?within_days=100000 from scanning the table.
const expiryHorizon = 730
