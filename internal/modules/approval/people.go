package approval

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"accessnow/internal/platform/errs"
)

// Person is one human an approver type resolves to. The contact columns travel with them
// because the notification the guide asks for is sent to the approver whose turn it is, and
// looking that up a second time per ask is how a queue becomes slow.
type Person struct {
	UserID int64   `db:"user_id"`
	Name   string  `db:"name"`
	Email  *string `db:"email"`
	Phone  *string `db:"phone"`
}

// Active reports a person who may still be handed a task. A disabled login is not a
// delegation: their asks have to move on without them.
func (p Person) Active() bool { return p.UserID != 0 }

const personColumns = `u.id AS user_id, CONCAT_WS(' ', u.first_name, u.last_name) AS name,
	u.email, u.phone`

// personAlive is the one filter every approver lookup shares. An approver who has left must
// not be able to answer, so a step never lands on them in the first place.
const personAlive = `u.status = 'active' AND u.deleted_at IS NULL`

func (r *Repository) PersonByID(ctx context.Context, tenantID, userID int64) (Person, error) {
	const query = `SELECT ` + personColumns + ` FROM users u
		WHERE u.tenant_id = ? AND u.id = ? AND ` + personAlive
	var out Person
	if err := r.q.GetContext(ctx, &out, query, tenantID, userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Person{}, errs.NotFound("user " + fmt.Sprint(userID))
		}
		return Person{}, fmt.Errorf("read person: %w", err)
	}
	return out, nil
}

// RoleHolders is everyone who holds a role right now, at this site or tenant-wide. A role
// that is empty is not an error: the engine decides what to do about an unstaffed step.
func (r *Repository) RoleHolders(ctx context.Context, tenantID int64, roleKey string, siteID int64) ([]Person, error) {
	const query = `
		SELECT ` + personColumns + `
		FROM   user_roles ur
		JOIN   roles r ON r.id = ur.role_id AND r.tenant_id = ur.tenant_id
		JOIN   users u ON u.id = ur.user_id  AND u.tenant_id = ur.tenant_id
		WHERE  ur.tenant_id = ? AND r.role_key = ?
		  AND  r.status = 'active' AND r.deleted_at IS NULL
		  AND  ` + personAlive + `
		  AND  (ur.expires_at IS NULL OR ur.expires_at > UTC_TIMESTAMP(6))
		  AND  (? = 0 OR ur.site_id IS NULL OR ur.site_id = ?)
		ORDER  BY u.last_name, u.first_name, u.id`
	var out []Person
	if err := r.q.SelectContext(ctx, &out, query, tenantID, roleKey, siteID, siteID); err != nil {
		return nil, fmt.Errorf("read role holders: %w", err)
	}
	return out, nil
}

// RoleKeysOf is the other half of the inbox: the asks addressed to a role the caller holds
// rather than to them by name.
func (r *Repository) RoleKeysOf(ctx context.Context, tenantID, userID int64) ([]string, error) {
	const query = `
		SELECT DISTINCT r.role_key
		FROM   user_roles ur
		JOIN   roles r ON r.id = ur.role_id AND r.tenant_id = ur.tenant_id
		WHERE  ur.tenant_id = ? AND ur.user_id = ? AND r.status = 'active' AND r.deleted_at IS NULL
		  AND  (ur.expires_at IS NULL OR ur.expires_at > UTC_TIMESTAMP(6))`
	var out []string
	if err := r.q.SelectContext(ctx, &out, query, tenantID, userID); err != nil {
		return nil, fmt.Errorf("read role keys: %w", err)
	}
	return out, nil
}

// SupervisorOf walks the requester's staff row to the person above them. An employee with no
// login, no staff record or no supervisor has nobody here, which the caller reads as "not
// resolvable" rather than as a broken chain.
func (r *Repository) SupervisorOf(ctx context.Context, tenantID, userID int64) (Person, error) {
	const query = `
		SELECT ` + personColumns + `
		FROM   staff s
		JOIN   staff sup ON sup.id = s.supervisor_id AND sup.tenant_id = s.tenant_id
		JOIN   users u   ON u.id = sup.user_id       AND u.tenant_id = sup.tenant_id
		WHERE  s.tenant_id = ? AND s.user_id = ? AND s.supervisor_id IS NOT NULL
		  AND  sup.status = 'active' AND ` + personAlive
	return onePerson(ctx, r, query, tenantID, userID)
}

// DepartmentHead is the person the directory names as owning a department.
func (r *Repository) DepartmentHead(ctx context.Context, tenantID, departmentID int64) (Person, error) {
	const query = `
		SELECT ` + personColumns + `
		FROM   departments d
		JOIN   users u ON u.id = d.head_user_id AND u.tenant_id = d.tenant_id
		WHERE  d.tenant_id = ? AND d.id = ? AND d.head_user_id IS NOT NULL
		  AND  ` + personAlive
	return onePerson(ctx, r, query, tenantID, departmentID)
}

func onePerson(ctx context.Context, r *Repository, query string, args ...any) (Person, error) {
	var out Person
	if err := r.q.GetContext(ctx, &out, query, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Person{}, nil
		}
		return Person{}, fmt.Errorf("read approver: %w", err)
	}
	return out, nil
}

