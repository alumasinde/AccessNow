package passes

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/httpx"
)

type Repository struct{ q db.Conn }

func NewRepository(q db.Conn) *Repository { return &Repository{q: q} }

func (r *Repository) In(c db.Conn) *Repository { bound := *r; bound.q = c; return &bound }

type PassType struct {
	ID                   int64  `db:"id"`
	TypeKey              string `db:"type_key"`
	ModuleKey            string `db:"module_key"`
	CategoryKey          string `db:"category_key"`
	RequiresApproval     bool   `db:"requires_approval"`
	RequiresHost         bool   `db:"requires_host"`
	DefaultValidityHours *int   `db:"default_validity_hours"`
	DefaultMaxEntries    *int   `db:"default_max_entries"`
	// DefaultDwellLimitMinutes belongs to the catalogue because it is a tenant rule about
	// a kind of pass, not a fact about one vehicle: the vehicle module snapshots it onto
	// the pass so a later edit of the rule cannot rewrite history.
	DefaultDwellLimitMinutes *int `db:"default_dwell_limit_minutes"`
}

func (r *Repository) TypeByKey(ctx context.Context, typeKey string) (PassType, error) {
	const query = `
		SELECT id, type_key, module_key, category_key, requires_approval, requires_host,
		       default_validity_hours, default_max_entries, default_dwell_limit_minutes
		FROM   pass_types
		WHERE  type_key = ? AND status = 'active'`
	var t PassType
	err := r.q.GetContext(ctx, &t, query, typeKey)
	if errors.Is(err, sql.ErrNoRows) {
		return PassType{}, errs.NotFound("pass type " + typeKey)
	}
	if err != nil {
		return PassType{}, fmt.Errorf("read pass type: %w", err)
	}
	return t, nil
}

// TypeByID reads the same catalogue row a pass already points at. A module that only has
// the pass in hand - the gate reaction, for instance - still has to ask the pass type
// whether another entry is allowed.
func (r *Repository) TypeByID(ctx context.Context, id int64) (PassType, error) {
	const query = `
		SELECT id, type_key, module_key, category_key, requires_approval, requires_host,
		       default_validity_hours, default_max_entries, default_dwell_limit_minutes
		FROM   pass_types
		WHERE  id = ?`
	var t PassType
	err := r.q.GetContext(ctx, &t, query, id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return PassType{}, errs.NotFound("pass type")
	case err != nil:
		return PassType{}, fmt.Errorf("read pass type %d: %w", id, err)
	}
	return t, nil
}

const passColumns = `id, uuid, tenant_id, site_id, pass_type_id, category_key, source_module_key,
	pass_no, status, origin, subject_name, subject_phone, subject_email, purpose,
	requester_user_id, host_user_id, valid_from, valid_until, entry_count,
	issued_at, decided_at, decision_note, closed_at, created_at`

