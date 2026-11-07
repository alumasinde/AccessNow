// Package auth is Core login: it resolves a tenant, checks a password and owns the
// server-side session that carries the active gate.
package auth

import "time"

type Tenant struct {
	ID       int64  `db:"id"`
	Name     string `db:"name"`
	Timezone string `db:"timezone"`
}

type Site struct {
	ID       int64  `db:"id"`
	TenantID int64  `db:"tenant_id"`
	Name     string `db:"name"`
	Timezone string `db:"timezone"`
}

type User struct {
	ID                 int64      `db:"id"`
	TenantID           int64      `db:"tenant_id"`
	FirstName          string     `db:"first_name"`
	LastName           string     `db:"last_name"`
	Email              *string    `db:"email"`
	Username           *string    `db:"username"`
	PasswordHash       *string    `db:"password_hash"`
	Status             string     `db:"status"`
	IsSuperAdmin       bool       `db:"is_super_admin"`
	MustChangePassword bool       `db:"must_change_password"`
	FailedAttempts     int        `db:"failed_attempts"`
	LockedUntil        *time.Time `db:"locked_until"`
	PreferredTimezone  *string    `db:"preferred_timezone"`
}

func (u User) FullName() string { return u.FirstName + " " + u.LastName }

func (u User) Loginable() bool {
	return u.Status == "active" && u.PasswordHash != nil && *u.PasswordHash != ""
}

// Session mirrors user_sessions. The raw token is never stored: only its SHA-256, which
// is what the row is looked up by.
type Session struct {
	ID             int64      `db:"id"`
	TenantID       int64      `db:"tenant_id"`
	UserID         int64      `db:"user_id"`
	ActiveSiteID   *int64     `db:"active_site_id"`
	ActiveGateID   *int64     `db:"active_gate_id"`
	DeviceID       *int64     `db:"device_id"`
	AuthMethod     string     `db:"auth_method"`
	TenantTimezone string     `db:"tenant_timezone"`
	ExpiresAt      time.Time  `db:"expires_at"`
	CreatedAt      time.Time  `db:"created_at"`
	RevokedAt      *time.Time `db:"revoked_at"`
}

func (s Session) Live(now time.Time) bool {
	return s.RevokedAt == nil && s.ExpiresAt.After(now)
}
