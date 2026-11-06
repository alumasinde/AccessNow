// Package staff is the outpass slice: an employee asks to step out, a supervisor says yes,
// and the gate is what records them actually leaving and actually coming back. The reason
// catalogue carries the time limit, so a pharmacy run and a two hour errand are judged
// against different clocks without anybody typing a number.
package staff

import "time"

// ModuleKey is the catalogue row this module switches on and off with.
const ModuleKey = "staff_outpass"

// numberFormat is the tenant's outpass series - OP-{YY}{MM}{SEQ} until the tenant edits it.
const numberFormat = "staff"

// passTypeKey is the catalogue row that holds the approval rule and the validity window.
const passTypeKey = "staff_outpass"

// aggregateType names this module's rows on the outbox, so a subscriber never has to guess
// whether an id is an outpass or a pass.
const aggregateType = "outpass"

// reasonOther is the catalogue row whose text is the reason. The desk refuses it blank:
// "Other" on a register nobody can read is not a reason.
const reasonOther = "other"

// StaffActive is the only directory status that may be sent out. The others are checked at
// request time, not at the barrier - a suspended employee's old outpass is the rules
// module's problem to block, not this one's to guess at.
const StaffActive = "active"

// Status mirrors chk_outpasses_status. out is an employee who is off the property; overdue
// and unreturned are the two escalations of it - past the time they gave, then past the
// window the pass was issued for, when somebody has to go looking.
type Status string

const (
	StatusPending    Status = "pending"
	StatusApproved   Status = "approved"
	StatusRejected   Status = "rejected"
	StatusCancelled  Status = "cancelled"
	StatusOut        Status = "out"
	StatusReturned   Status = "returned"
	StatusOverdue    Status = "overdue"
	StatusUnreturned Status = "unreturned"
)

// Staff is the directory row an outpass belongs to. Only the fields the outpass desk reads
// and prints are here; id_number and photo_path stay in Core's directory, where the
// retention policy can purge them.
type Staff struct {
	ID           int64      `db:"id"`
	TenantID     int64      `db:"tenant_id"`
	UserID       *int64     `db:"user_id"`
	DepartmentID *int64     `db:"department_id"`
	EmployeeNo   string     `db:"employee_no"`
	FirstName    string     `db:"first_name"`
	LastName     string     `db:"last_name"`
	JobTitle     *string    `db:"job_title"`
	Phone        *string    `db:"phone"`
	AccessCardNo *string    `db:"access_card_no"`
	ShiftPattern *string    `db:"shift_pattern"`
	Status       string     `db:"status"`
	StartedOn    *time.Time `db:"started_on"`
	EndedOn      *time.Time `db:"ended_on"`
}

func (s Staff) Name() string { return s.FirstName + " " + s.LastName }

// Active reports somebody the tenant still employs. on_leave is not active: an employee
// who is already away does not need permission to step out.
func (s Staff) Active() bool { return s.Status == StaffActive }

// Reason is one row of the outpass catalogue. It is shared reference data, not tenant
// rows: the tenant edits the pass type, and this table says what the reasons are.
type Reason struct {
	ID                     int64  `db:"id"`
	ReasonKey              string `db:"reason_key"`
	Name                   string `db:"name"`
	DefaultDurationMinutes *int   `db:"default_duration_minutes"`
	RequiresManager        bool   `db:"requires_manager"`
	RequiresReturnScan     bool   `db:"requires_return_scan"`
	SortOrder              int    `db:"sort_order"`
	Status                 string `db:"status"`
}

func (r Reason) Active() bool { return r.Status == "active" }

// Outpass is one step-out: the detail row, the Core pass it hangs off, and the name of the
// employee it belongs to.
type Outpass struct {
	ID                 int64      `db:"id"`
	TenantID           int64      `db:"tenant_id"`
	PassID             int64      `db:"pass_id"`
	PassNo             string     `db:"pass_no"`
	PassStatus         string     `db:"pass_status"`
	EntryCount         int        `db:"entry_count"`
	StaffID            int64      `db:"staff_id"`
	StaffName          string     `db:"staff_name"`
	EmployeeNo         string     `db:"employee_no"`
	DepartmentID       *int64     `db:"department_id"`
	SiteID             int64      `db:"site_id"`
	ReasonKey          string     `db:"reason_key"`
	ReasonNote         *string    `db:"reason_note"`
	Destination        *string    `db:"destination"`
	VehiclePlate       *string    `db:"vehicle_plate"`
	CompanyVehicle     bool       `db:"is_company_vehicle"`
	RequestedAt        time.Time  `db:"requested_at"`
	ApprovedAt         *time.Time `db:"approved_at"`
	ApprovedByUserID   *int64     `db:"approved_by_user_id"`
	RejectionReason    *string    `db:"rejection_reason"`
	ExpectedDuration   *int       `db:"expected_duration_minutes"`
	ExpectedReturnAt   *time.Time `db:"expected_return_at"`
	ExitedAt           *time.Time `db:"exited_at"`
	ExitGateID         *int64     `db:"exit_gate_id"`
	ExitMovementID     *int64     `db:"exit_movement_id"`
	ReturnScanRequired bool       `db:"return_scan_required"`
	ReturnedAt         *time.Time `db:"returned_at"`
	ReturnGateID       *int64     `db:"return_gate_id"`
	ReturnMovementID   *int64     `db:"return_movement_id"`
	ActualDuration     *int       `db:"actual_duration_minutes"`
	OverDuration       bool       `db:"is_over_duration"`
	EndOfShiftAt       *time.Time `db:"end_of_shift_at"`
	Unreturned         bool       `db:"is_unreturned"`
	Status             string     `db:"status"`
	Notes              *string    `db:"notes"`
	CreatedAt          time.Time  `db:"created_at"`
}

