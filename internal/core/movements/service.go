package movements

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"accessnow/internal/core/passes"
	"accessnow/internal/platform/audit"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/events"
	"accessnow/internal/platform/logging"
	"accessnow/internal/platform/tenancy"
)

type Service struct {
	pool     *sqlx.DB
	repo     *Repository
	passes   *passes.Repository
	chain    audit.Writer
	events   events.Publisher
	relay    events.Relay
	screener Screener
}

// relay is the outbox drain. It is a dependency rather than a goroutine so a scan's
// reactions finish before the response goes out, and a failure never costs a movement.
// screener is the list a barrier consults before it opens; nil means the site does not
// screen at all, which is how the gate keeps working with the module switched off.
func NewService(pool *sqlx.DB, repo *Repository, passRepo *passes.Repository, chain audit.Writer,
	bus events.Publisher, relay events.Relay, screener Screener) *Service {
	return &Service{pool: pool, repo: repo, passes: passRepo, chain: chain, events: bus,
		relay: relay, screener: screener}
}

// Scan is the whole gate decision: window, status and gate category are checked here, the
// row is appended, and the result is the only thing the tablet needs.
func (s *Service) Scan(ctx context.Context, scope tenancy.Scope, scan Scan) (Outcome, error) {
	if scope.GateID == 0 {
		return Outcome{}, errs.New(errs.CodeForbidden, "no active gate on this session")
	}
	if scan.Direction != DirectionIn && scan.Direction != DirectionOut {
		return Outcome{}, errs.Invalid("direction must be in or out")
	}
	pass, err := s.loadPass(ctx, scope, scan)
	if err != nil {
		return Outcome{}, err
	}

	refused, verdict := s.refuse(ctx, scope, pass, scan)
	result := ResultGranted
	if refused != "" {
		result = ResultDenied
	}

	movement, replayed, err := s.record(ctx, scope, pass, scan, result, refused)
	if err != nil {
		return Outcome{}, err
	}
	if replayed {
		// A retry gets the answer that was given the first time, not a fresh decision:
		// the pass has moved on since, and re-deciding would show a different colour.
		return Outcome{
			Movement: movement, Result: Result(movement.Result), Reason: movement.Reason,
			PassNo: pass.PassNo, Status: pass.Status,
		}, nil
	}
	s.note(ctx, scope, pass, verdict, movement, result, refused)
	s.drain(ctx, scope)
	return Outcome{
		Movement: movement, Result: result, Reason: refused,
		PassNo: pass.PassNo, Status: pass.Status,
	}, nil
}

// note hands the screening answer back to whoever gave it, once the movement it decided is
// on file. The guard already has his colour, so a list that fails to write its own record is
// logged and left alone: it never turns a green light into an error page.
func (s *Service) note(ctx context.Context, scope tenancy.Scope, pass passes.Pass,
	verdict Verdict, movement Movement, result Result, reason string) {
	if s.screener == nil || len(verdict.Matches) == 0 {
		return
	}
	if err := s.screener.Note(ctx, verdict, HitRef{
		MovementID: movement.ID, TenantID: scope.TenantID, SiteID: pass.SiteID,
		GateID: scope.GateID, GateSessionID: scope.GateShiftID, ActorUserID: scope.UserID,
		PassID: pass.ID, PassNo: pass.PassNo,
		SubjectName: firstOf(derefString(movement.SubjectName), pass.SubjectName),
		Result:      result, Reason: reason,
	}); err != nil {
		logging.From(ctx).ErrorContext(ctx, "screening record", "movement_id", movement.ID,
			"error", err)
	}
}

// drain runs the reactions this tenant owes, including any backlog a previous request
// left behind. The movement is already committed and the guard has his answer, so a
// handler that fails is logged and its row stays pending: a module that is switched off
// or broken never turns a green light into an error page.
func (s *Service) drain(ctx context.Context, scope tenancy.Scope) {
	if err := s.relay.Relay(ctx, scope.TenantID); err != nil {
		logging.From(ctx).ErrorContext(ctx, "event relay", "tenant_id", scope.TenantID, "error", err)
	}
}

