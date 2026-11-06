package staff

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

const staffColumns = `id, tenant_id, user_id, department_id, employee_no, first_name, last_name,
	job_title, phone, access_card_no, shift_pattern, status, started_on, ended_on`

const reasonColumns = `id, reason_key, name, default_duration_minutes, requires_manager,
	requires_return_scan, sort_order, status`

// The register reads the outpass with the Core pass behind it and the employee it belongs
// to, so one query answers "who is out, on what number, and did the pass keep up".
const outpassColumns = `o.id, o.tenant_id, o.pass_id, o.staff_id, o.department_id, o.site_id,
	o.reason_key, o.reason_note, o.destination, o.vehicle_plate, o.is_company_vehicle,
	o.requested_at, o.approved_at, o.approved_by_user_id, o.rejection_reason,
	o.expected_duration_minutes, o.expected_return_at, o.exited_at, o.exit_gate_id,
	o.exit_movement_id, o.return_scan_required, o.returned_at, o.return_gate_id,
	o.return_movement_id, o.actual_duration_minutes, o.is_over_duration, o.end_of_shift_at,
	o.is_unreturned, o.status, o.notes, o.created_at,
	p.pass_no, p.status AS pass_status, p.entry_count,
	CONCAT_WS(' ', s.first_name, s.last_name) AS staff_name, s.employee_no`

const outpassFrom = `
	FROM   outpasses o
	JOIN   passes p ON p.id = o.pass_id AND p.tenant_id = o.tenant_id
	JOIN   staff s  ON s.id = o.staff_id  AND s.tenant_id = o.tenant_id`

// The directory.

func (r *Repository) StaffByID(ctx context.Context, tenantID, id int64) (Staff, error) {
	return r.readStaff(ctx, ` WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL`, tenantID, id)
}

// StaffByUserID is how an employee asks for their own outpass: the desk finds the directory
// row behind the login instead of making somebody remember an employee number.
func (r *Repository) StaffByUserID(ctx context.Context, tenantID, userID int64) (Staff, error) {
	return r.readStaff(ctx, ` WHERE tenant_id = ? AND user_id = ? AND deleted_at IS NULL`, tenantID, userID)
}

func (r *Repository) readStaff(ctx context.Context, where string, args ...any) (Staff, error) {
	query := `SELECT ` + staffColumns + ` FROM staff` + where
	var s Staff
	if err := r.q.GetContext(ctx, &s, query, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Staff{}, errs.NotFound("staff member")
		}
		return Staff{}, fmt.Errorf("read staff: %w", err)
	}
	return s, nil
}

// The reason catalogue.

func (r *Repository) ReasonByKey(ctx context.Context, reasonKey string) (Reason, error) {
	const query = `SELECT ` + reasonColumns + ` FROM outpass_reasons WHERE reason_key = ?`
	var reason Reason
	if err := r.q.GetContext(ctx, &reason, query, reasonKey); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Reason{}, errs.NotFound("outpass reason " + reasonKey)
		}
		return Reason{}, fmt.Errorf("read outpass reason: %w", err)
	}
	return reason, nil
}

func (r *Repository) ListReasons(ctx context.Context) ([]Reason, error) {
	const query = `SELECT ` + reasonColumns + ` FROM outpass_reasons
		WHERE  status = 'active' ORDER BY sort_order, name`
	rows := []Reason{}
	if err := r.q.SelectContext(ctx, &rows, query); err != nil {
		return nil, fmt.Errorf("list outpass reasons: %w", err)
	}
	return rows, nil
}

// Outpasses.

