package visitor

import "context"

// hostSnapshot is the name kept on the visit row, so the record still says who was
// invited after that person leaves the directory.
type hostSnapshot struct {
	ID   *int64
	Name string
}

func (s *Service) host(ctx context.Context, userID int64) (hostSnapshot, error) {
	if userID == 0 {
		return hostSnapshot{}, nil
	}
	user, err := s.users.UserByID(ctx, userID)
	if err != nil {
		return hostSnapshot{}, err
	}
	return hostSnapshot{ID: num(userID), Name: user.FullName()}, nil
}
