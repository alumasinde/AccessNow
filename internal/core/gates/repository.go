package gates

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
)

type Repository struct{ q db.Conn }

func NewRepository(q db.Conn) *Repository { return &Repository{q: q} }

func (r *Repository) In(c db.Conn) *Repository { bound := *r; bound.q = c; return &bound }

// gateColumns is qualified because Assignable joins gate_users, and an unqualified
// tenant_id or status there is ambiguous.
const gateColumns = `g.id, g.tenant_id, g.site_id, g.code, g.name, g.gate_type, g.direction, g.status,
                     g.is_default, g.offline_capable, g.same_gate_exit_required,
                     g.requires_reason_on_override`

func (r *Repository) Assignable(ctx context.Context, tenantID, userID int64) ([]Gate, error) {
	const query = `SELECT ` + gateColumns + `
	               FROM   gates g
	               JOIN   gate_users gu ON gu.gate_id = g.id
	               WHERE  gu.user_id = ? AND g.tenant_id = ? AND g.deleted_at IS NULL
	                      AND g.status = 'active'
	               ORDER  BY g.sort_order, g.name`
	var gates []Gate
	if err := r.q.SelectContext(ctx, &gates, query, userID, tenantID); err != nil {
		return nil, fmt.Errorf("read assignable gates: %w", err)
	}
	return gates, nil
}

func (r *Repository) ByID(ctx context.Context, tenantID, gateID int64) (Gate, error) {
	const query = `SELECT ` + gateColumns + ` FROM gates g WHERE g.id = ? AND g.tenant_id = ? AND g.deleted_at IS NULL`
	var g Gate
	err := r.q.GetContext(ctx, &g, query, gateID, tenantID)
	if errors.Is(err, sql.ErrNoRows) {
		return Gate{}, errs.NotFound("gate")
	}
	if err != nil {
		return Gate{}, fmt.Errorf("read gate: %w", err)
	}
	return g, nil
}

func (r *Repository) IsAssigned(ctx context.Context, tenantID, gateID, userID int64) (bool, error) {
	const query = `SELECT COUNT(*) > 0 FROM gate_users WHERE gate_id = ? AND user_id = ? AND tenant_id = ?`
	var assigned bool
	if err := r.q.GetContext(ctx, &assigned, query, gateID, userID, tenantID); err != nil {
		return false, fmt.Errorf("read gate assignment: %w", err)
	}
	return assigned, nil
}

func (r *Repository) Categories(ctx context.Context, gateID int64) ([]string, error) {
	const query = `SELECT category_key FROM gate_categories WHERE gate_id = ? ORDER BY category_key`
	var keys []string
	if err := r.q.SelectContext(ctx, &keys, query, gateID); err != nil {
		return nil, fmt.Errorf("read gate categories: %w", err)
	}
	return keys, nil
}

type OpenShift struct {
	TenantID int64
	SiteID   int64
	UserID   int64
	GateID   int64
	DeviceID *int64
	Reason   string
}

// OpenShift closes the guard's current shift and opens the new one in the same statement
// set: uk_gate_sessions_one_open only allows a single open row per user, so the two
// writes cannot be split across transactions.
func (r *Repository) OpenShift(ctx context.Context, in OpenShift) (int64, error) {
	const closePrevious = `
		UPDATE gate_sessions
		SET    closed_at = UTC_TIMESTAMP(6), close_reason = ?
		WHERE  user_id = ? AND closed_at IS NULL`
	reason := in.Reason
	if reason == "" {
		reason = "switched_gate"
	}
	if _, err := r.q.ExecContext(ctx, closePrevious, reason, in.UserID); err != nil {
		return 0, fmt.Errorf("close previous gate shift: %w", err)
	}

	const insert = `
		INSERT INTO gate_sessions (uuid, tenant_id, site_id, user_id, gate_id, device_id)
		VALUES (?, ?, ?, ?, ?, ?)`
	result, err := r.q.ExecContext(ctx, insert, uuid.NewString(),
		in.TenantID, in.SiteID, in.UserID, in.GateID, in.DeviceID)
	if err != nil {
		return 0, fmt.Errorf("open gate shift: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read gate shift id: %w", err)
	}
	return id, nil
}

func (r *Repository) CurrentShift(ctx context.Context, userID int64) (*Shift, error) {
	const query = `
		SELECT id, tenant_id, site_id, user_id, gate_id, device_id, opened_at, closed_at, close_reason
		FROM   gate_sessions
		WHERE  user_id = ? AND closed_at IS NULL
		LIMIT  1`
	var s Shift
	err := r.q.GetContext(ctx, &s, query, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read open gate shift: %w", err)
	}
	return &s, nil
}

func (r *Repository) CloseShift(ctx context.Context, shiftID int64, reason string) error {
	const query = `
		UPDATE gate_sessions
		SET    closed_at = UTC_TIMESTAMP(6), close_reason = ?
		WHERE  id = ? AND closed_at IS NULL`
	if _, err := r.q.ExecContext(ctx, query, reason, shiftID); err != nil {
		return fmt.Errorf("close gate shift: %w", err)
	}
	return nil
}

func (r *Repository) DeviceByTokenHash(ctx context.Context, tokenHash string) (Device, error) {
	const query = `
		SELECT id, tenant_id, site_id, gate_id, label, is_shared, is_active, revoked_at
		FROM   gate_devices
		WHERE  device_token_hash = ?
		LIMIT  1`
	var d Device
	err := r.q.GetContext(ctx, &d, query, tokenHash)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, errs.Unauthenticated("device is not enrolled")
	}
	if err != nil {
		return Device{}, fmt.Errorf("read device: %w", err)
	}
	return d, nil
}
