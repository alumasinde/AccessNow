package movements

import (
	"context"
	"strconv"
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
	policy   Policy
	// eligible is the module's own rules about its own passes, asked before the rulebook
	// because a waiver cannot resurrect paperwork that has expired.
	eligible Eligibility
	// tokens is the badge a device read, resolved before anything is decided because the token
	// is what names the pass. An unbound one refuses token reads and leaves pass numbers alone.
	tokens Tokens
	ledger Ledger
}

// relay is the outbox drain. It is a dependency rather than a goroutine so a scan's
// reactions finish before the response goes out, and a failure never costs a movement.
// screener is the list a barrier consults before it opens; nil means the site does not
// screen at all, which is how the gate keeps working with the module switched off. policy is
// the tenant's own rulebook, and nil is the same promise for the module that owns it. eligible
// is the rule a module keeps about its own passes, and an unbound one has no opinion. tokens
// is the badge a device read, and an unbound one still takes a pass number typed at the desk.
// ledger is the book kept of a replayed offline batch, and nil is that same promise a fifth
// time: the queue still lands, nobody files it.
func NewService(pool *sqlx.DB, repo *Repository, passRepo *passes.Repository, chain audit.Writer,
	bus events.Publisher, relay events.Relay, screener Screener, policy Policy,
	eligible Eligibility, tokens Tokens, ledger Ledger) *Service {
	return &Service{pool: pool, repo: repo, passes: passRepo, chain: chain, events: bus,
		relay: relay, screener: screener, policy: policy, eligible: eligible, tokens: tokens,
		ledger: ledger}
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
	read, err := s.resolve(ctx, scope, scan)
	if err != nil {
		return Outcome{}, err
	}
	if read.refusal != "" {
		// A token nobody can resolve never becomes a movement: there is no pass to write it
		// against. The read is still filed, because a refusal at a barrier is what the scan log
		// exists to explain afterwards.
		outcome := Outcome{Result: ResultDenied, Reason: deny(read.refusal)}
		s.attempt(ctx, scope, scan, read, outcome)
		return outcome, nil
	}
	pass := read.pass

	call := s.decide(ctx, scope, pass, scan)

	movement, replayed, err := s.record(ctx, scope, pass, scan, call.result, call.reason)
	if err != nil {
		return Outcome{}, err
	}
	if replayed {
		// A retry gets the answer that was given the first time, not a fresh decision:
		// the pass has moved on since, and re-deciding would show a different colour.
		return Outcome{
			Movement: movement, Result: Result(movement.Result), Reason: movement.Reason,
			PassNo: pass.PassNo, Status: pass.Status, Replayed: true,
		}, nil
	}
	s.note(ctx, scope, pass, call, movement)
	outcome := Outcome{
		Movement: movement, Result: call.result, Reason: call.reason,
		PassNo: pass.PassNo, Status: pass.Status,
	}
	s.attempt(ctx, scope, scan, read, outcome)
	s.drain(ctx, scope)
	return outcome, nil
}

// resolve names the pass: by id, by number, or through a token a device read. A token that
// cannot be honoured comes back as a refusal rather than an error, so the guard gets a red light
// and a sentence instead of a 500 his tablet cannot show.
func (s *Service) resolve(ctx context.Context, scope tenancy.Scope, scan Scan) (presented, error) {
	if scan.Token == "" {
		pass, err := s.loadPass(ctx, scope, scan)
		return presented{pass: pass}, err
	}
	verdict, err := s.tokens.Resolve(ctx, TokenQuery{
		TenantID: scope.TenantID, SiteID: scope.SiteID, GateID: scope.GateID,
		GateSessionID: scope.GateShiftID, ActorUserID: scope.UserID,
		DeviceID: scope.DeviceID, Token: scan.Token, Input: scan.Input,
		At: time.Now().UTC(),
	})
	if err != nil {
		logging.From(ctx).ErrorContext(ctx, "read the token register", "error", err)
		return presented{input: scan.Input, refusal: "the token register could not be read"}, nil
	}
	read := presented{tokenID: verdict.TokenID, input: firstOf(verdict.Input, scan.Input)}
	if verdict.Refusal != "" {
		read.refusal = verdict.Refusal
		return read, nil
	}
	pass, err := s.passes.ByID(ctx, scope.TenantID, verdict.PassID)
	if err != nil {
		if errs.CodeOf(err) == errs.CodeNotFound {
			read.refusal = "the pass behind that token is not on file"
			return read, nil
		}
		return presented{}, err
	}
	read.pass = pass
	return read, nil
}