func (r *Repository) Create(ctx context.Context, in NewOutpass) (int64, error) {
	const query = `
		INSERT INTO outpasses
			(tenant_id, pass_id, staff_id, department_id, site_id, reason_key, reason_note,
			 destination, vehicle_plate, is_company_vehicle, approved_at, approved_by_user_id,
			 expected_duration_minutes, expected_return_at, return_scan_required,
			 end_of_shift_at, status, notes)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	// An outpass that needs no approval is approved the moment it is filed, and the person
	// who filed it is the only approver there was.
	var approvedAt, approver any
	if in.Status == StatusApproved {
		approvedAt, approver = in.ApprovedAt, nullable(in.ApprovedByUserID)
	}

	result, err := r.q.ExecContext(ctx, query,
		in.TenantID, in.PassID, in.StaffID, in.DepartmentID, in.SiteID, in.ReasonKey,
		text(in.ReasonNote), text(in.Destination), text(in.VehiclePlate),
		boolInt(in.CompanyVehicle), approvedAt, approver, in.ExpectedDuration,
		nullableTime(in.ExpectedReturn), boolInt(in.ReturnScanRequired),
		nullableTime(in.EndOfShift), string(in.Status), text(in.Notes))
	if err != nil {
		return 0, fmt.Errorf("create outpass: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read outpass id: %w", err)
	}
	return id, nil
}

func (r *Repository) ByID(ctx context.Context, tenantID, id int64) (Outpass, error) {
	return r.read(ctx, ` WHERE o.id = ? AND o.tenant_id = ?`, id, tenantID)
}

// ByPass is how the gate reaction finds its row: the movement knows the pass, and only this
// module owns an outpass for it.
func (r *Repository) ByPass(ctx context.Context, tenantID, passID int64) (Outpass, error) {
	return r.read(ctx, ` WHERE o.pass_id = ? AND o.tenant_id = ?`, passID, tenantID)
}

func (r *Repository) read(ctx context.Context, where string, args ...any) (Outpass, error) {
	query := `SELECT ` + outpassColumns + outpassFrom + where
	var o Outpass
	if err := r.q.GetContext(ctx, &o, query, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Outpass{}, errs.NotFound("outpass")
		}
		return Outpass{}, fmt.Errorf("read outpass: %w", err)
	}
	return o, nil
}

// MarkDecision answers a request. approved_at and rejection_reason move with the status
// because the register is the audit trail a supervisor signs; a second click lands on a row
// that is no longer pending and changes nothing, which is what the caller reports.
func (r *Repository) MarkDecision(ctx context.Context, tenantID, id int64, to Status,
	actorUserID int64, note string) (bool, error) {
	query := `UPDATE outpasses SET status = ?`
	args := []any{string(to)}
	switch to {
	case StatusApproved:
		query += `, approved_at = UTC_TIMESTAMP(6), approved_by_user_id = ?`
		args = append(args, nullable(actorUserID))
	case StatusRejected:
		query += `, rejection_reason = ?`
		args = append(args, text(note))
	}
	query += ` WHERE id = ? AND tenant_id = ? AND status = ?`
	args = append(args, id, tenantID, string(StatusPending))

	result, err := r.q.ExecContext(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("decide outpass: %w", err)
	}
	return changed(result)
}

// MarkCancelled takes a request back before the employee left.
func (r *Repository) MarkCancelled(ctx context.Context, tenantID, id int64, from Status) (bool, error) {
	const query = `UPDATE outpasses SET status = ? WHERE id = ? AND tenant_id = ? AND status = ?`
	result, err := r.q.ExecContext(ctx, query, string(StatusCancelled), id, tenantID, string(from))
	if err != nil {
		return false, fmt.Errorf("cancel outpass: %w", err)
	}
	return changed(result)
}

// MarkOut starts the clock. The promised return is stamped here rather than at the request
// because the time they gave is measured from the moment the gate let them out, and a desk
// that already wrote one down keeps it.
func (r *Repository) MarkOut(ctx context.Context, tenantID, id, gateID, movementID int64) (bool, error) {
	const query = `
		UPDATE outpasses
		SET    status = ?, exited_at = UTC_TIMESTAMP(6), exit_gate_id = ?, exit_movement_id = ?,
		       expected_return_at = COALESCE(expected_return_at,
		           UTC_TIMESTAMP(6) + INTERVAL COALESCE(expected_duration_minutes, 0) MINUTE)
		WHERE  id = ? AND tenant_id = ? AND status = ?`
	result, err := r.q.ExecContext(ctx, query, string(StatusOut), nullable(gateID),
		nullable(movementID), id, tenantID, string(StatusApproved))
	if err != nil {
		return false, fmt.Errorf("mark outpass departed: %w", err)
	}
	return changed(result)
}

// MarkReturned stops it. The row can be reached from overdue or unreturned as well as out:
// an employee who was written off at the end of shift and then walked back through the
// barrier is still an employee who came back, and the register has to say so.
//
// gateID and movementID are zero for a desk close, which is how a reason that needs no
// return scan is settled.
func (r *Repository) MarkReturned(ctx context.Context, tenantID, id, gateID, movementID int64,
	minutes int, overDuration bool) (bool, error) {
	const query = `
		UPDATE outpasses
		SET    status = ?, returned_at = UTC_TIMESTAMP(6), return_gate_id = ?,
		       return_movement_id = ?, actual_duration_minutes = ?, is_over_duration = ?
		WHERE  id = ? AND tenant_id = ? AND status IN (?, ?, ?)`
	result, err := r.q.ExecContext(ctx, query, string(StatusReturned), nullable(gateID),
		nullable(movementID), minutes, boolInt(overDuration), id, tenantID,
		string(StatusOut), string(StatusOverdue), string(StatusUnreturned))
	if err != nil {
		return false, fmt.Errorf("mark outpass returned: %w", err)
	}
	return changed(result)
}

// The two escalations, each keyed on the deadline the sweep compares against, plus the
// roster's own set of open states. They are constants, never caller strings.
const (
	overdueCondition = `o.status = 'out' AND o.expected_return_at IS NOT NULL
		AND o.expected_return_at <= UTC_TIMESTAMP(6)`
	unreturnedCondition = `o.status = 'overdue' AND o.end_of_shift_at IS NOT NULL
		AND o.end_of_shift_at <= UTC_TIMESTAMP(6)`
)

// DueForOverdue and DueForUnreturned are the rows a sweep owes an escalation for, and
// Escalate is the only writer of those two statuses. Reading them first is what lets the
// sweep publish an event per row instead of one blunt notice per site.
func (r *Repository) DueForOverdue(ctx context.Context, tenantID int64) ([]Outpass, error) {
	return r.due(ctx, tenantID, overdueCondition)
}

func (r *Repository) DueForUnreturned(ctx context.Context, tenantID int64) ([]Outpass, error) {
	return r.due(ctx, tenantID, unreturnedCondition)
}

func (r *Repository) due(ctx context.Context, tenantID int64, condition string) ([]Outpass, error) {
	query := `SELECT ` + outpassColumns + outpassFrom + ` WHERE o.tenant_id = ? AND (` +
		condition + `)`
	rows := []Outpass{}
	if err := r.q.SelectContext(ctx, &rows, query, tenantID); err != nil {
		return nil, fmt.Errorf("read outpasses due: %w", err)
	}
	return rows, nil
}

// MarkEscalated is the sweep's compare-and-set: out to overdue, overdue to unreturned. It
// writes is_unreturned with the second one so the flag and the status can never disagree,
// and it moves nothing that has already been escalated by another run.
func (r *Repository) MarkEscalated(ctx context.Context, tenantID, id int64, from, to Status) (bool, error) {
	const query = `
		UPDATE outpasses
		SET    status = ?, is_unreturned = GREATEST(is_unreturned, ?)
		WHERE  id = ? AND tenant_id = ? AND status = ?`
	result, err := r.q.ExecContext(ctx, query, string(to),
		boolInt(to == StatusUnreturned), id, tenantID, string(from))
	if err != nil {
		return false, fmt.Errorf("escalate outpass: %w", err)
	}
	return changed(result)
}

// ListFilter is the outpass register. Zero values do not filter.
type ListFilter struct {
	TenantID     int64
	Status       string
	StaffID      int64
	DepartmentID int64
	ReasonKey    string
	OpenOnly     bool
	Search       string
	Limit        int
	Before       *httpx.Cursor
}

func (r *Repository) List(ctx context.Context, f ListFilter) ([]Outpass, error) {
	where := []string{"o.tenant_id = ?"}
	args := []any{f.TenantID}
	if f.Status != "" {
		where = append(where, "o.status = ?")
		args = append(args, f.Status)
	}
	if f.OpenOnly {
		where = append(where, "o.status IN ('out', 'overdue', 'unreturned')")
	}
	if f.StaffID > 0 {
		where = append(where, "o.staff_id = ?")
		args = append(args, f.StaffID)
	}
	if f.DepartmentID > 0 {
		where = append(where, "o.department_id = ?")
		args = append(args, f.DepartmentID)
	}
	if f.ReasonKey != "" {
		where = append(where, "o.reason_key = ?")
		args = append(args, f.ReasonKey)
	}
	if f.Search != "" {
		where = append(where, `(p.pass_no LIKE ? OR s.employee_no LIKE ?
			OR s.first_name LIKE ? OR s.last_name LIKE ? OR o.destination LIKE ?)`)
		for range 5 {
			args = append(args, like(f.Search))
		}
	}
	if f.Before != nil {
		where = append(where, "(o.created_at < ? OR (o.created_at = ? AND o.id < ?))")
		args = append(args, f.Before.At.UTC(), f.Before.At.UTC(), f.Before.ID)
	}

	query := `SELECT ` + outpassColumns + outpassFrom + ` WHERE ` +
		strings.Join(where, " AND ") + ` ORDER BY o.created_at DESC, o.id DESC LIMIT ?`
	rows := []Outpass{}
	if err := r.q.SelectContext(ctx, &rows, query, append(args, f.Limit)...); err != nil {
		return nil, fmt.Errorf("list outpasses: %w", err)
	}
	return rows, nil
}

// Outstanding is the roll-call roster. The view already limits it to the employees who are
// still off the site and does the minutes arithmetic, so nothing here computes a second
// answer that could disagree with it.
func (r *Repository) Outstanding(ctx context.Context, tenantID int64, limit int) ([]OutstandingRow, error) {
	const query = `
		SELECT outpass_id, pass_id, pass_no, staff_id, first_name, last_name, employee_no,
		       department_id, reason_key, expected_return_at, expected_duration_minutes,
		       return_scan_required, exited_at, minutes_out, is_overdue, status
		FROM   v_staff_out_of_site
		WHERE  tenant_id = ?
		ORDER  BY expected_return_at, exited_at
		LIMIT  ?`
	rows := []OutstandingRow{}
	if err := r.q.SelectContext(ctx, &rows, query, tenantID, limit); err != nil {
		return nil, fmt.Errorf("read staff out of site: %w", err)
	}
	return rows, nil
}

func changed(result sql.Result) (bool, error) { return db.RowsChanged(result) }

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func like(term string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(term) + "%"
}

func text(value string) any {
	if value == "" {
		return nil
	}
	return value
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
