package compliance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"accessnow/internal/platform/audit"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
)

type Repository struct{ q db.Conn }

func NewRepository(q db.Conn) *Repository { return &Repository{q: q} }

func (r *Repository) In(c db.Conn) *Repository { bound := *r; bound.q = c; return &bound }

const policyColumns = `id, tenant_id, data_category, retention_days, purge_action,
	notify_before_days, last_run_at, is_active, updated_by, created_at, updated_at`

func (r *Repository) Policies(ctx context.Context, tenantID int64) ([]Policy, error) {
	var out []Policy
	if err := r.q.SelectContext(ctx, &out, `SELECT `+policyColumns+` FROM retention_policies
		WHERE tenant_id = ? ORDER BY data_category`, tenantID); err != nil {
		return nil, fmt.Errorf("read retention policies: %w", err)
	}
	return out, nil
}

func (r *Repository) Policy(ctx context.Context, tenantID, id int64) (Policy, error) {
	var out Policy
	err := r.q.GetContext(ctx, &out, `SELECT `+policyColumns+` FROM retention_policies
		WHERE tenant_id = ? AND id = ?`, tenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Policy{}, errs.NotFound("retention policy")
	}
	if err != nil {
		return Policy{}, fmt.Errorf("read retention policy: %w", err)
	}
	return out, nil
}

// PolicyFor is the policy a purge is run under. Without one there is no retention period to
// apply, and a purge with a guessed period is worse than a purge that refuses.
func (r *Repository) PolicyFor(ctx context.Context, tenantID int64, category string) (Policy, error) {
	var out Policy
	err := r.q.GetContext(ctx, &out, `SELECT `+policyColumns+` FROM retention_policies
		WHERE tenant_id = ? AND data_category = ?`, tenantID, category)
	if errors.Is(err, sql.ErrNoRows) {
		return Policy{}, errs.NotFound("retention policy")
	}
	if err != nil {
		return Policy{}, fmt.Errorf("read retention policy: %w", err)
	}
	return out, nil
}

// UpsertPolicy is one row per category, so a second save edits the policy the purge already
// uses rather than leaving two and picking one.
func (r *Repository) UpsertPolicy(ctx context.Context, p Policy, at time.Time) (int64, error) {
	const query = `
		INSERT INTO retention_policies
			(tenant_id, data_category, retention_days, purge_action, notify_before_days,
			 is_active, updated_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			id = LAST_INSERT_ID(id), retention_days = VALUES(retention_days),
			purge_action = VALUES(purge_action), notify_before_days = VALUES(notify_before_days),
			is_active = VALUES(is_active), updated_by = VALUES(updated_by),
			updated_at = VALUES(updated_at)`
	result, err := r.q.ExecContext(ctx, query, p.TenantID, p.DataCategory, p.RetentionDays,
		p.PurgeAction, p.NotifyBeforeDays, p.IsActive, p.UpdatedBy, at, at)
	if err != nil {
		return 0, fmt.Errorf("save retention policy: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read retention policy id: %w", err)
	}
	return id, nil
}

// TouchPolicy records that a purge used this policy, so the desk can see the rule doing work.
func (r *Repository) TouchPolicy(ctx context.Context, tenantID, id int64, at time.Time) error {
	if _, err := r.q.ExecContext(ctx, `UPDATE retention_policies SET last_run_at = ?, updated_at = ?
		WHERE tenant_id = ? AND id = ?`, at, at, tenantID, id); err != nil {
		return fmt.Errorf("record policy run: %w", err)
	}
	return nil
}

const holdColumns = `id, tenant_id, entity_type, entity_id, pass_id, case_ref, reason, status,
	placed_by_user_id, placed_at, expires_at, reviewed_at, released_by_user_id, released_at,
	release_reason, created_at, updated_at`

