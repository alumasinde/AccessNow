package visitor

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/httpx"
)

type Repository struct{ q db.Conn }

func NewRepository(q db.Conn) *Repository { return &Repository{q: q} }

func (r *Repository) In(c db.Conn) *Repository { bound := *r; bound.q = c; return &bound }

// The desk shows a number and a colour, so a visit is always read with the pass it hangs
// off. The join is on the composite key, so a pass from another tenant can never appear.
const visitColumns = `v.id, v.tenant_id, v.pass_id, v.visitor_id, v.visitor_company_id,
	v.host_user_id, v.host_name_snapshot, v.host_phone_snapshot, v.department_id, v.purpose,
	v.people_count, v.escort_required, v.badge_no, v.is_walk_in, v.status, v.host_response,
	v.host_responded_at, v.expected_from, v.expected_until, v.checked_in_at,
	v.checked_in_gate_id, v.check_in_movement_id, v.checked_out_at, v.checked_out_gate_id,
	v.check_out_movement_id, v.created_at, p.pass_no, p.status AS pass_status,
	per.first_name AS visitor_first_name, per.last_name AS visitor_last_name`

const visitFrom = `
	FROM   visits v
	JOIN   passes p ON p.id = v.pass_id AND p.tenant_id = v.tenant_id
	JOIN   visitors per ON per.id = v.visitor_id AND per.tenant_id = v.tenant_id`

const visitorColumns = `id, tenant_id, visitor_company_id, first_name, last_name, email, phone,
	id_type, id_number, badge_no, preferred_host_user_id, status, visit_count, last_visited_at`

// one reads a single visitor and turns a miss into sql.ErrNoRows, which is what
// FindOrCreateVisitor needs to decide between matching and inserting.
func one(ctx context.Context, q db.Conn, query string, args ...any) (Visitor, error) {
	var v Visitor
	err := q.GetContext(ctx, &v, query, args...)
	if errors.Is(err, sql.ErrNoRows) {
		return Visitor{}, sql.ErrNoRows
	}
	if err != nil {
		return Visitor{}, fmt.Errorf("read visitor: %w", err)
	}
	return v, nil
}

// boolInt writes the flags the CHECK constraints expect: 0 or 1, never NULL.
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func peopleCount(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

// FindOrCreateVisitor matches on email first because it is unique per tenant, then on the
// phone and name pair. A walk-in with no contact details becomes a new row, which is the
// honest answer: the desk did not recognise anybody.
func (r *Repository) FindOrCreateVisitor(ctx context.Context, tenantID int64, in NewVisitor, createdBy int64) (Visitor, error) {
	if found, err := r.matchVisitor(ctx, tenantID, in); err == nil {
		return found, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Visitor{}, err
	}

	const insert = `
		INSERT INTO visitors
			(tenant_id, visitor_company_id, first_name, last_name, email, phone,
			 id_type, id_number, preferred_host_user_id, created_by)
		VALUES (?, NULLIF(?, 0), ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''),
		        NULLIF(?, 0), NULLIF(?, 0))`
	result, err := r.q.ExecContext(ctx, insert,
		tenantID, in.CompanyID, in.FirstName, in.LastName, in.Email, in.Phone,
		in.IDType, in.IDNumber, in.HostUserID, createdBy)
	if err != nil {
		return Visitor{}, fmt.Errorf("create visitor: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Visitor{}, fmt.Errorf("read visitor id: %w", err)
	}
	return r.VisitorByID(ctx, tenantID, id)
}

func (r *Repository) matchVisitor(ctx context.Context, tenantID int64, in NewVisitor) (Visitor, error) {
	if in.Email != "" {
		return one(ctx, r.q, `SELECT `+visitorColumns+` FROM visitors
			WHERE tenant_id = ? AND email = ? AND deleted_at IS NULL`, tenantID, in.Email)
	}
	if in.Phone == "" {
		return Visitor{}, sql.ErrNoRows
	}
	return one(ctx, r.q, `SELECT `+visitorColumns+` FROM visitors
		WHERE tenant_id = ? AND phone = ? AND first_name = ? AND last_name = ? AND deleted_at IS NULL
		LIMIT 1`, tenantID, in.Phone, in.FirstName, in.LastName)
}

func (r *Repository) VisitorByID(ctx context.Context, tenantID, id int64) (Visitor, error) {
	return one(ctx, r.q, `SELECT `+visitorColumns+` FROM visitors WHERE id = ? AND tenant_id = ?`, id, tenantID)
}

// CreateVisit hangs the detail row off the pass Core just issued. uk_visits_pass makes a
// second visit for the same pass impossible, which is what keeps a retried request honest.
func (r *Repository) CreateVisit(ctx context.Context, in NewVisit) (int64, error) {
	const query = `
		INSERT INTO visits
			(tenant_id, pass_id, visitor_id, visitor_company_id, host_user_id, host_name_snapshot,
			 department_id, purpose, people_count, escort_required, badge_no,
			 is_walk_in, status, expected_from, expected_until)
		VALUES (?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?)`
	result, err := r.q.ExecContext(ctx, query,
		in.TenantID, in.PassID, in.VisitorID, in.CompanyID, in.HostUserID, in.HostName,
		in.DepartmentID, in.Purpose, peopleCount(in.PeopleCount), boolInt(in.EscortRequire),
		in.BadgeNo, boolInt(in.IsWalkIn), string(in.Status), in.ExpectedFrom, in.ExpectedUntil)
	if err != nil {
		return 0, fmt.Errorf("create visit: %w", err)
	}
	return result.LastInsertId()
}

func (r *Repository) VisitByID(ctx context.Context, tenantID, id int64) (Visit, error) {
	query := `SELECT ` + visitColumns + visitFrom + ` WHERE v.id = ? AND v.tenant_id = ?`
	var v Visit
	if err := r.q.GetContext(ctx, &v, query, id, tenantID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Visit{}, errs.NotFound("visit")
		}
		return Visit{}, fmt.Errorf("read visit: %w", err)
	}
	return v, nil
}

// VisitByPass is how the gate reaction finds its row: the movement knows the pass, and
// only this module owns a visit for it. A miss is a NotFound, which the handler reads as
// "that pass is not mine".
func (r *Repository) VisitByPass(ctx context.Context, tenantID, passID int64) (Visit, error) {
	query := `SELECT ` + visitColumns + visitFrom + ` WHERE v.pass_id = ? AND v.tenant_id = ?`
	var v Visit
	if err := r.q.GetContext(ctx, &v, query, passID, tenantID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Visit{}, errs.NotFound("visit for this pass")
		}
		return Visit{}, fmt.Errorf("read visit by pass: %w", err)
	}
	return v, nil
}