// Open reports an employee the site is still without. unreturned is open too: the desk must
// not be able to lose somebody off the roll-call by letting their row age out of it.
func (o Outpass) Open() bool {
	return o.Status == string(StatusOut) || o.Status == string(StatusOverdue) ||
		o.Status == string(StatusUnreturned)
}

// Decided reports the one state an approval click belongs to.
func (o Outpass) Decided() bool { return o.Status != string(StatusPending) }

// PastExpected reports the employee who did not come back when they said they would. The
// exit scan sets the deadline from the reason's duration, so this is the same arithmetic
// the roster's is_overdue does, and the two screens cannot disagree.
func (o Outpass) PastExpected(at time.Time) bool {
	return o.Open() && o.ExpectedReturnAt != nil && !at.Before(*o.ExpectedReturnAt)
}

// PastEndOfShift reports somebody the site has now lost for the day: still out after the
// window the pass was issued for. This is what the unreturned sweep looks for.
func (o Outpass) PastEndOfShift(at time.Time) bool {
	return o.Open() && o.EndOfShiftAt != nil && !at.Before(*o.EndOfShiftAt)
}

// MinutesOut is how long the employee has been off the property, or nil before the exit
// scan that started the clock.
func (o Outpass) MinutesOut(at time.Time) *int {
	if o.ExitedAt == nil {
		return nil
	}
	end := at
	if o.ReturnedAt != nil {
		end = *o.ReturnedAt
	}
	minutes := int(end.Sub(*o.ExitedAt).Minutes())
	if minutes < 0 {
		minutes = 0
	}
	return &minutes
}

// MinutesLate is how far past the time the employee gave they still have not come back, in
// whole minutes rounded up. It is the number an overdue notice quotes: a person thirty seconds
// late is in their first minute late, and a notice that says "( minutes)" tells the duty desk
// nothing it can act on.
func (o Outpass) MinutesLate(at time.Time) *int {
	if !o.Open() || o.ExpectedReturnAt == nil || !at.After(*o.ExpectedReturnAt) {
		return nil
	}
	minutes := int(at.Sub(*o.ExpectedReturnAt).Seconds()+59) / 60
	if minutes < 1 {
		minutes = 1
	}
	return &minutes
}

// New is one outpass as the desk or the employee filed it, before the pass exists.
type New struct {
	StaffID          int64
	ReasonKey        string
	ReasonNote       string
	Destination      string
	VehiclePlate     string
	CompanyVehicle   bool
	ExpectedDuration *int
	ExpectedReturn   *time.Time
	EndOfShift       *time.Time
	Notes            string
}

// NewOutpass is the detail row once Core has issued the pass behind it.
type NewOutpass struct {
	TenantID           int64
	PassID             int64
	StaffID            int64
	DepartmentID       *int64
	SiteID             int64
	ReasonKey          string
	ReasonNote         string
	Destination        string
	VehiclePlate       string
	CompanyVehicle     bool
	ExpectedDuration   *int
	ExpectedReturn     *time.Time
	EndOfShift         *time.Time
	ReturnScanRequired bool
	Status             Status
	// ApprovedAt and ApprovedByUserID are only set on the auto-approved path, and they are
	// why the register needs no separate approval row: the supervisor who filed it lands on
	// the same line a desk click would record.
	ApprovedAt       *time.Time
	ApprovedByUserID int64
	Notes            string
}

// OutstandingRow is one employee still off the site, straight off v_staff_out_of_site.
type OutstandingRow struct {
	OutpassID          int64      `db:"outpass_id" json:"outpass_id"`
	PassID             int64      `db:"pass_id" json:"pass_id"`
	PassNo             string     `db:"pass_no" json:"pass_no"`
	StaffID            int64      `db:"staff_id" json:"staff_id"`
	FirstName          string     `db:"first_name" json:"first_name"`
	LastName           string     `db:"last_name" json:"last_name"`
	EmployeeNo         string     `db:"employee_no" json:"employee_no"`
	DepartmentID       *int64     `db:"department_id" json:"department_id,omitempty"`
	ReasonKey          string     `db:"reason_key" json:"reason_key"`
	ExpectedReturnAt   *time.Time `db:"expected_return_at" json:"expected_return_at,omitempty"`
	ExpectedDuration   *int       `db:"expected_duration_minutes" json:"expected_duration_minutes,omitempty"`
	ReturnScanRequired bool       `db:"return_scan_required" json:"return_scan_required"`
	ExitedAt           *time.Time `db:"exited_at" json:"exited_at,omitempty"`
	MinutesOut         int        `db:"minutes_out" json:"minutes_out"`
	Overdue            bool       `db:"is_overdue" json:"is_overdue"`
	Status             string     `db:"status" json:"status"`
}
