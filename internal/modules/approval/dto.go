package approval

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"accessnow/internal/core/passes"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/httpx"
	"accessnow/internal/platform/tenancy"
)

const defaultPage = 25

// DecisionRequest is one approver's click. The action is a word rather than a boolean
// because a client that sends false by accident should not be able to reject a pass.
type DecisionRequest struct {
	Action  string `json:"action" validate:"required,oneof=approve reject return"`
	Comment string `json:"comment" validate:"omitempty,max=2000"`
}

func (req DecisionRequest) toDecision() Decision {
	return Decision{Action: Action(strings.TrimSpace(req.Action)), Comment: req.Comment}
}

// ReasonRequest is the body cancel and reset share: a withdrawal that does not say why
// leaves the next approver with a pass and no story.
type ReasonRequest struct {
	Reason string `json:"reason" validate:"required,max=2000"`
}

// StepBody is one step of a flow as its author writes it. The unknown approver types are
// refused by checkFlow, which names the step, rather than by the database constraint, which
// names the row.
type StepBody struct {
	StepNo             int    `json:"step_no" validate:"required,gt=0,lte=999"`
	Name               string `json:"name" validate:"required,max=128"`
	ApproverType       string `json:"approver_type" validate:"required,max=32"`
	ApproverRoleKey    string `json:"approver_role_key" validate:"omitempty,max=64"`
	ApproverUserID     int64  `json:"approver_user_id" validate:"omitempty,gt=0"`
	Mode               string `json:"mode" validate:"required,max=16"`
	RequiresAll        bool   `json:"requires_all"`
	SLAHours           *int   `json:"sla_hours" validate:"omitempty,gte=1,lte=8760"`
	EscalateAfterHours *int   `json:"escalate_after_hours" validate:"omitempty,gte=1,lte=8760"`
	EscalateToRoleKey  string `json:"escalate_to_role_key" validate:"omitempty,max=64"`
	EscalateToUserID   int64  `json:"escalate_to_user_id" validate:"omitempty,gt=0"`
	AllowSelfApproval  bool   `json:"allow_self_approval"`
	RequireComment     bool   `json:"require_comment"`
	Memo               string `json:"memo" validate:"omitempty,max=512"`
}

func (b StepBody) toStep() NewStep {
	return NewStep{
		StepNo: b.StepNo, Name: b.Name, ApproverType: b.ApproverType,
		ApproverRoleKey: strings.TrimSpace(b.ApproverRoleKey), ApproverUserID: b.ApproverUserID,
		Mode: b.Mode, RequiresAll: b.RequiresAll, SLAHours: b.SLAHours,
		EscalateAfterHours: b.EscalateAfterHours,
		EscalateToRoleKey:  strings.TrimSpace(b.EscalateToRoleKey),
		EscalateToUserID:   b.EscalateToUserID,
		AllowSelfApproval:  b.AllowSelfApproval, RequireComment: b.RequireComment,
		Memo: b.Memo,
	}
}

// FlowBody is a routing rule with its whole chain. A flow is always saved with its steps: a
// rule with none would start a chain that asks nobody and holds the pass anyway.
type FlowBody struct {
	Name             string     `json:"name" validate:"required,max=128"`
	PassTypeID       int64      `json:"pass_type_id" validate:"omitempty,gt=0"`
	CategoryKey      string     `json:"category_key" validate:"omitempty,max=32"`
	DepartmentID     int64      `json:"department_id" validate:"omitempty,gt=0"`
	SiteID           int64      `json:"site_id" validate:"omitempty,gt=0"`
	MinValue         *float64   `json:"min_value" validate:"omitempty,gte=0"`
	MaxValue         *float64   `json:"max_value" validate:"omitempty,gte=0"`
	Currency         string     `json:"currency" validate:"omitempty,len=3"`
	Priority         int        `json:"priority" validate:"omitempty,gte=0"`
	IsDefault        bool       `json:"is_default"`
	OutsideHoursOnly bool       `json:"outside_hours_only"`
	Status           string     `json:"status" validate:"omitempty,oneof=active inactive"`
	Description      string     `json:"description" validate:"omitempty,max=512"`
	Steps            []StepBody `json:"steps" validate:"required,min=1,dive"`
}