func (r *Repository) Holds(ctx context.Context, tenantID int64, status string,
	limit int) ([]Hold, error) {
	where := "tenant_id = ?"
	args := []any{tenantID}
	if status != "" {
		where += " AND status = ?"
		args = append(args, status)
	}
	args = append(args, limit)
	var out []Hold
	if err := r.q.SelectContext(ctx, &out, `SELECT `+holdColumns+` FROM legal_holds
		WHERE `+where+` ORDER BY placed_at DESC, id DESC LIMIT ?`, args...); err != nil {
		return nil, fmt.Errorf("read legal holds: %w", err)
	}
	return out, nil
}

func (r *Repository) Hold(ctx context.Context, tenantID, id int64) (Hold, error) {
	var out Hold
	err := r.q.GetContext(ctx, &out, `SELECT `+holdColumns+` FROM legal_holds
		WHERE tenant_id = ? AND id = ?`, tenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Hold{}, errs.NotFound("legal hold")
	}
	if err != nil {
		return Hold{}, fmt.Errorf("read legal hold: %w", err)
	}
	return out, nil
}

func (r *Repository) InsertHold(ctx context.Context, h Hold, at time.Time) (int64, error) {
	const query = `
		INSERT INTO legal_holds
			(tenant_id, entity_type, entity_id, pass_id, case_ref, reason, status,
			 placed_by_user_id, placed_at, expires_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, 'active', ?, ?, ?, ?, ?)`
	result, err := r.q.ExecContext(ctx, query, h.TenantID, h.EntityType, h.EntityID, h.PassID,
		h.CaseRef, h.Reason, h.PlacedBy, at, h.ExpiresAt, at, at)
	if err != nil {
		return 0, fmt.Errorf("place legal hold: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read legal hold id: %w", err)
	}
	return id, nil
}

// ReleaseHold is a compare-and-set on 'active': two people releasing the same case both
// succeed in writing a reason, and only the first one is the release.
func (r *Repository) ReleaseHold(ctx context.Context, tenantID, id, by int64, reason string,
	at time.Time) (bool, error) {
	const query = `UPDATE legal_holds
		SET status = 'released', released_by_user_id = ?, released_at = ?, release_reason = ?,
		    updated_at = ?
		WHERE tenant_id = ? AND id = ? AND status = 'active'`
	result, err := r.q.ExecContext(ctx, query, by, at, reason, at, tenantID, id)
	if err != nil {
		return false, fmt.Errorf("release legal hold: %w", err)
	}
	return db.RowsChanged(result)
}

// ExpireHolds closes holds whose own expiry has passed. It runs before a hold list is read, so
// a case nobody extended stops protecting data at the time it said it would.
func (r *Repository) ExpireHolds(ctx context.Context, tenantID int64, at time.Time) (int64, error) {
	const query = `UPDATE legal_holds SET status = 'expired', updated_at = ?
		WHERE tenant_id = ? AND status = 'active' AND expires_at IS NOT NULL AND expires_at <= ?`
	result, err := r.q.ExecContext(ctx, query, at, tenantID, at)
	if err != nil {
		return 0, fmt.Errorf("expire legal holds: %w", err)
	}
	n, err := result.RowsAffected()
	return n, err
}

func (r *Repository) OnHold(ctx context.Context, tenantID int64, entityType string, entityID int64,
	at time.Time) (bool, error) {
	const query = `SELECT COUNT(*) FROM legal_holds
		WHERE tenant_id = ? AND entity_type = ? AND entity_id = ? AND status = 'active'
		  AND (expires_at IS NULL OR expires_at > ?)`
	var n int
	if err := r.q.GetContext(ctx, &n, query, tenantID, entityType, entityID, at); err != nil {
		return false, fmt.Errorf("read legal hold for row: %w", err)
	}
	return n > 0, nil
}