// OnSite is the roll-call roster: one row per still-open entry, with the minutes counted
// by the view.
func (s *Service) OnSite(ctx context.Context, scope tenancy.Scope, gateID int64, limit int) ([]Occupant, error) {
	if scope.SiteID == 0 {
		return nil, errs.Forbidden("no site on this session")
	}
	return s.repo.OnSite(ctx, scope.TenantID, scope.SiteID, gateID, limit)
}

func (s *Service) loadPass(ctx context.Context, scope tenancy.Scope, scan Scan) (passes.Pass, error) {
	switch {
	case scan.PassID > 0:
		return s.passes.ByID(ctx, scope.TenantID, scan.PassID)
	case scan.PassNo != "":
		return s.passes.ByPassNo(ctx, scope.TenantID, scan.PassNo)
	default:
		return passes.Pass{}, errs.Invalid("pass_id or pass_no is required")
	}
}

// refuse returns empty when the gate should open, and the screening verdict that came with
// the answer. The status stays on the response so the guard screen can show what the pass
// actually is.
//
// The list is asked last on purpose: a pass outside its window or already on site is refused
// by facts Core holds on its own row, and a gate that is busy should not spend a query on a
// screening answer nobody needed.
func (s *Service) refuse(ctx context.Context, scope tenancy.Scope, pass passes.Pass,
	scan Scan) (string, Verdict) {
	if reason := s.ruleRefusal(ctx, scope, pass, scan); reason != "" {
		return reason, Verdict{}
	}
	if s.screener == nil {
		return "", Verdict{}
	}
	verdict, err := s.screener.Screen(ctx, queryOf(scope, pass, scan))
	if err != nil {
		// A list that could not be read is not a list with nothing on it. The gate stays shut
		// and says which of the two the guard is looking at, because "the screening service
		// is down" is a sentence he can act on and "not on the list" is not.
		logging.From(ctx).ErrorContext(ctx, "screening", "pass_no", pass.PassNo, "error", err)
		return deny("the screening list could not be read"), Verdict{}
	}
	if verdict.Blocked {
		return deny(verdict.Reason), verdict
	}
	return "", verdict
}

// ruleRefusal is the decision Core can make by itself: gate category, validity window, pass
// status and the entry an exit has to pair against.
func (s *Service) ruleRefusal(ctx context.Context, scope tenancy.Scope, pass passes.Pass, scan Scan) string {
	categories, err := s.repo.GateCategories(ctx, scope.GateID)
	if err != nil {
		return deny("could not read the gate categories")
	}
	if len(categories) > 0 && !contains(categories, pass.CategoryKey) {
		return deny("not valid at this gate")
	}
	if !pass.WithinWindow(time.Now().UTC()) {
		return deny("outside the validity window")
	}
	// One status gate for both directions: a pending pass has no permission yet and a
	// completed one has none left, whichever way the guard points the scanner.
	if !pass.Usable() {
		return deny("pass is " + pass.Status)
	}

	switch scan.Direction {
	case DirectionIn:
		if pass.Status == string(passes.StatusOnSite) {
			return deny("already on site")
		}
	case DirectionOut:
		// A pass that never came in has nothing to close. Goods leave from the gate
		// they were issued at, and a staff outpass is written at the desk: the first
		// scan on those rows is an exit, so only a pass with entries behind it needs
		// an open entry to pair against.
		if pass.EntryCount > 0 {
			if _, err := s.repo.OpenEntry(ctx, scope.TenantID, pass.ID); err != nil {
				if errs.CodeOf(err) == errs.CodeNotFound {
					return deny("no open entry to close")
				}
				return deny("could not read the open entry")
			}
		}
	}
	return ""
}

