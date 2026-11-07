package rollcall

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/httpx"
)

var errsSummaryRequired = errs.BadField("summary",
	"say what happened to the people still unaccounted for")

type Repository struct{ pool *sqlx.DB }

func NewRepository(pool *sqlx.DB) *Repository { return &Repository{pool: pool} }

const incidentColumns = `i.id, i.uuid, i.tenant_id, i.site_id, COALESCE(s.name, '') AS site_name,
	i.gate_id, i.kind, i.status, i.started_at, i.started_by_user_id, i.roster_frozen_at,
	i.expected_person_count, i.accounted_count, i.muster_point, i.closed_at,
	i.closed_by_user_id, i.summary, i.notes, i.created_at`

const incidentSource = ` FROM evacuation_incidents i
	LEFT JOIN sites s ON s.id = i.site_id AND s.tenant_id = i.tenant_id`

// NewIncident is an alarm as it is raised.
type NewIncident struct {
	TenantID    int64
	SiteID      int64
	GateID      int64
	StartedBy   int64
	Kind        string
	MusterPoint string
	Notes       string
}

func (r *Repository) Create(ctx context.Context, c db.Conn, in NewIncident) (int64, error) {
	const query = `
		INSERT INTO evacuation_incidents
			(tenant_id, uuid, site_id, gate_id, kind, status, started_by_user_id,
			 roster_frozen_at, muster_point, notes)
		VALUES (?, ?, ?, ?, ?, 'open', ?, UTC_TIMESTAMP(6), ?, ?)`
	result, err := c.ExecContext(ctx, query, in.TenantID, uuid.NewString(), in.SiteID,
		nullID(in.GateID), in.Kind, nullID(in.StartedBy), nullText(in.MusterPoint),
		nullText(in.Notes))
	if err != nil {
		return 0, fmt.Errorf("open roll-call incident: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read incident id: %w", err)
	}
	return id, nil
}

// Freeze copies everybody the open-movements view says is on site into the roster. The copy is
// the point: people marked accounted later must not vanish from the board, and people who
// arrive after the alarm must not swell it.
func (r *Repository) Freeze(ctx context.Context, c db.Conn, tenantID, siteID, incidentID int64) (int64, error) {
	const query = `
		INSERT INTO evacuation_roster
			(tenant_id, evacuation_incident_id, person_key, person_type, pass_id, movement_id,
			 gate_session_id, name_snapshot, phone_snapshot, plate_snapshot, last_seen_at,
			 last_seen_gate_id, department_id, status, source)
		SELECT ?, ?, CONCAT('pass:', m.pass_id),
		       CASE p.source_module_key
		           WHEN 'visitor_management' THEN 'visitor'
		           WHEN 'staff_outpass'      THEN 'staff'
		           WHEN 'contractor_access'  THEN 'contractor'
		           WHEN 'vehicle_tracking'   THEN 'driver'
		           ELSE 'other'
		       END,
		       m.pass_id, m.id, m.gate_session_id,
		       COALESCE(NULLIF(p.subject_name, ''), NULLIF(m.subject_name, ''), p.pass_no),
		       p.subject_phone, m.plate, m.occurred_at, m.gate_id, p.department_id,
		       'unaccounted', 'system'
		FROM   v_open_movements m
		JOIN   passes p ON p.id = m.pass_id AND p.tenant_id = m.tenant_id
		WHERE  m.tenant_id = ? AND m.site_id = ?
		ON DUPLICATE KEY UPDATE id = id`
	result, err := c.ExecContext(ctx, query, tenantID, incidentID, tenantID, siteID)
	if err != nil {
		return 0, fmt.Errorf("freeze roll-call roster: %w", err)
	}
	added, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count frozen roster: %w", err)
	}
	return added, nil
}

