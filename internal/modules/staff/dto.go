package staff

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/httpx"
)

const defaultPage = 25

// OutpassRequest is one step-out as the employee or the desk files it. staff_id is optional
// and means "somebody else": left out, the request is for the login that sent it.
type OutpassRequest struct {
	StaffID        int64      `json:"staff_id" validate:"omitempty,gt=0"`
	ReasonKey      string     `json:"reason_key" validate:"required,max=64"`
	ReasonNote     string     `json:"reason_note" validate:"omitempty,max=512"`
	Destination    string     `json:"destination" validate:"omitempty,max=255"`
	VehiclePlate   string     `json:"vehicle_plate" validate:"omitempty,max=32"`
	CompanyVehicle bool       `json:"is_company_vehicle"`
	Duration       *int       `json:"expected_duration_minutes" validate:"omitempty,gte=1,lte=1440"`
	ExpectedReturn *time.Time `json:"expected_return_at"`
	EndOfShift     *time.Time `json:"end_of_shift_at"`
	Origin         string     `json:"origin" validate:"omitempty,oneof=walk_in pre_registered online api import device renewal"`
	Notes          string     `json:"notes" validate:"omitempty,max=2000"`
}

func (req OutpassRequest) toRequest() Request {
	return Request{
		StaffID: req.StaffID, ReasonKey: req.ReasonKey, ReasonNote: req.ReasonNote,
		Destination: req.Destination, VehiclePlate: req.VehiclePlate,
		CompanyVehicle: req.CompanyVehicle, Duration: req.Duration,
		ExpectedReturn: req.ExpectedReturn, EndOfShift: req.EndOfShift,
		Origin: req.Origin, Notes: req.Notes,
	}
}

// DecisionRequest is the approve or reject click. An explicit action beats a boolean a
// client can send as false by accident.
type DecisionRequest struct {
	Action string `json:"action" validate:"required,oneof=approve reject"`
	Note   string `json:"note" validate:"omitempty,max=512"`
}

// NoteRequest is the body the cancel and close clicks share: a reason nobody has to
// invent a field name for.
type NoteRequest struct {
	Note string `json:"note" validate:"omitempty,max=512"`
}

