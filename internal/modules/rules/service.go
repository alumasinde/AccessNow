package rules

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"accessnow/internal/core/access"
	"accessnow/internal/core/movements"
	"accessnow/internal/platform/audit"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/events"
	"accessnow/internal/platform/logging"
	"accessnow/internal/platform/tenancy"
)

// Service is the rulebook put to work: the gate asks it what the tenant's own restrictions
// say about a scan, the desk files and edits those restrictions through it, and it keeps the
// log that explains a red light afterwards. It never writes a pass or a movement - those are
// Core's rows - and it never reads another module's.
type Service struct {
	pool   *sqlx.DB
	repo   *Repository
	access *access.Service
	events events.Publisher
	chain  audit.Writer
}

func NewService(pool *sqlx.DB, repo *Repository, accessSvc *access.Service,
	bus events.Publisher, chain audit.Writer) *Service {
	return &Service{pool: pool, repo: repo, access: accessSvc, events: bus, chain: chain}
}

// enabled is the switch that keeps the module optional. With it off the gate gets an empty
// Decision and the desk surface is not mounted at all, so a site with a written rulebook and
// a site that judges every scan on Core's own facts are one tenant flag apart.
func (s *Service) enabled(ctx context.Context, tenantID int64) (bool, error) {
	if s.access == nil {
		return true, nil
	}
	return s.access.Enabled(ctx, tenantID, ModuleKey)
}

// The gate.

// candidateCeiling is how many rules one scan may walk. A tenant files dozens of rules; the
// ceiling is there so a rulebook that has been allowed to grow past anything a person can
// read cannot turn a barrier into a report on how long the database was busy.
const candidateCeiling = 200

// Decide is the rulebook's answer to one scan. The rules are narrowed in SQL - live, in
// scope, in the stage the gate walks - and the condition trees are walked in Go, where a
// mistake in one cannot corrupt a query.
func (s *Service) Decide(ctx context.Context, q movements.PolicyQuery) (movements.Decision, error) {
	on, err := s.enabled(ctx, q.TenantID)
	if err != nil {
		return movements.Decision{}, err
	}
	if !on {
		return movements.Decision{}, nil
	}

	now := at(q)
	rows, err := s.repo.Candidates(ctx, matchOf(q), now, candidateCeiling)
	if err != nil {
		return movements.Decision{}, err
	}
	candidates := trees(ctx, rows)
	if len(candidates) == 0 {
		return movements.Decision{}, nil
	}

	facts := factsOf(q, now, zone(q.Timezone))
	limits, err := s.allowances(ctx, q.TenantID, candidates, facts)
	if err != nil {
		return movements.Decision{}, err
	}
	return s.settle(ctx, evaluate(candidates, facts, limits), q), nil
}

// settle turns a walk through the rulebook into the answer Core reads. A refusal is a refusal
// unless the rule says it may be waived, the guard asked, and this person holds rule.override
// - which is the only place those three questions are asked together, because the rule that
// refused is the one that says whether it can be lifted and RBAC is the only place that knows
// who is holding the scanner.
func (s *Service) settle(ctx context.Context, v verdict, q movements.PolicyQuery) movements.Decision {
	d := movements.Decision{Fired: firingsOf(v)}
	if v.refusal == nil {
		if v.warning != nil {
			d.Result, d.Reason = movements.ResultWarned, clip(v.warning.message)
		}
		return d
	}
	if s.waivable(ctx, *v.refusal, q) {
		d.Result, d.Reason = movements.ResultOverride, clip(v.refusal.message)
		return d
	}
	d.Result, d.Reason = movements.ResultDenied, clip(v.refusal.message)
	return d
}

func (s *Service) waivable(ctx context.Context, hit firing, q movements.PolicyQuery) bool {
	if !q.Override.Requested || !hit.rule.Overridable || s.access == nil {
		return false
	}
	may, err := s.access.Can(ctx, q.TenantID, q.ActorUserID, OverridePermission)
	if err != nil {
		// A permission check that could not be answered is a waiver that was not granted.
		// The guard still gets the refusal and its reason, and can ask a supervisor.
		logging.From(ctx).ErrorContext(ctx, "rule override check", "rule_id", hit.rule.ID,
			"error", err)
		return false
	}
	return may
}