// record appends the movement and runs what a granted scan implies. The second result is
// true when this scan was already on file, i.e. the device is retrying.
func (s *Service) record(ctx context.Context, scope tenancy.Scope, pass passes.Pass, scan Scan, result Result, reason string) (Movement, bool, error) {
	source := scan.Source
	if source == "" {
		source = SourceScan
	}
	idempotency := scan.IdempotencyKey
	if idempotency == "" {
		idempotency = uuid.NewString()
	}
	occurred := time.Now().UTC()

	if scan.IdempotencyKey != "" {
		seen, err := s.repo.ByIdempotency(ctx, scope.TenantID, scan.IdempotencyKey)
		switch {
		case err == nil:
			return seen, true, nil
		case errs.CodeOf(err) != errs.CodeNotFound:
			return Movement{}, false, err
		}
	}

	var movement Movement
	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		passesRepo := s.passes.In(c)

		rec := Record{
			TenantID: scope.TenantID, SiteID: pass.SiteID, GateID: scope.GateID, PassID: pass.ID,
			GateSessionID: scope.GateShiftID, GuardUserID: scope.UserID, Direction: scan.Direction,
			Result: result, Source: source, OccurredAt: occurred,
			DeviceOccurredAt: scan.OccurredAt.UTC(), Reason: firstOf(scan.Reason, reason),
			IsLateSync:  source == SourceOffline,
			SubjectName: firstOf(scan.SubjectName, pass.SubjectName), Plate: scan.Plate,
			IdempotencyKey: idempotency,
		}
		if result == ResultGranted && scan.Direction == DirectionOut {
			if pass.EntryCount > 0 {
				entry, err := repo.OpenEntry(ctx, scope.TenantID, pass.ID)
				if err != nil {
					return err
				}
				dwell := int(occurred.Sub(entry.OccurredAt).Minutes())
				rec.CloseOfMovementID = entry.ID
				rec.DwellMinutes = &dwell
			}
			// An exit always takes the pass off the roster. Approved and on_site are
			// the two states it can be in here; either one becomes active, which is
			// Core's way of saying "used, still inside its window" and is what lets a
			// multi-entry visitor come back in without a desk touching the row.
			from := passes.Status(pass.Status)
			if from == passes.StatusApproved || from == passes.StatusOnSite {
				if _, err := passesRepo.MoveStatus(ctx, passes.Decision{
					TenantID: scope.TenantID, PassID: pass.ID, From: from,
					To: passes.StatusActive, ByUserID: scope.UserID,
				}); err != nil {
					return err
				}
			}
		}

		written, err := repo.Insert(ctx, rec)
		if err != nil {
			return err
		}
		movement = written

		if result == ResultGranted && scan.Direction == DirectionIn {
			if err := passesRepo.RecordEntry(ctx, scope.TenantID, pass.ID); err != nil {
				return err
			}
			if pass.Status == string(passes.StatusApproved) {
				if _, err := passesRepo.MoveStatus(ctx, passes.Decision{
					TenantID: scope.TenantID, PassID: pass.ID, From: passes.StatusApproved,
					To: passes.StatusOnSite, ByUserID: scope.UserID,
				}); err != nil {
					return err
				}
			}
		}

		if err := s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "gate.scan",
			SubjectType: "movement", SubjectID: written.ID, PassID: pass.ID,
			SiteID: pass.SiteID, GateID: scope.GateID, GateSessionID: scope.GateShiftID,
			Reason: reason,
			After:  map[string]any{"direction": string(scan.Direction), "result": string(result), "pass_no": pass.PassNo},
		}); err != nil {
			return err
		}

		return s.events.Publish(ctx, c, events.Event{
			TenantID: scope.TenantID, EventKey: "movement.recorded",
			AggregateType: "movement", AggregateID: written.ID, PassID: pass.ID,
			SiteID: pass.SiteID, GateID: scope.GateID, ActorUserID: scope.UserID,
			DedupeKey: idempotency,
			Payload: map[string]any{
				"direction": string(scan.Direction), "result": string(result),
				"pass_no": pass.PassNo, "category": pass.CategoryKey, "reason": reason,
			},
		})
	})
	if err != nil {
		return Movement{}, false, err
	}
	return movement, false, nil
}

func deny(reason string) string { return "denied: " + reason }

func contains(haystack []string, needle string) bool {
	needle = strings.ToLower(needle)
	for _, straw := range haystack {
		if strings.ToLower(straw) == needle {
			return true
		}
	}
	return false
}

func firstOf(candidates ...string) string {
	for _, candidate := range candidates {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}
