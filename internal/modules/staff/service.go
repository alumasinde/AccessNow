package staff

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

// Service owns the requests and their answers. The gate owns the two facts that actually
// matter - the employee went out, and the employee came back - so this service writes the
// paperwork and lets the movement reaction stamp the times, the same division of work the
// visitor, goods and vehicle modules use.
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

// Request is one step-out as the employee or the desk files it. StaffID 0 means "me": an
// employee with a login asks for themselves and the directory row is found behind the
// session, so nobody has to know their own employee number.
type Request struct {
	StaffID        int64
	ReasonKey      string
	ReasonNote     string
	Destination    string
	VehiclePlate   string
	CompanyVehicle bool
	Duration       *int
	ExpectedReturn *time.Time
	EndOfShift     *time.Time
	Origin         string
	Notes          string
}

func (req Request) toNew() New {
	return New{
		StaffID: req.StaffID, ReasonKey: req.ReasonKey, ReasonNote: req.ReasonNote,
		Destination: req.Destination, VehiclePlate: req.VehiclePlate,
		CompanyVehicle: req.CompanyVehicle, ExpectedDuration: req.Duration,
		ExpectedReturn: req.ExpectedReturn, EndOfShift: req.EndOfShift, Notes: req.Notes,
	}
}

func (in New) validate() error {
	if in.ReasonKey == "" {
		return errs.BadField("reason_key", "pick a reason")
	}
	// The catalogue row called Other is the one whose text has to be typed: a register that
	// says "Other" three hundred times tells the supervisor nothing.
	if in.ReasonKey == reasonOther && in.ReasonNote == "" {
		return errs.BadField("reason_note", "say what the reason was")
	}
	return nil
}

// Escalation is what one sweep moved. It is a count, not a page of rows: the caller is a
// cron job or a desk button, and both only want to know whether anything aged out.
type Escalation struct {
	Overdue    int `json:"overdue"`
	Unreturned int `json:"unreturned"`
}