func matchOf(q movements.PolicyQuery) Match {
	return Match{
		TenantID: q.TenantID, SiteID: q.SiteID, GateID: q.GateID,
		ModuleKey: q.SourceModuleKey, CategoryKey: q.CategoryKey,
		DepartmentID: q.DepartmentID, PassTypeID: q.PassTypeID,
		Stages: []string{string(StageScan), string(StageBoth)},
	}
}

// trees parses the condition trees, and drops a rule whose tree is not a tree. The write path
// validates before filing, so this only happens to a row edited directly - and a rule that
// cannot be read is a rule that does not bite, never a gate that will not answer.
func trees(ctx context.Context, rows []Rule) []candidate {
	out := make([]candidate, 0, len(rows))
	for _, rule := range rows {
		var tree Cond
		if err := json.Unmarshal(rule.Conditions, &tree); err != nil {
			logging.From(ctx).ErrorContext(ctx, "rule condition tree", "rule_id", rule.ID,
				"error", err)
			continue
		}
		out = append(out, candidate{rule: rule, tree: tree})
	}
	return out
}

// allowances reads how much of each counted allowance the subject at the gate has already
// spent, so the engine can stay a function of its inputs and be tested with a map.
func (s *Service) allowances(ctx context.Context, tenantID int64, cands []candidate,
	f Facts) (Limits, error) {
	out := Limits{}
	for _, cand := range cands {
		if cand.rule.ActionKind() != ActionLimit {
			continue
		}
		quota, ok := quotaOf(cand.rule, f)
		if !ok {
			continue
		}
		used, err := s.repo.Used(ctx, quota, tenantID)
		if err != nil {
			return nil, err
		}
		out[cand.rule.ID] = used
	}
	return out, nil
}

func firingsOf(v verdict) []movements.Firing {
	out := make([]movements.Firing, 0, len(v.firings))
	for _, hit := range v.firings {
		out = append(out, movements.Firing{
			RuleID: hit.rule.ID, RuleName: hit.rule.Name, Action: hit.rule.Action,
			Outcome: hit.outcome, Message: hit.message, Quota: hit.quota,
		})
	}
	return out
}

// The rulebook's own log.

// Note writes one evaluation row per rule that fired, spends the allowance of each counted
// rule that was still inside its limit, and raises the one notice this scan is worth. It runs
// after the movement is committed, in its own transaction: the guard already has his colour,
// so a failure here is logged by Core and never turns a green light into an error page.
func (s *Service) Note(ctx context.Context, q movements.PolicyQuery, d movements.Decision,
	ref movements.HitRef) error {
	if len(d.Fired) == 0 {
		return nil
	}
	on, err := s.enabled(ctx, q.TenantID)
	if err != nil {
		return err
	}
	if !on {
		return nil
	}

	now := at(q)
	evidence := evidenceOf(factsOf(q, now, zone(q.Timezone)))
	return db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		for _, hit := range d.Fired {
			if err := repo.LogEvaluation(ctx, rowOf(q, d, ref, hit, evidence, now)); err != nil {
				return err
			}
			if hit.Quota != nil && hit.Quota.Spent && d.Result.Opens() {
				// A scan that ended in a refusal below this rule never happened for the
				// purposes of its allowance: the person did not come in, so the count that
				// let them in is not spent.
				if err := repo.Spend(ctx, *hit.Quota, q.TenantID, hit.Quota.Limit, now); err != nil {
					return err
				}
			}
		}
		return s.announce(ctx, c, q, d, ref)
	})
}

