package scanning

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/httpx"
)

// Repository is every query this module makes. Everything is tenant-scoped: a token belongs to
// one tenant's pass, and a hash from another tenant's badge is simply a hash nobody has.
type Repository struct{ pool *sqlx.DB }

func NewRepository(pool *sqlx.DB) *Repository { return &Repository{pool: pool} }

const tokenColumns = `t.id, t.tenant_id, t.pass_id, t.token_hash, t.token_prefix, t.format,
	t.issued_at, t.expires_at, t.revoked_at, t.revoke_reason, t.max_uses, t.use_count,
	t.last_used_at, t.last_gate_id, t.is_single_use, t.issued_by_user_id, t.delivery_channel,
	p.pass_no, p.status AS pass_status, p.subject_name`

const tokenSource = ` FROM pass_tokens t
	JOIN passes p ON p.id = t.pass_id AND p.tenant_id = t.tenant_id`

const attemptColumns = `a.id, a.tenant_id, a.site_id, a.gate_id, a.device_id, a.gate_session_id,
	a.input_kind, a.token_hash, a.token_prefix, a.plate_read, a.outcome, a.response_code,
	a.response_message, a.matched_pass_id, a.matched_token_id, a.matched_movement_id,
	a.direction_requested, a.latency_ms, a.occurred_at, a.idempotency_key, a.created_at,
	COALESCE(g.name, '') AS gate_name, COALESCE(p.pass_no, '') AS pass_no, p.subject_name`

const attemptSource = ` FROM scan_attempts a
	LEFT JOIN gates g ON g.id = a.gate_id AND g.tenant_id = a.tenant_id
	LEFT JOIN passes p ON p.id = a.matched_pass_id AND p.tenant_id = a.tenant_id`

// NewToken is a token ready to be written. The hash is what is stored; the secret it came from
// is handed back to the caller and then forgotten here.
type NewToken struct {
	TenantID    int64
	PassID      int64
	TokenHash   string
	TokenPrefix string
	Format      string
	ExpiresAt   *time.Time
	MaxUses     *int
	SingleUse   bool
	IssuedBy    int64
	Delivery    string
}

