package staff

import "context"

// Get is one register row. The repository already joins the Core pass and the employee, so
// the desk does not have to ask twice for either.
func (s *Service) Get(ctx context.Context, tenantID, id int64) (Outpass, error) {
	return s.repo.ByID(ctx, tenantID, id)
}

func (s *Service) List(ctx context.Context, filter ListFilter) ([]Outpass, error) {
	if filter.Limit <= 0 {
		filter.Limit = defaultPage
	}
	return s.repo.List(ctx, filter)
}

// Outstanding is the roll-call: who is off the site, on what number, and who is past the
// time they gave.
func (s *Service) Outstanding(ctx context.Context, tenantID int64, limit int) ([]OutstandingRow, error) {
	if limit <= 0 {
		limit = defaultPage
	}
	return s.repo.Outstanding(ctx, tenantID, limit)
}

// Reasons is the catalogue behind the form's dropdown.
func (s *Service) Reasons(ctx context.Context) ([]Reason, error) {
	return s.repo.ListReasons(ctx)
}