// Candidates is the rows older than the cut-off that still carry the value a policy targets.
// The query per category is the module's knowledge, and v_purge_candidates exists for the
// reports that want the whole picture.
func (r *Repository) Candidates(ctx context.Context, category Category, tenantID int64,
	cutoff time.Time, limit int) ([]Candidate, error) {
	queries := map[Category]string{
		CategoryVisitorIdentity: `SELECT 'visitors' AS entity_type, id AS entity_id,
			id_number AS value FROM visitors
			WHERE tenant_id = ? AND id_number IS NOT NULL AND id_number_purged_at IS NULL
			  AND created_at < ? ORDER BY id LIMIT ?`,
		CategoryVisitorPhoto: `SELECT 'visitors' AS entity_type, id AS entity_id,
			'' AS value FROM visitors
			WHERE tenant_id = ? AND photo_path IS NOT NULL AND photo_purged_at IS NULL
			  AND created_at < ? ORDER BY id LIMIT ?`,
		CategoryPassDocument: `SELECT 'passes' AS entity_type, id AS entity_id,
			subject_document_no AS value FROM passes
			WHERE tenant_id = ? AND subject_document_no IS NOT NULL
			  AND subject_document_purged_at IS NULL AND created_at < ?
			ORDER BY id LIMIT ?`,
		CategoryAttachment: `SELECT 'pass_attachments' AS entity_type, id AS entity_id,
			'' AS value FROM pass_attachments
			WHERE tenant_id = ? AND purged_at IS NULL AND created_at < ?
			ORDER BY id LIMIT ?`,
		CategoryNotifyAddress: `SELECT 'notifications' AS entity_type, id AS entity_id,
			to_address AS value FROM notifications
			WHERE tenant_id = ? AND to_address IS NOT NULL AND to_address_purged_at IS NULL
			  AND created_at < ? ORDER BY id LIMIT ?`,
	}
	query, ok := queries[category]
	if !ok {
		return nil, errs.BadField("data_category", "this category cannot be purged")
	}
	var out []Candidate
	if err := r.q.SelectContext(ctx, &out, query, tenantID, cutoff, limit); err != nil {
		return nil, fmt.Errorf("read purge candidates: %w", err)
	}
	return out, nil
}

// Apply erases the value in place: null, anonymise and archive all end with a row that is still
// there and no longer carries the personal data. The stamp is written in the same statement as
// the blank, so a row cannot end up purged without saying when, which is what makes a second
// run a no-op.
func (r *Repository) Apply(ctx context.Context, category Category, tenantID, id int64,
	action PurgeAction, at time.Time) error {
	var err error
	switch category {
	case CategoryVisitorIdentity:
		if action == ActionAnonymise {
			_, err = r.q.ExecContext(ctx, `UPDATE visitors
				SET id_number = SHA2(CONCAT(tenant_id, '|', id_number), 256),
				    id_number_purged_at = ?, updated_at = ?
				WHERE tenant_id = ? AND id = ? AND id_number_purged_at IS NULL`,
				at, at, tenantID, id)
		} else {
			_, err = r.q.ExecContext(ctx, `UPDATE visitors
				SET id_number = NULL, id_number_purged_at = ?, updated_at = ?
				WHERE tenant_id = ? AND id = ? AND id_number_purged_at IS NULL`, at, at, tenantID, id)
		}
	case CategoryVisitorPhoto:
		_, err = r.q.ExecContext(ctx, `UPDATE visitors
			SET photo_path = NULL, photo_purged_at = ?, updated_at = ?
			WHERE tenant_id = ? AND id = ? AND photo_purged_at IS NULL`, at, at, tenantID, id)
	case CategoryPassDocument:
		if action == ActionAnonymise {
			_, err = r.q.ExecContext(ctx, `UPDATE passes
				SET subject_document_no = SHA2(CONCAT(tenant_id, '|', subject_document_no), 256),
				    subject_document_purged_at = ?, updated_at = ?
				WHERE tenant_id = ? AND id = ? AND subject_document_purged_at IS NULL`,
				at, at, tenantID, id)
		} else {
			_, err = r.q.ExecContext(ctx, `UPDATE passes
				SET subject_document_no = NULL, subject_document_purged_at = ?, updated_at = ?
				WHERE tenant_id = ? AND id = ? AND subject_document_purged_at IS NULL`,
				at, at, tenantID, id)
		}
	case CategoryAttachment:
		// The path, the original name and the digest can each name the person the document
		// was about, so the stamp alone is not enough: the content reference goes too.
		_, err = r.q.ExecContext(ctx, `UPDATE pass_attachments
			SET file_path = '', original_name = NULL, mime_type = NULL, sha256 = NULL,
			    purged_at = ?
			WHERE tenant_id = ? AND id = ? AND purged_at IS NULL`, at, tenantID, id)
	case CategoryNotifyAddress:
		_, err = r.q.ExecContext(ctx, `UPDATE notifications
			SET to_address = NULL, to_address_purged_at = ?, updated_at = ?
			WHERE tenant_id = ? AND id = ? AND to_address_purged_at IS NULL`, at, at, tenantID, id)
	default:
		return errs.BadField("data_category", "this category cannot be purged")
	}
	if err != nil {
		return fmt.Errorf("purge %s row %d: %w", category, id, err)
	}
	return nil
}

