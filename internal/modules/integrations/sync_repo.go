package integrations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"accessnow/internal/platform/errs"
)

const systemColumns = `id, tenant_id, kind, name, base_url, auth_type, credentials_ref,
	username, api_version, sync_direction, sync_frequency_minutes,
	COALESCE(field_map, '{}') AS field_map, COALESCE(options, '{}') AS options,
	health_status, last_sync_at, last_error, is_active, created_by, created_at, updated_at`

func (r *Repository) Systems(ctx context.Context, tenantID int64) ([]ExternalSystem, error) {
	var out []ExternalSystem
	if err := r.q.SelectContext(ctx, &out, `SELECT `+systemColumns+` FROM external_systems
		WHERE tenant_id = ? ORDER BY kind, name`, tenantID); err != nil {
		return nil, fmt.Errorf("read external systems: %w", err)
	}
	return out, nil
}

func (r *Repository) ExternalSystem(ctx context.Context, tenantID, id int64) (ExternalSystem, error) {
	var out ExternalSystem
	err := r.q.GetContext(ctx, &out, `SELECT `+systemColumns+` FROM external_systems
		WHERE tenant_id = ? AND id = ?`, tenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return ExternalSystem{}, errs.NotFound("external system")
	}
	if err != nil {
		return ExternalSystem{}, fmt.Errorf("read external system: %w", err)
	}
	return out, nil
}

func (r *Repository) InsertSystem(ctx context.Context, s ExternalSystem, at time.Time) (int64, error) {
	const query = `
		INSERT INTO external_systems
			(tenant_id, kind, name, base_url, auth_type, credentials_ref, username, api_version,
			 sync_direction, sync_frequency_minutes, field_map, options, health_status,
			 is_active, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'unknown', ?, ?, ?, ?)`
	result, err := r.q.ExecContext(ctx, query, s.TenantID, s.Kind, s.Name, s.BaseURL,
		s.AuthType, s.CredentialsRef, s.Username, s.APIVersion, s.SyncDirection,
		s.SyncFrequencyMinutes, s.FieldMap, s.Options, s.IsActive, s.CreatedBy, at, at)
	if err != nil {
		return 0, fmt.Errorf("create external system: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read external system id: %w", err)
	}
	return id, nil
}

