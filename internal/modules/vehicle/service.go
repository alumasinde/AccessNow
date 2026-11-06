package vehicle

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"

	"accessnow/internal/core/passes"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/events"
	"accessnow/internal/platform/tenancy"
)

// Service owns the register and the journeys. It writes the detail row, asks Core for the
// pass, and lets the gate decide when the plate actually moved - the same division of work
// the goods module uses, so a barrier never waits on a desk.
type Service struct {
	pool      *sqlx.DB
	repo      *Repository
	passes    *passes.Service
	passRepo  *passes.Repository
	publisher events.Publisher
}

func NewService(pool *sqlx.DB, repo *Repository, passService *passes.Service,
	passRepo *passes.Repository, publisher events.Publisher) *Service {
	return &Service{pool: pool, repo: repo, passes: passService, passRepo: passRepo, publisher: publisher}
}

// typeKeyFor maps the three kinds the desk offers onto the seeded catalogue rows. The
// tenant edits what those rows mean - approval, validity, dwell - without this module
// having to know anything about it.
func typeKeyFor(kind string) string { return "vehicle_" + kind }

func validKind(kind string) bool {
	return kind == KindDelivery || kind == KindFleet || kind == KindVisitor
}

// Log is one vehicle journey as the desk or an import files it.
type Log struct {
	Kind              string
	Plate             string
	VehicleID         int64
	DriverName        string
	DriverPhone       string
	DriverIDType      string
	DriverStaffID     int64
	EscortName        string
	Purpose           string
	Cargo             string
	ContainerNo       string
	SealNo            string
	ExpectedEntry     *time.Time
	ExpectedExit      *time.Time
	DwellLimitMinutes *int
	Origin            string
	Notes             string
}

func (in Log) validate(plate string) error {
	if !validKind(in.Kind) {
		return errs.BadField("kind", "must be delivery, fleet or visitor")
	}
	if plate == "" {
		return errs.BadField("plate", "is not a plate")
	}
	// A fleet vehicle has its driver on the register and comes and goes all day; the two
	// kinds a stranger drives must say who is driving, or the roster has a hole in it.
	if in.Kind != KindFleet && in.DriverName == "" {
		return errs.BadField("driver_name", "required on a "+in.Kind+" vehicle pass")
	}
	if in.Kind == KindVisitor && in.EscortName == "" {
		return errs.BadField("escort_name", "a visitor vehicle needs the person it came to see")
	}
	return nil
}

// Log files the journey. An unknown plate is registered as it is logged, so the first
// truck ever to arrive is also the first row in the register - a gate cannot be told to
// wait while somebody fills in a form.
func (s *Service) Log(ctx context.Context, scope tenancy.Scope, in Log) (Pass, error) {
	if scope.SiteID == 0 {
		return Pass{}, errs.Forbidden("no site on this session")
	}
	plate := NormalizePlate(in.Plate)
	if err := in.validate(plate); err != nil {
		return Pass{}, err
	}
	passType, err := s.passRepo.TypeByKey(ctx, typeKeyFor(in.Kind))
	if err != nil {
		return Pass{}, err
	}

	status := StatusApproved
	if passType.RequiresApproval {
		status = StatusPending
	}
	origin := in.Origin
	if origin == "" {
		origin = "walk_in"
	}
	now := time.Now().UTC()
	until := passes.Window(now, passType.DefaultValidityHours)

	var logged Pass
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		vehicle, err := s.resolve(ctx, repo, scope.TenantID, scope.UserID, in, plate)
		if err != nil {
			return err
		}
		// A blocked plate is refused here, not at the barrier: the desk already knows the
		// vehicle is not wanted, and a pass it issues would only be red at the gate.
		if vehicle.Blocked() {
			return errs.Conflict("plate " + plate + " is " + vehicle.Status)
		}
		pass, err := s.passes.Issue(ctx, c, passes.Issue{
			New: passes.New{
				TenantID: scope.TenantID, SiteID: scope.SiteID, PassTypeID: passType.ID,
				CategoryKey: passType.CategoryKey, SourceModuleKey: ModuleKey,
				Status: passStatus(status), Origin: origin, SubjectName: in.DriverName,
				SubjectPhone:    blankToNil(in.DriverPhone),
				Purpose:         blankToNil(purpose(in)),
				RequesterUserID: num(scope.UserID), ValidFrom: now, ValidUntil: until,
			},
			FormatKey: numberFormat, Timezone: scope.Timezone, ActorUserID: scope.UserID,
		})
		if err != nil {
			return err
		}

		detail := in.NewPass(scope.TenantID, pass.ID, vehicle, plate, status)
		if detail.DwellLimit == nil {
			detail.DwellLimit = passType.DefaultDwellLimitMinutes
		}
		id, err := repo.CreatePass(ctx, detail)
		if err != nil {
			return err
		}

		payload := map[string]any{
			"pass_no": pass.PassNo, "kind": in.Kind, "plate": plate, "status": string(status),
			"driver": in.DriverName, "dwell_limit_minutes": detail.DwellLimit,
		}
		if err := s.publisher.Publish(ctx, c, event(scope, "vehicle.logged", id, pass.ID, payload)); err != nil {
			return err
		}
		if status == StatusPending {
			if err := s.publisher.Publish(ctx, c,
				event(scope, "vehicle.approval_needed", id, pass.ID, payload)); err != nil {
				return err
			}
		}
		logged, err = repo.PassByID(ctx, scope.TenantID, id)
		return err
	})
	if err != nil {
		return Pass{}, err
	}
	return logged, nil
}