// Request files the step-out and asks Core for the pass. Nothing here decides whether the
// employee may leave - that is the supervisor's click and, later, the guard's scan.
func (s *Service) Request(ctx context.Context, scope tenancy.Scope, req Request) (Outpass, error) {
	if scope.SiteID == 0 {
		return Outpass{}, errs.Forbidden("no site on this session")
	}
	in := req.toNew()
	if err := in.validate(); err != nil {
		return Outpass{}, err
	}

	passType, err := s.passRepo.TypeByKey(ctx, passTypeKey)
	if err != nil {
		return Outpass{}, err
	}
	reason, err := s.repo.ReasonByKey(ctx, in.ReasonKey)
	if err != nil {
		return Outpass{}, err
	}
	if !reason.Active() {
		return Outpass{}, errs.BadField("reason_key", reason.ReasonKey+" is not in use")
	}
	member, err := s.resolveStaff(ctx, scope, in.StaffID)
	if err != nil {
		return Outpass{}, err
	}
	if !member.Active() {
		return Outpass{}, errs.Conflict(member.Name() + " is " + member.Status +
			" and cannot be signed out")
	}

	duration := firstDuration(in.ExpectedDuration, reason.DefaultDurationMinutes,
		passType.DefaultDwellLimitMinutes)
	// Two rules can both insist on a supervisor: the tenant's pass type and the reason
	// itself, because "family emergency" is worth a manager even at a site that auto-approves
	// everything else. Either one is enough to leave the request pending.
	status := StatusApproved
	if passType.RequiresApproval || reason.RequiresManager {
		status = StatusPending
	}
	origin := req.Origin
	if origin == "" {
		origin = defaultOrigin(in.StaffID)
	}

	now := time.Now().UTC()
	until := passes.Window(now, passType.DefaultValidityHours)
	// There is no shift roster in this system yet, so the deadline an employee is written off
	// by is the end of the pass window the tenant configured - four hours by default. A desk
	// that knows the real shift end can still send one.
	endOfShift := firstTime(in.EndOfShift, until)
	// An outpass that needs no approval is approved the moment it is filed, and the operator
	// who filed it is the only approver there was.
	var approvedAt *time.Time
	if status == StatusApproved {
		approvedAt = &now
	}

	var filed Outpass
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		pass, err := s.passes.Issue(ctx, c, passes.Issue{
			New: passes.New{
				TenantID: scope.TenantID, SiteID: scope.SiteID, PassTypeID: passType.ID,
				CategoryKey: passType.CategoryKey, SourceModuleKey: ModuleKey,
				Status: coreStatus(status), Origin: origin, SubjectName: member.Name(),
				SubjectPhone: member.Phone, Purpose: strPtr(purpose(in, reason)),
				RequesterUserID: num(scope.UserID), DepartmentID: member.DepartmentID,
				ValidFrom: now, ValidUntil: until,
			},
			FormatKey: numberFormat, Timezone: scope.Timezone, ActorUserID: scope.UserID,
		})
		if err != nil {
			return err
		}

		id, err := s.repo.In(c).Create(ctx, NewOutpass{
			TenantID: scope.TenantID, PassID: pass.ID, StaffID: member.ID,
			DepartmentID: member.DepartmentID, SiteID: scope.SiteID, ReasonKey: reason.ReasonKey,
			ReasonNote: in.ReasonNote, Destination: in.Destination, VehiclePlate: in.VehiclePlate,
			CompanyVehicle: in.CompanyVehicle, ExpectedDuration: duration,
			ExpectedReturn: in.ExpectedReturn, EndOfShift: endOfShift,
			ReturnScanRequired: reason.RequiresReturnScan, Status: status,
			ApprovedAt: approvedAt, ApprovedByUserID: scope.UserID, Notes: in.Notes,
		})
		if err != nil {
			return err
		}

		payload := map[string]any{
			"pass_no": pass.PassNo, "employee_no": member.EmployeeNo, "staff_name": member.Name(),
			"reason_key": reason.ReasonKey, "status": string(status),
			"expected_duration_minutes": duration, "destination": in.Destination,
			"return_scan_required": reason.RequiresReturnScan,
		}
		if err := s.publisher.Publish(ctx, c,
			event(scope, "outpass.requested", id, pass.ID, payload)); err != nil {
			return err
		}
		if status == StatusPending {
			// The key the notification catalogue already knows: an approver who signed up for
			// outpass notices is waiting on this row, not on outpass.requested in general.
			if err := s.publisher.Publish(ctx, c,
				event(scope, "outpass.approval_needed", id, pass.ID, payload)); err != nil {
				return err
			}
		}
		filed, err = s.repo.In(c).ByID(ctx, scope.TenantID, id)
		return err
	})
	if err != nil {
		return Outpass{}, err
	}
	return filed, nil
}

// resolveStaff finds whose step-out this is. A named employee is checked out of the
// directory; with no name given, the session's own login is looked up, which is what makes
// self-service work without a second id in the request.
func (s *Service) resolveStaff(ctx context.Context, scope tenancy.Scope, staffID int64) (Staff, error) {
	if staffID > 0 {
		return s.repo.StaffByID(ctx, scope.TenantID, staffID)
	}
	if scope.UserID == 0 {
		return Staff{}, errs.Forbidden("no user on this session")
	}
	member, err := s.repo.StaffByUserID(ctx, scope.TenantID, scope.UserID)
	if errs.CodeOf(err) == errs.CodeNotFound {
		return Staff{}, errs.Conflict("this login has no staff record; name the employee")
	}
	return member, err
}