func (b FlowBody) toFlow() NewFlow {
	status := b.Status
	if status == "" {
		status = "active"
	}
	return NewFlow{
		Name: strings.TrimSpace(b.Name), PassTypeID: b.PassTypeID,
		CategoryKey: strings.TrimSpace(b.CategoryKey), DepartmentID: b.DepartmentID,
		SiteID: b.SiteID, MinValue: b.MinValue, MaxValue: b.MaxValue,
		Currency: strings.ToUpper(strings.TrimSpace(b.Currency)), Priority: b.Priority,
		IsDefault: b.IsDefault, OutsideHoursOnly: b.OutsideHoursOnly, Status: status,
		Description: b.Description,
	}
}

func (b FlowBody) toSteps() []NewStep {
	steps := make([]NewStep, 0, len(b.Steps))
	for _, step := range b.Steps {
		steps = append(steps, step.toStep())
	}
	return steps
}

// DelegationBody is a cover. starts_at is optional and means now, because the person filing
// it is describing the absence they are about to have, not one they already took.
type DelegationBody struct {
	FromUserID   int64      `json:"from_user_id" validate:"omitempty,gt=0"`
	ToUserID     int64      `json:"to_user_id" validate:"required,gt=0"`
	FlowID       int64      `json:"approval_flow_id" validate:"omitempty,gt=0"`
	PassTypeID   int64      `json:"pass_type_id" validate:"omitempty,gt=0"`
	DepartmentID int64      `json:"department_id" validate:"omitempty,gt=0"`
	StartsAt     *time.Time `json:"starts_at"`
	EndsAt       time.Time  `json:"ends_at" validate:"required"`
	Reason       string     `json:"reason" validate:"omitempty,max=512"`
}

func (b DelegationBody) toDelegation(scope tenancy.Scope) NewDelegation {
	starts := time.Now().UTC()
	if b.StartsAt != nil {
		starts = b.StartsAt.UTC()
	}
	return NewDelegation{
		FromUserID: b.FromUserID, ToUserID: b.ToUserID, FlowID: b.FlowID,
		PassTypeID: b.PassTypeID, DepartmentID: b.DepartmentID,
		StartsAt: starts, EndsAt: b.EndsAt.UTC(), Reason: b.Reason, CreatedBy: scope.UserID,
	}
}

// PassBrief is the pass as an approver needs it to decide: whose it is, what it is worth and
// whether it already opens a gate. It is carried inside a chain so the queue screen does not
// have to know which module issued the pass it is looking at.
type PassBrief struct {
	ID              int64      `json:"id"`
	UUID            string     `json:"uuid"`
	PassNo          string     `json:"pass_no"`
	Status          string     `json:"status"`
	SubjectName     string     `json:"subject_name"`
	PassTypeID      int64      `json:"pass_type_id"`
	CategoryKey     string     `json:"category_key"`
	SourceModuleKey string     `json:"source_module_key"`
	ValueAmount     *float64   `json:"value_amount,omitempty"`
	ValueCurrency   *string    `json:"value_currency,omitempty"`
	RequesterUserID *int64     `json:"requester_user_id,omitempty"`
	HostUserID      *int64     `json:"host_user_id,omitempty"`
	ValidFrom       time.Time  `json:"valid_from"`
	ValidUntil      *time.Time `json:"valid_until,omitempty"`
	Scannable       bool       `json:"is_scannable"`
}

func briefOf(p passes.Pass) PassBrief {
	return PassBrief{
		ID: p.ID, UUID: p.UUID, PassNo: p.PassNo, Status: p.Status, SubjectName: p.SubjectName,
		PassTypeID: p.PassTypeID, CategoryKey: p.CategoryKey, SourceModuleKey: p.SourceModuleKey,
		ValueAmount: p.ValueAmount, ValueCurrency: p.ValueCurrency,
		RequesterUserID: p.RequesterUserID, HostUserID: p.HostUserID,
		ValidFrom: p.ValidFrom, ValidUntil: p.ValidUntil, Scannable: p.Usable(),
	}
}

// ChainSummary is one row of the register of chains. It carries the pass id and no pass
// fields on purpose: the register is a page of chains, and the screen that wants the paper
// opens the chain.
type ChainSummary struct {
	ID              int64      `json:"id"`
	PassID          int64      `json:"pass_id"`
	FlowID          *int64     `json:"approval_flow_id,omitempty"`
	Status          string     `json:"status"`
	CurrentStepNo   int        `json:"current_step_no"`
	TotalSteps      int        `json:"total_steps"`
	ValueAmount     *float64   `json:"value_amount,omitempty"`
	Currency        *string    `json:"currency,omitempty"`
	EscalationCount int        `json:"escalation_count"`
	StartedAt       time.Time  `json:"started_at"`
	DeadlineAt      *time.Time `json:"deadline_at,omitempty"`
	CompletedAt     *time.Time `json:"completed_at,omitempty"`
	DecidedByUserID *int64     `json:"decided_by_user_id,omitempty"`
	DecisionNote    *string    `json:"decision_note,omitempty"`
	Open            bool       `json:"is_open"`
}

