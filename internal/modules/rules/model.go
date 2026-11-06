// Package rules is the tenant's own rulebook: the restrictions a site writes down instead of
// telling the guard over the radio. A rule is a scope, a condition tree and one action, and it
// bites at the barrier - which is where a rule that nobody wrote down used to live.
package rules

import (
	"encoding/json"
	"strings"
	"time"
)

// ModuleKey is the switch this module answers to. With it off the guard gets an empty
// Decision and every gate is judged on the rules Core has always owned, so a site that wants
// a rulebook and a site that does not are one flag apart rather than two deployments.
const ModuleKey = "rules_policy"

const aggregateType = "rule"

// OverridePermission is what a session has to hold for a rulebook refusal to be waived. It is
// named here rather than read from a row because the question "may this be overridden" has two
// parts - the rule says whether it can be, and RBAC says whether this person may - and only
// this module is allowed to hold both.
const OverridePermission = "rule.override"

type Stage string

const (
	StageRequest Stage = "request"
	StageScan    Stage = "scan"
	StageBoth    Stage = "both"
	StageNightly Stage = "nightly"
)

type Action string

const (
	ActionBlock   Action = "block"
	ActionWarn    Action = "warn"
	ActionNotify  Action = "notify"
	ActionAllow   Action = "allow"
	ActionLimit   Action = "limit"
	ActionRequire Action = "require_approval"
	ActionField   Action = "require_field"
)

// The action words the rulebook's log files a match under. They are the same set
// chk_rule_evaluations_outcome allows, so a log row can be read by a report without a
// translation table.
const (
	OutcomeBlocked  = "blocked"
	OutcomeWarned   = "warned"
	OutcomeLimit    = "limit_reached"
	OutcomeRequired = "approval_required"
	OutcomeOverride = "override_granted"
	// OutcomePassed is what a match that changed nothing is filed under: an allow rule, a
	// notify rule, and a counted rule still inside its allowance all let the scan through.
	// chk_rule_evaluations_outcome has no word for notify, and it is right not to - the
	// notice is the event, the row is the pass.
	OutcomePassed = "passed"
)

func (a Action) Valid() bool {
	switch a {
	case ActionBlock, ActionWarn, ActionNotify, ActionAllow, ActionLimit, ActionRequire, ActionField:
		return true
	default:
		return false
	}
}

func (s Stage) Valid() bool {
	switch s {
	case StageRequest, StageScan, StageBoth, StageNightly:
		return true
	default:
		return false
	}
}

// Bites reports whether an action means anything at a barrier. require_field does not: a guard
// cannot ask a pass to fill in a box, and a rule that only files a demand nobody will ever
// read is worse than no rule at all, because it looks like a control.
func (a Action) Bites() bool { return a != ActionField }

// Refuses reports whether an action is one the guard is refused for. A counted rule that is
// still inside its allowance and a rule that only asks for somebody to be told are not, which
// is why this is a property of the action and not of a match.
func (a Action) Refuses() bool {
	switch a {
	case ActionBlock, ActionLimit, ActionRequire:
		return true
	default:
		return false
	}
}

// StageAsked reports whether this rule is in the walk the gate makes. request and nightly are
// on file, and nothing evaluates them yet - see the note on New.Validate.
func (s Stage) StageAsked() bool { return s == StageScan || s == StageBoth }

type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

func (s Severity) Valid() bool {
	switch s {
	case SeverityInfo, SeverityWarning, SeverityCritical:
		return true
	default:
		return false
	}
}

type Period string

const (
	PeriodHour  Period = "hour"
	PeriodDay   Period = "day"
	PeriodWeek  Period = "week"
	PeriodMonth Period = "month"
)

func (p Period) Valid() bool {
	switch p {
	case PeriodHour, PeriodDay, PeriodWeek, PeriodMonth:
		return true
	default:
		return false
	}
}