func (r *Repository) ByID(ctx context.Context, tenantID, id int64) (Incident, error) {
	query := "SELECT " + incidentColumns + incidentSource + " WHERE i.tenant_id = ? AND i.id = ?"
	var incident Incident
	if err := r.pool.GetContext(ctx, &incident, query, tenantID, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Incident{}, errs.NotFound("roll-call incident " + fmt.Sprint(id))
		}
		return Incident{}, fmt.Errorf("read incident %d: %w", id, err)
	}
	return incident, nil
}

// OpenAt is the one open incident at a site, if there is one. Two alarms at once at the same
// gate is not a second roster, it is one roster with more shouting.
func (r *Repository) OpenAt(ctx context.Context, tenantID, siteID int64) (Incident, error) {
	query := "SELECT " + incidentColumns + incidentSource +
		" WHERE i.tenant_id = ? AND i.site_id = ? AND i.status IN ('open', 'in_progress')" +
		" ORDER BY i.id DESC LIMIT 1"
	var incident Incident
	if err := r.pool.GetContext(ctx, &incident, query, tenantID, siteID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Incident{}, errs.NotFound("open roll-call incident")
		}
		return Incident{}, fmt.Errorf("read open incident: %w", err)
	}
	return incident, nil
}

// IncidentFilter is the register's questions: this site, this status, this far back.
type IncidentFilter struct {
	TenantID int64
	SiteID   int64
	Status   string
	Before   *httpx.Cursor
	Limit    int
}

func (r *Repository) Incidents(ctx context.Context, f IncidentFilter) ([]Incident, error) {
	clauses := []string{"i.tenant_id = ?"}
	args := []any{f.TenantID}
	if f.SiteID != 0 {
		clauses = append(clauses, "i.site_id = ?")
		args = append(args, f.SiteID)
	}
	if f.Status != "" {
		clauses = append(clauses, "i.status = ?")
		args = append(args, f.Status)
	}
	if f.Before != nil {
		clauses = append(clauses, "(i.started_at < ? OR (i.started_at = ? AND i.id < ?))")
		args = append(args, f.Before.At, f.Before.At, f.Before.ID)
	}
	query := "SELECT " + incidentColumns + incidentSource + " WHERE " +
		strings.Join(clauses, " AND ") + " ORDER BY i.started_at DESC, i.id DESC LIMIT ?"
	args = append(args, f.Limit)
	var rows []Incident
	if err := r.pool.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list incidents: %w", err)
	}
	return rows, nil
}

// RosterFilter is one muster point's board.
type RosterFilter struct {
	TenantID   int64
	IncidentID int64
	Status     string
	Search     string
	Limit      int
}

func (r *Repository) Roster(ctx context.Context, f RosterFilter) ([]Person, error) {
	clauses := []string{"r.tenant_id = ?", "r.evacuation_incident_id = ?"}
	args := []any{f.TenantID, f.IncidentID}
	if f.Status != "" {
		clauses = append(clauses, "r.status = ?")
		args = append(args, f.Status)
	}
	if f.Search != "" {
		clauses = append(clauses, "(r.name_snapshot LIKE ? OR r.person_key LIKE ?)")
		like := "%" + f.Search + "%"
		args = append(args, like, like)
	}
	// Unaccounted first, because that is the question the board is asked: who is still out.
	query := `
		SELECT r.id, r.tenant_id, r.evacuation_incident_id, r.person_key, r.person_type,
		       r.pass_id, COALESCE(p.pass_no, '') AS pass_no, r.movement_id,
		       r.gate_session_id, r.name_snapshot, r.phone_snapshot, r.company_snapshot,
		       r.plate_snapshot, r.last_seen_at, r.last_seen_gate_id, r.department_id,
		       r.status, r.marked_by_user_id, r.marked_at, r.marked_gate_id, r.muster_point,
		       r.source, r.note, r.created_at
		FROM   evacuation_roster r
		LEFT JOIN passes p ON p.id = r.pass_id AND p.tenant_id = r.tenant_id
		WHERE  ` + strings.Join(clauses, " AND ") + `
		ORDER  BY FIELD(r.status, 'missing', 'deceased', 'unaccounted', 'injured', 'accounted',
		                'evacuated', 'not_on_site'), r.name_snapshot
		LIMIT  ?`
	args = append(args, f.Limit)
	var rows []Person
	if err := r.pool.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list roster: %w", err)
	}
	return rows, nil
}

