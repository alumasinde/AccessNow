// Package approval is the workflow slice: who has to say yes, in what order, before a pass
// is worth anything at a barrier. It owns no pass of its own - every chain hangs off a Core
// pass issued by another module - and it never decides at the gate. A flow is a tenant's
// routing rule; a request is one pass working its way through a flow.
package approval

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ModuleKey is the catalogue row this module switches on and off with.
const ModuleKey = "approval_workflow"

// aggregateType is what the outbox calls a chain, so an event can be traced back to the
// request that produced it without a second lookup.
const aggregateType = "approval_request"

// RequestStatus mirrors chk_approval_requests_status.
type RequestStatus string

const (
	StatusNotRequired RequestStatus = "not_required"
	StatusPending     RequestStatus = "pending"
	StatusInProgress  RequestStatus = "in_progress"
	StatusApproved    RequestStatus = "approved"
	StatusRejected    RequestStatus = "rejected"
	StatusReturned    RequestStatus = "returned"
	StatusCancelled   RequestStatus = "cancelled"
	StatusExpired     RequestStatus = "expired"
	StatusEscalated   RequestStatus = "escalated"
)

// StepStatus mirrors chk_approval_request_steps_status. A recalled row is a live one that
// was taken off its assignee by an escalation; a delegated row stays on file so the audit
// trail shows who was asked before the answer actually came.
type StepStatus string

const (
	StepPending      StepStatus = "pending"
	StepApproved     StepStatus = "approved"
	StepRejected     StepStatus = "rejected"
	StepReturned     StepStatus = "returned"
	StepSkipped      StepStatus = "skipped"
	StepDelegated    StepStatus = "delegated"
	StepExpired      StepStatus = "expired"
	StepRecalled     StepStatus = "recalled"
	StepAutoApproved StepStatus = "auto_approved"
)

// The three ways a step can be answered. sequential walks the approvers one after
// another; parallel waits for every assignee when the step requires it; any_one is the
// round-robin step where the first yes is enough.
const (
	ModeSequential = "sequential"
	ModeParallel   = "parallel"
	ModeAnyOne     = "any_one"
)

// Approver kinds mirror chk_approval_flow_steps_type. Each one is resolved to real people
// when the step is assigned, never when the flow is saved: a role that is empty today may
// be staffed by the time the pass arrives.
const (
	ApproverRole             = "role"
	ApproverUser             = "user"
	ApproverSupervisor       = "supervisor"
	ApproverHost             = "host"
	ApproverDepartmentHead   = "department_head"
	ApproverRequesterManager = "requester_manager"
)

// Action is what an approver does to a step. reject always costs a comment - the guide asks
// for a mandatory reason and the schema has require_comment, so the stricter rule wins for
// the one decision a requester has to be able to read.
type Action string

const (
	ActionApprove Action = "approve"
	ActionReject  Action = "reject"
	ActionReturn  Action = "return"
)

func (a Action) Valid() bool {
	switch a {
	case ActionApprove, ActionReject, ActionReturn:
		return true
	default:
		return false
	}
}

// Flow is one tenant routing rule: which passes it matches, and how urgent it is to pick
// this flow over another.
type Flow struct {
	ID               int64      `db:"id"`
	TenantID         int64      `db:"tenant_id"`
	Name             string     `db:"name"`
	PassTypeID       *int64     `db:"pass_type_id"`
	CategoryKey      *string    `db:"category_key"`
	DepartmentID     *int64     `db:"department_id"`
	SiteID           *int64     `db:"site_id"`
	MinValue         *float64   `db:"min_value"`
	MaxValue         *float64   `db:"max_value"`
	Currency         *string    `db:"currency"`
	Priority         int        `db:"priority"`
	IsDefault        bool       `db:"is_default"`
	OutsideHoursOnly bool       `db:"outside_hours_only"`
	Status           string     `db:"status"`
	Description      *string    `db:"description"`
	CreatedBy        *int64     `db:"created_by"`
	CreatedAt        time.Time  `db:"created_at"`
	UpdatedAt        time.Time  `db:"updated_at"`
	DeletedAt        *time.Time `db:"deleted_at"`
}

// Active reports a flow the matcher may pick up. An inactive flow keeps its history: the
// requests already working through it were started under a rule the tenant has not deleted.
func (f Flow) Active() bool { return f.Status == "active" && f.DeletedAt == nil }

