package blocklist

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/httpx"
)

type Repository struct{ q db.Conn }

func NewRepository(q db.Conn) *Repository { return &Repository{q: q} }

// In rebinds the repository to an open transaction, keeping one method set.
func (r *Repository) In(c db.Conn) *Repository { bound := *r; bound.q = c; return &bound }

const entryColumns = `id, tenant_id, list_type, entity_type, match_type, match_value,
	display_name, organisation, photo_path, reason, severity, source, evidence_ref,
	incident_pass_id, alert_supervisor_role_key, hit_count, last_hit_at, status,
	added_by_user_id, added_at, expires_at, revoked_at, revoke_reason, notes,
	created_at, updated_at`

const hitColumns = `h.id, h.tenant_id, h.entry_id, h.matched_on, h.matched_value, h.pass_id,
	h.movement_id, h.gate_id, h.gate_session_id, h.action_taken, h.occurred_at,
	h.resolved_by_user_id, h.resolved_at, h.resolution,
	e.list_type AS entry_list_type, e.severity AS entry_severity, e.reason AS entry_reason,
	e.display_name, e.match_type AS entry_match_type, e.match_value AS entry_match_value,
	p.pass_no, g.name AS gate_name`

const hitFrom = `
	FROM   blocklist_hits h
	JOIN   blocklist_entries e ON e.id = h.entry_id AND e.tenant_id = h.tenant_id
	LEFT   JOIN passes p ON p.id = h.pass_id
	LEFT   JOIN gates g ON g.id = h.gate_id`

// The listings.

func (r *Repository) Insert(ctx context.Context, in New, at time.Time) (int64, error) {
	const query = `
		INSERT INTO blocklist_entries
			(tenant_id, list_type, entity_type, match_type, match_value, match_hash,
			 display_name, organisation, reason, severity, source, evidence_ref,
			 incident_pass_id, alert_supervisor_role_key, status, added_by_user_id, added_at,
			 expires_at, notes, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', ?, ?, ?, ?, ?, ?)`
	result, err := r.q.ExecContext(ctx, query,
		in.TenantID, string(in.ListType), string(in.EntityType), string(in.MatchType),
		in.Normalised(), in.Hash(), text(in.DisplayName), text(in.Organisation), in.Reason,
		string(in.Severity), text(in.Source), text(in.EvidenceRef), nullable(in.IncidentPass),
		text(in.AlertRoleKey), nullable(in.AddedBy), at, nullableTime(in.ExpiresAt),
		text(in.Notes), at, at)
	if err != nil {
		return 0, fmt.Errorf("file listing: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read listing id: %w", err)
	}
	return id, nil
}

func (r *Repository) ByID(ctx context.Context, tenantID, id int64) (Entry, error) {
	var out Entry
	err := r.q.GetContext(ctx, &out, `SELECT `+entryColumns+` FROM blocklist_entries
		WHERE tenant_id = ? AND id = ?`, tenantID, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Entry{}, errs.NotFound("listing")
		}
		return Entry{}, fmt.Errorf("read listing: %w", err)
	}
	return out, nil
}

// FindByHash is the listing the gate is looking for, if there is one. The hash alone is not
// the key: two tenants, or a phone and a badge number that happen to be the same digits, must
// never read as the same person, so the match type is part of the question and the tenant is
// always part of it.
//
// An entry whose own expiry has passed is not returned rather than returned as expired: the
// gate's answer must not depend on a sweep having run first, and a listing that timed out is
// the same no as one that was never filed.
//
// When the same identifier is on both lists the blocklist row wins, because that is what the
// unique key allows and a watch note about a banned phone is not a reason to open the gate.
func (r *Repository) FindByHash(ctx context.Context, tenantID int64, matchType MatchType,
	hash string, now time.Time) (Entry, error) {
	const query = `SELECT ` + entryColumns + ` FROM blocklist_entries
		WHERE tenant_id = ? AND match_type = ? AND status IN ('active', 'under_review')
		  AND (expires_at IS NULL OR expires_at > ?)
		  AND match_hash = ?
		ORDER BY list_type = 'blocklist' DESC, id LIMIT 1`
	var out Entry
	err := r.q.GetContext(ctx, &out, query, tenantID, string(matchType), now, hash)
	if errors.Is(err, sql.ErrNoRows) {
		return Entry{}, errs.NotFound("listing")
	}
	if err != nil {
		return Entry{}, fmt.Errorf("screen the list: %w", err)
	}
	return out, nil
}