// PersonForUpdate locks one roster row for the length of a mark. Two wardens tapping the same
// name must not interleave into two transitions that disagree about where they came from.
func (r *Repository) PersonForUpdate(ctx context.Context, c db.Conn, tenantID, incidentID,
	id int64) (Person, error) {
	query := `
		SELECT id, tenant_id, evacuation_incident_id, person_key, person_type, pass_id,
		       '' AS pass_no, movement_id, gate_session_id, name_snapshot, phone_snapshot,
		       company_snapshot, plate_snapshot, last_seen_at, last_seen_gate_id,
		       department_id, status, marked_by_user_id, marked_at, marked_gate_id,
		       muster_point, source, note, created_at
		FROM   evacuation_roster
		WHERE  tenant_id = ? AND evacuation_incident_id = ? AND id = ?
		FOR UPDATE`
	var person Person
	if err := c.GetContext(ctx, &person, query, tenantID, incidentID, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Person{}, errs.NotFound("roster entry " + fmt.Sprint(id))
		}
		return Person{}, fmt.Errorf("read roster entry %d: %w", id, err)
	}
	return person, nil
}

func (r *Repository) Person(ctx context.Context, tenantID, incidentID, id int64) (Person, error) {
	query := `
		SELECT r.id, r.tenant_id, r.evacuation_incident_id, r.person_key, r.person_type,
		       r.pass_id, COALESCE(p.pass_no, '') AS pass_no, r.movement_id,
		       r.gate_session_id, r.name_snapshot, r.phone_snapshot, r.company_snapshot,
		       r.plate_snapshot, r.last_seen_at, r.last_seen_gate_id, r.department_id,
		       r.status, r.marked_by_user_id, r.marked_at, r.marked_gate_id, r.muster_point,
		       r.source, r.note, r.created_at
		FROM   evacuation_roster r
		LEFT JOIN passes p ON p.id = r.pass_id AND p.tenant_id = r.tenant_id
		WHERE  r.tenant_id = ? AND r.evacuation_incident_id = ? AND r.id = ?`
	var person Person
	if err := r.pool.GetContext(ctx, &person, query, tenantID, incidentID, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Person{}, errs.NotFound("roster entry " + fmt.Sprint(id))
		}
		return Person{}, fmt.Errorf("read roster entry %d: %w", id, err)
	}
	return person, nil
}

// MarkRequest is one transition as the service validated it.
type MarkInput struct {
	TenantID    int64
	IncidentID  int64
	PersonID    int64
	From        string
	To          string
	MarkedBy    int64
	GateID      int64
	GateSession int64
	MusterPoint string
	Note        string
	Source      string
}

