package movements

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
)

type Repository struct{ q db.Conn }

func NewRepository(q db.Conn) *Repository { return &Repository{q: q} }

func (r *Repository) In(c db.Conn) *Repository { bound := *r; bound.q = c; return &bound }

type Record struct {
	TenantID          int64
	SiteID            int64
	GateID            int64
	PassID            int64
	GateSessionID     int64
	GuardUserID       int64
	Direction         Direction
	Result            Result
	Source            Source
	OccurredAt        time.Time
	DeviceOccurredAt  time.Time
	Reason            string
	SubjectName       string
	Plate             string
	DwellMinutes      *int
	CloseOfMovementID int64
	IdempotencyKey    string
	SyncBatchID       int64
	IsLateSync        bool
}

// Insert is idempotent on (tenant_id, idempotency_key) so an offline batch replayed twice
// lands once; uk_gate_movements_idempotency is what makes that true. LAST_INSERT_ID(id)
// in the duplicate branch hands back the row that was already there, so a replay answers
// with the original movement instead of a miss.
func (r *Repository) Insert(ctx context.Context, rec Record) (Movement, error) {
	const query = `
		INSERT INTO gate_movements
			(uuid, tenant_id, site_id, gate_id, pass_id, gate_session_id, guard_user_id,
			 direction, result, source, occurred_at, device_occurred_at, reason_note,
			 subject_name, plate, dwell_minutes, close_of_movement_id, idempotency_key,
			 sync_batch_id, is_late_sync, clock_skew_seconds)
		VALUES (?, ?, ?, ?, ?, NULLIF(?, 0), NULLIF(?, 0), ?, ?, ?, ?, ?, ?,
		        NULLIF(?, ''), NULLIF(?, ''), ?, NULLIF(?, 0), ?, NULLIF(?, 0), ?, ?)
		ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(id)`
	result, err := r.q.ExecContext(ctx, query,
		uuid.NewString(), rec.TenantID, rec.SiteID, rec.GateID, rec.PassID, rec.GateSessionID,
		rec.GuardUserID, string(rec.Direction), string(rec.Result), string(rec.Source),
		rec.OccurredAt.UTC(), nullable(rec.DeviceOccurredAt), rec.Reason,
		rec.SubjectName, rec.Plate, rec.DwellMinutes, rec.CloseOfMovementID,
		rec.IdempotencyKey, rec.SyncBatchID, rec.IsLateSync, skewSeconds(rec.OccurredAt, rec.DeviceOccurredAt))
	if err != nil {
		return Movement{}, fmt.Errorf("record movement: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Movement{}, fmt.Errorf("read movement id: %w", err)
	}
	return r.ByID(ctx, rec.TenantID, id)
}

// nullable maps a zero time to NULL, so an optional device clock never writes 0000-00-00.
func nullable(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}

// skewSeconds is how far the tablet's clock was from ours when it made the scan; the sync
// report uses it to tell a stale device from a dishonest one.
func skewSeconds(received, device time.Time) any {
	if device.IsZero() {
		return nil
	}
	return int(received.Sub(device).Seconds())
}

func (r *Repository) ByID(ctx context.Context, tenantID, id int64) (Movement, error) {
	const query = `
		SELECT id, uuid, tenant_id, site_id, gate_id, pass_id, gate_session_id, guard_user_id,
		       direction, result, source, COALESCE(reason_note, '') AS reason_note,
		       occurred_at, subject_name, plate, dwell_minutes,
		       close_of_movement_id, sync_batch_id, is_late_sync = 1 AS is_late_sync
		FROM   gate_movements WHERE id = ? AND tenant_id = ?`
	var m Movement
	err := r.q.GetContext(ctx, &m, query, id, tenantID)
	if errors.Is(err, sql.ErrNoRows) {
		return Movement{}, errs.NotFound("movement")
	}
	if err != nil {
		return Movement{}, fmt.Errorf("read movement: %w", err)
	}
	return m, nil
}

// ByIdempotency finds the movement a device already delivered. A replay is answered from
// the row that exists, so the pass is not entered twice and no second event is published.
func (r *Repository) ByIdempotency(ctx context.Context, tenantID int64, key string) (Movement, error) {
	const query = `
		SELECT id, uuid, tenant_id, site_id, gate_id, pass_id, gate_session_id, guard_user_id,
		       direction, result, source, COALESCE(reason_note, '') AS reason_note,
		       occurred_at, subject_name, plate, dwell_minutes,
		       close_of_movement_id, sync_batch_id, is_late_sync = 1 AS is_late_sync
		FROM   gate_movements
		WHERE  tenant_id = ? AND idempotency_key = ?`
	var m Movement
	err := r.q.GetContext(ctx, &m, query, tenantID, key)
	if errors.Is(err, sql.ErrNoRows) {
		return Movement{}, errs.NotFound("movement for " + key)
	}
	if err != nil {
		return Movement{}, fmt.Errorf("read movement by idempotency key: %w", err)
	}
	return m, nil
}

// OpenEntry is the row an exit should close: the view already knows what "still on site"
// means, so the pairing rule lives in one place.
func (r *Repository) OpenEntry(ctx context.Context, tenantID, passID int64) (Movement, error) {
	const query = `
		SELECT id, uuid, tenant_id, site_id, gate_id, pass_id, gate_session_id, guard_user_id,
		       direction, result, source, '' AS reason_note, occurred_at, subject_name, plate,
		       NULL AS dwell_minutes, NULL AS close_of_movement_id, NULL AS sync_batch_id,
		       0 AS is_late_sync
		FROM   v_open_movements
		WHERE  tenant_id = ? AND pass_id = ?
		ORDER  BY occurred_at DESC
		LIMIT  1`
	var m Movement
	err := r.q.GetContext(ctx, &m, query, tenantID, passID)
	if errors.Is(err, sql.ErrNoRows) {
		return Movement{}, errs.NotFound("open movement")
	}
	if err != nil {
		return Movement{}, fmt.Errorf("read open movement: %w", err)
	}
	return m, nil
}

// OnSite is the roster. gateID 0 means the whole site, which is what a roll-call wants;
// a gate tablet passes its own gate and sees its own queue.
func (r *Repository) OnSite(ctx context.Context, tenantID, siteID, gateID int64, limit int) ([]Occupant, error) {
	query := `
		SELECT id, pass_id, gate_id, subject_name, plate, occurred_at, minutes_on_site
		FROM   v_open_movements
		WHERE  tenant_id = ? AND site_id = ?`
	args := []any{tenantID, siteID}
	if gateID > 0 {
		query += ` AND gate_id = ?`
		args = append(args, gateID)
	}
	query += ` ORDER BY occurred_at DESC LIMIT ?`
	args = append(args, limit)

	// an empty roster answers with [] not null, so the tablet never parses two shapes
	roster := []Occupant{}
	if err := r.q.SelectContext(ctx, &roster, query, args...); err != nil {
		return nil, fmt.Errorf("read open movements: %w", err)
	}
	return roster, nil
}

func (r *Repository) GateCategories(ctx context.Context, gateID int64) ([]string, error) {
	const query = `SELECT category_key FROM gate_categories WHERE gate_id = ?`
	var keys []string
	if err := r.q.SelectContext(ctx, &keys, query, gateID); err != nil {
		return nil, fmt.Errorf("read gate categories: %w", err)
	}
	return keys, nil
}