// ListFilter is the desk and host queue. Zero values do not filter.
type ListFilter struct {
	TenantID   int64
	Status     string
	HostUserID int64
	VisitorID  int64
	Limit      int
	Before     *httpx.Cursor
}

func (r *Repository) List(ctx context.Context, f ListFilter) ([]Visit, error) {
	where := []string{"v.tenant_id = ?"}
	args := []any{f.TenantID}
	if f.Status != "" {
		where = append(where, "v.status = ?")
		args = append(args, f.Status)
	}
	if f.HostUserID > 0 {
		where = append(where, "v.host_user_id = ?")
		args = append(args, f.HostUserID)
	}
	if f.VisitorID > 0 {
		where = append(where, "v.visitor_id = ?")
		args = append(args, f.VisitorID)
	}
	if f.Before != nil {
		where = append(where, "(v.created_at < ? OR (v.created_at = ? AND v.id < ?))")
		args = append(args, f.Before.At.UTC(), f.Before.At.UTC(), f.Before.ID)
	}

	query := `SELECT ` + visitColumns + visitFrom + ` WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY v.created_at DESC, v.id DESC LIMIT ?`
	visits := []Visit{}
	if err := r.q.SelectContext(ctx, &visits, query, append(args, f.Limit)...); err != nil {
		return nil, fmt.Errorf("list visits: %w", err)
	}
	return visits, nil
}

func (r *Repository) exec(ctx context.Context, query string, args ...any) error {
	if _, err := r.q.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("update visit: %w", err)
	}
	return nil
}

// move is a compare-and-set that reports whether it took the row. Zero rows is not an
// error: it is somebody else having got there first, which is exactly what the caller needs
// to know before it announces anything.
func (r *Repository) move(ctx context.Context, query string, args ...any) (bool, error) {
	result, err := r.q.ExecContext(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("update visit: %w", err)
	}
	return db.RowsChanged(result)
}

// MarkStatus moves a visit to the status the desk put on the pass. It is a compare and set
// on the status the caller read, so two answers to one pending visit cannot both apply and
// both be announced.
func (r *Repository) MarkStatus(ctx context.Context, tenantID, visitID int64, from, to Status) (bool, error) {
	return r.move(ctx, `UPDATE visits SET status = ? WHERE id = ? AND tenant_id = ? AND status = ?`,
		string(to), visitID, tenantID, string(from))
}

func (r *Repository) MarkHostResponse(ctx context.Context, tenantID, visitID int64, response string) error {
	return r.exec(ctx, `
		UPDATE visits
		SET    host_response = ?, host_responded_at = UTC_TIMESTAMP(6)
		WHERE  id = ? AND tenant_id = ?`, response, visitID, tenantID)
}