// attempt files the raw read. It runs after the guard already has his colour, so a failure here
// is logged and never rewrites the answer.
func (s *Service) attempt(ctx context.Context, scope tenancy.Scope, scan Scan,
	read presented, outcome Outcome) {
	if read.tokenID == 0 && scan.Token == "" {
		return
	}
	at := outcome.Movement.OccurredAt
	if at.IsZero() {
		at = time.Now().UTC()
	}
	err := s.tokens.Record(ctx, Attempt{
		TenantID: scope.TenantID, SiteID: scope.SiteID, GateID: scope.GateID,
		GateSessionID: scope.GateShiftID, ActorUserID: scope.UserID,
		DeviceID: scope.DeviceID, Input: read.input, Token: scan.Token, Plate: scan.Plate,
		Direction: scan.Direction, Result: outcome.Result, Reason: outcome.Reason,
		PassID: read.pass.ID, TokenID: read.tokenID, MovementID: outcome.Movement.ID,
		PassNo: read.pass.PassNo, OccurredAt: at, IdempotencyKey: scan.IdempotencyKey,
	})
	if err != nil {
		logging.From(ctx).ErrorContext(ctx, "record scan attempt",
			"pass_no", read.pass.PassNo, "error", err)
	}
}

// judgement is the gate's answer before any of it is written down: one colour, one line, and
// the two outside answers that have to be handed back once the movement exists. query is the
// question the rulebook was given, kept so its own log describes the scan that was decided
// rather than a second reading of the pass row after the fact.
type judgement struct {
	result Result
	reason string
	screen Verdict
	policy Decision
	query  PolicyQuery
}