// rowOf files one firing. A waived refusal is filed as the waiver rather than as the block it
// was: a supervisor asking how many refusals were lifted counts these rows, and a block that
// was lifted is not one of them.
func rowOf(q movements.PolicyQuery, d movements.Decision, ref movements.HitRef,
	hit movements.Firing, evidence []byte, now time.Time) Evaluation {
	ruleID := hit.RuleID
	out := Evaluation{
		TenantID: ref.TenantID, RuleID: &ruleID, Stage: string(StageScan), Outcome: hit.Outcome,
		Matched: true, PassID: optionalID(ref.PassID), MovementID: optionalID(ref.MovementID),
		GateID: optionalID(ref.GateID), EvaluatedAt: now, Context: evidence,
		Message: optionalText(clip(hit.Message)),
	}
	if d.Result == movements.ResultOverride && refuses(hit.Outcome) {
		out.Outcome = OutcomeOverride
		out.OverrideBy = optionalID(ref.ActorUserID)
		out.OverrideWhy = optionalText(clip(q.Override.Reason))
	}
	return out
}

// refuses names the outcomes that stopped the scan.
func refuses(outcome string) bool {
	switch outcome {
	case OutcomeBlocked, OutcomeLimit, OutcomeRequired:
		return true
	default:
		return false
	}
}

// evidenceOf is what the log keeps of the facts the engine was given, so a rule that bit in
// March can be re-read against the hour, the gate and the category it bit on. Contact details
// are left out on purpose: an evaluation log is copied into reports and incident reviews, and
// a phone number is not evidence about a rule.
func evidenceOf(f Facts) []byte {
	out := map[string]any{}
	put := func(key string, value string) {
		if strings.TrimSpace(value) != "" {
			out[key] = value
		}
	}
	putNumber := func(key string, value int64) {
		if value != 0 {
			out[key] = value
		}
	}
	put("direction", f.Direction)
	put("category", f.Category)
	put("module", f.Module)
	put("status", f.Status)
	put("origin", f.Origin)
	put("pass_no", f.PassNo)
	put("subject", f.Subject)
	put("plate", f.Plate)
	put("purpose", f.Purpose)
	put("clock", f.clock().Format("15:04"))
	putNumber("site", f.Site)
	putNumber("gate", f.Gate)
	putNumber("department", f.Department)
	putNumber("type", f.Type)
	putNumber("hour", int64(f.clock().Hour()))
	putNumber("weekday", int64(int(f.clock().Weekday())+6)%7+1)
	if f.Entries != 0 {
		out["entries"] = f.Entries
	}
	if f.Value != nil {
		out["value"] = *f.Value
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return nil
	}
	return raw
}

// The notice. A waiver is a different event from a match because the two are read by
// different people: the waiver is about the guard who used it, the match is about the rule.
const (
	wordRefused  = "refused"
	wordWarned   = "warned"
	wordWaived   = "waived"
	wordNotified = "notified"
)

// announce publishes the one event this scan is worth. A rule that only noted a pass that was
// already let through has nothing to say to anybody and is not announced.
func (s *Service) announce(ctx context.Context, c db.Conn, q movements.PolicyQuery,
	d movements.Decision, ref movements.HitRef) error {
	if s.events == nil {
		return nil
	}
	hit, what, ok := headline(d)
	if !ok {
		return nil
	}

	key := "rule.matched"
	if what == wordWaived {
		key = "gate.override_used"
	}
	payload := map[string]any{
		"rule_id": hit.RuleID, "rule_name": hit.RuleName, "action": hit.Action,
		"outcome": hit.Outcome, "decision": what, "reason": hit.Message,
		"pass_no": ref.PassNo, "subject_name": ref.SubjectName, "category": q.CategoryKey,
		"direction": string(q.Direction),
	}
	if strings.TrimSpace(q.Plate) != "" {
		payload["plate"] = q.Plate
	}
	if what == wordWaived {
		// {{note}} is the sentence the guard typed and {{actor_name}} is who typed it: the
		// two things a supervisor reads when a waiver is questioned.
		payload["note"] = q.Override.Reason
	}
	return s.events.Publish(ctx, c, events.Event{
		TenantID: ref.TenantID, EventKey: key, AggregateType: aggregateType,
		AggregateID: hit.RuleID, PassID: ref.PassID, SiteID: q.SiteID, GateID: ref.GateID,
		ActorUserID: ref.ActorUserID,
		DedupeKey:   key + ":" + strconv.FormatInt(ref.MovementID, 10),
		Payload:     payload,
	})
}