// Decide answers a pending request. Core moves the spine and the register mirrors it, so a
// crash between the two leaves a decided pass the desk can settle again.
func (s *Service) Decide(ctx context.Context, scope tenancy.Scope, id int64, approve bool,
	note string) (Outpass, error) {
	filed, err := s.repo.ByID(ctx, scope.TenantID, id)
	if err != nil {
		return Outpass{}, err
	}
	if filed.Decided() {
		return Outpass{}, errs.Conflict("outpass " + filed.PassNo + " is already " + filed.Status)
	}

	to, mirror := passes.StatusApproved, StatusApproved
	if !approve {
		to, mirror = passes.StatusRejected, StatusRejected
	}
	if _, err := s.passes.Decide(ctx, scope, filed.PassID, to, note); err != nil {
		return Outpass{}, err
	}
	// Keyed on pending, which is the status this request read. Only a decision moves a
	// pending row and every decision lands on the same status, so the re-read below is right
	// whether or not this update was the one that moved it.
	if _, err := s.repo.MarkDecision(ctx, scope.TenantID, id, mirror, scope.UserID, note); err != nil {
		return Outpass{}, err
	}
	return s.repo.ByID(ctx, scope.TenantID, id)
}

// Cancel takes a request back before the employee left. Like every other write here, it
// stops at the door: once a barrier has let somebody out, only the barrier closes it.
func (s *Service) Cancel(ctx context.Context, scope tenancy.Scope, id int64, note string) (Outpass, error) {
	filed, err := s.repo.ByID(ctx, scope.TenantID, id)
	if err != nil {
		return Outpass{}, err
	}
	from := Status(filed.Status)
	if from != StatusPending && from != StatusApproved {
		return Outpass{}, errs.Conflict("outpass " + filed.PassNo + " is " + filed.Status +
			", and only a step-out that has not happened can be cancelled")
	}
	if _, err := s.passes.Decide(ctx, scope, filed.PassID, passes.StatusCancelled, note); err != nil {
		return Outpass{}, err
	}
	if _, err := s.repo.MarkCancelled(ctx, scope.TenantID, id, from); err != nil {
		return Outpass{}, err
	}
	return s.repo.ByID(ctx, scope.TenantID, id)
}

// Close settles an employee who is still out without a return scan: the reason needed none,
// the badge was lost, or the supervisor settled it in person. It is the desk's authority and
// it says so in the event, because "came back" and "written off as back" are different facts.
func (s *Service) Close(ctx context.Context, scope tenancy.Scope, id int64, note string) (Outpass, error) {
	filed, err := s.repo.ByID(ctx, scope.TenantID, id)
	if err != nil {
		return Outpass{}, err
	}
	if !filed.Open() {
		return Outpass{}, errs.Conflict("outpass " + filed.PassNo + " is " + filed.Status +
			", and only an employee who is still out can be closed")
	}

	now := time.Now().UTC()
	minutes := derefMinutes(filed.MinutesOut(now))
	over := filed.PastExpected(now)
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		moved, err := s.repo.In(c).MarkReturned(ctx, scope.TenantID, id, 0, 0, minutes, over)
		if err != nil || !moved {
			return err
		}
		return s.publish(ctx, c, scope, filed, "outpass.returned", map[string]any{
			"minutes_out": minutes, "by": "desk", "note": note,
		})
	})
	if err != nil {
		return Outpass{}, err
	}
	if err := s.finish(ctx, scope, filed.PassID, "outpass closed by the desk"); err != nil {
		return Outpass{}, err
	}
	return s.repo.ByID(ctx, scope.TenantID, id)
}