type ListFilter struct {
	TenantID     int64
	ListType     string
	Severity     string
	Status       string
	Search       string
	Limit        int
	Before       *httpx.Cursor
	IncludeStale bool
}

// List pages the register newest first. search looks at the name and the value a desk would
// type, never at the hash: the hash is the index, not the interface.
func (r *Repository) List(ctx context.Context, in ListFilter) ([]Entry, error) {
	where := []string{"tenant_id = ?"}
	args := []any{in.TenantID}
	add(&where, &args, in.ListType, "list_type")
	add(&where, &args, in.Severity, "severity")
	add(&where, &args, in.Status, "status")
	if !in.IncludeStale && in.Status == "" {
		// The register shows the listings that can still bite unless the caller asks for
		// the history as well.
		where = append(where, "status IN ('active', 'under_review')")
	}
	if term := strings.TrimSpace(in.Search); term != "" {
		// No UPPER() on the two free-text columns: the table's collation is already
		// case-insensitive, so folding them by hand buys nothing except a reason for the
		// database to skip idx_blocklist_display. match_value is stored stripped of its
		// separators and upper case, which is why that one column is asked in its own form.
		where = append(where, "(match_value LIKE ? OR display_name LIKE ? OR organisation LIKE ?)")
		args = append(args, normalizePlain(term)+"%", term+"%", term+"%")
	}
	if in.Before != nil {
		where = append(where, "(added_at < ? OR (added_at = ? AND id < ?))")
		args = append(args, in.Before.At, in.Before.At, in.Before.ID)
	}

	query := `SELECT ` + entryColumns + ` FROM blocklist_entries WHERE ` +
		strings.Join(where, " AND ") + ` ORDER BY added_at DESC, id DESC LIMIT ?`
	args = append(args, in.Limit)
	var out []Entry
	if err := r.q.SelectContext(ctx, &out, query, args...); err != nil {
		return nil, fmt.Errorf("read the list: %w", err)
	}
	return out, nil
}

// CountByType is the two numbers the desk screen leads with: how many are refused, how many
// are watched.
func (r *Repository) CountByType(ctx context.Context, tenantID int64) (map[string]int, error) {
	const query = `SELECT list_type, COUNT(*) FROM blocklist_entries
		WHERE tenant_id = ? AND status IN ('active', 'under_review') GROUP BY list_type`
	rows, err := r.q.QueryxContext(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("count the list: %w", err)
	}
	defer rows.Close()

	out := map[string]int{string(ListBlock): 0, string(ListWatch): 0}
	for rows.Next() {
		var key string
		var n int
		if err := rows.Scan(&key, &n); err != nil {
			return nil, fmt.Errorf("count the list: %w", err)
		}
		out[key] = n
	}
	return out, rows.Err()
}

// Revoke takes a listing off the list. It is an update, never a delete: the hits behind it
// stay pointing at a real row, and a ban that was wrong for a week still has to be auditable
// as having been wrong.
func (r *Repository) Revoke(ctx context.Context, tenantID, id, by int64, reason string,
	at time.Time) (bool, error) {
	const query = `UPDATE blocklist_entries
		SET status = 'revoked', revoked_at = ?, revoke_reason = ?, updated_at = ?
		WHERE tenant_id = ? AND id = ? AND status IN ('active', 'under_review')`
	result, err := r.q.ExecContext(ctx, query, at, reason, at, tenantID, id)
	if err != nil {
		return false, fmt.Errorf("revoke listing: %w", err)
	}
	return changed(result)
}

// Touch records that a listing bit: how many times and when last. It is one statement
// because a hit that counted twice on a replayed scan would make a low-severity listing look
// like the site's biggest problem.
func (r *Repository) Touch(ctx context.Context, tenantID, id int64, at time.Time) error {
	const query = `UPDATE blocklist_entries
		SET hit_count = hit_count + 1, last_hit_at = ?, updated_at = ?
		WHERE tenant_id = ? AND id = ?`
	if _, err := r.q.ExecContext(ctx, query, at, at, tenantID, id); err != nil {
		return fmt.Errorf("count the hit: %w", err)
	}
	return nil
}

// The hits.