// headline picks the firing a notice is written about: the refusal the gate acted on, then the
// warning it passed on, then a rule that only asked for somebody to be told.
func headline(d movements.Decision) (movements.Firing, string, bool) {
	for _, hit := range d.Fired {
		if !refuses(hit.Outcome) {
			continue
		}
		if d.Result == movements.ResultOverride {
			return hit, wordWaived, true
		}
		if d.Result == movements.ResultDenied {
			return hit, wordRefused, true
		}
	}
	if d.Result == movements.ResultWarned {
		for _, hit := range d.Fired {
			if hit.Outcome == OutcomeWarned {
				return hit, wordWarned, true
			}
		}
	}
	for _, hit := range d.Fired {
		if Action(hit.Action) == ActionNotify {
			return hit, wordNotified, true
		}
	}
	return movements.Firing{}, "", false
}

// The desk.

// validate checks the rule and fills in the defaults the desk did not state. Every refusal is
// a way the rule would stop meaning what its author intended, which at a barrier is the same
// as a lock that looks fitted - so a stage nothing evaluates and an action no barrier can
// carry are refused here rather than filed as something that quietly does nothing.
func (in *New) validate(now time.Time) error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return errs.BadField("name", "is required")
	}
	if in.Stage == "" {
		in.Stage = StageBoth
	}
	if !in.Stage.Valid() {
		return errs.BadField("stage", "must be request, scan, both or nightly")
	}
	if !in.Stage.StageAsked() {
		return errs.BadField("stage",
			"nothing evaluates "+string(in.Stage)+" rules yet; use scan or both")
	}
	if !in.Action.Valid() {
		return errs.BadField("action", "must be one of "+strings.Join(actionNames(), ", "))
	}
	if !in.Action.Bites() {
		return errs.BadField("action",
			"require_field is answered at a desk, not at a barrier; use require_approval")
	}
	if err := Validate(in.Conditions); err != nil {
		return err
	}
	if in.Action == ActionLimit {
		if err := in.Params.validate(); err != nil {
			return err
		}
	}
	if in.Priority == 0 {
		in.Priority = 100
	}
	if in.Severity == "" {
		in.Severity = SeverityWarning
	}
	if !in.Severity.Valid() {
		return errs.BadField("severity", "must be info, warning or critical")
	}
	return in.validateWindow(now)
}

func (in *New) validateWindow(now time.Time) error {
	if in.UntilAt != nil && !in.UntilAt.After(now) {
		return errs.BadField("effective_until", "is already past")
	}
	if in.UntilAt != nil && in.EffectiveAt != nil && !in.UntilAt.After(*in.EffectiveAt) {
		return errs.BadField("effective_until", "must be after effective_from")
	}
	return nil
}

// validate checks the counted part of a rule. A limit with nothing to count is a rule that
// never bites, and a period this build cannot open a bucket for is a rule that counts the
// wrong thing - both are worse than a save that fails.
func (p Params) validate() error {
	if p.Limit <= 0 {
		return errs.BadField("limit", "a counted rule needs a limit above zero")
	}
	period := p.Period
	if period == "" {
		period = PeriodDay
	}
	if !period.Valid() {
		return errs.BadField("period", "must be hour, day, week or month")
	}
	if p.By != "" && !knownCount(p.By) {
		return errs.BadField("by", "must be pass, plate, phone, subject, department or requester")
	}
	return nil
}

func knownCount(by string) bool {
	for _, option := range countedBy {
		if strings.EqualFold(option, by) {
			return true
		}
	}
	return false
}

var countedBy = []string{"pass", "plate", "phone", "subject", "department", "requester"}

func actionNames() []string {
	return []string{string(ActionBlock), string(ActionWarn), string(ActionAllow),
		string(ActionNotify), string(ActionLimit), string(ActionRequire)}
}