func summaryOf(r Request) ChainSummary {
	return ChainSummary{
		ID: r.ID, PassID: r.PassID, FlowID: r.ApprovalFlowID, Status: r.Status,
		CurrentStepNo: r.CurrentStepNo, TotalSteps: r.TotalSteps, ValueAmount: r.ValueAmount,
		Currency: r.Currency, EscalationCount: r.EscalationCount, StartedAt: r.StartedAt,
		DeadlineAt: r.DeadlineAt, CompletedAt: r.CompletedAt, DecidedByUserID: r.DecidedByUserID,
		DecisionNote: r.DecisionNote, Open: r.Open(),
	}
}

func summariesOf(requests []Request) []ChainSummary {
	out := make([]ChainSummary, 0, len(requests))
	for _, req := range requests {
		out = append(out, summaryOf(req))
	}
	return out
}

// AskView is one assignee being asked, with the answer if it has come.
type AskView struct {
	ID                int64      `json:"id"`
	StepNo            int        `json:"step_no"`
	Name              string     `json:"name"`
	Mode              string     `json:"mode"`
	Assignee          string     `json:"assignee"`
	AssigneeUserID    *int64     `json:"assignee_user_id,omitempty"`
	ApproverRoleKey   *string    `json:"approver_role_key,omitempty"`
	Status            string     `json:"status"`
	DueAt             *time.Time `json:"due_at,omitempty"`
	AssignedAt        time.Time  `json:"assigned_at"`
	ActedAt           *time.Time `json:"acted_at,omitempty"`
	Comment           *string    `json:"comment,omitempty"`
	DelegatedFrom     *int64     `json:"delegated_from_user_id,omitempty"`
	EscalatedFromStep *int       `json:"escalated_from_step_no,omitempty"`
	Waiting           bool       `json:"is_waiting"`
	Overdue           bool       `json:"is_overdue"`
	CanAnswer         bool       `json:"can_answer"`
}

// askOf reads one ask as the screen shows it. can_answer mirrors who may answer this in the
// engine's own terms - the named assignee, or any holder of the role - and is only a hint:
// Answer re-checks everything, including the self-approval rule that lives on the step.
func askOf(ask RequestStep, viewerID int64, roles []string, at time.Time) AskView {
	return AskView{
		ID: ask.ID, StepNo: ask.StepNo, Name: str(ask.Name), Mode: ask.Mode,
		Assignee: nameOf(ask), AssigneeUserID: ask.AssigneeUserID,
		ApproverRoleKey: ask.ApproverRoleKey, Status: ask.Status, DueAt: ask.DueAt,
		AssignedAt: ask.AssignedAt, ActedAt: ask.ActedAt, Comment: ask.Comment,
		DelegatedFrom: ask.DelegatedFromUserID, EscalatedFromStep: ask.EscalatedFromStepNo,
		Waiting: ask.Waiting(), Overdue: past(ask.DueAt, at),
		CanAnswer: ask.Waiting() && holds(ask, viewerID, roles),
	}
}

func asksOf(asks []RequestStep, viewerID int64, roles []string) []AskView {
	at := time.Now().UTC()
	out := make([]AskView, 0, len(asks))
	for _, ask := range asks {
		out = append(out, askOf(ask, viewerID, roles, at))
	}
	return out
}

func holds(ask RequestStep, viewerID int64, roles []string) bool {
	if ask.AssigneeUserID != nil {
		return *ask.AssigneeUserID == viewerID
	}
	role := str(ask.ApproverRoleKey)
	if role == "" {
		return false
	}
	for _, key := range roles {
		if key == role {
			return true
		}
	}
	return false
}

func past(at *time.Time, now time.Time) bool { return at != nil && at.Before(now) }