// Escalate is the sweep: employees past the time they gave become overdue, and the ones
// still out at the end of their pass window become unreturned. Nothing in this system runs
// a clock of its own, so this is the endpoint a site's cron calls hourly - and the button a
// supervisor presses before the roll-call. Both are safe to repeat: every row is a
// compare-and-set on the status the sweep read, so a second run publishes nothing.
func (s *Service) Escalate(ctx context.Context, scope tenancy.Scope) (Escalation, error) {
	due, err := s.repo.DueForOverdue(ctx, scope.TenantID)
	if err != nil {
		return Escalation{}, err
	}
	overdue, err := s.escalate(ctx, scope, due, StatusOut, StatusOverdue, "outpass.overdue")
	if err != nil {
		return Escalation{Overdue: overdue}, err
	}

	// Read after the first pass, so a row that is past both deadlines in one run escalates
	// once and is not skipped by having been read before it became overdue.
	due, err = s.repo.DueForUnreturned(ctx, scope.TenantID)
	if err != nil {
		return Escalation{Overdue: overdue}, err
	}
	unreturned, err := s.escalate(ctx, scope, due, StatusOverdue, StatusUnreturned,
		"outpass.unreturned")
	if err != nil {
		return Escalation{Overdue: overdue, Unreturned: unreturned}, err
	}
	return Escalation{Overdue: overdue, Unreturned: unreturned}, nil
}

func (s *Service) escalate(ctx context.Context, scope tenancy.Scope, rows []Outpass,
	from, to Status, key string) (int, error) {
	moved := 0
	for _, filed := range rows {
		err := db.Transact(ctx, s.pool, func(c db.Conn) error {
			ok, err := s.repo.In(c).MarkEscalated(ctx, scope.TenantID, filed.ID, from, to)
			if err != nil || !ok {
				return err
			}
			return s.publish(ctx, c, scope, filed, key, map[string]any{"swept": true})
		})
		if err != nil {
			return moved, err
		}
		moved++
	}
	return moved, nil
}

// finish closes the Core pass once the employee is back. This is what turns a second exit
// red: Core would happily let a used pass out again inside its window, and an outpass that
// has been returned is not a permission to step out a second time.
func (s *Service) finish(ctx context.Context, scope tenancy.Scope, passID int64, note string) error {
	current, err := s.passRepo.ByID(ctx, scope.TenantID, passID)
	if err != nil {
		return err
	}
	if current.Status == string(passes.StatusCompleted) || !current.Decidable() {
		return nil
	}
	_, err = s.passes.Decide(ctx, scope, passID, passes.StatusCompleted, note)
	return err
}

func coreStatus(status Status) passes.Status {
	if status == StatusPending {
		return passes.StatusPending
	}
	return passes.StatusApproved
}

// purpose is the one line Core shows on the pass. The reason names it and the note, when the
// desk gave one, is the detail.
func purpose(in New, reason Reason) string {
	if in.ReasonNote == "" {
		return reason.Name
	}
	return reason.Name + ": " + in.ReasonNote
}

// defaultOrigin records who asked. An employee with no login can only be signed out by
// somebody at a desk, which is walk_in; a login asking for itself came from the app. Both
// values are in chk_passes_origin - the vocabulary belongs to Core, not to this module.
func defaultOrigin(staffID int64) string {
	if staffID == 0 {
		return "online"
	}
	return "walk_in"
}

// firstDuration is the request's number, else the reason's, else the pass type's. The order
// is the whole point: the desk can override a rule for one employee, and an empty catalogue
// row still leaves every outpass judged against something.
func firstDuration(explicit, reason, passType *int) *int {
	for _, candidate := range []*int{explicit, reason, passType} {
		if candidate != nil && *candidate > 0 {
			return candidate
		}
	}
	return nil
}

func firstTime(explicit, fallback *time.Time) *time.Time {
	if explicit != nil {
		return explicit
	}
	return fallback
}

func event(scope tenancy.Scope, key string, outpassID, passID int64, payload map[string]any) events.Event {
	return events.Event{
		TenantID: scope.TenantID, EventKey: key, AggregateType: aggregateType,
		AggregateID: outpassID, PassID: passID, SiteID: scope.SiteID, GateID: scope.GateID,
		ActorUserID: scope.UserID, Payload: payload,
	}
}

func derefMinutes(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func num(value int64) *int64 {
	if value == 0 {
		return nil
	}
	return &value
}

func strPtr(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