// Rule is one written-down restriction, exactly as the desk filed it. The rulebook is versioned
// in place: an edit raises version rather than replacing the row, because every evaluation
// log names the rule id and "what was the rule in November" is the first question an incident
// review asks.
type Rule struct {
	ID           int64   `db:"id"`
	TenantID     int64   `db:"tenant_id"`
	Name         string  `db:"name"`
	Description  *string `db:"description"`
	ModuleKey    *string `db:"module_key"`
	PassTypeID   *int64  `db:"pass_type_id"`
	CategoryKey  *string `db:"category_key"`
	SiteID       *int64  `db:"site_id"`
	GateID       *int64  `db:"gate_id"`
	DepartmentID *int64  `db:"department_id"`
	Stage        string  `db:"stage"`
	Action       string  `db:"action"`
	// Payload is action_payload as stored: the limit, the period and what to count, and
	// anything a later stage puts in the same column.
	Payload []byte `db:"action_payload"`
	// Conditions is the predicate tree, still as JSON. One parse per scan turns it into a
	// tree the engine walks, so this row stays a row.
	Conditions  []byte     `db:"conditions"`
	Priority    int        `db:"priority"`
	Severity    string     `db:"severity"`
	FailureMsg  *string    `db:"failure_message"`
	Overridable bool       `db:"override_allowed"`
	EffectiveAt *time.Time `db:"effective_from"`
	UntilAt     *time.Time `db:"effective_until"`
	Version     int        `db:"version"`
	IsActive    bool       `db:"is_active"`
	CreatedBy   *int64     `db:"created_by"`
	CreatedAt   time.Time  `db:"created_at"`
	UpdatedAt   time.Time  `db:"updated_at"`
	DeletedAt   *time.Time `db:"deleted_at"`
}

// Live reports whether the rule is still on the books. A rule whose own window has not opened
// or has shut is not evaluated, and a paused rule is not evaluated either - the desk pauses a
// rule rather than deleting it because the evaluation log has to keep pointing at something.
func (r Rule) Live(now time.Time) bool {
	if !r.IsActive || r.DeletedAt != nil {
		return false
	}
	if r.EffectiveAt != nil && now.Before(*r.EffectiveAt) {
		return false
	}
	return r.UntilAt == nil || now.Before(*r.UntilAt)
}

func (r Rule) ActionKind() Action { return Action(r.Action) }
func (r Rule) StageKind() Stage   { return Stage(r.Stage) }

// Reason is the sentence the guard is shown. A rule with no failure message of its own is
// named instead: "Blocked by rule 14" is not something a guard can act on and "Delivery gate
// only after 18:00" is.
func (r Rule) Reason() string {
	if r.FailureMsg != nil && strings.TrimSpace(*r.FailureMsg) != "" {
		return strings.TrimSpace(*r.FailureMsg)
	}
	return r.Name
}

// Params is the part of action_payload this module reads. Everything else in the column is
// left alone: it belongs to whatever stage grows into it next.
type Params struct {
	// Limit is how many times the counted thing is allowed inside Period.
	Limit int `json:"limit"`
	// Period is which bucket of time Limit is counted in.
	Period Period `json:"period"`
	// By names the fact that gets counted: one bucket per pass, per plate, per phone or per
	// department. Empty is pass, which is the only answer that is always available.
	By string `json:"by"`
}

func (r Rule) params() Params {
	var out Params
	if len(r.Payload) == 0 {
		return out
	}
	// A payload that will not decode is a payload with no parameters, never a gate that
	// breaks: the desk that wrote it gets the error when it saves, the guard gets a decision.
	if json.Unmarshal(r.Payload, &out) != nil {
		return Params{}
	}
	return out
}

// New is one rule as the desk files it. The condition tree arrives as raw JSON because the
// engine, not the transport, owns what it means.
type New struct {
	TenantID     int64
	Name         string
	Description  string
	ModuleKey    string
	PassTypeID   int64
	CategoryKey  string
	SiteID       int64
	GateID       int64
	DepartmentID int64
	Stage        Stage
	Action       Action
	Params       Params
	Conditions   json.RawMessage
	Priority     int
	Severity     Severity
	FailureMsg   string
	Override     bool
	EffectiveAt  *time.Time
	UntilAt      *time.Time
	IsActive     bool
	CreatedBy    int64
}
