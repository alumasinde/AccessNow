// Package passes is the Core spine every module hangs from: one row per pass, whatever
// its kind, plus the movements against it.
package passes

import "time"

type Status string

const (
	StatusDraft     Status = "draft"
	StatusPending   Status = "pending"
	StatusApproved  Status = "approved"
	StatusRejected  Status = "rejected"
	StatusCancelled Status = "cancelled"
	StatusExpired   Status = "expired"
	StatusActive    Status = "active"
	StatusOnSite    Status = "on_site"
	StatusCompleted Status = "completed"
	StatusReturned  Status = "returned"
	StatusOverdue   Status = "overdue"
	StatusVoid      Status = "void"
)

// Allowed matches chk_passes_status. Kept here so a service can refuse a transition the
// database would also refuse, with a message that says which state it came from.
var Allowed = []Status{
	StatusDraft, StatusPending, StatusApproved, StatusRejected, StatusCancelled,
	StatusExpired, StatusActive, StatusOnSite, StatusCompleted, StatusReturned,
	StatusOverdue, StatusVoid,
}

func Valid(status Status) bool {
	for _, candidate := range Allowed {
		if candidate == status {
			return true
		}
	}
	return false
}

type Pass struct {
	ID              int64      `db:"id"`
	UUID            string     `db:"uuid"`
	TenantID        int64      `db:"tenant_id"`
	SiteID          int64      `db:"site_id"`
	PassTypeID      int64      `db:"pass_type_id"`
	CategoryKey     string     `db:"category_key"`
	SourceModuleKey string     `db:"source_module_key"`
	PassNo          string     `db:"pass_no"`
	Status          string     `db:"status"`
	Origin          string     `db:"origin"`
	SubjectName     string     `db:"subject_name"`
	SubjectPhone    *string    `db:"subject_phone"`
	SubjectEmail    *string    `db:"subject_email"`
	Purpose         *string    `db:"purpose"`
	RequesterUserID *int64     `db:"requester_user_id"`
	HostUserID      *int64     `db:"host_user_id"`
	ValidFrom       time.Time  `db:"valid_from"`
	ValidUntil      *time.Time `db:"valid_until"`
	EntryCount      int        `db:"entry_count"`
	IssuedAt        *time.Time `db:"issued_at"`
	DecidedAt       *time.Time `db:"decided_at"`
	DecisionNote    *string    `db:"decision_note"`
	ClosedAt        *time.Time `db:"closed_at"`
	CreatedAt       time.Time  `db:"created_at"`
}

// Decidable reports whether a pass can still move state. Expired and closed rows are
// history, and the guard screen must not offer a button for them.
func (p Pass) Decidable() bool {
	return p.ClosedAt == nil && p.Status != string(StatusVoid) && p.Status != string(StatusCompleted)
}

// Usable reports whether this pass may be honoured at a barrier at all. pending is a
// request, not a permission, and completed is history: both must turn a gate red in
// either direction, or an unapproved lorry walks out and a closed one walks out twice.
func (p Pass) Usable() bool {
	switch p.Status {
	case string(StatusApproved), string(StatusActive), string(StatusOnSite):
		return true
	default:
		return false
	}
}

func (p Pass) WithinWindow(at time.Time) bool {
	if p.ValidUntil == nil {
		return !at.Before(p.ValidFrom)
	}
	return !at.Before(p.ValidFrom) && at.Before(*p.ValidUntil)
}

// New is what a module hands Core when it issues a pass.
type New struct {
	TenantID        int64
	SiteID          int64
	PassTypeID      int64
	CategoryKey     string
	SourceModuleKey string
	PassNo          string
	Status          Status
	Origin          string
	SubjectName     string
	SubjectPhone    *string
	SubjectEmail    *string
	Purpose         *string
	RequesterUserID *int64
	HostUserID      *int64
	ValidFrom       time.Time
	ValidUntil      *time.Time
}
