package visitor

import (
	"context"

	"accessnow/internal/platform/tenancy"
)

// CloseOut is what a visit becomes when nobody came. It is the only writer of no_show and
// expired, and both of those words exist in the schema for exactly this.
//
// A site hangs a cron job on it and a supervisor can press a button on it: the same code,
// and both are safe to run twice, because every row is a compare-and-set on the status the
// sweep read, so a row another run already closed moves nothing and announces nothing.
type CloseOut struct {
	NoShow  int `json:"no_show"`
	Expired int `json:"expired"`
}

// The no-show pass runs first and the expired pass reads afterwards, so a visit that is
// past its window and was never answered closes as a no_show - the more useful of the two
// words, because the host did say yes and is the one waiting to be told.
func (s *Service) CloseOut(ctx context.Context, scope tenancy.Scope) (CloseOut, error) {
	missed, err := s.repo.DueForNoShow(ctx, scope.TenantID)
	if err != nil {
		return CloseOut{}, err
	}
	noShow, err := s.close(ctx, scope, missed, StatusNoShow, "visit.no_show")
	if err != nil {
		return CloseOut{NoShow: noShow}, err
	}

	stale, err := s.repo.DueForExpired(ctx, scope.TenantID)
	if err != nil {
		return CloseOut{NoShow: noShow}, err
	}
	expired, err := s.close(ctx, scope, stale, StatusExpired, "visit.expired")
	return CloseOut{NoShow: noShow, Expired: expired}, err
}

// close walks the rows one at a time, each with its own notice: a sweep that moved two
// hundred visits and published nothing would tell two hundred hosts nothing about the guest
// who never arrived. The guard is the status the row had when it was read rather than a
// constant, because a pending ask and an approval both expire.
func (s *Service) close(ctx context.Context, scope tenancy.Scope, rows []Visit,
	to Status, key string) (int, error) {
	moved := 0
	for _, visit := range rows {
		ok, err := s.repo.CloseVisit(ctx, scope.TenantID, visit.ID, Status(visit.Status), to)
		if err != nil {
			return moved, err
		}
		if !ok {
			continue
		}
		payload := map[string]any{
			"visitor": visit.VisitorName(), "pass_no": visit.PassNo, "purpose": visit.Purpose,
			"status": string(to), "host_user_id": visit.HostUserID,
		}
		if err := s.publisher.Publish(ctx, s.pool,
			event(scope, key, visit.ID, visit.PassID, payload)); err != nil {
			return moved, err
		}
		moved++
	}
	return moved, nil
}
