package reports

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
)

type Repository struct{ q db.Conn }

func NewRepository(q db.Conn) *Repository { return &Repository{q: q} }

// In rebinds the repository to an open transaction.
func (r *Repository) In(c db.Conn) *Repository { bound := *r; bound.q = c; return &bound }

const definitionColumns = `d.id, d.definition_key, d.name, d.module_key, d.description,
	d.period_grain, d.default_format, d.sort_order`

// visible is the one definition query, used by both the list and the single read: a report a
// tenant's modules do not cover is not merely hidden from the screen, it cannot be run by
// naming its key either.
const visible = `
	FROM report_definitions d
	WHERE d.status = 'active'
	  AND (d.module_key = 'core' OR EXISTS (
	        SELECT 1 FROM tenant_modules tm
	        WHERE tm.tenant_id = ? AND tm.module_key = d.module_key AND tm.enabled = 1))`

func (r *Repository) Definitions(ctx context.Context, tenantID int64) ([]Definition, error) {
	query := `SELECT ` + definitionColumns + visible + ` ORDER BY d.sort_order, d.definition_key`
	var out []Definition
	if err := r.q.SelectContext(ctx, &out, query, tenantID); err != nil {
		return nil, fmt.Errorf("read report definitions: %w", err)
	}
	return out, nil
}

func (r *Repository) Definition(ctx context.Context, tenantID int64, key string) (Definition, error) {
	query := `SELECT ` + definitionColumns + visible + ` AND d.definition_key = ?`
	var out Definition
	err := r.q.GetContext(ctx, &out, query, tenantID, key)
	if errors.Is(err, sql.ErrNoRows) {
		return Definition{}, errs.NotFound("report definition")
	}
	if err != nil {
		return Definition{}, fmt.Errorf("read report definition: %w", err)
	}
	return out, nil
}

// ModuleEnabled is the worker's per-tenant check: a schedule left running for a tenant that
// switched reports off is skipped, not failed.
func (r *Repository) ModuleEnabled(ctx context.Context, tenantID int64) (bool, error) {
	const query = `SELECT COUNT(*) FROM tenant_modules
		WHERE tenant_id = ? AND module_key = ? AND enabled = 1`
	var n int
	if err := r.q.GetContext(ctx, &n, query, tenantID, ModuleKey); err != nil {
		return false, fmt.Errorf("read report module flag: %w", err)
	}
	return n > 0, nil
}

// Rows runs one already-built catalogue query. from and to are UTC, as every stored instant
// in this schema is; the bucket the SQL prints is the tenant's day only when the caller asked
// for a day, which is why the period grain is on the definition.
func (r *Repository) Rows(ctx context.Context, query string, tenantID int64, from, to time.Time,
	limit int) ([]Row, error) {
	var out []Row
	if err := r.q.SelectContext(ctx, &out, query, tenantID, from, to, limit); err != nil {
		return nil, fmt.Errorf("run report: %w", err)
	}
	return out, nil
}

func (r *Repository) InsertRun(ctx context.Context, run Run, at time.Time) (int64, error) {
	const query = `
		INSERT INTO report_runs
			(tenant_id, definition_key, report_schedule_id, triggered_by, period_from, period_to,
			 params, format, status, started_at, requested_by_user_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 'running', ?, ?, ?, ?)`
	result, err := r.q.ExecContext(ctx, query, run.TenantID, run.Definition, run.ScheduleID,
		run.TriggeredBy, run.PeriodFrom, run.PeriodTo, run.Params, run.Format, at,
		run.RequestedBy, at, at)
	if err != nil {
		return 0, fmt.Errorf("file report run: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read report run id: %w", err)
	}
	return id, nil
}

func (r *Repository) FinishRun(ctx context.Context, tenantID, id int64, rows int, fileName string,
	size int64, checksum string, durationMS int, at time.Time) error {
	const query = `
		UPDATE report_runs
		SET status = 'succeeded', finished_at = ?, duration_ms = ?, row_count = ?,
		    file_name = ?, file_size_bytes = ?, checksum_sha256 = ?, updated_at = ?
		WHERE tenant_id = ? AND id = ? AND status = 'running'`
	if _, err := r.q.ExecContext(ctx, query, at, durationMS, rows, fileName, size, checksum, at,
		tenantID, id); err != nil {
		return fmt.Errorf("finish report run: %w", err)
	}
	return nil
}

