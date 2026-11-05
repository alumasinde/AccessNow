package visitor

import (
	"net/http"
	"time"

	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/httpx"
)

const defaultPage = 25

// CreateRequest is the desk form. One body creates the person, the pass and the visit;
// the desk is not expected to know that three tables are involved.
type CreateRequest struct {
	FirstName      string     `json:"first_name" validate:"required,max=128"`
	LastName       string     `json:"last_name" validate:"required,max=128"`
	Email          string     `json:"email" validate:"omitempty,email,max=255"`
	Phone          string     `json:"phone" validate:"omitempty,max=32"`
	IDType         string     `json:"id_type" validate:"omitempty,oneof=national_id passport drivers_licence work_permit refugee_id other"`
	IDNumber       string     `json:"id_number" validate:"omitempty,max=64"`
	CompanyID      int64      `json:"visitor_company_id" validate:"omitempty,gt=0"`
	HostUserID     int64      `json:"host_user_id" validate:"omitempty,gt=0"`
	Purpose        string     `json:"purpose" validate:"required,max=512"`
	PeopleCount    int        `json:"people_count" validate:"omitempty,min=1,max=50"`
	EscortRequired bool       `json:"escort_required"`
	BadgeNo        string     `json:"badge_no" validate:"omitempty,max=64"`
	DepartmentID   int64      `json:"department_id" validate:"omitempty,gt=0"`
	ExpectedFrom   *time.Time `json:"expected_from"`
	ExpectedUntil  *time.Time `json:"expected_until"`
	Origin         string     `json:"origin" validate:"omitempty,oneof=walk_in pre_registered online api"`
}

func (req CreateRequest) toCreate() Create {
	return Create{
		Visitor: NewVisitor{
			FirstName: req.FirstName, LastName: req.LastName, Email: req.Email,
			Phone: req.Phone, IDType: req.IDType, IDNumber: req.IDNumber,
			CompanyID: req.CompanyID, HostUserID: req.HostUserID,
		},
		Purpose: req.Purpose, PeopleCount: req.PeopleCount, EscortRequired: req.EscortRequired,
		BadgeNo: req.BadgeNo, DepartmentID: req.DepartmentID,
		ExpectedFrom: req.ExpectedFrom, ExpectedUntil: req.ExpectedUntil, Origin: req.Origin,
	}
}

// DecisionRequest is the approve or reject click. An explicit action beats a boolean a
// client can send as false by accident.
type DecisionRequest struct {
	Action string `json:"action" validate:"required,oneof=approve reject"`
	Note   string `json:"note" validate:"omitempty,max=512"`
}

type CancelRequest struct {
	Reason string `json:"reason" validate:"required,max=512"`
}

// VisitView is the desk card and the queue row.
type VisitView struct {
	ID             int64      `json:"id"`
	PassID         int64      `json:"pass_id"`
	PassNo         string     `json:"pass_no"`
	PassStatus     string     `json:"pass_status"`
	Status         string     `json:"status"`
	VisitorID      int64      `json:"visitor_id"`
	VisitorName    string     `json:"visitor_name"`
	CompanyID      *int64     `json:"visitor_company_id,omitempty"`
	HostUserID     *int64     `json:"host_user_id,omitempty"`
	HostName       *string    `json:"host_name,omitempty"`
	DepartmentID   *int64     `json:"department_id,omitempty"`
	Purpose        string     `json:"purpose"`
	PeopleCount    int        `json:"people_count"`
	EscortRequired bool       `json:"escort_required"`
	BadgeNo        *string    `json:"badge_no,omitempty"`
	IsWalkIn       bool       `json:"is_walk_in"`
	HostResponse   *string    `json:"host_response,omitempty"`
	ExpectedFrom   *time.Time `json:"expected_from,omitempty"`
	ExpectedUntil  *time.Time `json:"expected_until,omitempty"`
	CheckedInAt    *time.Time `json:"checked_in_at,omitempty"`
	CheckedOutAt   *time.Time `json:"checked_out_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

func viewOf(v Visit) VisitView {
	return VisitView{
		ID: v.ID, PassID: v.PassID, PassNo: v.PassNo, PassStatus: v.PassStatus,
		Status: v.Status, VisitorID: v.VisitorID, VisitorName: v.VisitorName(),
		CompanyID: v.CompanyID, HostUserID: v.HostUserID, HostName: v.HostName,
		DepartmentID: v.DepartmentID, Purpose: v.Purpose, PeopleCount: v.PeopleCount,
		EscortRequired: v.EscortRequired, BadgeNo: v.BadgeNo, IsWalkIn: v.IsWalkIn,
		HostResponse: v.HostResponse, ExpectedFrom: v.ExpectedFrom,
		ExpectedUntil: v.ExpectedUntil, CheckedInAt: v.CheckedInAt,
		CheckedOutAt: v.CheckedOutAt, CreatedAt: v.CreatedAt,
	}
}

// VisitorView is the person record. id_number is absent: the desk reads it from the
// document in the guard's hand, and the retention job blanks the column anyway.
type VisitorView struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Email       *string    `json:"email,omitempty"`
	Phone       *string    `json:"phone,omitempty"`
	CompanyID   *int64     `json:"visitor_company_id,omitempty"`
	HostUserID  *int64     `json:"preferred_host_user_id,omitempty"`
	Status      string     `json:"status"`
	VisitCount  int        `json:"visit_count"`
	LastVisited *time.Time `json:"last_visited_at,omitempty"`
}

func viewOfVisitor(v Visitor) VisitorView {
	return VisitorView{
		ID: v.ID, Name: v.Name(), Email: v.Email, Phone: v.Phone, CompanyID: v.CompanyID,
		HostUserID: v.PreferredHost, Status: v.Status, VisitCount: v.VisitCount,
		LastVisited: v.LastVisitedAt,
	}
}

// ParseList reads the queue filters: ?status=&host_id=&visitor_id=&limit=&cursor=.
func ParseList(r *http.Request) (ListFilter, httpx.Page, error) {
	query := r.URL.Query()
	filter := ListFilter{Status: query.Get("status")}
	if filter.Status != "" && !validStatus(filter.Status) {
		return filter, httpx.Page{}, errs.BadField("status", filter.Status+" is not a visit status")
	}
	if query.Get("host_id") != "" {
		id, err := httpx.QueryID(r, "host_id")
		if err != nil {
			return filter, httpx.Page{}, err
		}
		filter.HostUserID = id
	}
	if query.Get("visitor_id") != "" {
		id, err := httpx.QueryID(r, "visitor_id")
		if err != nil {
			return filter, httpx.Page{}, err
		}
		filter.VisitorID = id
	}
	page, err := httpx.ParsePage(r, defaultPage, 100)
	return filter, page, err
}

func validStatus(status string) bool {
	for _, candidate := range []Status{
		StatusPending, StatusApproved, StatusRejected, StatusCancelled,
		StatusOnSite, StatusCompleted, StatusExpired, StatusNoShow,
	} {
		if string(candidate) == status {
			return true
		}
	}
	return false
}
