package vehicle

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

// LogRequest is one journey as the desk or an import files it. `kind` is the only choice
// that matters: it picks the catalogue pass type, and that type carries the approval rule,
// the validity window and the dwell threshold the tenant set.
type LogRequest struct {
	Kind              string     `json:"kind" validate:"required,oneof=delivery fleet visitor"`
	Plate             string     `json:"plate" validate:"omitempty,max=32"`
	VehicleID         int64      `json:"vehicle_id" validate:"omitempty,gt=0"`
	DriverName        string     `json:"driver_name" validate:"omitempty,max=255"`
	DriverPhone       string     `json:"driver_phone" validate:"omitempty,max=32"`
	DriverIDType      string     `json:"driver_id_type" validate:"omitempty,oneof=national_id passport drivers_licence work_permit other"`
	DriverStaffID     int64      `json:"driver_staff_id" validate:"omitempty,gt=0"`
	EscortName        string     `json:"escort_name" validate:"omitempty,max=255"`
	Purpose           string     `json:"purpose" validate:"omitempty,max=512"`
	Cargo             string     `json:"cargo_description" validate:"omitempty,max=512"`
	ContainerNo       string     `json:"container_no" validate:"omitempty,max=32"`
	SealNo            string     `json:"seal_no" validate:"omitempty,max=64"`
	ExpectedEntry     *time.Time `json:"expected_entry_at"`
	ExpectedExit      *time.Time `json:"expected_exit_at"`
	DwellLimitMinutes *int       `json:"dwell_limit_minutes" validate:"omitempty,gte=1,lte=10080"`
	Origin            string     `json:"origin" validate:"omitempty,oneof=walk_in pre_registered online api import device renewal"`
	Notes             string     `json:"notes" validate:"omitempty,max=2000"`
}

func (req LogRequest) toLog() Log {
	return Log{
		Kind: req.Kind, Plate: req.Plate, VehicleID: req.VehicleID, DriverName: req.DriverName,
		DriverPhone: req.DriverPhone, DriverIDType: req.DriverIDType,
		DriverStaffID: req.DriverStaffID, EscortName: req.EscortName, Purpose: req.Purpose,
		Cargo: req.Cargo, ContainerNo: req.ContainerNo, SealNo: req.SealNo,
		ExpectedEntry: req.ExpectedEntry, ExpectedExit: req.ExpectedExit,
		DwellLimitMinutes: req.DwellLimitMinutes, Origin: req.Origin, Notes: req.Notes,
	}
}

// DecisionRequest is the approve or reject click. An explicit action beats a boolean a
// client can send as false by accident.
type DecisionRequest struct {
	Action string `json:"action" validate:"required,oneof=approve reject"`
	Note   string `json:"note" validate:"omitempty,max=512"`
}

// CancelRequest withdraws a journey that has not been driven yet.
type CancelRequest struct {
	Note string `json:"note" validate:"omitempty,max=512"`
}

// VehicleRequest registers a plate with its paperwork. The plate is the key, so it is
// required here and absent from the PATCH: a plate typed wrong is a new registration.
type VehicleRequest struct {
	Plate                    string     `json:"plate" validate:"required,max=32"`
	CountryCode              string     `json:"country_code" validate:"omitempty,len=2"`
	Make                     string     `json:"make" validate:"omitempty,max=64"`
	Model                    string     `json:"model" validate:"omitempty,max=64"`
	Color                    string     `json:"color" validate:"omitempty,max=32"`
	Class                    string     `json:"class" validate:"omitempty,oneof=car motorcycle van pickup truck bus trailer heavy_machinery bicycle other"`
	BodyType                 string     `json:"body_type" validate:"omitempty,max=32"`
	Fleet                    bool       `json:"is_fleet"`
	OwnerType                string     `json:"owner_type" validate:"omitempty,oneof=staff contractor company visitor tenant unknown"`
	OwnerUserID              int64      `json:"owner_user_id" validate:"omitempty,gt=0"`
	OwnerStaffID             int64      `json:"owner_staff_id" validate:"omitempty,gt=0"`
	OwnerLabel               string     `json:"owner_label" validate:"omitempty,max=255"`
	InsuranceExpiry          *time.Time `json:"insurance_expiry"`
	FitnessExpiry            *time.Time `json:"fitness_expiry"`
	PermitRequired           bool       `json:"permit_required"`
	DefaultDwellLimitMinutes *int       `json:"default_dwell_limit_minutes" validate:"omitempty,gte=1,lte=10080"`
	Notes                    string     `json:"notes" validate:"omitempty,max=2000"`
	ExternalRef              string     `json:"external_ref" validate:"omitempty,max=128"`
}

