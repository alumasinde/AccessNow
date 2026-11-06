package notifications

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	"accessnow/internal/platform/errs"
)

// Person is a human a notice can be addressed to. The contact columns travel with every
// lookup because the channel a recipient is reachable on is decided at queue time, not when
// somebody opens the inbox: a notice queued to a phone number that no longer exists still
// has to say so on the row.
type Person struct {
	UserID   int64   `db:"user_id"`
	Name     string  `db:"name"`
	Email    *string `db:"email"`
	Phone    *string `db:"phone"`
	Locale   string  `db:"locale"`
	Timezone string  `db:"timezone"`
}

// Contact is somebody the system can tell who has no login: the visitor at the gate, the
// driver on a vehicle pass. They get a channel and an address, never an inbox row.
type Contact struct {
	Name  string
	Email *string
	Phone *string
}

const personColumns = `u.id AS user_id,
	COALESCE(NULLIF(u.display_name, ''), CONCAT_WS(' ', u.first_name, u.last_name)) AS name,
	u.email, u.phone,
	COALESCE(NULLIF(u.preferred_locale, ''), 'en') AS locale,
	COALESCE(NULLIF(u.preferred_timezone, ''), 'Africa/Nairobi') AS timezone`

// personAlive is the same filter the workflow uses before handing a human an ask: a disabled
// or deleted login gets no notice, because a notice nobody can act on is noise.
const personAlive = `u.status = 'active' AND u.deleted_at IS NULL`

func (r *Repository) PersonByID(ctx context.Context, tenantID, userID int64) (Person, error) {
	const query = `SELECT ` + personColumns + ` FROM users u
		WHERE u.tenant_id = ? AND u.id = ? AND ` + personAlive
	var out Person
	if err := r.q.GetContext(ctx, &out, query, tenantID, userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Person{}, errs.NotFound("user")
		}
		return Person{}, fmt.Errorf("read person %d: %w", userID, err)
	}
	return out, nil
}

// People resolves a list of user ids in one pass. The workflow's escalation and delegation
// paths name ids in the event payload, and a queue that queried each one separately would
// do a round trip per recipient per channel.
func (r *Repository) People(ctx context.Context, tenantID int64, ids []int64) ([]Person, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	const query = `SELECT ` + personColumns + ` FROM users u
		WHERE u.tenant_id = ? AND u.id IN (?) AND ` + personAlive
	expanded, args, err := sqlx.In(query, tenantID, ids)
	if err != nil {
		return nil, fmt.Errorf("expand person ids: %w", err)
	}
	var out []Person
	// No rebind: sqlx.In only needs to widen the IN list, and the pool already speaks "?".
	if err := r.q.SelectContext(ctx, &out, expanded, args...); err != nil {
		return nil, fmt.Errorf("read people: %w", err)
	}
	return out, nil
}

// RoleHolders is how "the gate supervisors" becomes a list of addresses. siteID 0 means the
// whole tenant; a grant tied to another site is skipped, because the supervisor of the
// Mombasa gate has no business being told about a Nairobi step-out.
func (r *Repository) RoleHolders(ctx context.Context, tenantID int64, roleKey string,
	siteID int64) ([]Person, error) {
	const query = `SELECT ` + personColumns + ` FROM users u
		JOIN user_roles ur ON ur.user_id = u.id AND ur.tenant_id = u.tenant_id
		JOIN roles ro ON ro.id = ur.role_id AND ro.tenant_id = ur.tenant_id
		WHERE u.tenant_id = ? AND ro.role_key = ?
		  AND ro.status = 'active' AND ro.deleted_at IS NULL
		  AND ` + personAlive + `
		  AND (ur.expires_at IS NULL OR ur.expires_at > UTC_TIMESTAMP(6))
		  AND (? = 0 OR ur.site_id IS NULL OR ur.site_id = ?)
		ORDER BY u.id`
	var out []Person
	if err := r.q.SelectContext(ctx, &out, query, tenantID, roleKey, siteID, siteID); err != nil {
		return nil, fmt.Errorf("read role holders for %s: %w", roleKey, err)
	}
	return out, nil
}

// PassContext is everything a notice needs about the pass behind an event, read once: the
// people it names, the site it belongs to, and the text the template interpolates.
//
// It is read from the pass row rather than trusted from the payload for one reason - every
// module's payload names the actor, and the actor is the guard at the scan or the cron that
// swept, not the person who filed the pass.
type PassContext struct {
	PassID         int64      `db:"pass_id"`
	TenantID       int64      `db:"tenant_id"`
	PassNo         string     `db:"pass_no"`
	Status         string     `db:"status"`
	CategoryKey    string     `db:"category_key"`
	ModuleKey      string     `db:"source_module_key"`
	SiteID         int64      `db:"site_id"`
	SiteName       string     `db:"site_name"`
	SiteTimezone   string     `db:"site_timezone"`
	SubjectName    string     `db:"subject_name"`
	SubjectPhone   *string    `db:"subject_phone"`
	SubjectEmail   *string    `db:"subject_email"`
	Purpose        *string    `db:"purpose"`
	Requester      *int64     `db:"requester_user_id"`
	Host           *int64     `db:"host_user_id"`
	CreatedBy      *int64     `db:"created_by"`
	DepartmentID   *int64     `db:"department_id"`
	Department     string     `db:"department_name"`
	DepartmentHead *int64     `db:"department_head_user_id"`
	ValidUntil     *time.Time `db:"valid_until"`
	ValueAmount    *float64   `db:"value_amount"`
	ValueCurrency  *string    `db:"value_currency"`
}

const passContextColumns = `p.id AS pass_id, p.tenant_id, p.pass_no, p.status, p.category_key,
	p.source_module_key, p.site_id, s.name AS site_name, s.timezone AS site_timezone,
	p.subject_name, p.subject_phone, p.subject_email, p.purpose,
	p.requester_user_id, p.host_user_id, p.created_by, p.department_id,
	COALESCE(d.name, '') AS department_name, d.head_user_id AS department_head_user_id,
	p.valid_until, p.value_amount, p.value_currency`

func (r *Repository) Pass(ctx context.Context, tenantID, passID int64) (PassContext, error) {
	const query = `SELECT ` + passContextColumns + ` FROM passes p
		JOIN sites s ON s.id = p.site_id AND s.tenant_id = p.tenant_id
		LEFT JOIN departments d ON d.id = p.department_id AND d.tenant_id = p.tenant_id
		WHERE p.tenant_id = ? AND p.id = ? AND p.deleted_at IS NULL`
	var out PassContext
	if err := r.q.GetContext(ctx, &out, query, tenantID, passID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PassContext{}, errs.NotFound("pass")
		}
		return PassContext{}, fmt.Errorf("read pass %d context: %w", passID, err)
	}
	return out, nil
}
