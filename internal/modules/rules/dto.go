package rules

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/httpx"
)

const defaultPage = 25

// RuleRequest is one rule as the desk files it. The condition tree arrives as JSON the client
// assembled, because the shape of a rule is the thing this module exists to make unnecessary
// in a release - and it is validated by the engine that will read it, not by a form.
type RuleRequest struct {
	Name        string `json:"name" validate:"required,max=255"`
	Description string `json:"description" validate:"omitempty,max=512"`

	// The scope. Anything left out means "everything", which is why a rule written once for a
	// whole tenant does not have to be written once per gate.
	ModuleKey    string `json:"module_key" validate:"omitempty,max=64"`
	PassTypeID   int64  `json:"pass_type_id" validate:"omitempty,gt=0"`
	CategoryKey  string `json:"category_key" validate:"omitempty,max=64"`
	SiteID       int64  `json:"site_id" validate:"omitempty,gt=0"`
	GateID       int64  `json:"gate_id" validate:"omitempty,gt=0"`
	DepartmentID int64  `json:"department_id" validate:"omitempty,gt=0"`

	Stage    string          `json:"stage" validate:"omitempty,oneof=scan both request nightly"`
	Action   string          `json:"action" validate:"required"`
	Cond     json.RawMessage `json:"conditions" validate:"required"`
	Priority int             `json:"priority" validate:"omitempty,gte=1,lte=1000"`
	Severity string          `json:"severity" validate:"omitempty,oneof=info warning critical"`

	FailureMessage string `json:"failure_message" validate:"omitempty,max=255"`

	// Limit, Period and By are the counted rule's boxes. They are only read when the action is
	// limit, and the service refuses a counted rule with nothing to count.
	Limit  int    `json:"limit" validate:"omitempty,gte=1"`
	Period string `json:"period" validate:"omitempty,max=12"`
	By     string `json:"by" validate:"omitempty,max=32"`

	// Both pointers, because a rule the desk did not mean to pause and a rule the desk did not
	// mean to make non-overridable are two different mistakes, and the body cannot tell them
	// apart. Omitted means active and overridable, which is what a new rule should be.
	OverrideAllowed *bool      `json:"override_allowed"`
	IsActive        *bool      `json:"is_active"`
	EffectiveFrom   *time.Time `json:"effective_from"`
	EffectiveUntil  *time.Time `json:"effective_until"`
}

func (req RuleRequest) toNew() New {
	in := New{
		Name: strings.TrimSpace(req.Name), Description: strings.TrimSpace(req.Description),
		ModuleKey: strings.TrimSpace(req.ModuleKey), PassTypeID: req.PassTypeID,
		CategoryKey: strings.TrimSpace(req.CategoryKey), SiteID: req.SiteID,
		GateID: req.GateID, DepartmentID: req.DepartmentID,
		Stage: Stage(strings.TrimSpace(req.Stage)), Action: Action(strings.TrimSpace(req.Action)),
		Conditions: req.Cond, Priority: req.Priority, Severity: Severity(req.Severity),
		FailureMsg:  strings.TrimSpace(req.FailureMessage),
		Params:      Params{Limit: req.Limit, Period: Period(req.Period), By: req.By},
		Override:    req.OverrideAllowed == nil || *req.OverrideAllowed,
		IsActive:    req.IsActive == nil || *req.IsActive,
		EffectiveAt: req.EffectiveFrom,
		UntilAt:     req.EffectiveUntil,
	}
	if in.Severity == "" {
		in.Severity = SeverityWarning
	}
	return in
}

// RetireRequest is the sentence that explains taking a rule off the books.
type RetireRequest struct {
	Reason string `json:"reason" validate:"required,max=512"`
}

// ActiveRequest is the pause switch: one field, because a rule is either on the books or not,
// and a body with nothing in it would be a guess about which the desk meant.
type ActiveRequest struct {
	IsActive bool `json:"is_active"`
}