func (r *Repository) UpdateSystem(ctx context.Context, s ExternalSystem, at time.Time) (bool, error) {
	const query = `
		UPDATE external_systems
		SET kind = ?, name = ?, base_url = ?, auth_type = ?, credentials_ref = ?,
		    username = ?, api_version = ?, sync_direction = ?, sync_frequency_minutes = ?,
		    field_map = ?, options = ?, is_active = ?, updated_at = ?
		WHERE tenant_id = ? AND id = ?`
	result, err := r.q.ExecContext(ctx, query, s.Kind, s.Name, s.BaseURL, s.AuthType,
		s.CredentialsRef, s.Username, s.APIVersion, s.SyncDirection, s.SyncFrequencyMinutes,
		s.FieldMap, s.Options, s.IsActive, at, s.TenantID, s.ID)
	if err != nil {
		return false, fmt.Errorf("update external system: %w", err)
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

func (r *Repository) SetSystemActive(ctx context.Context, tenantID, id int64, active bool,
	at time.Time) (bool, error) {
	const query = `UPDATE external_systems SET is_active = ?, updated_at = ?
		WHERE tenant_id = ? AND id = ? AND is_active <> ?`
	result, err := r.q.ExecContext(ctx, query, active, at, tenantID, id, active)
	if err != nil {
		return false, fmt.Errorf("switch external system: %w", err)
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

// ScheduledSystems is the worker's list: active systems with a frequency. The due comparison
// happens in Go because the interval is a column, not a constant.
func (r *Repository) ScheduledSystems(ctx context.Context) ([]ExternalSystem, error) {
	var out []ExternalSystem
	if err := r.q.SelectContext(ctx, &out, `SELECT `+systemColumns+` FROM external_systems
		WHERE is_active = 1 AND sync_frequency_minutes IS NOT NULL
		ORDER BY id LIMIT 100`); err != nil {
		return nil, fmt.Errorf("read scheduled systems: %w", err)
	}
	return out, nil
}

func (r *Repository) MarkSystemResult(ctx context.Context, id int64, health, message string,
	at time.Time) error {
	if _, err := r.q.ExecContext(ctx, `UPDATE external_systems
		SET health_status = ?, last_sync_at = ?, last_error = NULLIF(?, ''), updated_at = ?
		WHERE id = ?`, health, at, message, at, id); err != nil {
		return fmt.Errorf("record system health: %w", err)
	}
	return nil
}

// The jobs.

const jobColumns = `j.id, j.tenant_id, j.external_system_id, j.entity_type, j.direction,
	j.trigger_source, j.status, j.attempts, j.next_attempt_at, j.records_seen,
	j.records_created, j.records_updated, j.records_failed, j.cursor_value, j.error_summary,
	j.started_at, j.finished_at, j.created_at, s.name AS system_name`

const jobFrom = ` FROM sync_jobs j JOIN external_systems s ON s.id = j.external_system_id`

func (r *Repository) QueueJob(ctx context.Context, job SyncJob, at time.Time) (int64, error) {
	const query = `
		INSERT INTO sync_jobs
			(tenant_id, external_system_id, entity_type, direction, trigger_source, status,
			 created_at)
		VALUES (?, ?, ?, ?, ?, 'queued', ?)`
	result, err := r.q.ExecContext(ctx, query, job.TenantID, job.SystemID, job.EntityType,
		job.Direction, job.TriggerSource, at)
	if err != nil {
		return 0, fmt.Errorf("queue sync job: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read sync job id: %w", err)
	}
	return id, nil
}

func (r *Repository) Job(ctx context.Context, tenantID, id int64) (SyncJob, error) {
	var out SyncJob
	err := r.q.GetContext(ctx, &out, `SELECT `+jobColumns+jobFrom+`
		WHERE j.tenant_id = ? AND j.id = ?`, tenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return SyncJob{}, errs.NotFound("sync job")
	}
	if err != nil {
		return SyncJob{}, fmt.Errorf("read sync job: %w", err)
	}
	return out, nil
}

func (r *Repository) SyncJobs(ctx context.Context, tenantID, systemID int64,
	limit int) ([]SyncJob, error) {
	where := "j.tenant_id = ?"
	args := []any{tenantID}
	if systemID > 0 {
		where += " AND j.external_system_id = ?"
		args = append(args, systemID)
	}
	args = append(args, limit)
	var out []SyncJob
	if err := r.q.SelectContext(ctx, &out, `SELECT `+jobColumns+jobFrom+`
		WHERE `+where+` ORDER BY j.created_at DESC, j.id DESC LIMIT ?`, args...); err != nil {
		return nil, fmt.Errorf("read sync jobs: %w", err)
	}
	return out, nil
}

// DueJobs is the queue as the worker sees it: queued, and either never retried or past the
// backoff the previous attempt set.
func (r *Repository) DueJobs(ctx context.Context, now time.Time, limit int) ([]SyncJob, error) {
	var out []SyncJob
	if err := r.q.SelectContext(ctx, &out, `SELECT `+jobColumns+jobFrom+`
		WHERE j.status = 'queued' AND (j.next_attempt_at IS NULL OR j.next_attempt_at <= ?)
		ORDER BY j.created_at, j.id LIMIT ?`, now, limit); err != nil {
		return nil, fmt.Errorf("read due sync jobs: %w", err)
	}
	return out, nil
}

// ClaimJob counts the run it is about to start, so a job that dies mid-run still remembers it
// was attempted and cannot retry forever.
func (r *Repository) ClaimJob(ctx context.Context, id int64, at time.Time) (bool, error) {
	result, err := r.q.ExecContext(ctx, `UPDATE sync_jobs
		SET status = 'running', started_at = ?, attempts = attempts + 1
		WHERE id = ? AND status = 'queued'`, at, id)
	if err != nil {
		return false, fmt.Errorf("claim sync job: %w", err)
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

func (r *Repository) ReclaimStaleJobs(ctx context.Context, before time.Time, limit int) (int64, error) {
	result, err := r.q.ExecContext(ctx, `UPDATE sync_jobs
		SET status = 'queued', started_at = NULL
		WHERE status = 'running' AND started_at < ? LIMIT ?`, before, limit)
	if err != nil {
		return 0, fmt.Errorf("reclaim sync jobs: %w", err)
	}
	return result.RowsAffected()
}

// RetryJob puts a failed run back in the queue at the time the ladder chose. It only moves a
// job that is still 'running', so a job someone else already finished is not resurrected.
func (r *Repository) RetryJob(ctx context.Context, id int64, summary string, nextAt,
	at time.Time) (bool, error) {
	const query = `UPDATE sync_jobs
		SET status = 'queued', started_at = NULL, error_summary = ?, next_attempt_at = ?
		WHERE id = ? AND status = 'running'`
	result, err := r.q.ExecContext(ctx, query, summary, nextAt, id)
	if err != nil {
		return false, fmt.Errorf("schedule sync retry: %w", err)
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

func (r *Repository) FinishJob(ctx context.Context, id int64, status string, seen, created,
	updated, failed int, cursor, summary *string, at time.Time) error {
	const query = `UPDATE sync_jobs
		SET status = ?, records_seen = ?, records_created = ?, records_updated = ?,
		    records_failed = ?, cursor_value = ?, error_summary = ?, next_attempt_at = NULL,
		    finished_at = ?
		WHERE id = ? AND status = 'running'`
	if _, err := r.q.ExecContext(ctx, query, status, seen, created, updated, failed, cursor,
		summary, at, id); err != nil {
		return fmt.Errorf("finish sync job: %w", err)
	}
	return nil
}

func (r *Repository) HasOpenJob(ctx context.Context, systemID int64, entity, direction string) (bool, error) {
	var n int
	if err := r.q.GetContext(ctx, &n, `SELECT COUNT(*) FROM sync_jobs
		WHERE external_system_id = ? AND entity_type = ? AND direction = ?
		  AND status IN ('queued', 'running')`, systemID, entity, direction); err != nil {
		return false, fmt.Errorf("read open sync job: %w", err)
	}
	return n > 0, nil
}

// The links.

const linkColumns = `l.id, l.tenant_id, l.external_system_id, l.entity_type, l.external_id,
	l.internal_id, l.content_hash, l.last_synced_at, l.created_at, l.updated_at,
	s.name AS system_name`

const linkFrom = ` FROM external_entity_links l
	JOIN external_systems s ON s.id = l.external_system_id`

// UpsertLink records a remote record and whether its content changed. A digest that matches the
// last one is a no-op write: a scheduled sync over a register nobody edited should not touch
// every row every five minutes.
func (r *Repository) UpsertLink(ctx context.Context, tenantID, systemID int64, entity,
	external string, internal *int64, digest string, at time.Time) (bool, bool, error) {
	var existing struct {
		ID   int64   `db:"id"`
		Hash *string `db:"content_hash"`
	}
	err := r.q.GetContext(ctx, &existing, `SELECT id, content_hash FROM external_entity_links
		WHERE external_system_id = ? AND entity_type = ? AND external_id = ?`,
		systemID, entity, external)
	if errors.Is(err, sql.ErrNoRows) {
		const query = `
			INSERT INTO external_entity_links
				(tenant_id, external_system_id, entity_type, external_id, internal_id,
				 content_hash, last_synced_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
		if _, err := r.q.ExecContext(ctx, query, tenantID, systemID, entity, external, internal,
			digest, at, at, at); err != nil {
			return false, false, fmt.Errorf("link synced record: %w", err)
		}
		return true, false, nil
	}
	if err != nil {
		return false, false, fmt.Errorf("read synced record link: %w", err)
	}
	if existing.Hash != nil && *existing.Hash == digest {
		if _, err := r.q.ExecContext(ctx, `UPDATE external_entity_links
			SET last_synced_at = ?, updated_at = ? WHERE id = ?`, at, at, existing.ID); err != nil {
			return false, false, fmt.Errorf("touch synced record link: %w", err)
		}
		return false, false, nil
	}
	if _, err := r.q.ExecContext(ctx, `UPDATE external_entity_links
		SET internal_id = COALESCE(?, internal_id), content_hash = ?, last_synced_at = ?,
		    updated_at = ?
		WHERE id = ?`, internal, digest, at, at, existing.ID); err != nil {
		return false, false, fmt.Errorf("update synced record link: %w", err)
	}
	return false, true, nil
}

func (r *Repository) Links(ctx context.Context, tenantID, systemID int64, entity string,
	limit int) ([]EntityLink, error) {
	where := "l.tenant_id = ?"
	args := []any{tenantID}
	if systemID > 0 {
		where += " AND l.external_system_id = ?"
		args = append(args, systemID)
	}
	if entity = strings.TrimSpace(entity); entity != "" {
		where += " AND l.entity_type = ?"
		args = append(args, entity)
	}
	args = append(args, limit)
	var out []EntityLink
	if err := r.q.SelectContext(ctx, &out, `SELECT `+linkColumns+linkFrom+`
		WHERE `+where+` ORDER BY l.updated_at DESC, l.id DESC LIMIT ?`, args...); err != nil {
		return nil, fmt.Errorf("read entity links: %w", err)
	}
	return out, nil
}

func (r *Repository) DeleteLink(ctx context.Context, tenantID, id int64) (bool, error) {
	result, err := r.q.ExecContext(ctx, `DELETE FROM external_entity_links
		WHERE tenant_id = ? AND id = ?`, tenantID, id)
	if err != nil {
		return false, fmt.Errorf("delete entity link: %w", err)
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

// localRecordQueries is the outbound half. Each query builds the JSON object itself, so the
// shape sent to a remote system is fixed next to the columns it reads.
var localRecordQueries = map[string]string{
	"staff": `SELECT JSON_OBJECT('id', s.id, 'employee_no', s.employee_no,
			'first_name', s.first_name, 'last_name', s.last_name, 'job_title', s.job_title,
			'department_id', s.department_id, 'phone', s.phone, 'email', s.email,
			'access_card_no', s.access_card_no, 'status', s.status, 'site_id', s.home_site_id)
		FROM staff s WHERE s.tenant_id = ? AND s.deleted_at IS NULL ORDER BY s.id LIMIT ?`,
	"department": `SELECT JSON_OBJECT('id', d.id, 'name', d.name)
		FROM departments d WHERE d.tenant_id = ? ORDER BY d.id LIMIT ?`,
	"visitor_company": `SELECT JSON_OBJECT('id', c.id, 'name', c.name)
		FROM visitor_companies c WHERE c.tenant_id = ? ORDER BY c.id LIMIT ?`,
}

func (r *Repository) LocalRecords(ctx context.Context, tenantID int64, entity string,
	limit int) ([]json.RawMessage, error) {
	query, ok := localRecordQueries[entity]
	if !ok {
		return nil, errs.BadField("entity_type", "outbound sync is not available for "+entity)
	}
	var values []string
	if err := r.q.SelectContext(ctx, &values, query, tenantID, limit); err != nil {
		return nil, fmt.Errorf("read local %s records: %w", entity, err)
	}
	out := make([]json.RawMessage, 0, len(values))
	for _, value := range values {
		out = append(out, json.RawMessage(value))
	}
	return out, nil
}