// File writes a new rule onto the books.
func (s *Service) File(ctx context.Context, scope tenancy.Scope, in New) (Rule, error) {
	in.TenantID, in.CreatedBy = scope.TenantID, scope.UserID
	now := time.Now().UTC()
	if err := in.validate(now); err != nil {
		return Rule{}, err
	}

	var id int64
	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		filed, err := s.repo.In(c).Insert(ctx, in, now)
		if err != nil {
			return err
		}
		id = filed
		return s.record(ctx, c, scope, "rule.filed", id, in.Name, brief(in))
	})
	if err != nil {
		return Rule{}, err
	}
	return s.repo.ByID(ctx, scope.TenantID, id)
}

// Save rewrites a rule in place and raises its version. It is a whole rewrite rather than a
// patch: a rule is read as one sentence, and a save that left half of it from the row before
// is a rule nobody can quote back.
func (s *Service) Save(ctx context.Context, scope tenancy.Scope, id int64, in New) (Rule, error) {
	if _, err := s.repo.ByID(ctx, scope.TenantID, id); err != nil {
		return Rule{}, err
	}
	in.TenantID = scope.TenantID
	now := time.Now().UTC()
	if err := in.validate(now); err != nil {
		return Rule{}, err
	}

	rewritten := false
	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		moved, err := s.repo.In(c).Update(ctx, scope.TenantID, id, in, now)
		if err != nil || !moved {
			rewritten = moved
			return err
		}
		rewritten = true
		return s.record(ctx, c, scope, "rule.rewritten", id, in.Name, brief(in))
	})
	if err != nil {
		return Rule{}, err
	}
	if !rewritten {
		return Rule{}, errs.NotFound("rule")
	}
	return s.repo.ByID(ctx, scope.TenantID, id)
}

// Pause takes a rule off the books, and Resume puts it back. Nothing is deleted: the
// evaluations behind a rule keep pointing at a real row, which is what makes a rule that was
// wrong in March auditable as having been wrong.
func (s *Service) Pause(ctx context.Context, scope tenancy.Scope, id int64,
	active bool) (Rule, error) {
	shifted := false
	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		moved, err := s.repo.In(c).Pause(ctx, scope.TenantID, id, active, time.Now().UTC())
		if err != nil || !moved {
			shifted = moved
			return err
		}
		shifted = true
		action := "rule.paused"
		if active {
			action = "rule.resumed"
		}
		return s.record(ctx, c, scope, action, id, "", map[string]any{"is_active": active})
	})
	if err != nil {
		return Rule{}, err
	}
	rule, err := s.repo.ByID(ctx, scope.TenantID, id)
	if err != nil {
		return Rule{}, err
	}
	if !shifted {
		return Rule{}, errs.Conflict("rule is already " + wordOf(rule.IsActive))
	}
	return rule, nil
}

// Retire is the only delete this module does, and it is soft. A reason is required: a rule
// that has refused scans for a year and is then gone is exactly the thing an incident review
// comes back to ask about, and "the desk removed it" is not an answer.
func (s *Service) Retire(ctx context.Context, scope tenancy.Scope, id int64,
	reason string) (Rule, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Rule{}, errs.BadField("reason", "is required to take a rule off the books")
	}
	retired := false
	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		moved, err := s.repo.In(c).Retire(ctx, scope.TenantID, id, time.Now().UTC())
		if err != nil || !moved {
			retired = moved
			return err
		}
		retired = true
		return s.record(ctx, c, scope, "rule.retired", id, reason,
			map[string]any{"deleted": true})
	})
	if err != nil {
		return Rule{}, err
	}
	rule, err := s.repo.ByID(ctx, scope.TenantID, id)
	if err != nil {
		return Rule{}, err
	}
	if !retired {
		return Rule{}, errs.Conflict("rule is already retired")
	}
	return rule, nil
}

func (s *Service) Get(ctx context.Context, tenantID, id int64) (Rule, error) {
	return s.repo.ByID(ctx, tenantID, id)
}