// NewPass turns the log plus the vehicle it belongs to into the detail row.
func (in Log) NewPass(tenantID, passID int64, v Vehicle, plate string, status Status) NewPass {
	return NewPass{
		TenantID: tenantID, PassID: passID, VehicleID: v.ID, PlateNorm: plate,
		PlateDisplay: displayPlate(in, v), FleetSnapshot: v.Fleet, DriverName: in.DriverName,
		DriverPhone: in.DriverPhone, DriverIDType: in.DriverIDType, DriverStaffID: in.DriverStaffID,
		EscortName: in.EscortName, Purpose: in.Purpose, Cargo: in.Cargo,
		ContainerNo: in.ContainerNo, SealNo: in.SealNo, ExpectedEntry: in.ExpectedEntry,
		ExpectedExit: in.ExpectedExit, DwellLimit: in.DwellLimitMinutes, Status: status,
		Notes: in.Notes,
	}
}

// resolve finds the register row for the plate, filing a bare one when the vehicle has
// never been seen. A caller that names a vehicle by id gets that vehicle or nothing: the
// plate it carries is then the plate on the pass, so the two can never disagree.
func (s *Service) resolve(ctx context.Context, repo *Repository, tenantID, actorUserID int64,
	in Log, plate string) (Vehicle, error) {
	if in.VehicleID > 0 {
		return repo.VehicleByID(ctx, tenantID, in.VehicleID)
	}
	vehicle, err := repo.VehicleByPlate(ctx, tenantID, plate)
	if err == nil {
		return vehicle, nil
	}
	if errs.CodeOf(err) != errs.CodeNotFound {
		return Vehicle{}, err
	}
	id, err := repo.CreateVehicle(ctx, NewVehicle{
		TenantID: tenantID, PlateNormalized: plate, PlateDisplay: firstNonBlank(in.Plate, plate),
		CreatedBy: actorUserID,
	})
	if err != nil {
		if !db.IsDuplicate(err) {
			return Vehicle{}, err
		}
		// Somebody registered it while this request was in flight. Their row wins: it has
		// the paperwork on it, and the pass only needs a plate.
		return repo.VehicleByPlate(ctx, tenantID, plate)
	}
	return repo.VehicleByID(ctx, tenantID, id)
}

// Decide answers a pending vehicle pass. Core owns the spine and the journey mirrors it, so
// a crash between the two leaves a decided pass the desk can settle again.
func (s *Service) Decide(ctx context.Context, scope tenancy.Scope, id int64, approve bool, note string) (Pass, error) {
	journey, err := s.repo.PassByID(ctx, scope.TenantID, id)
	if err != nil {
		return Pass{}, err
	}
	if journey.Status != string(StatusPending) {
		return Pass{}, errs.Conflict("vehicle pass " + journey.PassNo + " is already " + journey.Status)
	}

	to, mirror := passes.StatusApproved, StatusApproved
	if !approve {
		to, mirror = passes.StatusRejected, StatusRejected
	}
	if _, err := s.passes.Decide(ctx, scope, journey.PassID, to, note); err != nil {
		return Pass{}, err
	}
	// The mirror is keyed on pending, which is the status this request read. Only a desk
	// decision moves a pending row, and a second desk decision lands on the same status, so
	// the re-read below is right whether or not this update was the one that moved it.
	if _, err := s.repo.MarkStatus(ctx, scope.TenantID, id, StatusPending, mirror); err != nil {
		return Pass{}, err
	}
	return s.repo.PassByID(ctx, scope.TenantID, id)
}

