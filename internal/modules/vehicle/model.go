// Package vehicle is the fleet slice: a register of plates and the passes that put one on
// the site for a journey. Entry and exit are gate events, so a lorry that came through
// without a desk still has its dwell measured and its overstay recorded.
package vehicle

import (
	"strings"
	"time"
	"unicode"
)

// ModuleKey is the catalogue row this module switches on and off with.
const ModuleKey = "vehicle_tracking"

// numberFormat is the tenant's vehicle series.
const numberFormat = "vehicle"

// Status mirrors chk_vehicle_passes_status.
type Status string

const (
	StatusPending   Status = "pending"
	StatusApproved  Status = "approved"
	StatusRejected  Status = "rejected"
	StatusCancelled Status = "cancelled"
	StatusOnSite    Status = "on_site"
	StatusCompleted Status = "completed"
	StatusOverdue   Status = "overdue"
	StatusExpired   Status = "expired"
)

// Register statuses mirror chk_vehicles_status. blocked is a desk decision about the plate,
// not a barrier rule: the blocklist module owns gate-time refusal.
const (
	VehicleActive   = "active"
	VehicleBlocked  = "blocked"
	VehicleArchived = "archived"
)

// The three kinds the desk asks for. They map one-to-one onto the seeded pass types, so
// the tenant's approval rule, validity window and dwell threshold come from the catalogue
// instead of from whatever the operator felt like typing.
const (
	KindDelivery = "delivery"
	KindFleet    = "fleet"
	KindVisitor  = "visitor"
)

// OwnerUnknown is what a plate we have never seen before is filed under.
const OwnerUnknown = "unknown"

const defaultClass = "car"

// Vehicle is one registered plate. plate_normalized is the comparison key and
// plate_display is what the receipt prints, so "KCA 123M" and "kca-123 m" are one vehicle.
type Vehicle struct {
	ID                       int64      `db:"id"`
	TenantID                 int64      `db:"tenant_id"`
	PlateNormalized          string     `db:"plate_normalized"`
	PlateDisplay             string     `db:"plate_display"`
	CountryCode              *string    `db:"country_code"`
	Make                     *string    `db:"make"`
	Model                    *string    `db:"model"`
	Color                    *string    `db:"color"`
	Class                    string     `db:"vehicle_class"`
	BodyType                 *string    `db:"body_type"`
	Fleet                    bool       `db:"is_fleet"`
	OwnerType                string     `db:"owner_type"`
	OwnerUserID              *int64     `db:"owner_user_id"`
	OwnerStaffID             *int64     `db:"owner_staff_id"`
	OwnerLabel               *string    `db:"owner_label"`
	OwnerRefID               *int64     `db:"owner_ref_id"`
	InsuranceExpiry          *time.Time `db:"insurance_expiry"`
	FitnessExpiry            *time.Time `db:"fitness_expiry"`
	PermitRequired           bool       `db:"permit_required"`
	DefaultDwellLimitMinutes *int       `db:"default_dwell_limit_minutes"`
	Status                   string     `db:"status"`
	Notes                    *string    `db:"notes"`
	ExternalRef              *string    `db:"external_ref"`
	CreatedAt                time.Time  `db:"created_at"`
	UpdatedAt                time.Time  `db:"updated_at"`
}

// Blocked reports a plate the tenant has stopped dealing with. It is checked when a pass is
// logged, which is the last moment a human is in the loop.
func (v Vehicle) Blocked() bool { return v.Status == VehicleBlocked || v.Status == VehicleArchived }

// InsuranceExpired is worth a warning on the desk card. It is not a gate rule: the rules
// module is what turns paperwork into a barrier decision.
func (v Vehicle) InsuranceExpired(at time.Time) bool {
	return v.InsuranceExpiry != nil && !at.Before(*v.InsuranceExpiry)
}

// Pass is one vehicle journey: the detail row plus the Core pass it hangs off.
type Pass struct {
	ID                int64      `db:"id"`
	TenantID          int64      `db:"tenant_id"`
	PassID            int64      `db:"pass_id"`
	PassNo            string     `db:"pass_no"`
	PassStatus        string     `db:"pass_status"`
	EntryCount        int        `db:"entry_count"`
	VehicleID         *int64     `db:"vehicle_id"`
	PlateNormalized   string     `db:"plate_normalized"`
	PlateDisplay      *string    `db:"plate_display"`
	FleetSnapshot     bool       `db:"is_fleet_snapshot"`
	DriverName        *string    `db:"driver_name"`
	DriverPhone       *string    `db:"driver_phone"`
	DriverIDType      *string    `db:"driver_id_type"`
	DriverStaffID     *int64     `db:"driver_staff_id"`
	EscortName        *string    `db:"escort_name"`
	Purpose           *string    `db:"purpose"`
	CargoDescription  *string    `db:"cargo_description"`
	ContainerNo       *string    `db:"container_no"`
	SealNo            *string    `db:"seal_no"`
	ExpectedEntryAt   *time.Time `db:"expected_entry_at"`
	ExpectedExitAt    *time.Time `db:"expected_exit_at"`
	EnteredAt         *time.Time `db:"entered_at"`
	ExitedAt          *time.Time `db:"exited_at"`
	EntryGateID       *int64     `db:"entry_gate_id"`
	ExitGateID        *int64     `db:"exit_gate_id"`
	DwellLimitMinutes *int       `db:"dwell_limit_minutes"`
	Overstaying       bool       `db:"is_overstaying"`
	OverstayFlaggedAt *time.Time `db:"overstay_flagged_at"`
	ActualDwell       *int       `db:"actual_dwell_minutes"`
	Status            string     `db:"status"`
	Notes             *string    `db:"notes"`
	CreatedAt         time.Time  `db:"created_at"`
}

