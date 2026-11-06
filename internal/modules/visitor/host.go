package visitor

import (
	"context"

	"accessnow/internal/platform/errs"
)

// hostSnapshot is the name kept on the visit row, so the record still says who was
// invited after that person leaves the directory.
type hostSnapshot struct {
	ID   *int64
	Name string
}

func (s *Service) host(ctx context.Context, tenantID, userID int64) (hostSnapshot, error) {
	if userID == 0 {
		return hostSnapshot{}, nil
	}
	user, err := s.users.UserByID(ctx, userID)
	if err != nil {
		return hostSnapshot{}, err
	}
	// host_user_id arrives from the request, and auth's lookup is by primary key alone.
	// Somebody else's tenant id is not a host: it is a bad request, and the name of a person
	// in another company has no business being snapshotted onto this one's visit row.
	if user.TenantID != tenantID {
		return hostSnapshot{}, errs.NotFound("host")
	}
	return hostSnapshot{ID: num(userID), Name: user.FullName()}, nil
}