// Delete removes a row whose entire purpose is the personal data. It is only offered for
// attachments: a visitor is a visit history and a pass is a trail, so deleting either would
// erase evidence the compliance module exists to protect.
func (r *Repository) Delete(ctx context.Context, category Category, tenantID, id int64) error {
	if category != CategoryAttachment {
		return errs.BadField("data_category", "this category cannot be deleted row by row")
	}
	if _, err := r.q.ExecContext(ctx, `DELETE FROM pass_attachments
		WHERE tenant_id = ? AND id = ? AND purged_at IS NULL`, tenantID, id); err != nil {
		return fmt.Errorf("delete %s row %d: %w", category, id, err)
	}
	return nil
}

// InsertArchive files the receipt for an archive action. It is written in the same transaction
// as the erase and the run's finish, so a receipt never exists for a row that was not touched.
func (r *Repository) InsertArchive(ctx context.Context, a Archive, at time.Time) error {
	const query = `
		INSERT INTO data_purge_archives
			(tenant_id, purge_run_id, data_category, entity_type, entity_id, value_hash,
			 archived_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := r.q.ExecContext(ctx, query, a.TenantID, a.PurgeRunID, a.DataCategory,
		a.EntityType, a.EntityID, a.ValueHash, at, at); err != nil {
		return fmt.Errorf("file purge archive receipt: %w", err)
	}
	return nil
}

// The purge ledger.

const purgeColumns = `id, tenant_id, retention_policy_id, data_category, purge_action,
	is_dry_run, status, rows_scanned, rows_purged, rows_skipped_on_hold, cutoff_at,
	error_message, started_at, finished_at, requested_by_user_id, created_at`

func (r *Repository) InsertPurgeRun(ctx context.Context, run PurgeRun, at time.Time) (int64, error) {
	const query = `
		INSERT INTO data_purge_runs
			(tenant_id, retention_policy_id, data_category, purge_action, is_dry_run, status,
			 cutoff_at, started_at, requested_by_user_id, created_at)
		VALUES (?, ?, ?, ?, ?, 'running', ?, ?, ?, ?)`
	result, err := r.q.ExecContext(ctx, query, run.TenantID, run.PolicyID, run.DataCategory,
		run.PurgeAction, run.IsDryRun, run.CutoffAt, at, run.RequestedBy, at)
	if err != nil {
		return 0, fmt.Errorf("file purge run: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read purge run id: %w", err)
	}
	return id, nil
}

func (r *Repository) FinishPurgeRun(ctx context.Context, tenantID, id int64, scanned, purged,
	onHold int64, status string, message *string, at time.Time) error {
	const query = `UPDATE data_purge_runs
		SET status = ?, rows_scanned = ?, rows_purged = ?, rows_skipped_on_hold = ?,
		    error_message = ?, finished_at = ?
		WHERE tenant_id = ? AND id = ? AND status = 'running'`
	if _, err := r.q.ExecContext(ctx, query, status, scanned, purged, onHold, message, at,
		tenantID, id); err != nil {
		return fmt.Errorf("finish purge run: %w", err)
	}
	return nil
}

func (r *Repository) PurgeRuns(ctx context.Context, tenantID int64, limit int) ([]PurgeRun, error) {
	var out []PurgeRun
	if err := r.q.SelectContext(ctx, &out, `SELECT `+purgeColumns+` FROM data_purge_runs
		WHERE tenant_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`, tenantID, limit); err != nil {
		return nil, fmt.Errorf("read purge runs: %w", err)
	}
	return out, nil
}

// AuditRows is the verify walk: the tenant's own stream, in seq order, bounded by the range the
// caller asked for and by a batch size so one request cannot read a decade of history.
func (r *Repository) AuditRows(ctx context.Context, tenantID int64, fromSeq, toSeq uint64,
	limit int) ([]audit.ChainRow, error) {
	const query = `SELECT seq, tenant_id, actor_type, action,
		COALESCE(subject_type, '') AS subject_type, COALESCE(subject_id, 0) AS subject_id,
		occurred_at, COALESCE(prev_hash, '') AS prev_hash, COALESCE(row_hash, '') AS row_hash
		FROM audit_logs
		WHERE tenant_id = ? AND stream_key = 'audit' AND seq >= ? AND seq <= ?
		ORDER BY seq LIMIT ?`
	var out []audit.ChainRow
	if err := r.q.SelectContext(ctx, &out, query, tenantID, fromSeq, toSeq, limit); err != nil {
		return nil, fmt.Errorf("read audit chain: %w", err)
	}
	return out, nil
}

const verificationColumns = `id, tenant_id, stream_key, from_seq, to_seq, rows_checked,
	outcome, first_mismatch_seq, first_mismatch_hash, tip_hash, duration_ms,
	checked_by_user_id, checked_at, note, created_at`

func (r *Repository) InsertVerification(ctx context.Context, v Verification,
	at time.Time) (int64, error) {
	const query = `
		INSERT INTO audit_verifications
			(tenant_id, stream_key, from_seq, to_seq, rows_checked, outcome,
			 first_mismatch_seq, first_mismatch_hash, tip_hash, duration_ms,
			 checked_by_user_id, checked_at, note, created_at)
		VALUES (?, 'audit', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	result, err := r.q.ExecContext(ctx, query, v.TenantID, v.FromSeq, v.ToSeq, v.RowsChecked,
		v.Outcome, v.FirstMismatchSeq, v.FirstMismatchHash, v.TipHash, v.DurationMS,
		v.CheckedBy, at, v.Note, at)
	if err != nil {
		return 0, fmt.Errorf("record audit verification: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read verification id: %w", err)
	}
	return id, nil
}

func (r *Repository) Verifications(ctx context.Context, tenantID int64,
	limit int) ([]Verification, error) {
	var out []Verification
	if err := r.q.SelectContext(ctx, &out, `SELECT `+verificationColumns+`
		FROM audit_verifications WHERE tenant_id = ?
		ORDER BY checked_at DESC, id DESC LIMIT ?`, tenantID, limit); err != nil {
		return nil, fmt.Errorf("read audit verifications: %w", err)
	}
	return out, nil
}

// ModuleEnabled is the worker's per-tenant switch check.
func (r *Repository) ModuleEnabled(ctx context.Context, tenantID int64) (bool, error) {
	const query = `SELECT COUNT(*) FROM tenant_modules
		WHERE tenant_id = ? AND module_key = ? AND enabled = 1`
	var n int
	if err := r.q.GetContext(ctx, &n, query, tenantID, ModuleKey); err != nil {
		return false, fmt.Errorf("read compliance module flag: %w", err)
	}
	return n > 0, nil
}