func (r *Repository) FailRun(ctx context.Context, tenantID, id int64, code, message string,
	durationMS int, at time.Time) error {
	const query = `
		UPDATE report_runs
		SET status = 'failed', finished_at = ?, duration_ms = ?, error_code = ?,
		    error_message = ?, updated_at = ?
		WHERE tenant_id = ? AND id = ? AND status = 'running'`
	if _, err := r.q.ExecContext(ctx, query, at, durationMS, code, clip(message, 480), at,
		tenantID, id); err != nil {
		return fmt.Errorf("fail report run: %w", err)
	}
	return nil
}

func (r *Repository) Run(ctx context.Context, tenantID, id int64) (Run, error) {
	var out Run
	err := r.q.GetContext(ctx, &out, `SELECT `+runColumns+` FROM report_runs
		WHERE tenant_id = ? AND id = ?`, tenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, errs.NotFound("report run")
	}
	if err != nil {
		return Run{}, fmt.Errorf("read report run: %w", err)
	}
	return out, nil
}

const runColumns = `id, tenant_id, definition_key, report_schedule_id, triggered_by,
	period_from, period_to, params, format, status, started_at, finished_at, duration_ms,
	row_count, file_name, file_size_bytes, checksum_sha256, error_code, error_message,
	requested_by_user_id, created_at`

func (r *Repository) Runs(ctx context.Context, filter RunFilter) ([]Run, error) {
	where := []string{"tenant_id = ?"}
	args := []any{filter.TenantID}
	if filter.Definition != "" {
		where = append(where, "definition_key = ?")
		args = append(args, filter.Definition)
	}
	if filter.Status != "" {
		where = append(where, "status = ?")
		args = append(args, filter.Status)
	}
	if filter.Before != nil {
		where = append(where, "(created_at < ? OR (created_at = ? AND id < ?))")
		args = append(args, filter.Before, filter.Before, filter.BeforeID)
	}
	query := `SELECT ` + runColumns + ` FROM report_runs WHERE ` + strings.Join(where, " AND ") +
		` ORDER BY created_at DESC, id DESC LIMIT ?`
	args = append(args, filter.Limit)

	var out []Run
	if err := r.q.SelectContext(ctx, &out, query, args...); err != nil {
		return nil, fmt.Errorf("read report runs: %w", err)
	}
	return out, nil
}

// The schedules.

const scheduleColumns = `id, tenant_id, definition_key, name, frequency, day_of_week,
	day_of_month, hour_of_day, minute_of_hour, timezone, lookback_days,
	COALESCE(params, '{}') AS params, format, COALESCE(recipients, '[]') AS recipients,
	COALESCE(recipient_roles, '[]') AS recipient_roles, send_to_tenant_admins, is_active,
	next_run_at, last_run_at, last_status, last_error, created_by, created_at, updated_at`