// OutpassView is one row of the register. minutes_out is computed rather than stored: it is
// the number the desk reads out loud, and a stored one would be wrong the minute it was
// written.
type OutpassView struct {
	ID                 int64      `json:"id"`
	PassID             int64      `json:"pass_id"`
	PassNo             string     `json:"pass_no"`
	PassStatus         string     `json:"pass_status"`
	EntryCount         int        `json:"entry_count"`
	StaffID            int64      `json:"staff_id"`
	StaffName          string     `json:"staff_name"`
	EmployeeNo         string     `json:"employee_no"`
	DepartmentID       *int64     `json:"department_id,omitempty"`
	ReasonKey          string     `json:"reason_key"`
	ReasonNote         *string    `json:"reason_note,omitempty"`
	Destination        *string    `json:"destination,omitempty"`
	VehiclePlate       *string    `json:"vehicle_plate,omitempty"`
	CompanyVehicle     bool       `json:"is_company_vehicle"`
	RequestedAt        time.Time  `json:"requested_at"`
	ApprovedAt         *time.Time `json:"approved_at,omitempty"`
	ApprovedByUserID   *int64     `json:"approved_by_user_id,omitempty"`
	RejectionReason    *string    `json:"rejection_reason,omitempty"`
	ExpectedDuration   *int       `json:"expected_duration_minutes,omitempty"`
	ExpectedReturnAt   *time.Time `json:"expected_return_at,omitempty"`
	ExitedAt           *time.Time `json:"exited_at,omitempty"`
	ReturnScanRequired bool       `json:"return_scan_required"`
	ReturnedAt         *time.Time `json:"returned_at,omitempty"`
	ActualDuration     *int       `json:"actual_duration_minutes,omitempty"`
	OverDuration       bool       `json:"is_over_duration"`
	EndOfShiftAt       *time.Time `json:"end_of_shift_at,omitempty"`
	Unreturned         bool       `json:"is_unreturned"`
	Overdue            bool       `json:"is_overdue"`
	MinutesOut         *int       `json:"minutes_out,omitempty"`
	Status             string     `json:"status"`
	Notes              *string    `json:"notes,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
}

func viewOf(o Outpass) OutpassView {
	now := time.Now().UTC()
	return OutpassView{
		ID: o.ID, PassID: o.PassID, PassNo: o.PassNo, PassStatus: o.PassStatus,
		EntryCount: o.EntryCount, StaffID: o.StaffID, StaffName: o.StaffName,
		EmployeeNo: o.EmployeeNo, DepartmentID: o.DepartmentID, ReasonKey: o.ReasonKey,
		ReasonNote: o.ReasonNote, Destination: o.Destination, VehiclePlate: o.VehiclePlate,
		CompanyVehicle: o.CompanyVehicle, RequestedAt: o.RequestedAt, ApprovedAt: o.ApprovedAt,
		ApprovedByUserID: o.ApprovedByUserID, RejectionReason: o.RejectionReason,
		ExpectedDuration: o.ExpectedDuration, ExpectedReturnAt: o.ExpectedReturnAt,
		ExitedAt: o.ExitedAt, ReturnScanRequired: o.ReturnScanRequired,
		ReturnedAt: o.ReturnedAt, ActualDuration: o.ActualDuration,
		OverDuration: o.OverDuration, EndOfShiftAt: o.EndOfShiftAt, Unreturned: o.Unreturned,
		Overdue: o.PastExpected(now), MinutesOut: o.MinutesOut(now),
		Status: o.Status, Notes: o.Notes, CreatedAt: o.CreatedAt,
	}
}

func viewsOf(outpasses []Outpass) []OutpassView {
	views := make([]OutpassView, 0, len(outpasses))
	for _, filed := range outpasses {
		views = append(views, viewOf(filed))
	}
	return views
}

// ReasonView is one catalogue row. The desk draws its dropdown from here, which is why the
// duration and the two flags come back with it: the form should say what a reason costs
// before anybody picks it.
type ReasonView struct {
	ReasonKey              string `json:"reason_key"`
	Name                   string `json:"name"`
	DefaultDurationMinutes *int   `json:"default_duration_minutes,omitempty"`
	RequiresManager        bool   `json:"requires_manager"`
	RequiresReturnScan     bool   `json:"requires_return_scan"`
}

func viewOfReason(r Reason) ReasonView {
	return ReasonView{
		ReasonKey: r.ReasonKey, Name: r.Name,
		DefaultDurationMinutes: r.DefaultDurationMinutes, RequiresManager: r.RequiresManager,
		RequiresReturnScan: r.RequiresReturnScan,
	}
}

func viewsOfReasons(reasons []Reason) []ReasonView {
	views := make([]ReasonView, 0, len(reasons))
	for _, reason := range reasons {
		views = append(views, viewOfReason(reason))
	}
	return views
}

// ParseList reads the register filters:
// ?status=&staff_id=&department_id=&reason=&open=&search=.
func ParseList(r *http.Request) (ListFilter, httpx.Page, error) {
	query := r.URL.Query()
	staffID, err := optionalID(query, "staff_id")
	if err != nil {
		return ListFilter{}, httpx.Page{}, err
	}
	departmentID, err := optionalID(query, "department_id")
	if err != nil {
		return ListFilter{}, httpx.Page{}, err
	}
	open, err := optionalBool(query, "open")
	if err != nil {
		return ListFilter{}, httpx.Page{}, err
	}

	filter := ListFilter{
		Status: query.Get("status"), StaffID: staffID, DepartmentID: departmentID,
		ReasonKey: query.Get("reason"), Search: query.Get("search"),
		OpenOnly: open != nil && *open,
	}
	if filter.Status != "" && !validStatus(filter.Status) {
		return filter, httpx.Page{}, errs.BadField("status", filter.Status+" is not an outpass status")
	}
	if len(filter.ReasonKey) > 64 {
		return filter, httpx.Page{}, errs.BadField("reason", "too long")
	}
	if len(filter.Search) > 128 {
		return filter, httpx.Page{}, errs.BadField("search", "too long")
	}
	page, err := httpx.ParsePage(r, defaultPage, 100)
	return filter, page, err
}

func validStatus(status string) bool {
	for _, candidate := range []Status{
		StatusPending, StatusApproved, StatusRejected, StatusCancelled,
		StatusOut, StatusReturned, StatusOverdue, StatusUnreturned,
	} {
		if string(candidate) == status {
			return true
		}
	}
	return false
}

func optionalID(query url.Values, name string) (int64, error) {
	raw := query.Get(name)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, errs.BadField(name, "expected a positive id")
	}
	return value, nil
}

// optionalBool is an optional boolean filter. A value that is neither yes nor no is refused
// rather than ignored: silently dropping ?open=maybe would list the whole register.
func optionalBool(query url.Values, name string) (*bool, error) {
	switch strings.ToLower(query.Get(name)) {
	case "":
		return nil, nil
	case "true", "1":
		yes := true
		return &yes, nil
	case "false", "0":
		no := false
		return &no, nil
	default:
		return nil, errs.BadField(name, "expected true or false")
	}
}