// SiteWindow is what the matcher needs about the place a pass belongs to: the wall clock the
// tenant works to, and the hours that site counts as open.
type SiteWindow struct {
	SiteID      int64   `db:"site_id"`
	Timezone    string  `db:"timezone"`
	OpensAt     *string `db:"opens_at"`
	ClosesAt    *string `db:"closes_at"`
	WorkingDays *string `db:"working_days"`

	location *time.Location
}

// SiteWindowFor reads the site's clock and its declared business hours. The window is kept
// in sites.settings rather than in a column: a night shift in Mombasa is not a night shift
// in Nairobi, and only the site knows which it is.
func (r *Repository) SiteWindowFor(ctx context.Context, tenantID, siteID int64) (SiteWindow, error) {
	const query = `
		SELECT s.id AS site_id, s.timezone,
		       JSON_UNQUOTE(JSON_EXTRACT(s.settings, '$.business_hours.open'))  AS opens_at,
		       JSON_UNQUOTE(JSON_EXTRACT(s.settings, '$.business_hours.close')) AS closes_at,
		       JSON_UNQUOTE(JSON_EXTRACT(s.settings, '$.business_hours.days'))  AS working_days
		FROM   sites s
		WHERE  s.tenant_id = ? AND s.id = ? AND s.deleted_at IS NULL`
	var out SiteWindow
	if err := r.q.GetContext(ctx, &out, query, tenantID, siteID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SiteWindow{}, errs.NotFound("site")
		}
		return SiteWindow{}, fmt.Errorf("read site window: %w", err)
	}
	loc, err := time.LoadLocation(out.Timezone)
	if err != nil {
		// A bad zone name must not stop a pass being approved. The tenant's clock is
		// unknown, so the site is treated as always open and no after-hours rule fires.
		loc = time.UTC
	}
	out.location = loc
	return out, nil
}

// OutsideHours reports whether a request arrived while the site was shut. A day nobody
// works is shut whatever the clock says, and an undeclared window means "always open", which
// is the safe answer: a tenant that has not said when it works does not get a security
// manager step it did not ask for.
func (w SiteWindow) OutsideHours(at time.Time) bool {
	local := at.In(w.zone())
	if !w.worksOn(local.Weekday()) {
		return true
	}
	if w.OpensAt == nil || w.ClosesAt == nil {
		return false
	}
	open, ok := minuteOfDay(*w.OpensAt)
	if !ok {
		return false
	}
	close, ok := minuteOfDay(*w.ClosesAt)
	if !ok || open == close {
		return false
	}
	atMinute, ok := minuteOfDay(local.Format("15:04"))
	if !ok {
		return false
	}
	if open < close {
		return atMinute < open || atMinute >= close
	}
	// A window that ends before it starts runs through midnight, so the site is open on
	// either side of it: a 20:00-06:00 night shift is working at eleven at night, not at
	// two in the afternoon.
	return atMinute < open && atMinute >= close
}

// worksOn reads the days half of business_hours - "mon-fri", "sat,sun", "all". Anything the
// tenant did not spell out is treated as every day, so a vague setting can only ever widen
// what counts as open, never quietly add an approval step.
func (w SiteWindow) worksOn(day time.Weekday) bool {
	if w.WorkingDays == nil {
		return true
	}
	days := parseDays(*w.WorkingDays)
	if len(days) == 0 {
		return true
	}
	return days[int(day)]
}

// parseDays returns the set of weekdays a site works, indexed the way time.Weekday is. An
// empty set means every day; an unreadable one falls back to the same, never to a hold.
func parseDays(spec string) map[int]bool {
	out := map[int]bool{}
	for _, token := range strings.Split(strings.ToLower(spec), ",") {
		token = strings.TrimSpace(token)
		if token == "" || token == "all" || token == "everyday" || token == "every day" {
			continue
		}
		from, to, ok := dayRange(token)
		if !ok {
			return nil
		}
		for d := from; ; d = (d + 1) % 7 {
			out[d] = true
			if d == to {
				break
			}
		}
	}
	return out
}

// dayRange is "mon-fri" or a single "sat"; the range may run through the weekend.
func dayRange(token string) (from, to int, ok bool) {
	first, rest, ranged := strings.Cut(token, "-")
	from, ok = dayNumber(first)
	if !ok {
		return 0, 0, false
	}
	if !ranged {
		return from, from, true
	}
	to, ok = dayNumber(rest)
	if !ok {
		return 0, 0, false
	}
	return from, to, true
}

func dayNumber(name string) (int, bool) {
	name = strings.TrimSpace(name)
	for i, day := range [...]string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"} {
		if strings.HasPrefix(name, day) {
			return i, true
		}
	}
	return 0, false
}

func (w SiteWindow) zone() *time.Location {
	if w.location != nil {
		return w.location
	}
	return time.UTC
}

