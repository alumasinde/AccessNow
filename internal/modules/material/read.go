package material

import "context"

// Get returns the declaration and its lines: the desk shows both on one card.
func (s *Service) Get(ctx context.Context, tenantID, materialID int64) (Material, []Item, error) {
	material, err := s.repo.MaterialByID(ctx, tenantID, materialID)
	if err != nil {
		return Material{}, nil, err
	}
	items, err := s.repo.Items(ctx, tenantID, materialID)
	if err != nil {
		return material, nil, err
	}
	return material, items, nil
}

func (s *Service) List(ctx context.Context, filter ListFilter) ([]Material, error) {
	if filter.Limit <= 0 {
		filter.Limit = defaultPage
	}
	return s.repo.List(ctx, filter)
}

// Outstanding is the "what has not come back" list. The view already drops lines that are
// whole again, so this is a read and nothing is computed here.
func (s *Service) Outstanding(ctx context.Context, tenantID int64, limit int) ([]OutstandingRow, error) {
	if limit <= 0 {
		limit = defaultPage
	}
	return s.repo.Outstanding(ctx, tenantID, limit)
}
