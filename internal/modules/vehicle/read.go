package vehicle

import "context"

// GetPass is the journey card. The repository already joins the Core row, so the desk does
// not have to ask twice for the pass number behind it.
func (s *Service) GetPass(ctx context.Context, tenantID, id int64) (Pass, error) {
	return s.repo.PassByID(ctx, tenantID, id)
}

func (s *Service) GetVehicle(ctx context.Context, tenantID, id int64) (Vehicle, error) {
	return s.repo.VehicleByID(ctx, tenantID, id)
}

func (s *Service) ListPasses(ctx context.Context, filter ListPassFilter) ([]Pass, error) {
	if filter.Limit <= 0 {
		filter.Limit = defaultPage
	}
	return s.repo.ListPasses(ctx, filter)
}

func (s *Service) ListVehicles(ctx context.Context, filter ListVehicleFilter) ([]Vehicle, error) {
	if filter.Limit <= 0 {
		filter.Limit = defaultPage
	}
	return s.repo.ListVehicles(ctx, filter)
}

// OnSite is the roll-call roster. The view does the dwell arithmetic and the overstay
// comparison, so nothing here computes a second answer that could disagree with it.
func (s *Service) OnSite(ctx context.Context, tenantID int64, limit int) ([]OnSiteRow, error) {
	if limit <= 0 {
		limit = defaultPage
	}
	return s.repo.OnSite(ctx, tenantID, limit)
}