// Cancel takes a journey back before it has been driven. It is the one desk write that
// does not have a gate twin: once a plate has crossed a barrier, only the barrier may undo
// it, and it does that by scanning the other way.
func (s *Service) Cancel(ctx context.Context, scope tenancy.Scope, id int64, note string) (Pass, error) {
	journey, err := s.repo.PassByID(ctx, scope.TenantID, id)
	if err != nil {
		return Pass{}, err
	}
	if journey.Status != string(StatusPending) && journey.Status != string(StatusApproved) {
		return Pass{}, errs.Conflict("vehicle pass " + journey.PassNo + " is " + journey.Status +
			", and only a pass that has not moved can be cancelled")
	}
	if _, err := s.passes.Decide(ctx, scope, journey.PassID, passes.StatusCancelled, note); err != nil {
		return Pass{}, err
	}
	if _, err := s.repo.MarkStatus(ctx, scope.TenantID, id, Status(journey.Status), StatusCancelled); err != nil {
		return Pass{}, err
	}
	return s.repo.PassByID(ctx, scope.TenantID, id)
}

// Register files a plate in advance: the fleet list, a contractor's truck, the MD's car.
func (s *Service) Register(ctx context.Context, scope tenancy.Scope, in NewVehicle) (Vehicle, error) {
	in.TenantID = scope.TenantID
	in.CreatedBy = scope.UserID
	in.PlateNormalized = NormalizePlate(in.PlateNormalized)
	if in.PlateNormalized == "" {
		return Vehicle{}, errs.BadField("plate", "is not a plate")
	}
	if in.PlateDisplay == "" {
		in.PlateDisplay = in.PlateNormalized
	}

	id, err := s.repo.CreateVehicle(ctx, in)
	if err != nil {
		if db.IsDuplicate(err) {
			return Vehicle{}, errs.Conflict("plate " + in.PlateNormalized + " is already registered")
		}
		return Vehicle{}, err
	}
	return s.repo.VehicleByID(ctx, scope.TenantID, id)
}

func (s *Service) UpdateVehicle(ctx context.Context, scope tenancy.Scope, id int64, in UpdateVehicle) (Vehicle, error) {
	if in.Status != nil {
		switch *in.Status {
		case VehicleActive, VehicleBlocked, VehicleArchived:
		default:
			return Vehicle{}, errs.BadField("status", "must be active, blocked or archived")
		}
	}
	if _, err := s.repo.VehicleByID(ctx, scope.TenantID, id); err != nil {
		return Vehicle{}, err
	}
	if err := s.repo.UpdateVehicle(ctx, scope.TenantID, id, in); err != nil {
		return Vehicle{}, err
	}
	return s.repo.VehicleByID(ctx, scope.TenantID, id)
}

func num(value int64) *int64 {
	if value == 0 {
		return nil
	}
	return &value
}

func passStatus(status Status) passes.Status {
	if status == StatusPending {
		return passes.StatusPending
	}
	return passes.StatusApproved
}

func purpose(in Log) string {
	for _, candidate := range []string{in.Purpose, in.Cargo, in.EscortName} {
		if candidate != "" {
			return candidate
		}
	}
	return "vehicle visit"
}

func displayPlate(in Log, v Vehicle) string {
	if in.Plate != "" {
		return in.Plate
	}
	return v.PlateDisplay
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func event(scope tenancy.Scope, key string, journeyID, passID int64, payload map[string]any) events.Event {
	return events.Event{
		TenantID: scope.TenantID, EventKey: key, AggregateType: "vehicle_pass",
		AggregateID: journeyID, PassID: passID, SiteID: scope.SiteID, GateID: scope.GateID,
		ActorUserID: scope.UserID, Payload: payload,
	}
}

func blankToNil(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
