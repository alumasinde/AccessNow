package offline

import (
	"context"
	"database/sql"
	"encoding/json"
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

// The batches.

// FileBatch opens a batch under the device's own name for it, and hands back the row either
// way. ON DUPLICATE KEY with LAST_INSERT_ID(id) is what makes a queue sent twice arrive as
// the same batch: the second attempt reads the first one's row instead of failing on the
// unique key, and the caller can see from FinishedAt that it is looking at a resend.
func (r *Repository) FileBatch(ctx context.Context, in NewBatch) (Batch, error) {
	const query = `
		INSERT INTO device_sync_batches
			(tenant_id, gate_device_id, gate_id, client_batch_id, kind, direction, status,
			 started_at, cache_version_from, requested_by_user_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(id)`
	result, err := r.q.ExecContext(ctx, query,
		in.TenantID, in.DeviceID, nullable(in.GateID), in.ClientID, string(in.Kind),
		in.Direction, string(in.Status), in.At.UTC(), nullable(in.CacheFrom),
		nullable(in.RequestedBy), in.At.UTC(), in.At.UTC())
	if err != nil {
		return Batch{}, fmt.Errorf("file sync batch: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Batch{}, fmt.Errorf("read sync batch id: %w", err)
	}
	return r.BatchByID(ctx, in.TenantID, id)
}

func (r *Repository) BatchByID(ctx context.Context, tenantID, id int64) (Batch, error) {
	var out Batch
	err := r.q.GetContext(ctx, &out, `SELECT `+deviceSyncColumns+`
		FROM device_sync_batches WHERE tenant_id = ? AND id = ?`, tenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Batch{}, errs.NotFound("sync batch")
	}
	if err != nil {
		return Batch{}, fmt.Errorf("read sync batch: %w", err)
	}
	return out, nil
}

// CloseBatch writes what the queue came to. It refuses to touch a batch that is already
// finished, because a device that resends a queue it has already delivered would otherwise
// rewrite the record of a shift that was reported the first time: the counts on a closed
// batch say what landed under that name, not what the second attempt managed to repeat.
func (r *Repository) CloseBatch(ctx context.Context, tenantID, id int64, t Tally,
	at time.Time) (bool, error) {
	const query = `
		UPDATE device_sync_batches
		SET    status = ?, finished_at = ?, record_count = ?, accepted_count = ?,
		       duplicate_count = ?, rejected_count = ?, conflict_count = ?,
		       device_first_at = ?, device_last_at = ?, cache_version_to = ?,
		       checksum_sha256 = ?, error_code = ?, error_message = ?
		WHERE  id = ? AND tenant_id = ? AND finished_at IS NULL`
	result, err := r.q.ExecContext(ctx, query,
		string(t.Status), at.UTC(), t.Records, t.Accepted, t.Duplicates, t.Rejected,
		t.Conflicts, nullableTime(t.FirstAt), nullableTime(t.LastAt), nullable(t.CacheTo),
		text(t.Checksum), text(t.ErrorCode), clip(t.Message), id, tenantID)
	if err != nil {
		return false, fmt.Errorf("close sync batch: %w", err)
	}
	return changed(result)
}

type BatchFilter struct {
	TenantID int64
	DeviceID int64
	GateID   int64
	Status   string
	OpenOnly bool
	Limit    int
	Before   *httpx.Cursor
}

func (r *Repository) Batches(ctx context.Context, f BatchFilter) ([]Batch, error) {
	where := []string{"tenant_id = ?"}
	args := []any{f.TenantID}
	addID(&where, &args, f.DeviceID, "gate_device_id")
	addID(&where, &args, f.GateID, "gate_id")
	add(&where, &args, f.Status, "status")
	if f.OpenOnly {
		where = append(where, "finished_at IS NULL")
	}
	if f.Before != nil {
		where = append(where, "(started_at < ? OR (started_at = ? AND id < ?))")
		args = append(args, f.Before.At.UTC(), f.Before.At.UTC(), f.Before.ID)
	}
	args = append(args, f.Limit)

	query := `SELECT ` + deviceSyncColumns + ` FROM device_sync_batches WHERE ` +
		strings.Join(where, " AND ") + ` ORDER BY started_at DESC, id DESC LIMIT ?`
	var out []Batch
	if err := r.q.SelectContext(ctx, &out, query, args...); err != nil {
		return nil, fmt.Errorf("read the batches: %w", err)
	}
	return out, nil
}

// The cached manifests.

// Held is what one device was last told to hold, and is the only record of it. The rows that
// matter are the live ones at the device's current epoch; a superseded row stays so the
// argument about what a gate did while it was cut off can be settled from the server's side.
func (r *Repository) Held(ctx context.Context, deviceID int64, version int64) ([]Manifest, error) {
	const query = `SELECT ` + manifestColumns + `
		FROM   offline_cache_manifests
		WHERE  gate_device_id = ? AND cache_version = ? AND superseded_at IS NULL`
	var out []Manifest
	if err := r.q.SelectContext(ctx, &out, query, deviceID, version); err != nil {
		return nil, fmt.Errorf("read the cache: %w", err)
	}
	return out, nil
}

// Stock is the epoch change: everything the device held is closed off, and the whole set it
// now holds is written at the new version.
//
// The full set is written even for the passes the device was not sent again, with their old
// pushed_at kept, so "what does this tablet hold at version N" is one question with one
// answer rather than a walk back through every bundle it ever pulled. The token column is
// written empty: a scan signature is the QR module's to issue, and until it exists a tablet
// honours a pass number and nothing else.
func (r *Repository) Stock(ctx context.Context, deviceID int64, at time.Time,
	rows []Manifest) error {
	if err := r.supersede(ctx, deviceID, at); err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	query := `
		INSERT INTO offline_cache_manifests
			(tenant_id, gate_device_id, gate_id, pass_id, token_hash, reason, cache_version,
			 valid_from, valid_until, pushed_at, created_at)
		VALUES ` + placeholders(len(rows), 11) + `
		ON DUPLICATE KEY UPDATE reason = VALUES(reason), valid_from = VALUES(valid_from),
		        valid_until = VALUES(valid_until), pushed_at = VALUES(pushed_at),
		        superseded_at = NULL`
	args := make([]any, 0, len(rows)*11)
	for _, row := range rows {
		args = append(args, row.TenantID, row.DeviceID, row.GateID, row.PassID, nil,
			row.Reason, row.Version, row.ValidFrom.UTC(), row.ValidUntil.UTC(), row.PushedAt.UTC(),
			at.UTC())
	}
	if _, err := r.q.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("stock the cache: %w", err)
	}
	return nil
}

func (r *Repository) supersede(ctx context.Context, deviceID int64, at time.Time) error {
	const query = `UPDATE offline_cache_manifests
		SET superseded_at = ? WHERE gate_device_id = ? AND superseded_at IS NULL`
	if _, err := r.q.ExecContext(ctx, query, at.UTC(), deviceID); err != nil {
		return fmt.Errorf("close off the old cache: %w", err)
	}
	return nil
}

// The conflicts.

// FileConflict writes one disagreement, or leaves the one already on file under the row the
// device named it by. The unique key is (tenant, device, device uuid), so a queue sent twice
// argues about the same movements twice instead of filling the desk with a backlog that is one
// backlog.
//
// The argument that was filed first is the one that stands, and nothing about it is rewritten:
// not the type, which reconcile chose by what can still be learned from the row, and above all
// not the resolution, which may already be a human's answer. A retry that carries a weaker
// opinion about the same movement must not leave the desk reading one type argued under another
// one's resolution.
func (r *Repository) FileConflict(ctx context.Context, in NewConflict) (int64, error) {
	detail, err := json.Marshal(in.Detail)
	if err != nil {
		return 0, fmt.Errorf("write the conflict detail: %w", err)
	}
	const query = `
		INSERT INTO sync_conflicts
			(tenant_id, device_sync_batch_id, gate_device_id, conflict_type,
			 device_movement_uuid, movement_id, pass_id, detail, resolution, detected_at,
			 created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(id)`
	result, err := r.q.ExecContext(ctx, query,
		in.TenantID, in.BatchID, in.DeviceID, string(in.Type), text(in.UUID),
		nullable(in.MovementID), nullable(in.PassID), string(detail),
		string(in.Resolution), in.At.UTC(), in.At.UTC())
	if err != nil {
		return 0, fmt.Errorf("file sync conflict: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read conflict id: %w", err)
	}
	return id, nil
}

func (r *Repository) ConflictByID(ctx context.Context, tenantID, id int64) (Conflict, error) {
	var out Conflict
	err := r.q.GetContext(ctx, &out, `SELECT `+conflictColumns+conflictFrom+`
		WHERE c.tenant_id = ? AND c.id = ?`, tenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Conflict{}, errs.NotFound("conflict")
	}
	if err != nil {
		return Conflict{}, fmt.Errorf("read conflict: %w", err)
	}
	return out, nil
}

type ConflictFilter struct {
	TenantID   int64
	DeviceID   int64
	BatchID    int64
	Type       string
	Unresolved bool
	Limit      int
	Before     *httpx.Cursor
}

func (r *Repository) Conflicts(ctx context.Context, f ConflictFilter) ([]Conflict, error) {
	where := []string{"c.tenant_id = ?"}
	args := []any{f.TenantID}
	addID(&where, &args, f.DeviceID, "c.gate_device_id")
	addID(&where, &args, f.BatchID, "c.device_sync_batch_id")
	add(&where, &args, f.Type, "c.conflict_type")
	if f.Unresolved {
		where = append(where, "c.resolved_at IS NULL")
	}
	if f.Before != nil {
		where = append(where, "(c.detected_at < ? OR (c.detected_at = ? AND c.id < ?))")
		args = append(args, f.Before.At.UTC(), f.Before.At.UTC(), f.Before.ID)
	}
	args = append(args, f.Limit)

	query := `SELECT ` + conflictColumns + conflictFrom + ` WHERE ` +
		strings.Join(where, " AND ") + ` ORDER BY c.detected_at DESC, c.id DESC LIMIT ?`
	var out []Conflict
	if err := r.q.SelectContext(ctx, &out, query, args...); err != nil {
		return nil, fmt.Errorf("read the conflicts: %w", err)
	}
	return out, nil
}

// Resolve writes the human's answer over the server's. A conflict that has already been
// settled stays settled: two supervisors looking at the same argument should not be able to
// leave it filed under whichever of them typed last.
func (r *Repository) Resolve(ctx context.Context, tenantID, id, by int64, resolution,
	note string, at time.Time) (bool, error) {
	const query = `UPDATE sync_conflicts
		SET resolution = ?, resolution_note = ?, resolved_by_user_id = ?, resolved_at = ?
		WHERE tenant_id = ? AND id = ? AND resolved_at IS NULL`
	result, err := r.q.ExecContext(ctx, query, resolution, note, by, at.UTC(), tenantID, id)
	if err != nil {
		return false, fmt.Errorf("resolve conflict: %w", err)
	}
	return changed(result)
}

// The desk view.

// Health is the device list, straight off Core's own view: what each tablet holds, when it
// last said anything, and how much it still owes.
func (r *Repository) Health(ctx context.Context, tenantID int64) ([]Health, error) {
	const query = `SELECT gate_device_id, tenant_id, label, COALESCE(gate_id, 0) AS gate_id,
	                       gate_name, sync_status, last_seen_at, last_sync_at, cache_version,
	                       open_batches, unresolved_conflicts
		FROM   v_device_sync_health
		WHERE  tenant_id = ?
		ORDER  BY last_sync_at IS NULL, last_sync_at ASC, gate_device_id`
	var out []Health
	if err := r.q.SelectContext(ctx, &out, query, tenantID); err != nil {
		return nil, fmt.Errorf("read device health: %w", err)
	}
	return out, nil
}

// RefreshConflicts re-counts the arguments filed under one batch and writes the number back.
// It is the resend path: a queue delivered twice keeps the counts it was first closed with,
// because those say what landed, but the second attempt can still add an argument about a row
// the first one argued about, and a batch whose conflict number has stopped matching the rows
// under it is a batch the desk cannot sort by anything.
func (r *Repository) RefreshConflicts(ctx context.Context, tenantID, batchID int64) (int, error) {
	const query = `UPDATE device_sync_batches
		SET    conflict_count = (SELECT COUNT(*) FROM sync_conflicts
		                         WHERE device_sync_batch_id = ?)
		WHERE  id = ? AND tenant_id = ?`
	if _, err := r.q.ExecContext(ctx, query, batchID, batchID, tenantID); err != nil {
		return 0, fmt.Errorf("recount the conflicts: %w", err)
	}
	const read = `SELECT conflict_count FROM device_sync_batches
		WHERE tenant_id = ? AND id = ?`
	var n int
	if err := r.q.GetContext(ctx, &n, read, tenantID, batchID); err != nil {
		return 0, fmt.Errorf("read the conflicts filed: %w", err)
	}
	return n, nil
}

// manifestColumns deliberately does not read token_hash: Stock writes it NULL until the QR
// module issues a scan signature, so a tablet holds pass numbers and nothing else.
const manifestColumns = `gate_device_id, pass_id, reason, cache_version, valid_from,
	valid_until, pushed_at, tenant_id, gate_id`

const conflictFrom = `
	FROM   sync_conflicts c
	JOIN   device_sync_batches b ON b.id = c.device_sync_batch_id
	JOIN   gate_devices gd       ON gd.id = c.gate_device_id
	LEFT   JOIN gates g          ON g.id = gd.gate_id
	LEFT   JOIN passes p         ON p.id = c.pass_id
	LEFT   JOIN gate_movements m ON m.id = c.movement_id`

func changed(result sql.Result) (bool, error) {
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count the rows: %w", err)
	}
	return n > 0, nil
}

// addID and add build the optional halves of a desk filter. A zero or an empty string is not
// a value to compare against, it is the absence of the filter.
func addID(where *[]string, args *[]any, value int64, column string) {
	if value == 0 {
		return
	}
	*where = append(*where, column+" = ?")
	*args = append(*args, value)
}

func add(where *[]string, args *[]any, value, column string) {
	if value == "" {
		return
	}
	*where = append(*where, column+" = ?")
	*args = append(*args, value)
}

func placeholders(rows, columns int) string {
	one := "(" + strings.TrimSuffix(strings.Repeat("?,", columns), ",") + ")"
	return strings.TrimSuffix(strings.Repeat(one+",", rows), ",")
}

func nullable(value int64) any {
	if value == 0 {
		return nil
	}
	return value
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}

func text(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

// clip holds a message inside the column it is written to, because a batch that failed for a
// reason longer than the column should not fail to be recorded as having failed.
func clip(value string) any {
	if value == "" {
		return nil
	}
	runes := []rune(value)
	if len(runes) <= 512 {
		return value
	}
	return string(runes[:507]) + "…"
}
