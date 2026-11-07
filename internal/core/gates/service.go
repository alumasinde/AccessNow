package gates

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"

	"accessnow/internal/core/access"
	"accessnow/internal/core/auth"
	"accessnow/internal/platform/audit"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/security"
	"accessnow/internal/platform/tenancy"
)

type Service struct {
	pool   *sqlx.DB
	repo   *Repository
	users  *auth.Repository
	access *access.Service
	chain  audit.Writer
}

func NewService(pool *sqlx.DB, repo *Repository, users *auth.Repository, access *access.Service, chain audit.Writer) *Service {
	return &Service{pool: pool, repo: repo, users: users, access: access, chain: chain}
}

func (s *Service) List(ctx context.Context, scope tenancy.Scope) ([]Gate, error) {
	return s.repo.Assignable(ctx, scope.TenantID, scope.UserID)
}

func (s *Service) Current(ctx context.Context, scope tenancy.Scope) (*Shift, error) {
	return s.repo.CurrentShift(ctx, scope.UserID)
}

// Select is the only way the active gate changes. The gate id comes from the URL, but it
// is validated against gate_users on every call, so a guard cannot work a gate they are
// not assigned to and the browser never decides where a movement is logged.
func (s *Service) Select(ctx context.Context, scope tenancy.Scope, gateID int64) (Selection, error) {
	gate, err := s.repo.ByID(ctx, scope.TenantID, gateID)
	if err != nil {
		return Selection{}, err
	}
	if gate.Status != "active" {
		return Selection{}, errs.Conflict(gate.Name + " is " + gate.Status)
	}
	if !scope.IsSuperAdmin {
		assigned, err := s.repo.IsAssigned(ctx, scope.TenantID, gateID, scope.UserID)
		if err != nil {
			return Selection{}, err
		}
		if !assigned {
			return Selection{}, errs.GateUnassigned(gateID)
		}
	}

	previous, err := s.repo.CurrentShift(ctx, scope.UserID)
	if err != nil {
		return Selection{}, err
	}
	shiftID, err := s.openShift(ctx, scope, gate, previous)
	if err != nil {
		return Selection{}, err
	}
	return Selection{Gate: gate, ShiftID: shiftID}, nil
}

func (s *Service) openShift(ctx context.Context, scope tenancy.Scope, gate Gate, previous *Shift) (int64, error) {
	var shiftID int64
	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		id, err := repo.OpenShift(ctx, OpenShift{
			TenantID: scope.TenantID, SiteID: gate.SiteID, UserID: scope.UserID,
			GateID: gate.ID, DeviceID: int64Ptr(scope.DeviceID),
		})
		if err != nil {
			return err
		}
		shiftID = id
		if scope.SessionID > 0 {
			if err := s.users.In(c).SetActiveGate(ctx, scope.SessionID, gate.ID); err != nil {
				return err
			}
		}
		after := map[string]any{"gate_id": gate.ID, "gate_code": gate.Code, "shift_id": id}
		if previous != nil {
			after["previous_shift_id"] = previous.ID
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "gate.switched",
			SubjectType: "gate", SubjectID: gate.ID, SiteID: gate.SiteID, GateID: gate.ID,
			GateSessionID: id, After: after,
		})
	})
	return shiftID, err
}

func (s *Service) EndShift(ctx context.Context, scope tenancy.Scope, reason string) error {
	shift, err := s.repo.CurrentShift(ctx, scope.UserID)
	if err != nil {
		return err
	}
	if shift == nil {
		return errs.Conflict("no open gate shift to end")
	}
	return db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		if err := repo.CloseShift(ctx, shift.ID, reason); err != nil {
			return err
		}
		if scope.SessionID > 0 {
			if err := s.users.In(c).SetActiveGate(ctx, scope.SessionID, 0); err != nil {
				return err
			}
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "gate.shift_ended",
			SubjectType: "gate_session", SubjectID: shift.ID, GateID: shift.GateID,
			GateSessionID: shift.ID, Reason: reason,
		})
	})
}