// FlowStep is one ordered step of a flow, as the tenant defined it. The JSON tags are for
// flow_snapshot: a chain freezes this list the moment the pass arrives, so a later edit to
// the flow cannot rewrite who was asked.
type FlowStep struct {
	ID                 int64     `db:"id" json:"-"`
	TenantID           int64     `db:"tenant_id" json:"-"`
	ApprovalFlowID     int64     `db:"approval_flow_id" json:"-"`
	StepNo             int       `db:"step_no" json:"step_no"`
	Name               string    `db:"name" json:"name"`
	ApproverType       string    `db:"approver_type" json:"approver_type"`
	ApproverRoleKey    *string   `db:"approver_role_key" json:"approver_role_key,omitempty"`
	ApproverUserID     *int64    `db:"approver_user_id" json:"approver_user_id,omitempty"`
	Mode               string    `db:"mode" json:"mode"`
	RequiresAll        bool      `db:"requires_all" json:"requires_all"`
	SLAHours           *int      `db:"sla_hours" json:"sla_hours,omitempty"`
	EscalateAfterHours *int      `db:"escalate_after_hours" json:"escalate_after_hours,omitempty"`
	EscalateToRoleKey  *string   `db:"escalate_to_role_key" json:"escalate_to_role_key,omitempty"`
	EscalateToUserID   *int64    `db:"escalate_to_user_id" json:"escalate_to_user_id,omitempty"`
	AllowSelfApproval  bool      `db:"allow_self_approval" json:"allow_self_approval"`
	RequireComment     bool      `db:"require_comment" json:"require_comment"`
	Memo               *string   `db:"memo" json:"memo,omitempty"`
	IsActive           bool      `db:"is_active" json:"-"`
	CreatedAt          time.Time `db:"created_at" json:"-"`
	UpdatedAt          time.Time `db:"updated_at" json:"-"`
}

// WaitsForEveryone reports whether this step needs every assignee, as opposed to the first
// one who turns up.
func (s FlowStep) WaitsForEveryone() bool {
	return s.Mode == ModeParallel && s.RequiresAll
}

// Deadline is when the step is due, or nil when the tenant set no clock on it.
func (s FlowStep) Deadline(from time.Time) *time.Time {
	hours := s.EscalateAfterHours
	if hours == nil {
		hours = s.SLAHours
	}
	if hours == nil || *hours <= 0 {
		return nil
	}
	due := from.Add(time.Duration(*hours) * time.Hour)
	return &due
}

// Request is one pass working through a flow.
type Request struct {
	ID              int64           `db:"id"`
	TenantID        int64           `db:"tenant_id"`
	PassID          int64           `db:"pass_id"`
	ApprovalFlowID  *int64          `db:"approval_flow_id"`
	FlowSnapshot    json.RawMessage `db:"flow_snapshot"`
	Status          string          `db:"status"`
	CurrentStepNo   int             `db:"current_step_no"`
	TotalSteps      int             `db:"total_steps"`
	ValueAmount     *float64        `db:"value_amount"`
	Currency        *string         `db:"currency"`
	StartedAt       time.Time       `db:"started_at"`
	DeadlineAt      *time.Time      `db:"deadline_at"`
	CompletedAt     *time.Time      `db:"completed_at"`
	DecidedByUserID *int64          `db:"decided_by_user_id"`
	DecisionNote    *string         `db:"decision_note"`
	EscalationCount int             `db:"escalation_count"`
	CreatedAt       time.Time       `db:"created_at"`
	UpdatedAt       time.Time       `db:"updated_at"`
}

// Open reports a chain somebody can still act on. It is also what frees the pass's unique
// open_pass_key slot, so a chain that ends always leaves the slot empty for the next one.
func (r Request) Open() bool {
	switch r.Status {
	case string(StatusPending), string(StatusInProgress), string(StatusEscalated):
		return true
	default:
		return false
	}
}

// Definition is the step list this chain was started under, read back out of the snapshot.
// The engine asks this rather than the live flow for exactly one reason: the person who
// approved a pass last week approved it under the rule that was true last week.
func (r Request) Definition() ([]FlowStep, error) {
	if len(r.FlowSnapshot) == 0 {
		return nil, nil
	}
	var steps []FlowStep
	if err := json.Unmarshal(r.FlowSnapshot, &steps); err != nil {
		return nil, fmt.Errorf("decode approval chain definition: %w", err)
	}
	return steps, nil
}