// RuleView is one rule as the desk reads it: the row, plus the three answers the screen needs
// but cannot compute from a row - whether it is live right now, whether it bites at a barrier
// and whether the gate would walk it at all.
type RuleView struct {
	ID           int64           `json:"id"`
	Name         string          `json:"name"`
	Description  *string         `json:"description,omitempty"`
	ModuleKey    *string         `json:"module_key,omitempty"`
	PassTypeID   *int64          `json:"pass_type_id,omitempty"`
	CategoryKey  *string         `json:"category_key,omitempty"`
	SiteID       *int64          `json:"site_id,omitempty"`
	GateID       *int64          `json:"gate_id,omitempty"`
	DepartmentID *int64          `json:"department_id,omitempty"`
	Stage        string          `json:"stage"`
	Action       string          `json:"action"`
	Conditions   json.RawMessage `json:"conditions"`
	Limit        int             `json:"limit,omitempty"`
	Period       string          `json:"period,omitempty"`
	Counted      string          `json:"counted_by,omitempty"`
	Priority     int             `json:"priority"`
	Severity     string          `json:"severity"`
	FailureMsg   *string         `json:"failure_message,omitempty"`
	Overridable  bool            `json:"override_allowed"`
	EffectiveAt  *time.Time      `json:"effective_from,omitempty"`
	UntilAt      *time.Time      `json:"effective_until,omitempty"`
	Version      int             `json:"version"`
	IsActive     bool            `json:"is_active"`
	CreatedBy    *int64          `json:"created_by,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
	DeletedAt    *time.Time      `json:"deleted_at,omitempty"`

	Now     time.Time `json:"evaluated_now"`
	Live    bool      `json:"is_live"`
	Walked  bool      `json:"is_walked_by_the_gate"`
	Refuses bool      `json:"refuses_a_scan"`
}

func viewOf(r Rule, now time.Time) RuleView {
	params := r.params()
	return RuleView{
		ID: r.ID, Name: r.Name, Description: r.Description, ModuleKey: r.ModuleKey,
		PassTypeID: r.PassTypeID, CategoryKey: r.CategoryKey, SiteID: r.SiteID,
		GateID: r.GateID, DepartmentID: r.DepartmentID,
		Stage: r.Stage, Action: r.Action, Conditions: json.RawMessage(r.Conditions),
		Limit: params.Limit, Period: string(params.Period), Counted: params.By,
		Priority: r.Priority, Severity: r.Severity, FailureMsg: r.FailureMsg,
		Overridable: r.Overridable, EffectiveAt: r.EffectiveAt, UntilAt: r.UntilAt,
		Version: r.Version, IsActive: r.IsActive, CreatedBy: r.CreatedBy,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, DeletedAt: r.DeletedAt,
		Now: now, Live: r.Live(now), Walked: r.StageKind().StageAsked(),
		Refuses: r.ActionKind().Refuses(),
	}
}

func viewsOf(rules []Rule) []RuleView {
	now := time.Now().UTC()
	views := make([]RuleView, 0, len(rules))
	for _, rule := range rules {
		views = append(views, viewOf(rule, now))
	}
	return views
}

// EvaluationView is one line of the rulebook's own log, with the rule's name carried along so
// a feed of refusals can be read without a second request per row.
type EvaluationView struct {
	ID          int64     `json:"id"`
	RuleID      *int64    `json:"rule_id,omitempty"`
	RuleName    *string   `json:"rule_name,omitempty"`
	RuleAction  *string   `json:"rule_action,omitempty"`
	Severity    *string   `json:"rule_severity,omitempty"`
	Priority    *int      `json:"rule_priority,omitempty"`
	Stage       string    `json:"stage"`
	Outcome     string    `json:"outcome"`
	Matched     bool      `json:"matched"`
	PassID      *int64    `json:"pass_id,omitempty"`
	PassNo      *string   `json:"pass_no,omitempty"`
	SubjectName *string   `json:"subject_name,omitempty"`
	MovementID  *int64    `json:"movement_id,omitempty"`
	GateID      *int64    `json:"gate_id,omitempty"`
	EvaluatedAt time.Time `json:"evaluated_at"`
	Message     *string   `json:"message,omitempty"`
	OverrideBy  *int64    `json:"override_by_user_id,omitempty"`
	OverrideWhy *string   `json:"override_reason,omitempty"`
	Context     []byte    `json:"-"`
	Waived      bool      `json:"was_waived"`
}

func evaluationViewOf(e Evaluation) EvaluationView {
	return EvaluationView{
		ID: e.ID, RuleID: e.RuleID, RuleName: e.RuleName, RuleAction: e.RuleAction,
		Severity: e.RuleSev, Priority: e.RulePrio, Stage: e.Stage, Outcome: e.Outcome,
		Matched: e.Matched, PassID: e.PassID, PassNo: e.PassNo, SubjectName: e.SubjectName,
		MovementID: e.MovementID, GateID: e.GateID, EvaluatedAt: e.EvaluatedAt,
		Message: e.Message, OverrideBy: e.OverrideBy, OverrideWhy: e.OverrideWhy,
		Context: e.Context, Waived: e.Outcome == OutcomeOverride,
	}
}

func evaluationViewsOf(rows []Evaluation) []EvaluationView {
	views := make([]EvaluationView, 0, len(rows))
	for _, row := range rows {
		views = append(views, evaluationViewOf(row))
	}
	return views
}

// ParseRules reads the rulebook filters: ?action=&stage=&category=&site_id=&include_retired=.
func ParseRules(r *http.Request) (ListFilter, httpx.Page, error) {
	in := r.URL.Query().Get("action")
	if in != "" && !Action(in).Valid() {
		return ListFilter{}, httpx.Page{}, errs.BadField("action", in+" is not a rule action")
	}
	stage := r.URL.Query().Get("stage")
	if stage != "" && !Stage(stage).Valid() {
		return ListFilter{}, httpx.Page{}, errs.BadField("stage", stage+" is not a rule stage")
	}
	siteID, err := httpx.QueryID(r, "site_id")
	if err != nil {
		return ListFilter{}, httpx.Page{}, err
	}
	quiet, err := flag(r, "include_retired")
	if err != nil {
		return ListFilter{}, httpx.Page{}, err
	}
	filter := ListFilter{
		Action: in, Stage: stage, SiteID: siteID,
		CategoryKey:  strings.TrimSpace(r.URL.Query().Get("category")),
		IncludeQuiet: quiet != nil && *quiet,
	}
	page, err := httpx.ParsePage(r, defaultPage, 100)
	return filter, page, err
}

// ParseEvaluations reads the feed filters: ?rule_id=&pass_id=&outcome=&matched=.
func ParseEvaluations(r *http.Request) (EvalFilter, httpx.Page, error) {
	outcome := r.URL.Query().Get("outcome")
	if outcome != "" && !knownOutcome(outcome) {
		return EvalFilter{}, httpx.Page{}, errs.BadField("outcome",
			"must be passed, warned, blocked, approval_required, limit_reached or override_granted")
	}
	ruleID, err := httpx.QueryID(r, "rule_id")
	if err != nil {
		return EvalFilter{}, httpx.Page{}, err
	}
	passID, err := httpx.QueryID(r, "pass_id")
	if err != nil {
		return EvalFilter{}, httpx.Page{}, err
	}
	matched, err := flag(r, "matched")
	if err != nil {
		return EvalFilter{}, httpx.Page{}, err
	}
	page, err := httpx.ParsePage(r, defaultPage, 100)
	return EvalFilter{
		Outcome: outcome, RuleID: ruleID, PassID: passID, Matched: matched, Limit: page.Limit,
		Before: page.Cursor,
	}, page, err
}

// knownOutcome accepts only the words the gate's own log can be filed under. no_match is
// left out because nothing writes it yet: a feed that filters on a row the system never
// produces is a filter that looks broken.
func knownOutcome(outcome string) bool {
	switch outcome {
	case OutcomePassed, OutcomeWarned, OutcomeBlocked, OutcomeRequired, OutcomeLimit,
		OutcomeOverride:
		return true
	default:
		return false
	}
}

// flag reads a query flag that means something different when it is absent: include_retired
// left out is "leave them out", and matched left out is "both". A value that is neither is an
// error, because a filter that quietly stops filtering looks like a feed with nothing in it.
func flag(r *http.Request, name string) (*bool, error) {
	switch strings.ToLower(r.URL.Query().Get(name)) {
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