func (r *Repository) Create(ctx context.Context, c db.Conn, in NewToken) (int64, error) {
	const query = `
		INSERT INTO pass_tokens
			(tenant_id, pass_id, token_hash, token_prefix, format, expires_at, max_uses,
			 is_single_use, issued_by_user_id, delivery_channel)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	result, err := c.ExecContext(ctx, query, in.TenantID, in.PassID, in.TokenHash,
		in.TokenPrefix, in.Format, in.ExpiresAt, in.MaxUses, in.SingleUse,
		nullID(in.IssuedBy), nullText(in.Delivery))
	if err != nil {
		return 0, fmt.Errorf("issue token for pass %d: %w", in.PassID, err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read token id: %w", err)
	}
	return id, nil
}

func (r *Repository) ByID(ctx context.Context, tenantID, id int64) (Token, error) {
	query := "SELECT " + tokenColumns + tokenSource + " WHERE t.tenant_id = ? AND t.id = ?"
	var token Token
	if err := r.pool.GetContext(ctx, &token, query, tenantID, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Token{}, errs.NotFound("token " + fmt.Sprint(id))
		}
		return Token{}, fmt.Errorf("read token %d: %w", id, err)
	}
	return token, nil
}

// ByHash is how a scan finds its pass. The lookup is tenant-scoped on purpose: a hash that
// belongs to another tenant is a miss, not a cross-tenant read.
func (r *Repository) ByHash(ctx context.Context, tenantID int64, hash string) (Token, error) {
	query := "SELECT " + tokenColumns + tokenSource +
		" WHERE t.tenant_id = ? AND t.token_hash = ?"
	var token Token
	if err := r.pool.GetContext(ctx, &token, query, tenantID, hash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Token{}, errs.NotFound("that token")
		}
		return Token{}, fmt.Errorf("read token by hash: %w", err)
	}
	return token, nil
}

// ForPass is the desk's list, newest first. Revoked tokens stay on it: "who had a badge for this
// pass, and what happened to it" is the question this list answers.
func (r *Repository) ForPass(ctx context.Context, tenantID, passID int64) ([]Token, error) {
	query := "SELECT " + tokenColumns + tokenSource +
		" WHERE t.tenant_id = ? AND t.pass_id = ? ORDER BY t.id DESC LIMIT 100"
	var tokens []Token
	if err := r.pool.SelectContext(ctx, &tokens, query, tenantID, passID); err != nil {
		return nil, fmt.Errorf("list tokens for pass %d: %w", passID, err)
	}
	return tokens, nil
}

// HasLive answers the one question an automatic issue needs: is this pass already carrying a
// badge? A relay that runs the same event twice must not mint a second one.
func (r *Repository) HasLive(ctx context.Context, tenantID, passID int64, now time.Time) (bool, error) {
	const query = `
		SELECT COUNT(*) FROM pass_tokens
		WHERE  tenant_id = ? AND pass_id = ? AND revoked_at IS NULL
		AND    (expires_at IS NULL OR expires_at > ?)
		AND    (max_uses IS NULL OR use_count < max_uses)`
	var count int
	if err := r.pool.GetContext(ctx, &count, query, tenantID, passID, now); err != nil {
		return false, fmt.Errorf("count live tokens for pass %d: %w", passID, err)
	}
	return count > 0, nil
}

// Revoke loses the race to nobody: a token already revoked keeps its first reason, and the
// caller learns from the row count whether this call was the one that did it.
func (r *Repository) Revoke(ctx context.Context, c db.Conn, tenantID, id int64, reason string) (bool, error) {
	const query = `
		UPDATE pass_tokens
		SET    revoked_at = UTC_TIMESTAMP(6), revoke_reason = ?
		WHERE  id = ? AND tenant_id = ? AND revoked_at IS NULL`
	result, err := c.ExecContext(ctx, query, reason, id, tenantID)
	if err != nil {
		return false, fmt.Errorf("revoke token %d: %w", id, err)
	}
	return db.RowsChanged(result)
}

// RevokeForPass takes every live badge off a pass at once. A pass that ends - refused, expired,
// cancelled - must not leave a working QR code behind it.
func (r *Repository) RevokeForPass(ctx context.Context, c db.Conn, tenantID, passID int64,
	reason string) (int64, error) {
	const query = `
		UPDATE pass_tokens
		SET    revoked_at = UTC_TIMESTAMP(6), revoke_reason = ?
		WHERE  tenant_id = ? AND pass_id = ? AND revoked_at IS NULL`
	result, err := c.ExecContext(ctx, query, reason, tenantID, passID)
	if err != nil {
		return 0, fmt.Errorf("revoke tokens for pass %d: %w", passID, err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count revoked tokens: %w", err)
	}
	return changed, nil
}

// ClaimUse burns one use of a token. It is a compare-and-set, not a read then a write: two
// tablets can scan the same badge at the same instant and exactly one of them may spend it.
func (r *Repository) ClaimUse(ctx context.Context, c db.Conn, tenantID, id, gateID int64,
	now time.Time) (bool, error) {
	const query = `
		UPDATE pass_tokens
		SET    use_count = use_count + 1, last_used_at = ?, last_gate_id = ?
		WHERE  id = ? AND tenant_id = ? AND revoked_at IS NULL
		AND    (expires_at IS NULL OR expires_at > ?)
		AND    (max_uses IS NULL OR use_count < max_uses)`
	result, err := c.ExecContext(ctx, query, now, gateID, id, tenantID, now)
	if err != nil {
		return false, fmt.Errorf("claim token %d: %w", id, err)
	}
	return db.RowsChanged(result)
}

// NewAttempt is one raw read as the device reported it. The secret is already a hash by the
// time it reaches this struct; the prefix is the only readable fragment stored.
type NewAttempt struct {
	TenantID      int64
	SiteID        int64
	GateID        int64
	DeviceID      int64
	GateSessionID int64
	InputKind     string
	TokenHash     string
	TokenPrefix   string
	Plate         string
	Outcome       string
	ResponseCode  string
	ResponseText  string
	PassID        int64
	TokenID       int64
	MovementID    int64
	Direction     string
	OccurredAt    time.Time
	IdemKey       string
}

func (r *Repository) InsertAttempt(ctx context.Context, c db.Conn, in NewAttempt) (int64, error) {
	// The idempotency key repeats when a tablet retries a scan whose answer it never saw. The
	// attempt is then already on file, and the retry must not double it.
	const query = `
		INSERT INTO scan_attempts
			(tenant_id, site_id, gate_id, device_id, gate_session_id, input_kind, token_hash,
			 token_prefix, plate_read, outcome, response_code, response_message,
			 matched_pass_id, matched_token_id, matched_movement_id, direction_requested,
			 occurred_at, idempotency_key)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(id)`
	result, err := c.ExecContext(ctx, query, in.TenantID, in.SiteID, in.GateID,
		nullID(in.DeviceID), nullID(in.GateSessionID), in.InputKind, nullText(in.TokenHash),
		nullText(in.TokenPrefix), nullText(in.Plate), in.Outcome, nullText(in.ResponseCode),
		nullText(in.ResponseText), nullID(in.PassID), nullID(in.TokenID), nullID(in.MovementID),
		nullText(in.Direction), in.OccurredAt, nullText(in.IdemKey))
	if err != nil {
		return 0, fmt.Errorf("record scan attempt: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read attempt id: %w", err)
	}
	return id, nil
}

// AttemptFilter is the supervisor's questions: what happened at this gate, on this pass, and
// what did the barrier say no to.
type AttemptFilter struct {
	TenantID int64
	GateID   int64
	PassID   int64
	Outcome  string
	Input    string
	Before   *httpx.Cursor
	Limit    int
}

func (f AttemptFilter) where() (string, []any) {
	clauses := []string{"a.tenant_id = ?"}
	args := []any{f.TenantID}
	if f.GateID != 0 {
		clauses = append(clauses, "a.gate_id = ?")
		args = append(args, f.GateID)
	}
	if f.PassID != 0 {
		clauses = append(clauses, "a.matched_pass_id = ?")
		args = append(args, f.PassID)
	}
	if f.Outcome != "" {
		clauses = append(clauses, "a.outcome = ?")
		args = append(args, f.Outcome)
	}
	if f.Input != "" {
		clauses = append(clauses, "a.input_kind = ?")
		args = append(args, f.Input)
	}
	if f.Before != nil {
		clauses = append(clauses,
			"(a.occurred_at < ? OR (a.occurred_at = ? AND a.id < ?))")
		args = append(args, f.Before.At, f.Before.At, f.Before.ID)
	}
	return strings.Join(clauses, " AND "), args
}

func (r *Repository) Attempts(ctx context.Context, f AttemptFilter) ([]Attempt, error) {
	where, args := f.where()
	query := "SELECT " + attemptColumns + attemptSource + " WHERE " + where +
		" ORDER BY a.id DESC LIMIT ?"
	args = append(args, f.Limit)
	var attempts []Attempt
	if err := r.pool.SelectContext(ctx, &attempts, query, args...); err != nil {
		return nil, fmt.Errorf("list scan attempts: %w", err)
	}
	return attempts, nil
}

func (r *Repository) Attempt(ctx context.Context, tenantID, id int64) (Attempt, error) {
	query := "SELECT " + attemptColumns + attemptSource + " WHERE a.tenant_id = ? AND a.id = ?"
	var attempt Attempt
	if err := r.pool.GetContext(ctx, &attempt, query, tenantID, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Attempt{}, errs.NotFound("scan attempt " + fmt.Sprint(id))
		}
		return Attempt{}, fmt.Errorf("read scan attempt %d: %w", id, err)
	}
	return attempt, nil
}

func nullID(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func nullText(value string) any {
	if value == "" {
		return nil
	}
	return value
}