// RequestStep is one assignee being asked. The row is the ask, and the answer on it is the
// only history the tenant keeps about who was asked and when.
type RequestStep struct {
	ID                   int64      `db:"id"`
	TenantID             int64      `db:"tenant_id"`
	ApprovalRequestID    int64      `db:"approval_request_id"`
	StepNo               int        `db:"step_no"`
	Name                 *string    `db:"name"`
	AssigneeKey          string     `db:"assignee_key"`
	AssigneeUserID       *int64     `db:"assignee_user_id"`
	ApproverRoleKey      *string    `db:"approver_role_key"`
	AssigneeNameSnapshot *string    `db:"assignee_name_snapshot"`
	Status               string     `db:"status"`
	Mode                 string     `db:"mode"`
	DueAt                *time.Time `db:"due_at"`
	AssignedAt           time.Time  `db:"assigned_at"`
	NotifiedAt           *time.Time `db:"notified_at"`
	ViewedAt             *time.Time `db:"viewed_at"`
	ActedAt              *time.Time `db:"acted_at"`
	ActedByUserID        *int64     `db:"acted_by_user_id"`
	Comment              *string    `db:"comment"`
	DelegatedFromUserID  *int64     `db:"delegated_from_user_id"`
	DelegationID         *int64     `db:"delegation_id"`
	EscalatedFromStepNo  *int       `db:"escalated_from_step_no"`
	CreatedAt            time.Time  `db:"created_at"`
	UpdatedAt            time.Time  `db:"updated_at"`
}

// Waiting reports the rows an approver can still answer.
func (s RequestStep) Waiting() bool { return s.Status == string(StepPending) }

// ForRole reports a step addressed to whoever holds a role rather than to one named person.
// Any holder may answer it, and the first one to do so owns the answer.
func (s RequestStep) ForRole() bool {
	return s.AssigneeUserID == nil && s.ApproverRoleKey != nil && *s.ApproverRoleKey != ""
}

// Delegation is an approver handing their turn to somebody else for a window. It is applied
// when a step is assigned, which is what the schema says in its own comment: a delegation
// made after the request arrived does not rewrite who was asked.
type Delegation struct {
	ID             int64     `db:"id"`
	TenantID       int64     `db:"tenant_id"`
	FromUserID     int64     `db:"from_user_id"`
	ToUserID       int64     `db:"to_user_id"`
	ApprovalFlowID *int64    `db:"approval_flow_id"`
	PassTypeID     *int64    `db:"pass_type_id"`
	DepartmentID   *int64    `db:"department_id"`
	StartsAt       time.Time `db:"starts_at"`
	EndsAt         time.Time `db:"ends_at"`
	Reason         *string   `db:"reason"`
	Status         string    `db:"status"`
	CreatedBy      *int64    `db:"created_by"`
	CreatedAt      time.Time `db:"created_at"`
	UpdatedAt      time.Time `db:"updated_at"`
}

// Covers reports whether this delegation was in force at a moment, for a pass type. A nil
// scope means "everything the delegator would have been asked about".
func (d Delegation) Covers(at time.Time, passTypeID int64) bool {
	if d.Status != "active" || at.Before(d.StartsAt) || !at.Before(d.EndsAt) {
		return false
	}
	return d.PassTypeID == nil || *d.PassTypeID == passTypeID
}

// assigneeKey is the unique handle for one ask: user:17, or role:shift_supervisor.
func assigneeKey(user int64) string { return "user:" + strconv.FormatInt(user, 10) }

func roleAssigneeKey(key string) string { return "role:" + strings.ToLower(key) }

// stepAt finds the definition of one numbered step inside a frozen chain. Parallel steps
// share a number, and every row of the group carries the same settings, so the first match
// is the whole answer.
func stepAt(steps []FlowStep, no int) (FlowStep, bool) {
	for _, step := range steps {
		if step.StepNo == no {
			return step, true
		}
	}
	return FlowStep{}, false
}

// str reads a nullable column the way the engine needs it: an unset role key is "" and
// therefore matches nothing, which is what an unfiltered step means.
func str(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
