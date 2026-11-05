package visitor

import "context"

// Get returns the visit and the person it belongs to; the desk shows both in one card.
func (s *Service) Get(ctx context.Context, tenantID, visitID int64) (Visit, Visitor, error) {
	visit, err := s.repo.VisitByID(ctx, tenantID, visitID)
	if err != nil {
		return Visit{}, Visitor{}, err
	}
	person, err := s.repo.VisitorByID(ctx, tenantID, visit.VisitorID)
	if err != nil {
		return visit, Visitor{}, err
	}
	return visit, person, nil
}

func (s *Service) List(ctx context.Context, filter ListFilter) ([]Visit, error) {
	if filter.Limit <= 0 {
		filter.Limit = defaultPage
	}
	return s.repo.List(ctx, filter)
}