// Device resolves the tablet token that decides which gate a request belongs to on the
// gate surface, so the guard never has to pick one correctly.
func (s *Service) Device(ctx context.Context, deviceToken string) (Device, error) {
	device, err := s.repo.DeviceByTokenHash(ctx, security.HashToken(deviceToken))
	if err != nil {
		return Device{}, err
	}
	if !device.Usable() {
		return Device{}, errs.Forbidden("device " + device.Label + " is not bound to an active gate")
	}
	return device, nil
}

// AttachDevice points the request scope at the device's gate and opens a shift there if
// the guard is not already standing at it. The assignment is re-checked here, not only when
// the gate was picked, so revoking a guard's gate stops the tablet in their hand.
func (s *Service) AttachDevice(ctx context.Context, scope tenancy.Scope, device Device) (tenancy.Scope, error) {
	gateID := *device.GateID
	if device.TenantID != scope.TenantID {
		return scope, errs.Forbidden("device belongs to another tenant")
	}
	gate, err := s.repo.ByID(ctx, scope.TenantID, gateID)
	if err != nil {
		return scope, err
	}
	if gate.Status != "active" {
		return scope, errs.Conflict(gate.Name + " is " + gate.Status)
	}
	if !scope.IsSuperAdmin {
		assigned, err := s.repo.IsAssigned(ctx, scope.TenantID, gateID, scope.UserID)
		if err != nil {
			return scope, err
		}
		if !assigned {
			return scope, errs.GateUnassigned(gateID)
		}
	}
	shift, err := s.repo.CurrentShift(ctx, scope.UserID)
	if err != nil {
		return scope, err
	}
	shiftID := int64(0)
	if shift == nil || shift.GateID != gateID || !shift.Open() {
		shiftID, err = s.openShift(ctx, scope, gate, shift)
		if err != nil {
			return scope, fmt.Errorf("attach device gate: %w", err)
		}
	} else {
		shiftID = shift.ID
	}
	scope.GateID = gateID
	scope.SiteID = device.SiteID
	scope.DeviceID = device.ID
	scope.GateShiftID = shiftID
	return scope, nil
}

// RevalidateActive refuses a session whose stored gate has been disabled or whose assignment
// has been revoked since it was picked. It is cheap - one gate row and, for a non-superadmin,
// one membership check - and it is the difference between a revoked guard and a revoked guard
// whose browser still works.
func (s *Service) RevalidateActive(ctx context.Context, scope tenancy.Scope) error {
	if scope.GateID == 0 {
		return nil
	}
	gate, err := s.repo.ByID(ctx, scope.TenantID, scope.GateID)
	if err != nil {
		if errs.CodeOf(err) == errs.CodeNotFound {
			return errs.Forbidden("your active gate is no longer available; choose a gate again")
		}
		return err
	}
	if gate.Status != "active" {
		return errs.Conflict(gate.Name + " is " + gate.Status + "; choose another gate")
	}
	if scope.IsSuperAdmin {
		return nil
	}
	assigned, err := s.repo.IsAssigned(ctx, scope.TenantID, gate.ID, scope.UserID)
	if err != nil {
		return err
	}
	if !assigned {
		return errs.GateUnassigned(gate.ID)
	}
	return nil
}

// CloseShiftForUser closes whatever shift the person has open, with the reason the session
// ended. Logout and timeout are already in the schema's vocabulary; this is what writes them.
func (s *Service) CloseShiftForUser(ctx context.Context, scope tenancy.Scope, reason string) error {
	if reason != "logout" && reason != "timeout" {
		reason = "logout"
	}
	shift, err := s.repo.CurrentShift(ctx, scope.UserID)
	if err != nil || shift == nil {
		return err
	}
	return db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		if err := repo.CloseShift(ctx, shift.ID, reason); err != nil {
			return err
		}
		if scope.SessionID > 0 {
			if err := s.users.In(c).SetActiveGate(ctx, scope.SessionID, 0); err != nil {
				return err
			}
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "gate.shift_ended",
			SubjectType: "gate_session", SubjectID: shift.ID, GateID: shift.GateID,
			GateSessionID: shift.ID, Reason: reason,
		})
	})
}

func int64Ptr(value int64) *int64 {
	if value == 0 {
		return nil
	}
	return &value
}