func (r *Repository) Mark(ctx context.Context, c db.Conn, in MarkInput) error {
	const update = `
		UPDATE evacuation_roster
		SET    status = ?, marked_by_user_id = ?, marked_at = UTC_TIMESTAMP(6),
		       marked_gate_id = ?, muster_point = COALESCE(?, muster_point), note = ?, source = ?
		WHERE  id = ? AND tenant_id = ? AND evacuation_incident_id = ?`
	if _, err := c.ExecContext(ctx, update, in.To, nullID(in.MarkedBy), nullID(in.GateID),
		nullText(in.MusterPoint), nullText(in.Note), in.Source, in.PersonID, in.TenantID,
		in.IncidentID); err != nil {
		return fmt.Errorf("mark roster entry %d: %w", in.PersonID, err)
	}
	const insert = `
		INSERT INTO roll_call_marks
			(tenant_id, evacuation_incident_id, evacuation_roster_id, from_status, to_status,
			 marked_by_user_id, gate_id, gate_session_id, muster_point, note)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if _, err := c.ExecContext(ctx, insert, in.TenantID, in.IncidentID, in.PersonID,
		nullText(in.From), in.To, nullID(in.MarkedBy), nullID(in.GateID),
		nullID(in.GateSession), nullText(in.MusterPoint), nullText(in.Note)); err != nil {
		return fmt.Errorf("record roll-call mark: %w", err)
	}
	return nil
}

// Recount rewrites the two totals from the roster itself rather than incrementing a counter:
// a counter drifts after a reload, a recount cannot.
func (r *Repository) Recount(ctx context.Context, c db.Conn, tenantID, incidentID int64) (Counts, error) {
	const update = `
		UPDATE evacuation_incidents i
		SET    expected_person_count = (
		           SELECT COUNT(*) FROM evacuation_roster r
		           WHERE  r.tenant_id = i.tenant_id AND r.evacuation_incident_id = i.id),
		       accounted_count = (
		           SELECT COUNT(*) FROM evacuation_roster r
		           WHERE  r.tenant_id = i.tenant_id AND r.evacuation_incident_id = i.id
		           AND    r.status IN ('accounted', 'evacuated', 'injured', 'not_on_site'))
		WHERE  i.id = ? AND i.tenant_id = ?`
	if _, err := c.ExecContext(ctx, update, incidentID, tenantID); err != nil {
		return Counts{}, fmt.Errorf("recount incident %d: %w", incidentID, err)
	}
	var counts Counts
	const read = `
		SELECT expected_person_count AS expected, accounted_count AS accounted
		FROM   evacuation_incidents WHERE id = ? AND tenant_id = ?`
	if err := c.GetContext(ctx, &counts, read, incidentID, tenantID); err != nil {
		return Counts{}, fmt.Errorf("read incident counts: %w", err)
	}
	return counts, nil
}

// Settle moves an incident to its end. It is a compare-and-set on the open states, so two
// supervisors pressing close cannot both believe they were the one who closed it.
func (r *Repository) Settle(ctx context.Context, c db.Conn, tenantID, id int64, status, summary string,
	closedBy int64) (bool, error) {
	const query = `
		UPDATE evacuation_incidents
		SET    status = ?, closed_at = UTC_TIMESTAMP(6), closed_by_user_id = ?,
		       summary = COALESCE(NULLIF(?, ''), summary)
		WHERE  id = ? AND tenant_id = ? AND status IN ('open', 'in_progress')`
	result, err := c.ExecContext(ctx, query, status, nullID(closedBy), summary, id, tenantID)
	if err != nil {
		return false, fmt.Errorf("settle incident %d: %w", id, err)
	}
	return db.RowsChanged(result)
}

// Marks is the history behind one incident, or behind one person when rosterID is given.
func (r *Repository) Marks(ctx context.Context, tenantID, incidentID, rosterID int64,
	limit int) ([]Mark, error) {
	query := `
		SELECT m.id, m.tenant_id, m.evacuation_incident_id, m.evacuation_roster_id,
		       m.from_status, m.to_status, m.marked_by_user_id, m.gate_id, m.gate_session_id,
		       m.muster_point, m.occurred_at, m.note, COALESCE(r.name_snapshot, '') AS name_snapshot
		FROM   roll_call_marks m
		LEFT JOIN evacuation_roster r ON r.id = m.evacuation_roster_id AND r.tenant_id = m.tenant_id
		WHERE  m.tenant_id = ? AND m.evacuation_incident_id = ?`
	args := []any{tenantID, incidentID}
	if rosterID != 0 {
		query += " AND m.evacuation_roster_id = ?"
		args = append(args, rosterID)
	}
	query += " ORDER BY m.occurred_at DESC, m.id DESC LIMIT ?"
	args = append(args, limit)
	var rows []Mark
	if err := r.pool.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, fmt.Errorf("list roll-call marks: %w", err)
	}
	return rows, nil
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
