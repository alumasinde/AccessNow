// Package passes is the Core spine every module hangs from: one row per pass, whatever
// its kind, plus the movements against it.
package passes

import "time"

type Status string

const (
	StatusDraft   Status = "draft"
	StatusPending Status = "pending"
	// StatusPendingReview is the state an approval chain sends a pass back to when an
	// approver returns it for correction: the requester still owns it, and it is not yet
	// worth anything at a barrier.
	StatusPendingReview Status = "pending_review"
	StatusApproved      Status = "approved"
	StatusRejected      Status = "rejected"
	StatusCancelled     Status = "cancelled"
	StatusExpired       Status = "expired"
	StatusActive        Status = "active"
	StatusOnSite        Status = "on_site"
	StatusCompleted     Status = "completed"
	StatusReturned      Status = "returned"
	StatusOverdue       Status = "overdue"
	StatusVoid          Status = "void"
)

// Allowed matches chk_passes_status. Kept here so a service can refuse a transition the
// database would also refuse, with a message that says which state it came from.
var Allowed = []Status{
	StatusDraft, StatusPending, StatusPendingReview, StatusApproved, StatusRejected,
	StatusCancelled, StatusExpired, StatusActive, StatusOnSite, StatusCompleted,
	StatusReturned, StatusOverdue, StatusVoid,
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
	DepartmentID    *int64     `db:"department_id"`
	ValidFrom       time.Time  `db:"valid_from"`
	ValidUntil      *time.Time `db:"valid_until"`
	ValueAmount     *float64   `db:"value_amount"`
	ValueCurrency   *string    `db:"value_currency"`
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

// AwaitingDecision reports the statuses an approval can still land on: a pass that has been
// cancelled, expired, refused or closed is history, and a stale ask in an approver's inbox
// must not turn it green at the barrier again.
func (p Pass) AwaitingDecision() bool {
	switch p.Status {
	case string(StatusDraft), string(StatusPending), string(StatusPendingReview):
		return true
	default:
		return false
	}
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
	// DepartmentID is the org unit the request belongs to. Approval routing can key a flow
	// to a department, so a pass that does not say which one it is can only match a flow
	// that does not care.
	DepartmentID *int64
	ValidFrom    time.Time
	ValidUntil   *time.Time
	// ValueAmount is what the subject is worth, as declared on the request: the value of
	// goods leaving the site, in the tenant's own currency. An approval flow can add a step
	// above a threshold, so a nil here means "nothing was declared", never "worth nothing".
	ValueAmount *float64
	// ValueCurrency is the three-letter code ValueAmount is counted in. A flow that says
	// "above 500,000" means nothing if the pass does not say what its 500,000 is worth in.
	ValueCurrency string
}