func (r *Repository) RecordHit(ctx context.Context, hit Hit, at time.Time) (int64, error) {
	const query = `
		INSERT INTO blocklist_hits
			(tenant_id, entry_id, matched_on, matched_value, pass_id, movement_id, gate_id,
			 gate_session_id, action_taken, occurred_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	result, err := r.q.ExecContext(ctx, query, hit.TenantID, hit.EntryID, hit.MatchedOn,
		hit.MatchedValue, hit.PassID, hit.MovementID, hit.GateID, hit.GateSession,
		hit.ActionTaken, at, at)
	if err != nil {
		return 0, fmt.Errorf("record hit: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read hit id: %w", err)
	}
	return id, nil
}

type HitFilter struct {
	TenantID       int64
	EntryID        int64
	ListType       string
	UnresolvedOnly bool
	Limit          int
	Before         *httpx.Cursor
}

func (r *Repository) Hits(ctx context.Context, in HitFilter) ([]Hit, error) {
	where := []string{"h.tenant_id = ?"}
	args := []any{in.TenantID}
	if in.EntryID != 0 {
		where = append(where, "h.entry_id = ?")
		args = append(args, in.EntryID)
	}
	if in.ListType != "" {
		where = append(where, "e.list_type = ?")
		args = append(args, in.ListType)
	}
	if in.UnresolvedOnly {
		where = append(where, "h.resolved_at IS NULL")
	}
	if in.Before != nil {
		where = append(where, "(h.occurred_at < ? OR (h.occurred_at = ? AND h.id < ?))")
		args = append(args, in.Before.At, in.Before.At, in.Before.ID)
	}
	query := `SELECT ` + hitColumns + hitFrom + ` WHERE ` + strings.Join(where, " AND ") +
		` ORDER BY h.occurred_at DESC, h.id DESC LIMIT ?`
	args = append(args, in.Limit)

	var out []Hit
	if err := r.q.SelectContext(ctx, &out, query, args...); err != nil {
		return nil, fmt.Errorf("read the hits: %w", err)
	}
	return out, nil
}

func (r *Repository) HitByID(ctx context.Context, tenantID, id int64) (Hit, error) {
	var out Hit
	err := r.q.GetContext(ctx, &out, `SELECT `+hitColumns+hitFrom+
		` WHERE h.tenant_id = ? AND h.id = ?`, tenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Hit{}, errs.NotFound("hit")
	}
	if err != nil {
		return Hit{}, fmt.Errorf("read hit: %w", err)
	}
	return out, nil
}

// Resolve closes a hit with the sentence somebody wrote about it. The listing itself is not
// touched: a hit that turns out to have been a different person with the same name is
// resolved, while the ban on the real person stays exactly where it was.
func (r *Repository) Resolve(ctx context.Context, tenantID, hitID, by int64, note string,
	at time.Time) (bool, error) {
	const query = `UPDATE blocklist_hits
		SET resolved_by_user_id = ?, resolved_at = ?, resolution = ?
		WHERE tenant_id = ? AND id = ? AND resolved_at IS NULL`
	result, err := r.q.ExecContext(ctx, query, by, at, note, tenantID, hitID)
	if err != nil {
		return false, fmt.Errorf("resolve hit: %w", err)
	}
	return changed(result)
}

// UnresolvedHits is the number a supervisor is chased with: hits nobody has explained yet.
func (r *Repository) UnresolvedHits(ctx context.Context, tenantID int64) (int, error) {
	const query = `SELECT COUNT(*) FROM blocklist_hits
		WHERE tenant_id = ? AND resolved_at IS NULL`
	var n int
	if err := r.q.GetContext(ctx, &n, query, tenantID); err != nil {
		return 0, fmt.Errorf("count open hits: %w", err)
	}
	return n, nil
}

func changed(result sql.Result) (bool, error) {
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count the rows: %w", err)
	}
	return n > 0, nil
}

func add(where *[]string, args *[]any, value, column string) {
	if value == "" {
		return
	}
	*where = append(*where, column+" = ?")
	*args = append(*args, value)
}

func text(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullable(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}

// normalizePlain is the search form of a value: whatever a desk types into a box is compared
// against the stored form, which has already had its separators taken out.
func normalizePlain(value string) string {
	return strings.ToUpper(strings.Join(strings.Fields(value), ""))
}