// MarkCheckIn and MarkCheckOut are compare and set, not blind writes: the gate reaction
// is delivered at least once, and a replayed row must not move checked_in_at forward or
// reopen a visit that already left.
//
// The inbound guard names the statuses that may still arrive rather than "anything except
// on_site", because a cancelled or refused visit whose badge was scanned yesterday must stay
// cancelled when an offline tablet syncs it this morning.
func (r *Repository) MarkCheckIn(ctx context.Context, tenantID, visitID, gateID, movementID int64) (bool, error) {
	return r.move(ctx, `
		UPDATE visits
		SET    status = ?, checked_in_at = UTC_TIMESTAMP(6), checked_in_gate_id = ?,
		       check_in_movement_id = ?
		WHERE  id = ? AND tenant_id = ? AND status IN (?, ?)`,
		string(StatusOnSite), num(gateID), num(movementID), visitID, tenantID,
		string(StatusPending), string(StatusApproved))
}

func (r *Repository) MarkCheckOut(ctx context.Context, tenantID, visitID, gateID, movementID int64) (bool, error) {
	return r.move(ctx, `
		UPDATE visits
		SET    status = ?, checked_out_at = UTC_TIMESTAMP(6), checked_out_gate_id = ?,
		       check_out_movement_id = ?
		WHERE  id = ? AND tenant_id = ? AND status = ?`,
		string(StatusCompleted), num(gateID), num(movementID), visitID, tenantID, string(StatusOnSite))
}

// MarkArrival is the person's own history: how many times they have been on site and when
// they were last here. The two live on visitors, not visits, because the desk screen that
// says "this is a regular" asks about the person and no join across every visit they have
// ever made is a cheap way to answer it.
//
// It is called only when MarkCheckIn actually moved the row, so a tablet syncing yesterday's
// scan this morning cannot count the same arrival twice.
func (r *Repository) MarkArrival(ctx context.Context, tenantID, visitorID int64) error {
	return r.exec(ctx, `
		UPDATE visitors
		SET    visit_count = visit_count + 1, last_visited_at = UTC_TIMESTAMP(6)
		WHERE  id = ? AND tenant_id = ?`, visitorID, tenantID)
}

// The two ways a visit ends without anybody coming. Both are the sweep's, never a caller's:
// the point of reading the rows first is that each one gets its own notice, and the point
// of naming the window in the SQL is that the clock is the database's, not whichever
// machine the cron job happened to run on.
const (
	// A guest the host said yes to, whose stated window ran out with no scan at the barrier.
	noShowCondition = `v.status = 'approved' AND v.checked_in_at IS NULL
		AND v.expected_until IS NOT NULL AND v.expected_until <= UTC_TIMESTAMP(6)`
	// Anything still open that never arrived: a pending ask nobody answered, or an approval
	// with no stated window whose pass has stopped working. A walk-in with neither date is
	// left alone because nothing has passed yet for it to have missed.
	expiredCondition = `v.status IN ('pending', 'approved') AND v.checked_in_at IS NULL
		AND ((v.expected_until IS NOT NULL AND v.expected_until <= UTC_TIMESTAMP(6))
		  OR (v.expected_until IS NULL AND p.valid_until IS NOT NULL
		      AND p.valid_until <= UTC_TIMESTAMP(6)))`
)

func (r *Repository) DueForNoShow(ctx context.Context, tenantID int64) ([]Visit, error) {
	return r.due(ctx, tenantID, noShowCondition)
}

func (r *Repository) DueForExpired(ctx context.Context, tenantID int64) ([]Visit, error) {
	return r.due(ctx, tenantID, expiredCondition)
}

func (r *Repository) due(ctx context.Context, tenantID int64, condition string) ([]Visit, error) {
	query := `SELECT ` + visitColumns + visitFrom + ` WHERE v.tenant_id = ? AND (` + condition + `)`
	rows := []Visit{}
	if err := r.q.SelectContext(ctx, &rows, query, tenantID); err != nil {
		return nil, fmt.Errorf("read visits due: %w", err)
	}
	return rows, nil
}

// CloseVisit is the sweep's compare-and-set. It reports whether it took the row so the
// second run of the same hour publishes nothing.
func (r *Repository) CloseVisit(ctx context.Context, tenantID, visitID int64, from, to Status) (bool, error) {
	return r.move(ctx, `UPDATE visits SET status = ? WHERE id = ? AND tenant_id = ? AND status = ?`,
		string(to), visitID, tenantID, string(from))
}