func (s *Service) List(ctx context.Context, filter ListFilter) ([]Rule, error) {
	return s.repo.List(ctx, filter)
}

func (s *Service) Evaluations(ctx context.Context, filter EvalFilter) ([]Evaluation, error) {
	return s.repo.Evaluations(ctx, filter)
}

// Summary is what a desk screen leads with: how much of the rulebook is live, how much of it
// refuses, and how many refusals were waived today.
type Summary struct {
	Active   int            `json:"active"`
	Blocking int            `json:"blocking"`
	Waived   int            `json:"waived_today"`
	ByAction map[string]int `json:"by_action"`
}

func (s *Service) Summary(ctx context.Context, tenantID int64) (Summary, error) {
	counts, err := s.repo.Counts(ctx, tenantID)
	if err != nil {
		return Summary{}, err
	}
	waived, err := s.repo.Overrides(ctx, tenantID, startOfDay(time.Now().UTC()))
	if err != nil {
		return Summary{}, err
	}
	out := Summary{ByAction: map[string]int{}, Waived: waived}
	for _, one := range counts {
		out.ByAction[one.Action] = one.Active
		out.Active += one.Active
		if Action(one.Action).Refuses() {
			out.Blocking += one.Active
		}
	}
	return out, nil
}

// Catalogue is what the desk can write about: the fields a condition may name, the operators,
// and the actions. It is served rather than hard-coded into a form so that a new field is a
// release and not a redesign of the rule editor.
type Catalogue struct {
	Fields    []FieldSpec `json:"fields"`
	Operators []string    `json:"operators"`
	Actions   []string    `json:"actions"`
	Stages    []string    `json:"stages"`
	Counted   []string    `json:"counted_by"`
}

func CatalogueOf() Catalogue {
	return Catalogue{
		Fields: FieldSpecs(), Operators: OperatorNames(), Actions: actionNames(),
		Stages: []string{string(StageScan), string(StageBoth)}, Counted: countedBy,
	}
}

// record is the audit trail of a desk decision. Who filed a rule, who rewrote it and who took
// it off the books are the questions an incident review asks about the rulebook, and a rules
// row on its own answers none of them.
func (s *Service) record(ctx context.Context, c db.Conn, scope tenancy.Scope, action string,
	subjectID int64, reason string, after map[string]any) error {
	return s.chain.Write(ctx, c, audit.Entry{
		TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: action,
		SubjectType: aggregateType, SubjectID: subjectID, SiteID: scope.SiteID,
		Reason: reason, After: after,
	})
}

// brief is the part of a filed rule an audit row should keep: what it does and to whom, not
// the whole condition tree, which is on the rules row itself.
func brief(in New) map[string]any {
	return map[string]any{
		"stage": string(in.Stage), "action": string(in.Action), "priority": in.Priority,
		"severity": string(in.Severity), "override_allowed": in.Override,
	}
}

// The small helpers this service shares with itself.

// at is the instant the scan was decided. Core always fills it; the fallback is for the rule
// being asked outside the gate, where a caller builds the question itself.
func at(q movements.PolicyQuery) time.Time {
	if q.At.IsZero() {
		return time.Now().UTC()
	}
	return q.At.UTC()
}

// zone is the place the scan happened, so a rule about "after ten at night" means ten at
// night there. A site whose timezone the runtime cannot resolve is judged on UTC, which is
// the same clock the row is stored in.
func zone(name string) *time.Location {
	if loc, err := time.LoadLocation(strings.TrimSpace(name)); err == nil {
		return loc
	}
	return time.UTC
}

func startOfDay(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
}

func wordOf(active bool) string {
	if active {
		return "active"
	}
	return "paused"
}

// messageRoom is how much of a rule's words may travel into a column. Evaluation message is
// VARCHAR(255) and the server runs strict, so a rule whose failure message was written long
// would otherwise turn the overflow into a lost log row.
const messageRoom = 240

func clip(value string) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= messageRoom {
		return string(runes)
	}
	return string(runes[:messageRoom-1]) + "…"
}

func optionalID(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}

func optionalText(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}