func (req VehicleRequest) toNewVehicle() NewVehicle {
	return NewVehicle{
		PlateNormalized: req.Plate, CountryCode: req.CountryCode, Make: req.Make,
		Model: req.Model, Color: req.Color, Class: req.Class, BodyType: req.BodyType,
		Fleet: req.Fleet, OwnerType: req.OwnerType, OwnerUserID: req.OwnerUserID,
		OwnerStaffID: req.OwnerStaffID, OwnerLabel: req.OwnerLabel,
		InsuranceExpiry: req.InsuranceExpiry, FitnessExpiry: req.FitnessExpiry,
		PermitRequired: req.PermitRequired, DefaultDwellLimitMinutes: req.DefaultDwellLimitMinutes,
		Notes: req.Notes, ExternalRef: req.ExternalRef,
	}
}

// UpdateVehicleRequest is the register PATCH. A nil means "leave what is on file", so an
// expiry can be cleared by sending it as null only where clearing it is honest - here the
// two dates and the notes, never the plate.
type UpdateVehicleRequest struct {
	Make                     *string    `json:"make" validate:"omitempty,max=64"`
	Model                    *string    `json:"model" validate:"omitempty,max=64"`
	Color                    *string    `json:"color" validate:"omitempty,max=32"`
	Class                    *string    `json:"class" validate:"omitempty,oneof=car motorcycle van pickup truck bus trailer heavy_machinery bicycle other"`
	BodyType                 *string    `json:"body_type" validate:"omitempty,max=32"`
	Fleet                    *bool      `json:"is_fleet"`
	OwnerType                *string    `json:"owner_type" validate:"omitempty,oneof=staff contractor company visitor tenant unknown"`
	OwnerLabel               *string    `json:"owner_label" validate:"omitempty,max=255"`
	InsuranceExpiry          *time.Time `json:"insurance_expiry"`
	FitnessExpiry            *time.Time `json:"fitness_expiry"`
	PermitRequired           *bool      `json:"permit_required"`
	DefaultDwellLimitMinutes *int       `json:"default_dwell_limit_minutes" validate:"omitempty,gte=1,lte=10080"`
	Status                   *string    `json:"status" validate:"omitempty,oneof=active blocked archived"`
	Notes                    *string    `json:"notes" validate:"omitempty,max=2000"`
}

func (req UpdateVehicleRequest) toUpdate() UpdateVehicle {
	return UpdateVehicle{
		Make: req.Make, Model: req.Model, Color: req.Color, Class: req.Class,
		BodyType: req.BodyType, Fleet: req.Fleet, OwnerType: req.OwnerType,
		OwnerLabel: req.OwnerLabel, InsuranceExpiry: req.InsuranceExpiry,
		FitnessExpiry: req.FitnessExpiry, PermitRequired: req.PermitRequired,
		DefaultDwellLimitMinutes: req.DefaultDwellLimitMinutes, Status: req.Status,
		Notes: req.Notes,
	}
}