// minuteOfDay parses "08:30", the shape business_hours stores. Anything else is not a time
// the tenant meant, so it reports false and the caller treats the window as absent.
func minuteOfDay(hhmm string) (int, bool) {
	if len(hhmm) < 5 || hhmm[2] != ':' {
		return 0, false
	}
	h, ok := digits(hhmm[0:2])
	if !ok {
		return 0, false
	}
	m, ok := digits(hhmm[3:5])
	if !ok || h > 23 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

func digits(s string) (int, bool) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
		n = n*10 + int(c-'0')
	}
	return n, true
}

const delegationColumns = `id, tenant_id, from_user_id, to_user_id, approval_flow_id,
	pass_type_id, department_id, starts_at, ends_at, reason, status, created_by,
	created_at, updated_at`

// DelegationScope is the question asked when a step is assigned: who was this person
// covering for, for this flow, this pass type and this department, at the moment the pass
// arrived?
type DelegationScope struct {
	TenantID     int64
	FromUserID   int64
	FlowID       int64
	PassTypeID   int64
	DepartmentID int64
	At           time.Time
}

// DelegationFor finds the cover a delegatee is standing in for. A narrower delegation is
// preferred over a blanket one because the tenant wrote the narrow one for a reason.
func (r *Repository) DelegationFor(ctx context.Context, in DelegationScope) (Delegation, bool, error) {
	const query = `SELECT ` + delegationColumns + ` FROM approval_delegations
		WHERE  tenant_id = ? AND from_user_id = ? AND status = 'active'
		  AND  starts_at <= ? AND ends_at > ?
		  AND  (approval_flow_id IS NULL OR approval_flow_id = ?)
		  AND  (pass_type_id   IS NULL OR pass_type_id   = ?)
		  AND  (department_id  IS NULL OR department_id  = ?)
		ORDER  BY (approval_flow_id IS NULL), (pass_type_id IS NULL), ends_at, id
		LIMIT  1`
	var out Delegation
	if err := r.q.GetContext(ctx, &out, query, in.TenantID, in.FromUserID, in.At.UTC(), in.At.UTC(),
		nullable(in.FlowID), nullable(in.PassTypeID), nullable(in.DepartmentID)); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Delegation{}, false, nil
		}
		return Delegation{}, false, fmt.Errorf("read delegation: %w", err)
	}
	return out, true, nil
}

// CreateDelegation files a cover. The schema refuses a window that ends before it starts and
// a person covering for themselves, so the tenant's mistake comes back as a validation error
// rather than a chain that never resolves.
func (r *Repository) CreateDelegation(ctx context.Context, in NewDelegation) (int64, error) {
	const query = `
		INSERT INTO approval_delegations
			(tenant_id, from_user_id, to_user_id, approval_flow_id, pass_type_id, department_id,
			 starts_at, ends_at, reason, status, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', ?)`
	result, err := r.q.ExecContext(ctx, query, in.TenantID, in.FromUserID, in.ToUserID,
		nullable(in.FlowID), nullable(in.PassTypeID), nullable(in.DepartmentID),
		in.StartsAt.UTC(), in.EndsAt.UTC(), text(in.Reason), nullable(in.CreatedBy))
	if err != nil {
		return 0, fmt.Errorf("create delegation: %w", err)
	}
	id, err := result.LastInsertId()
	return id, wrap(err, "read delegation id")
}

// NewDelegation is a cover as the delegator filed it.
type NewDelegation struct {
	TenantID     int64
	FromUserID   int64
	ToUserID     int64
	FlowID       int64
	PassTypeID   int64
	DepartmentID int64
	StartsAt     time.Time
	EndsAt       time.Time
	Reason       string
	CreatedBy    int64
}

func (r *Repository) DelegationByID(ctx context.Context, tenantID, id int64) (Delegation, error) {
	const query = `SELECT ` + delegationColumns + ` FROM approval_delegations
		WHERE id = ? AND tenant_id = ?`
	var out Delegation
	if err := r.q.GetContext(ctx, &out, query, id, tenantID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Delegation{}, errs.NotFound("delegation")
		}
		return Delegation{}, fmt.Errorf("read delegation: %w", err)
	}
	return out, nil
}

// ListDelegations is the cover board: current windows first, then the ones already closed.
func (r *Repository) ListDelegations(ctx context.Context, tenantID, fromUserID int64) ([]Delegation, error) {
	const query = `SELECT ` + delegationColumns + ` FROM approval_delegations
		WHERE tenant_id = ? AND (? = 0 OR from_user_id = ?)
		ORDER BY ends_at DESC, id DESC`
	var out []Delegation
	if err := r.q.SelectContext(ctx, &out, query, tenantID, fromUserID, fromUserID); err != nil {
		return nil, fmt.Errorf("list delegations: %w", err)
	}
	return out, nil
}

// EndDelegation cancels a cover early. Asks already handed to the delegatee stay with them:
// cancelling changes who is asked next, not what has already been answered.
func (r *Repository) EndDelegation(ctx context.Context, tenantID, id int64) error {
	const query = `UPDATE approval_delegations SET status = 'cancelled', ends_at = UTC_TIMESTAMP(6)
		WHERE id = ? AND tenant_id = ? AND status = 'active'`
	_, err := r.q.ExecContext(ctx, query, id, tenantID)
	return wrap(err, "cancel delegation")
}