func (r *Repository) InsertSchedule(ctx context.Context, s Schedule, at time.Time) (int64, error) {
	const query = `
		INSERT INTO report_schedules
			(tenant_id, definition_key, name, frequency, day_of_week, day_of_month, hour_of_day,
			 minute_of_hour, timezone, lookback_days, params, format, recipients,
			 recipient_roles, send_to_tenant_admins, is_active, next_run_at, created_by,
			 created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	result, err := r.q.ExecContext(ctx, query, s.TenantID, s.DefinitionKey, s.Name, s.Frequency,
		s.DayOfWeek, s.DayOfMonth, s.HourOfDay, s.MinuteOfHour, s.Timezone, s.LookbackDays,
		s.Params, s.Format, s.Recipients, s.RecipientRoles, s.SendToAdmins, s.IsActive,
		s.NextRunAt, s.CreatedBy, at, at)
	if err != nil {
		return 0, fmt.Errorf("create report schedule: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read report schedule id: %w", err)
	}
	return id, nil
}

func (r *Repository) UpdateSchedule(ctx context.Context, s Schedule, at time.Time) (bool, error) {
	const query = `
		UPDATE report_schedules
		SET definition_key = ?, name = ?, frequency = ?, day_of_week = ?, day_of_month = ?,
		    hour_of_day = ?, minute_of_hour = ?, timezone = ?, lookback_days = ?, params = ?,
		    format = ?, recipients = ?, recipient_roles = ?, send_to_tenant_admins = ?,
		    is_active = ?, next_run_at = ?, updated_at = ?
		WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL`
	result, err := r.q.ExecContext(ctx, query, s.DefinitionKey, s.Name, s.Frequency, s.DayOfWeek,
		s.DayOfMonth, s.HourOfDay, s.MinuteOfHour, s.Timezone, s.LookbackDays, s.Params,
		s.Format, s.Recipients, s.RecipientRoles, s.SendToAdmins, s.IsActive, s.NextRunAt, at,
		s.TenantID, s.ID)
	if err != nil {
		return false, fmt.Errorf("update report schedule: %w", err)
	}
	return db.RowsChanged(result)
}

func (r *Repository) Schedule(ctx context.Context, tenantID, id int64) (Schedule, error) {
	var out Schedule
	err := r.q.GetContext(ctx, &out, `SELECT `+scheduleColumns+` FROM report_schedules
		WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL`, tenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Schedule{}, errs.NotFound("report schedule")
	}
	if err != nil {
		return Schedule{}, fmt.Errorf("read report schedule: %w", err)
	}
	return out, nil
}

func (r *Repository) Schedules(ctx context.Context, tenantID int64, limit int) ([]Schedule, error) {
	var out []Schedule
	if err := r.q.SelectContext(ctx, &out, `SELECT `+scheduleColumns+` FROM report_schedules
		WHERE tenant_id = ? AND deleted_at IS NULL ORDER BY created_at DESC, id DESC LIMIT ?`,
		tenantID, limit); err != nil {
		return nil, fmt.Errorf("read report schedules: %w", err)
	}
	return out, nil
}

// SoftDelete keeps the run history that points at the schedule. A hard delete would null the
// link and turn every past export into an orphan nobody can explain.
func (r *Repository) SoftDelete(ctx context.Context, tenantID, id int64, at time.Time) (bool, error) {
	const query = `UPDATE report_schedules SET deleted_at = ?, is_active = 0, updated_at = ?
		WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL`
	result, err := r.q.ExecContext(ctx, query, at, at, tenantID, id)
	if err != nil {
		return false, fmt.Errorf("delete report schedule: %w", err)
	}
	return db.RowsChanged(result)
}

// Due is every schedule whose moment has passed, across tenants, because the worker is one
// loop for the whole deployment.
func (r *Repository) Due(ctx context.Context, now time.Time, limit int) ([]Schedule, error) {
	var out []Schedule
	if err := r.q.SelectContext(ctx, &out, `SELECT `+scheduleColumns+` FROM report_schedules
		WHERE is_active = 1 AND deleted_at IS NULL AND next_run_at IS NOT NULL AND next_run_at <= ?
		ORDER BY next_run_at, id LIMIT ?`, now, limit); err != nil {
		return nil, fmt.Errorf("read due report schedules: %w", err)
	}
	return out, nil
}

// MarkRun advances one schedule whether it succeeded, failed or was skipped, so a broken
// report cannot make the worker retry the same row every five seconds.
func (r *Repository) MarkRun(ctx context.Context, id int64, status string, lastErr *string,
	next *time.Time, at time.Time) error {
	const query = `
		UPDATE report_schedules
		SET last_run_at = ?, last_status = ?, last_error = ?, next_run_at = ?, updated_at = ?
		WHERE id = ?`
	if _, err := r.q.ExecContext(ctx, query, at, status, lastErr, next, at, id); err != nil {
		return fmt.Errorf("record schedule run: %w", err)
	}
	return nil
}

// clip shortens a stored error on runes so a long driver message does not overflow the column
// and turn a failed report into a failed transaction.
func clip(value string, room int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= room {
		return value
	}
	return string(runes[:room-1]) + "…"
}