// note hands both outside answers back to whoever gave them, once the movement they decided
// is on file. The guard already has his colour, so a list or a rulebook that fails to keep
// its own record is logged and left alone: it never turns a green light into an error page.
func (s *Service) note(ctx context.Context, scope tenancy.Scope, pass passes.Pass,
	call judgement, movement Movement) {
	ref := HitRef{
		MovementID: movement.ID, TenantID: scope.TenantID, SiteID: pass.SiteID,
		GateID: scope.GateID, GateSessionID: scope.GateShiftID, ActorUserID: scope.UserID,
		PassID: pass.ID, PassNo: pass.PassNo,
		SubjectName: firstOf(derefString(movement.SubjectName), pass.SubjectName),
		Result:      call.result, Reason: call.reason,
	}
	if s.screener != nil && len(call.screen.Matches) > 0 {
		if err := s.screener.Note(ctx, call.screen, ref); err != nil {
			logging.From(ctx).ErrorContext(ctx, "screening record", "movement_id", movement.ID,
				"error", err)
		}
	}
	if s.policy != nil && len(call.policy.Fired) > 0 {
		if err := s.policy.Note(ctx, call.query, call.policy, ref); err != nil {
			logging.From(ctx).ErrorContext(ctx, "rule evaluation log", "movement_id", movement.ID,
				"error", err)
		}
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

// decide returns the gate's judgement. Three sources answer in a fixed order, and the first
// one that refuses ends the walk: Core's own facts about the pass row, then the tenant's
// rulebook, then the screening list.
//
// The list is last because it is the most expensive question and the most specific: a pass
// outside its window or already on site is refused by facts Core holds on its own row, and a
// gate that is busy should not spend a query on a screening answer nobody needed. The
// rulebook comes before it on purpose too - a refusal a time rule already gave is not a hit
// the list should file and raise an alarm about, and a rule cannot waive a ban: the list is
// the one answer that is never asked twice, so it is always the last word.
func (s *Service) decide(ctx context.Context, scope tenancy.Scope, pass passes.Pass,
	scan Scan) judgement {
	call := judgement{result: ResultGranted}
	if reason := s.ruleRefusal(ctx, scope, pass, scan); reason != "" {
		call.result, call.reason = ResultDenied, reason
		return call
	}

	// A module's own pass can carry rules of its own - a contractor's rota, the expiry date on
	// the permit that lets them on site - and they are asked before the tenant's rulebook,
	// because rule.override waives a policy and does not resurrect paperwork.
	if reason := s.moduleRefusal(ctx, scope, pass, scan); reason != "" {
		call.result, call.reason = ResultDenied, reason
		return call
	}

	call.policy, call.query = s.rulesFor(ctx, scope, pass, scan)
	switch call.policy.Result {
	case ResultDenied:
		call.result, call.reason = ResultDenied, deny(call.policy.Reason)
		return call
	case ResultWarned:
		call.result, call.reason = ResultWarned, "warned: "+call.policy.Reason
	case ResultOverride:
		call.result, call.reason = ResultOverride, "overridden: "+call.policy.Reason
	}

	reason, verdict := s.screening(ctx, scope, pass, scan)
	call.screen = verdict
	if reason != "" {
		call.result, call.reason = ResultDenied, reason
	}
	return call
}

// moduleRefusal is the question only the module that issued the pass can answer.
func (s *Service) moduleRefusal(ctx context.Context, scope tenancy.Scope, pass passes.Pass,
	scan Scan) string {
	if s.eligible == nil {
		return ""
	}
	reason, err := s.eligible.Check(ctx, eligibilityQueryOf(scope, pass, scan))
	if err != nil {
		logging.From(ctx).ErrorContext(ctx, "pass eligibility", "pass_no", pass.PassNo, "error", err)
		return deny("the pass's own rules could not be read")
	}
	if reason == "" {
		return ""
	}
	return deny(reason)
}

// rulesFor asks the tenant's rulebook, and gives back the question it was asked so the
// module's own log describes the scan that was decided.
//
// An error is not "no rules matched". A rulebook that could not be read shuts the gate with
// the sentence that says so, for the same reason a list that could not be read does: the
// guard can act on "the rules could not be read" and cannot act on "nothing applies".
func (s *Service) rulesFor(ctx context.Context, scope tenancy.Scope, pass passes.Pass,
	scan Scan) (Decision, PolicyQuery) {
	if s.policy == nil {
		return Decision{}, PolicyQuery{}
	}
	query := policyQueryOf(scope, pass, scan, time.Now().UTC())
	decision, err := s.policy.Decide(ctx, query)
	if err != nil {
		logging.From(ctx).ErrorContext(ctx, "rules", "pass_no", pass.PassNo, "error", err)
		return Decision{Result: ResultDenied, Reason: "the rules could not be read"}, query
	}
	return decision, query
}

// screening is the list question the barrier cannot answer for itself. The reason comes back
// prefixed, and the verdict travels with it so the list can file its own record of the hit.
func (s *Service) screening(ctx context.Context, scope tenancy.Scope, pass passes.Pass,
	scan Scan) (string, Verdict) {
	if s.screener == nil {
		return "", Verdict{}
	}
	verdict, err := s.screener.Screen(ctx, queryOf(scope, pass, scan))
	if err != nil {
		logging.From(ctx).ErrorContext(ctx, "screening", "pass_no", pass.PassNo, "error", err)
		return deny("the screening list could not be read"), Verdict{}
	}
	if verdict.Blocked {
		return deny(verdict.Reason), verdict
	}
	return "", verdict
}

// ruleRefusal is the decision Core can make by itself: the gate's own state and hours, its
// categories and permitted gates, the pass window and status, and the entry an exit has to
// pair against.
func (s *Service) ruleRefusal(ctx context.Context, scope tenancy.Scope, pass passes.Pass, scan Scan) string {
	rules, err := s.repo.GateRules(ctx, scope.TenantID, scope.GateID, pass.ID)
	if err != nil {
		if errs.CodeOf(err) == errs.CodeNotFound {
			return deny("this gate is no longer on file")
		}
		return deny("could not read the gate's rules")
	}
	if rules.Status != "active" {
		return deny("gate is " + rules.Status)
	}

	now := time.Now().UTC()
	loc := gateZone(scope.Timezone)
	if reason := gateHoursRefusal(rules, now, loc); reason != "" {
		return deny(reason)
	}
	if len(rules.Categories) > 0 && !contains(rules.Categories, pass.CategoryKey) {
		return deny("not valid at this gate")
	}
	if len(rules.AllowedGates) > 0 && !containsID(rules.AllowedGates, scope.GateID) {
		return deny("this pass is not valid at this gate")
	}
	if reason := gateWindowRefusal(rules.Windows, pass.CategoryKey, now, loc); reason != "" {
		return deny(reason)
	}
	if !pass.WithinWindow(now) {
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
			entry, err := s.repo.OpenEntry(ctx, scope.TenantID, pass.ID)
			if err != nil {
				if errs.CodeOf(err) == errs.CodeNotFound {
					return deny("no open entry to close")
				}
				return deny("could not read the open entry")
			}
			if rules.SameGateExit && entry.GateID != scope.GateID {
				return deny("this pass has to exit through the gate it entered")
			}
		}
	}
	return ""
}

// gateHoursRefusal checks the checkpoint's own daily opening hours. A pair that wraps past
// midnight is read as the overnight window it is rather than as an impossible one.
func gateHoursRefusal(rules GateRules, now time.Time, loc *time.Location) string {
	if rules.OpensAt == "" && rules.ClosesAt == "" {
		return ""
	}
	if rules.OpensAt == "" || rules.ClosesAt == "" {
		return ""
	}
	if !insideClock(now, loc, rules.OpensAt, rules.ClosesAt) {
		return "outside this gate's opening hours"
	}
	return ""
}

// gateWindowRefusal applies the tenant's finer windows. A block that covers now always wins;
// when the gate has allow windows, being outside all of them is the same refusal. Warn-only
// windows change no colour, because a warning has no path from this layer to the guard.
func gateWindowRefusal(windows []GateWindow, category string, now time.Time, loc *time.Location) string {
	hasAllow, insideAllow, blocked := false, false, false
	iso := isoWeekday(now.In(loc))
	for _, window := range windows {
		if window.Category != nil && *window.Category != "" &&
			!strings.EqualFold(*window.Category, category) {
			continue
		}
		if window.Day != 0 && window.Day != iso {
			continue
		}
		covered := insideClock(now, loc, window.StartsAt, window.ClosesAt)
		switch window.Action {
		case "block":
			if covered {
				blocked = true
			}
		case "allow":
			hasAllow = true
			if covered {
				insideAllow = true
			}
		}
	}
	if blocked {
		return "not permitted at this gate at this time"
	}
	if hasAllow && !insideAllow {
		// An allow window that did not cover now was not marked inside above, so this
		// branch needs the gate's windows reconsidered: reaching here means no allow
		// window covered the scan.
		return "outside this gate's permitted hours"
	}
	return ""
}

func insideClock(now time.Time, loc *time.Location, opens, closes string) bool {
	start, okStart := clockSeconds(opens)
	end, okEnd := clockSeconds(closes)
	if !okStart || !okEnd || start == end {
		return true
	}
	local := now.In(loc)
	current := local.Hour()*3600 + local.Minute()*60 + local.Second()
	if end < start {
		return current >= start || current < end
	}
	return current >= start && current < end
}

func clockSeconds(value string) (int, bool) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) < 2 {
		return 0, false
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, false
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, false
	}
	second := 0
	if len(parts) > 2 {
		second, err = strconv.Atoi(parts[2])
		if err != nil {
			return 0, false
		}
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 || second < 0 || second > 59 {
		return 0, false
	}
	return hour*3600 + minute*60 + second, true
}

func isoWeekday(at time.Time) int { return (int(at.Weekday())+6)%7 + 1 }

func gateZone(name string) *time.Location {
	if loc, err := time.LoadLocation(strings.TrimSpace(name)); err == nil {
		return loc
	}
	return time.UTC
}

func containsID(haystack []int64, needle int64) bool {
	for _, straw := range haystack {
		if straw == needle {
			return true
		}
	}
	return false
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
			IdempotencyKey: idempotency, SyncBatchID: scan.BatchID,
		}
		if result.Opens() && scan.Direction == DirectionOut {
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

		if result.Opens() && scan.Direction == DirectionIn {
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
				"opened": result.Opens(), "pass_no": pass.PassNo,
				"category": pass.CategoryKey, "reason": reason,
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