func (r *Repository) Create(ctx context.Context, in New, createdBy int64) (int64, error) {
	const query = `
		INSERT INTO passes
			(uuid, tenant_id, site_id, pass_type_id, category_key, source_module_key, pass_no,
			 status, origin, subject_name, subject_phone, subject_email, purpose,
			 requester_user_id, host_user_id, valid_from, valid_until, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	result, err := r.q.ExecContext(ctx, query,
		uuid.NewString(), in.TenantID, in.SiteID, in.PassTypeID, in.CategoryKey, in.SourceModuleKey,
		in.PassNo, string(in.Status), in.Origin, in.SubjectName, in.SubjectPhone, in.SubjectEmail,
		in.Purpose, in.RequesterUserID, in.HostUserID, in.ValidFrom.UTC(), nullableTime(in.ValidUntil),
		nullableInt64(createdBy))
	if err != nil {
		return 0, fmt.Errorf("create pass: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read pass id: %w", err)
	}
	return id, nil
}

// ByID is always tenant scoped: the composite key on passes makes a cross tenant id
// miss rather than leak.
func (r *Repository) ByID(ctx context.Context, tenantID, passID int64) (Pass, error) {
	const query = `SELECT ` + passColumns + ` FROM passes WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`
	var p Pass
	err := r.q.GetContext(ctx, &p, query, passID, tenantID)
	if errors.Is(err, sql.ErrNoRows) {
		return Pass{}, errs.NotFound("pass")
	}
	if err != nil {
		return Pass{}, fmt.Errorf("read pass: %w", err)
	}
	return p, nil
}

// ListFilter is the register query, already validated by ParseFilter. The zero values
// mean "do not filter on this column".
type ListFilter struct {
	TenantID int64
	Filter
	Limit  int
	Before *httpx.Cursor
}

// List walks the register newest-first by keyset, never by offset: a tablet refreshes a
// busy list every few seconds and must not skip or repeat a row.
func (r *Repository) List(ctx context.Context, f ListFilter) ([]Pass, error) {
	where := []string{"tenant_id = ?", "deleted_at IS NULL"}
	args := []any{f.TenantID}

	if f.SiteID > 0 {
		where = append(where, "site_id = ?")
		args = append(args, f.SiteID)
	}
	if f.Status != "" {
		where = append(where, "status = ?")
		args = append(args, f.Status)
	}
	if f.Category != "" {
		where = append(where, "category_key = ?")
		args = append(args, f.Category)
	}
	if f.Module != "" {
		where = append(where, "source_module_key = ?")
		args = append(args, f.Module)
	}
	if f.Search != "" {
		where = append(where, "(pass_no LIKE ? OR subject_name LIKE ?)")
		args = append(args, like(f.Search), like(f.Search))
	}
	if f.Before != nil {
		where = append(where, "(created_at < ? OR (created_at = ? AND id < ?))")
		args = append(args, f.Before.At.UTC(), f.Before.At.UTC(), f.Before.ID)
	}

	query := `SELECT ` + passColumns + `
		FROM   passes
		WHERE  ` + strings.Join(where, " AND ") + `
		ORDER BY created_at DESC, id DESC
		LIMIT  ?`
	args = append(args, f.Limit)

	var items []Pass
	if err := r.q.SelectContext(ctx, &items, query, args...); err != nil {
		return nil, fmt.Errorf("list passes: %w", err)
	}
	return items, nil
}

// like escapes what the caller typed so a stray % matches a literal percent.
func like(term string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(term) + "%"
}

// ByPassNo is what a gate tablet hits after reading a QR or a printed number. The
// tenant predicate stays in the WHERE clause, so a number from another tenant is a
// miss, not a leak.
func (r *Repository) ByPassNo(ctx context.Context, tenantID int64, passNo string) (Pass, error) {
	const query = `SELECT ` + passColumns + `
		FROM   passes
		WHERE  tenant_id = ? AND pass_no = ? AND deleted_at IS NULL`
	var p Pass
	err := r.q.GetContext(ctx, &p, query, tenantID, passNo)
	if errors.Is(err, sql.ErrNoRows) {
		return Pass{}, errs.NotFound("pass " + passNo)
	}
	if err != nil {
		return Pass{}, fmt.Errorf("read pass by number: %w", err)
	}
	return p, nil
}

type Decision struct {
	TenantID int64
	PassID   int64
	From     Status
	To       Status
	ByUserID int64
	Note     string
	Issued   bool
}

// MoveStatus is a compare and swap on the current status, so an approver clicking twice
// and a guard scanning at the same moment cannot both win the row.
func (r *Repository) MoveStatus(ctx context.Context, d Decision) (bool, error) {
	if !Valid(d.To) {
		return false, errs.Invalid("status " + string(d.To) + " is not recognised")
	}
	const query = `
		UPDATE passes
		SET    status = ?, decided_by_user_id = ?, decided_at = UTC_TIMESTAMP(6),
		       decision_note = NULLIF(?, ''), issued_at = COALESCE(issued_at, ?), updated_by = ?
		WHERE  tenant_id = ? AND id = ? AND status = ? AND closed_at IS NULL`
	issued := time.Now().UTC()
	if !d.Issued {
		issued = time.Time{}
	}
	result, err := r.q.ExecContext(ctx, query,
		string(d.To), d.ByUserID, d.Note, nullableTime(zeroable(issued)), d.ByUserID,
		d.TenantID, d.PassID, string(d.From))
	if err != nil {
		return false, fmt.Errorf("move pass status: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read pass status result: %w", err)
	}
	return affected > 0, nil
}

func (r *Repository) RecordEntry(ctx context.Context, tenantID, passID int64) error {
	const query = `UPDATE passes SET entry_count = entry_count + 1 WHERE id = ? AND tenant_id = ?`
	if _, err := r.q.ExecContext(ctx, query, passID, tenantID); err != nil {
		return fmt.Errorf("record pass entry: %w", err)
	}
	return nil
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}

func nullableInt64(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func zeroable(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}