// Open reports a pass the tenant still has to finish. An expired plate that never scanned
// out is open on purpose: somebody has to say where it went.
func (p Pass) Open() bool {
	switch p.Status {
	case string(StatusPending), string(StatusApproved), string(StatusOnSite),
		string(StatusOverdue), string(StatusExpired):
		return true
	default:
		return false
	}
}

// OnSite reports the pass the roll-call counts. overdue is still on site, just not
// politely: dropping it from the roster would hide exactly the vehicles worth finding.
func (p Pass) OnSite() bool {
	return p.Status == string(StatusOnSite) || p.Status == string(StatusOverdue)
}

// PastDwell reports whether the vehicle has used up the time it was given. The gate and the
// desk both ask it, and both use the same clock, so a five-minute-rounding argument between
// the two screens cannot happen.
func (p Pass) PastDwell(at time.Time) bool {
	if p.DwellLimitMinutes == nil || p.EnteredAt == nil || !p.OnSite() {
		return false
	}
	return at.Sub(*p.EnteredAt) >= time.Duration(*p.DwellLimitMinutes)*time.Minute
}

// MinutesOnSite is the dwell so far, or nil for a vehicle that has not entered.
func (p Pass) MinutesOnSite(at time.Time) *int {
	if p.EnteredAt == nil {
		return nil
	}
	end := at
	if p.ExitedAt != nil {
		end = *p.ExitedAt
	}
	minutes := int(end.Sub(*p.EnteredAt).Minutes())
	if minutes < 0 {
		minutes = 0
	}
	return &minutes
}

// NewVehicle is the register row as the desk filed it.
type NewVehicle struct {
	TenantID                 int64
	PlateNormalized          string
	PlateDisplay             string
	CountryCode              string
	Make                     string
	Model                    string
	Color                    string
	Class                    string
	BodyType                 string
	Fleet                    bool
	OwnerType                string
	OwnerUserID              int64
	OwnerStaffID             int64
	OwnerLabel               string
	InsuranceExpiry          *time.Time
	FitnessExpiry            *time.Time
	PermitRequired           bool
	DefaultDwellLimitMinutes *int
	Notes                    string
	ExternalRef              string
	CreatedBy                int64
}

// UpdateVehicle is the register PATCH: only the fields somebody actually sent move, and a
// nil means "leave what is on file". The plate is absent on purpose - a plate typed wrong
// is a new registration, not an edit, because the passes already point at the old row.
type UpdateVehicle struct {
	Make                     *string
	Model                    *string
	Color                    *string
	Class                    *string
	BodyType                 *string
	Fleet                    *bool
	OwnerType                *string
	OwnerLabel               *string
	InsuranceExpiry          *time.Time
	FitnessExpiry            *time.Time
	PermitRequired           *bool
	DefaultDwellLimitMinutes *int
	Status                   *string
	Notes                    *string
}

// NewPass is the journey row, already tied to the pass Core just issued. driver_id_number
// is deliberately absent: the gate reads the licence from the document in hand and the
// column is retention-bound, so nothing in the API path ever carries it.
type NewPass struct {
	TenantID      int64
	PassID        int64
	VehicleID     int64
	PlateNorm     string
	PlateDisplay  string
	FleetSnapshot bool
	DriverName    string
	DriverPhone   string
	DriverIDType  string
	DriverStaffID int64
	EscortName    string
	Purpose       string
	Cargo         string
	ContainerNo   string
	SealNo        string
	ExpectedEntry *time.Time
	ExpectedExit  *time.Time
	DwellLimit    *int
	Status        Status
	Notes         string
}

// OnSiteRow is one vehicle still on the property, straight off v_vehicles_on_site.
type OnSiteRow struct {
	ID                  int64      `db:"id" json:"id"`
	PassID              int64      `db:"pass_id" json:"pass_id"`
	PassNo              string     `db:"pass_no" json:"pass_no"`
	PlateNormalized     string     `db:"plate_normalized" json:"plate_normalized"`
	PlateDisplay        *string    `db:"plate_display" json:"plate_display,omitempty"`
	DriverName          *string    `db:"driver_name" json:"driver_name,omitempty"`
	Purpose             *string    `db:"purpose" json:"purpose,omitempty"`
	EnteredAt           *time.Time `db:"entered_at" json:"entered_at,omitempty"`
	DwellLimit          *int       `db:"dwell_limit_minutes" json:"dwell_limit_minutes,omitempty"`
	Overstaying         bool       `db:"is_overstaying" json:"is_overstaying"`
	CurrentDwellMinutes int        `db:"current_dwell_minutes" json:"current_dwell_minutes"`
}

// NormalizePlate builds the comparison key behind uk_vehicles_plate: upper case, with the
// spaces and separators a guard types by hand removed. "KCA 123M", "kca-123 m" and
// "KCA/123M" are one vehicle; anything left with no letters or digits is not a plate at all.
func NormalizePlate(plate string) string {
	kept := make([]rune, 0, len(plate))
	for _, r := range strings.ToUpper(plate) {
		if r == ' ' || r == '-' || r == '.' || r == '/' || r == '_' {
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			kept = append(kept, r)
		}
	}
	return string(kept)
}
