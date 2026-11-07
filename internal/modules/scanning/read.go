package scanning

import (
	"context"
)

// Tokens is the desk's list of badges on one pass, newest first.
func (s *Service) Tokens(ctx context.Context, tenantID, passID int64) ([]Token, error) {
	return s.repo.ForPass(ctx, tenantID, passID)
}

// Attempts is the raw scan log, filtered to the gate, pass or answer the supervisor asked about.
func (s *Service) Attempts(ctx context.Context, filter AttemptFilter) ([]Attempt, error) {
	return s.repo.Attempts(ctx, filter)
}

func (s *Service) Attempt(ctx context.Context, tenantID, id int64) (Attempt, error) {
	return s.repo.Attempt(ctx, tenantID, id)
}