// ChainView is a whole approval story: the chain, the pass it is about, the steps it was
// frozen under, and every ask with its answer.
type ChainView struct {
	Request ChainSummary `json:"request"`
	Pass    PassBrief    `json:"pass"`
	Steps   []FlowStep   `json:"steps,omitempty"`
	Asks    []AskView    `json:"asks"`
	// WaitingOn names whoever owes the chain an answer, which is the one question a queue
	// and a reminder both ask.
	WaitingOn  []string   `json:"waiting_on,omitempty"`
	DeadlineAt *time.Time `json:"step_deadline_at,omitempty"`
	Overdue    bool       `json:"is_overdue"`
	Holds      bool       `json:"holds_the_pass"`
	Tally      ChainTally `json:"tally"`
}

// ChainTally is the step-by-step count a supervisor reads before deciding to chase anybody.
type ChainTally struct {
	Asked     int `json:"asked"`
	Answered  int `json:"answered"`
	Waiting   int `json:"waiting"`
	Approved  int `json:"approved"`
	Rejected  int `json:"rejected"`
	Returned  int `json:"returned"`
	Recalled  int `json:"recalled"`
	Remaining int `json:"steps_remaining"`
}

func chainViewOf(scope tenancy.Scope, d Detail) ChainView {
	var waiting []string
	tally := ChainTally{Asked: len(d.Chain.Asks), Waiting: len(d.Waiting)}
	for _, ask := range d.Chain.Asks {
		switch ask.Status {
		case string(StepApproved), string(StepAutoApproved):
			tally.Approved++
		case string(StepRejected), string(StepExpired):
			tally.Rejected++
		case string(StepReturned):
			tally.Returned++
		case string(StepRecalled):
			tally.Recalled++
		}
		if ask.Waiting() {
			waiting = append(waiting, nameOf(ask))
		}
	}
	tally.Answered = tally.Approved + tally.Rejected + tally.Returned
	tally.Remaining = d.Chain.Request.TotalSteps - d.Chain.Request.CurrentStepNo
	if tally.Remaining < 0 {
		tally.Remaining = 0
	}

	return ChainView{
		Request: summaryOf(d.Chain.Request), Pass: briefOf(d.Chain.Pass),
		Steps: d.Chain.Steps, Asks: asksOf(d.Chain.Asks, scope.UserID, d.HeldRoles),
		WaitingOn: waiting, DeadlineAt: d.Deadline, Overdue: d.Overdue,
		Holds: d.Chain.Request.Open(),
		Tally: tally,
	}
}

// DecisionView is what one answer leaves behind: the chain it landed in and the pass it
// decided. Both come back because the two are the same event seen from two screens, and a
// desk that has to ask for the pass again cannot show whether the gate is now open.
type DecisionView struct {
	Request ChainSummary `json:"request"`
	Pass    PassBrief    `json:"pass"`
}

// InboxItem is one ask the caller owes, with enough about the pass to decide it without
// opening the module that raised it.
type InboxItem struct {
	InboxRow
	Overdue bool `json:"is_overdue"`
}

func inboxViews(rows []InboxRow) []InboxItem {
	at := time.Now().UTC()
	out := make([]InboxItem, 0, len(rows))
	for _, row := range rows {
		out = append(out, InboxItem{InboxRow: row, Overdue: past(row.DueAt, at)})
	}
	return out
}

