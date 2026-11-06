// Package visitor is the reference module slice: it owns the visit detail table, hangs
// every visit off a Core pass, and shows the shape the other fourteen follow.
package visitor

import "time"

// Status mirrors chk_visits_status. The pass has its own status; a visit only ever
// mirrors it, so a module can be switched off without the spine disagreeing with itself.
type Status string

const (
	StatusPending   Status = "pending"
	StatusApproved  Status = "approved"
	StatusRejected  Status = "rejected"
	StatusCancelled Status = "cancelled"
	StatusOnSite    Status = "on_site"
	StatusCompleted Status = "completed"
	StatusExpired   Status = "expired"
	StatusNoShow    Status = "no_show"
)

type Visitor struct {
	ID            int64      `db:"id"`
	TenantID      int64      `db:"tenant_id"`
	CompanyID     *int64     `db:"visitor_company_id"`
	FirstName     string     `db:"first_name"`
	LastName      string     `db:"last_name"`
	Email         *string    `db:"email"`
	Phone         *string    `db:"phone"`
	IDType        *string    `db:"id_type"`
	IDNumber      *string    `db:"id_number"`
	BadgeNo       *string    `db:"badge_no"`
	PreferredHost *int64     `db:"preferred_host_user_id"`
	Status        string     `db:"status"`
	VisitCount    int        `db:"visit_count"`
	LastVisitedAt *time.Time `db:"last_visited_at"`
}

func (v Visitor) Name() string { return v.FirstName + " " + v.LastName }

type Visit struct {
	ID               int64      `db:"id"`
	TenantID         int64      `db:"tenant_id"`
	PassID           int64      `db:"pass_id"`
	VisitorID        int64      `db:"visitor_id"`
	CompanyID        *int64     `db:"visitor_company_id"`
	HostUserID       *int64     `db:"host_user_id"`
	HostName         *string    `db:"host_name_snapshot"`
	HostPhone        *string    `db:"host_phone_snapshot"`
	DepartmentID     *int64     `db:"department_id"`
	Purpose          string     `db:"purpose"`
	PeopleCount      int        `db:"people_count"`
	EscortRequired   bool       `db:"escort_required"`
	BadgeNo          *string    `db:"badge_no"`
	IsWalkIn         bool       `db:"is_walk_in"`
	Status           string     `db:"status"`
	HostResponse     *string    `db:"host_response"`
	HostRespondedAt  *time.Time `db:"host_responded_at"`
	ExpectedFrom     *time.Time `db:"expected_from"`
	ExpectedUntil    *time.Time `db:"expected_until"`
	CheckedInAt      *time.Time `db:"checked_in_at"`
	CheckedInGate    *int64     `db:"checked_in_gate_id"`
	CheckInMovement  *int64     `db:"check_in_movement_id"`
	CheckedOutAt     *time.Time `db:"checked_out_at"`
	CheckedOutGate   *int64     `db:"checked_out_gate_id"`
	CheckOutMovement *int64     `db:"check_out_movement_id"`
	CreatedAt        time.Time  `db:"created_at"`
	PassNo           string     `db:"pass_no"`
	PassStatus       string     `db:"pass_status"`
	VisitorFirst     string     `db:"visitor_first_name"`
	VisitorLast      string     `db:"visitor_last_name"`
}

func (v Visit) VisitorName() string { return v.VisitorFirst + " " + v.VisitorLast }

func (v Visit) Open() bool {
	return v.Status != string(StatusCompleted) && v.Status != string(StatusCancelled) &&
		v.Status != string(StatusRejected) && v.Status != string(StatusNoShow) &&
		v.Status != string(StatusExpired)
}

// NewVisitor is what the caller may say about the person at the desk.
type NewVisitor struct {
	FirstName  string
	LastName   string
	Email      string
	Phone      string
	IDType     string
	IDNumber   string
	CompanyID  int64
	HostUserID int64
}

// NewVisit is the visit row, already tied to the pass Core issued.
type NewVisit struct {
	TenantID      int64
	PassID        int64
	VisitorID     int64
	CompanyID     *int64
	HostUserID    *int64
	HostName      string
	DepartmentID  *int64
	Purpose       string
	PeopleCount   int
	EscortRequire bool
	BadgeNo       string
	IsWalkIn      bool
	Status        Status
	ExpectedFrom  *time.Time
	ExpectedUntil *time.Time
}
