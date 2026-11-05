package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
)

type Repository struct{ q db.Conn }

func NewRepository(q db.Conn) *Repository { return &Repository{q: q} }

func (r *Repository) In(c db.Conn) *Repository { bound := *r; bound.q = c; return &bound }

const userColumns = `id, tenant_id, first_name, last_name, email, username, password_hash,
                     status, is_super_admin, must_change_password, failed_attempts,
                     locked_until, preferred_timezone`

func (r *Repository) TenantBySlug(ctx context.Context, slug string) (Tenant, error) {
	const query = `SELECT id, name, timezone FROM tenants WHERE slug = ? AND status = 'active'`
	var t Tenant
	err := r.q.GetContext(ctx, &t, query, slug)
	if errors.Is(err, sql.ErrNoRows) {
		return Tenant{}, errs.NotFound("tenant " + slug)
	}
	if err != nil {
		return Tenant{}, fmt.Errorf("read tenant: %w", err)
	}
	return t, nil
}

// FindByIdentifier matches either half of the login form. A missing user and a mismatched
// password must produce the same response, so the caller never learns which one it was.
func (r *Repository) FindByIdentifier(ctx context.Context, tenantID int64, identifier string) (User, error) {
	const query = `SELECT ` + userColumns + `
	               FROM   users
	               WHERE  tenant_id = ? AND deleted_at IS NULL AND (email = ? OR username = ?)
	               LIMIT  1`
	var u User
	err := r.q.GetContext(ctx, &u, query, tenantID, identifier, identifier)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, errs.NotFound("user")
	}
	if err != nil {
		return User{}, fmt.Errorf("read user: %w", err)
	}
	return u, nil
}

func (r *Repository) FirstSite(ctx context.Context, tenantID int64) (*int64, error) {
	const query = `SELECT id FROM sites WHERE tenant_id = ? AND deleted_at IS NULL ORDER BY id LIMIT 1`
	var id int64
	err := r.q.GetContext(ctx, &id, query, tenantID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read default site: %w", err)
	}
	return &id, nil
}

func (r *Repository) UserByID(ctx context.Context, userID int64) (User, error) {
	const query = `SELECT ` + userColumns + ` FROM users WHERE id = ? AND deleted_at IS NULL`
	var u User
	err := r.q.GetContext(ctx, &u, query, userID)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, errs.NotFound("user")
	}
	if err != nil {
		return User{}, fmt.Errorf("read user: %w", err)
	}
	return u, nil
}

type NewSession struct {
	TenantID   int64
	UserID     int64
	TokenHash  string
	SiteID     *int64
	AuthMethod string
	IPAddress  string
	UserAgent  string
	ExpiresAt  time.Time
}

func (r *Repository) CreateSession(ctx context.Context, s NewSession) (int64, error) {
	const query = `
		INSERT INTO user_sessions
			(tenant_id, user_id, session_hash, active_site_id, auth_method, ip_address, user_agent,
			 expires_at, rotates_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	result, err := r.q.ExecContext(ctx, query,
		s.TenantID, s.UserID, s.TokenHash, s.SiteID, s.AuthMethod, s.IPAddress, s.UserAgent,
		s.ExpiresAt, s.ExpiresAt)
	if err != nil {
		return 0, fmt.Errorf("create session: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read session id: %w", err)
	}
	return id, nil
}

func (r *Repository) SessionByTokenHash(ctx context.Context, tokenHash string) (Session, error) {
	const query = `
		SELECT s.id, s.tenant_id, s.user_id, s.active_site_id, s.active_gate_id, s.device_id,
		       s.auth_method, t.timezone AS tenant_timezone, s.expires_at, s.created_at, s.revoked_at
		FROM   user_sessions s
		JOIN   tenants t ON t.id = s.tenant_id
		WHERE  s.session_hash = ?
		LIMIT  1`
	var s Session
	err := r.q.GetContext(ctx, &s, query, tokenHash)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, errs.Unauthenticated("session is not recognised")
	}
	if err != nil {
		return Session{}, fmt.Errorf("read session: %w", err)
	}
	return s, nil
}

func (r *Repository) RevokeSession(ctx context.Context, sessionID int64, reason string) error {
	const query = `
		UPDATE user_sessions
		SET    revoked_at = UTC_TIMESTAMP(6), revoke_reason = ?
		WHERE  id = ? AND revoked_at IS NULL`
	if _, err := r.q.ExecContext(ctx, query, reason, sessionID); err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

// SetActiveGate is the server side of "the gate stays picked": the browser never sends
// a gate id, it is read back out of the session on every request.
func (r *Repository) SetActiveGate(ctx context.Context, sessionID, gateID int64) error {
	const query = `UPDATE user_sessions SET active_gate_id = ? WHERE id = ?`
	if _, err := r.q.ExecContext(ctx, query, gateID, sessionID); err != nil {
		return fmt.Errorf("set active gate: %w", err)
	}
	return nil
}

func (r *Repository) RecordLogin(ctx context.Context, userID int64) error {
	const query = `
		UPDATE users
		SET    failed_attempts = 0, locked_until = NULL,
		       last_login_at = UTC_TIMESTAMP(6), last_active_at = UTC_TIMESTAMP(6)
		WHERE  id = ?`
	if _, err := r.q.ExecContext(ctx, query, userID); err != nil {
		return fmt.Errorf("record login: %w", err)
	}
	return nil
}

func (r *Repository) RecordFailure(ctx context.Context, userID int64, lockedUntil *time.Time) error {
	const query = `
		UPDATE users
		SET    failed_attempts = failed_attempts + 1, locked_until = ?
		WHERE  id = ?`
	if _, err := r.q.ExecContext(ctx, query, lockedUntil, userID); err != nil {
		return fmt.Errorf("record failed login: %w", err)
	}
	return nil
}

func (r *Repository) MarkActive(ctx context.Context, userID int64) error {
	const query = `UPDATE users SET last_active_at = UTC_TIMESTAMP(6) WHERE id = ?`
	if _, err := r.q.ExecContext(ctx, query, userID); err != nil {
		return fmt.Errorf("mark active: %w", err)
	}
	return nil
}