// FlowView is one routing rule as its owner reads it, with the chain it starts attached.
type FlowView struct {
	ID               int64      `json:"id"`
	Name             string     `json:"name"`
	PassTypeID       *int64     `json:"pass_type_id,omitempty"`
	CategoryKey      *string    `json:"category_key,omitempty"`
	DepartmentID     *int64     `json:"department_id,omitempty"`
	SiteID           *int64     `json:"site_id,omitempty"`
	MinValue         *float64   `json:"min_value,omitempty"`
	MaxValue         *float64   `json:"max_value,omitempty"`
	Currency         *string    `json:"currency,omitempty"`
	Priority         int        `json:"priority"`
	IsDefault        bool       `json:"is_default"`
	OutsideHoursOnly bool       `json:"outside_hours_only"`
	Status           string     `json:"status"`
	Description      *string    `json:"description,omitempty"`
	Active           bool       `json:"is_pickable"`
	CreatedBy        *int64     `json:"created_by,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	Steps            []FlowStep `json:"steps"`
}

func flowViewOf(detail FlowDetail) FlowView {
	flow := detail.Flow
	return FlowView{
		ID: flow.ID, Name: flow.Name, PassTypeID: flow.PassTypeID,
		CategoryKey: flow.CategoryKey, DepartmentID: flow.DepartmentID, SiteID: flow.SiteID,
		MinValue: flow.MinValue, MaxValue: flow.MaxValue, Currency: flow.Currency,
		Priority: flow.Priority, IsDefault: flow.IsDefault, Active: flow.Active(),
		OutsideHoursOnly: flow.OutsideHoursOnly, Status: flow.Status,
		Description: flow.Description, CreatedBy: flow.CreatedBy,
		CreatedAt: flow.CreatedAt, UpdatedAt: flow.UpdatedAt, Steps: detail.Steps,
	}
}

func flowViewsOf(details []FlowDetail) []FlowView {
	out := make([]FlowView, 0, len(details))
	for _, detail := range details {
		out = append(out, flowViewOf(detail))
	}
	return out
}

// DelegationView is one cover. The names are not here: a cover board is read by the two
// people on it, and the directory is a different module's table to join.
type DelegationView struct {
	ID           int64     `json:"id"`
	FromUserID   int64     `json:"from_user_id"`
	ToUserID     int64     `json:"to_user_id"`
	FlowID       *int64    `json:"approval_flow_id,omitempty"`
	PassTypeID   *int64    `json:"pass_type_id,omitempty"`
	DepartmentID *int64    `json:"department_id,omitempty"`
	StartsAt     time.Time `json:"starts_at"`
	EndsAt       time.Time `json:"ends_at"`
	Reason       *string   `json:"reason,omitempty"`
	Status       string    `json:"status"`
	InForce      bool      `json:"is_in_force"`
	CreatedBy    *int64    `json:"created_by,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

func delegationViewOf(d Delegation) DelegationView {
	return DelegationView{
		ID: d.ID, FromUserID: d.FromUserID, ToUserID: d.ToUserID, FlowID: d.ApprovalFlowID,
		PassTypeID: d.PassTypeID, DepartmentID: d.DepartmentID, StartsAt: d.StartsAt,
		EndsAt: d.EndsAt, Reason: d.Reason, Status: d.Status, CreatedBy: d.CreatedBy,
		CreatedAt: d.CreatedAt,
		InForce: d.Status == "active" && !d.StartsAt.After(time.Now().UTC()) &&
			d.EndsAt.After(time.Now().UTC()),
	}
}

func delegationViewsOf(delegations []Delegation) []DelegationView {
	out := make([]DelegationView, 0, len(delegations))
	for _, delegation := range delegations {
		out = append(out, delegationViewOf(delegation))
	}
	return out
}

// ParseList reads the register filters:
// ?status=&module=&flow_id=&assignee=&open=&limit=&cursor=.
func ParseList(r *http.Request) (ListFilter, httpx.Page, error) {
	query := r.URL.Query()
	flowID, err := optionalID(query, "flow_id")
	if err != nil {
		return ListFilter{}, httpx.Page{}, err
	}
	assignee, err := optionalID(query, "assignee")
	if err != nil {
		return ListFilter{}, httpx.Page{}, err
	}
	open, err := optionalBool(query, "open")
	if err != nil {
		return ListFilter{}, httpx.Page{}, err
	}

	filter := ListFilter{
		Status: query.Get("status"), ModuleKey: query.Get("module"), FlowID: flowID,
		Assignee: assignee, OpenOnly: open != nil && *open,
	}
	if filter.Status != "" && !knownRequestStatus(filter.Status) {
		return filter, httpx.Page{}, errs.BadField("status", filter.Status+" is not an approval status")
	}
	if len(filter.ModuleKey) > 64 {
		return filter, httpx.Page{}, errs.BadField("module", "too long")
	}
	page, err := httpx.ParsePage(r, defaultPage, 100)
	return filter, page, err
}

func knownRequestStatus(status string) bool {
	for _, candidate := range []RequestStatus{
		StatusNotRequired, StatusPending, StatusInProgress, StatusApproved, StatusRejected,
		StatusReturned, StatusCancelled, StatusExpired, StatusEscalated,
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

// optionalBool refuses a value that is neither yes nor no: silently dropping ?open=maybe
// would hand back the whole register and look like a filter that worked.
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

// scopeAndID reads the tenant off the session and the id off the path, which is what every
// one of these routes is: a pass, a chain, a step or a flow belonging to this tenant.
func scopeAndID(r *http.Request, name string) (tenancy.Scope, int64, error) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		return scope, 0, err
	}
	id, err := httpx.ID(r, name)
	return scope, id, err
}