// PassView is a journey. driver_id_number is not here because it is not stored: the gate
// reads the licence from the document in hand, and the column behind it is retention-bound.
type PassView struct {
	ID               int64      `json:"id"`
	PassID           int64      `json:"pass_id"`
	PassNo           string     `json:"pass_no"`
	PassStatus       string     `json:"pass_status"`
	EntryCount       int        `json:"entry_count"`
	VehicleID        *int64     `json:"vehicle_id,omitempty"`
	PlateNormalized  string     `json:"plate_normalized"`
	PlateDisplay     *string    `json:"plate_display,omitempty"`
	Fleet            bool       `json:"is_fleet_snapshot"`
	DriverName       *string    `json:"driver_name,omitempty"`
	DriverPhone      *string    `json:"driver_phone,omitempty"`
	DriverIDType     *string    `json:"driver_id_type,omitempty"`
	DriverStaffID    *int64     `json:"driver_staff_id,omitempty"`
	EscortName       *string    `json:"escort_name,omitempty"`
	Purpose          *string    `json:"purpose,omitempty"`
	CargoDescription *string    `json:"cargo_description,omitempty"`
	ContainerNo      *string    `json:"container_no,omitempty"`
	SealNo           *string    `json:"seal_no,omitempty"`
	ExpectedEntryAt  *time.Time `json:"expected_entry_at,omitempty"`
	ExpectedExitAt   *time.Time `json:"expected_exit_at,omitempty"`
	EnteredAt        *time.Time `json:"entered_at,omitempty"`
	ExitedAt         *time.Time `json:"exited_at,omitempty"`
	DwellLimit       *int       `json:"dwell_limit_minutes,omitempty"`
	ActualDwell      *int       `json:"actual_dwell_minutes,omitempty"`
	Overstaying      bool       `json:"is_overstaying"`
	Status           string     `json:"status"`
	Notes            *string    `json:"notes,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
}

func viewOf(p Pass) PassView {
	return PassView{
		ID: p.ID, PassID: p.PassID, PassNo: p.PassNo, PassStatus: p.PassStatus,
		EntryCount: p.EntryCount, VehicleID: p.VehicleID, PlateNormalized: p.PlateNormalized,
		PlateDisplay: p.PlateDisplay, Fleet: p.FleetSnapshot, DriverName: p.DriverName,
		DriverPhone: p.DriverPhone, DriverIDType: p.DriverIDType, DriverStaffID: p.DriverStaffID,
		EscortName: p.EscortName, Purpose: p.Purpose, CargoDescription: p.CargoDescription,
		ContainerNo: p.ContainerNo, SealNo: p.SealNo, ExpectedEntryAt: p.ExpectedEntryAt,
		ExpectedExitAt: p.ExpectedExitAt, EnteredAt: p.EnteredAt, ExitedAt: p.ExitedAt,
		DwellLimit: p.DwellLimitMinutes, ActualDwell: p.ActualDwell,
		Overstaying: p.Overstaying, Status: p.Status, Notes: p.Notes, CreatedAt: p.CreatedAt,
	}
}

func viewsOfPasses(passes []Pass) []PassView {
	views := make([]PassView, 0, len(passes))
	for _, journey := range passes {
		views = append(views, viewOf(journey))
	}
	return views
}

// VehicleView is the register card. insurance_expired is computed, not stored: it is the
// one paperwork fact a desk user reads before the dates themselves.
type VehicleView struct {
	ID                       int64      `json:"id"`
	PlateNormalized          string     `json:"plate_normalized"`
	PlateDisplay             string     `json:"plate_display"`
	CountryCode              *string    `json:"country_code,omitempty"`
	Make                     *string    `json:"make,omitempty"`
	Model                    *string    `json:"model,omitempty"`
	Color                    *string    `json:"color,omitempty"`
	Class                    string     `json:"class"`
	BodyType                 *string    `json:"body_type,omitempty"`
	Fleet                    bool       `json:"is_fleet"`
	OwnerType                string     `json:"owner_type"`
	OwnerUserID              *int64     `json:"owner_user_id,omitempty"`
	OwnerStaffID             *int64     `json:"owner_staff_id,omitempty"`
	OwnerLabel               *string    `json:"owner_label,omitempty"`
	InsuranceExpiry          *time.Time `json:"insurance_expiry,omitempty"`
	InsuranceExpired         bool       `json:"insurance_expired"`
	FitnessExpiry            *time.Time `json:"fitness_expiry,omitempty"`
	PermitRequired           bool       `json:"permit_required"`
	DefaultDwellLimitMinutes *int       `json:"default_dwell_limit_minutes,omitempty"`
	Status                   string     `json:"status"`
	Notes                    *string    `json:"notes,omitempty"`
	ExternalRef              *string    `json:"external_ref,omitempty"`
	CreatedAt                time.Time  `json:"created_at"`
	UpdatedAt                time.Time  `json:"updated_at"`
}

func viewOfVehicle(v Vehicle) VehicleView {
	now := time.Now().UTC()
	return VehicleView{
		ID: v.ID, PlateNormalized: v.PlateNormalized, PlateDisplay: v.PlateDisplay,
		CountryCode: v.CountryCode, Make: v.Make, Model: v.Model, Color: v.Color,
		Class: v.Class, BodyType: v.BodyType, Fleet: v.Fleet, OwnerType: v.OwnerType,
		OwnerUserID: v.OwnerUserID, OwnerStaffID: v.OwnerStaffID, OwnerLabel: v.OwnerLabel,
		InsuranceExpiry: v.InsuranceExpiry, InsuranceExpired: v.InsuranceExpired(now),
		FitnessExpiry: v.FitnessExpiry, PermitRequired: v.PermitRequired,
		DefaultDwellLimitMinutes: v.DefaultDwellLimitMinutes, Status: v.Status,
		Notes: v.Notes, ExternalRef: v.ExternalRef, CreatedAt: v.CreatedAt,
		UpdatedAt: v.UpdatedAt,
	}
}

func viewsOfVehicles(vehicles []Vehicle) []VehicleView {
	views := make([]VehicleView, 0, len(vehicles))
	for _, vehicle := range vehicles {
		views = append(views, viewOfVehicle(vehicle))
	}
	return views
}

// ParseList reads the journey register filters: ?status=&vehicle_id=&plate=&search=.
func ParseList(r *http.Request) (ListPassFilter, httpx.Page, error) {
	query := r.URL.Query()
	vehicleID, err := optionalID(query, "vehicle_id")
	if err != nil {
		return ListPassFilter{}, httpx.Page{}, err
	}
	filter := ListPassFilter{
		Status: query.Get("status"), VehicleID: vehicleID,
		Plate: NormalizePlate(query.Get("plate")), Search: query.Get("search"),
	}
	if filter.Status != "" && !validStatus(filter.Status) {
		return filter, httpx.Page{}, errs.BadField("status", filter.Status+" is not a vehicle pass status")
	}
	if query.Get("plate") != "" && filter.Plate == "" {
		return filter, httpx.Page{}, errs.BadField("plate", "is not a plate")
	}
	if len(filter.Search) > 128 {
		return filter, httpx.Page{}, errs.BadField("search", "too long")
	}
	page, err := httpx.ParsePage(r, defaultPage, 100)
	return filter, page, err
}

// ParseVehicles reads the fleet register filters: ?status=&class=&fleet=&search=.
func ParseVehicles(r *http.Request) (ListVehicleFilter, httpx.Page, error) {
	query := r.URL.Query()
	fleet, err := optionalBool(query, "fleet")
	if err != nil {
		return ListVehicleFilter{}, httpx.Page{}, err
	}
	filter := ListVehicleFilter{
		Status: query.Get("status"), Class: query.Get("class"),
		Fleet: fleet, Search: query.Get("search"),
	}
	if filter.Status != "" && filter.Status != VehicleActive && filter.Status != VehicleBlocked &&
		filter.Status != VehicleArchived {
		return filter, httpx.Page{}, errs.BadField("status", "must be active, blocked or archived")
	}
	if filter.Class != "" && len(filter.Class) > 24 {
		return filter, httpx.Page{}, errs.BadField("class", "too long")
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
		StatusOnSite, StatusCompleted, StatusOverdue, StatusExpired,
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
// rather than ignored: silently dropping ?fleet=maybe would list the whole fleet.
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
