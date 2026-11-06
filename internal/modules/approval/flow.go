package approval

import (
	"context"
	"strings"

	"accessnow/internal/platform/audit"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/tenancy"
)

// FlowDetail is a rule as its owner reads it: the match, and the chain it starts.
type FlowDetail struct {
	Flow  Flow       `json:"flow"`
	Steps []FlowStep `json:"steps"`
}

// ListFlows is the tenant's routing table. It comes back with each flow's steps attached
// because a screen that lists eleven rules and needs a click for each of them is a screen
// nobody uses to work out why a pass took a detour.
func (s *Service) ListFlows(ctx context.Context, scope tenancy.Scope) ([]FlowDetail, error) {
	if err := s.requireEnabled(ctx, scope.TenantID); err != nil {
		return nil, err
	}
	flows, err := s.repo.ListFlows(ctx, scope.TenantID)
	if err != nil {
		return nil, err
	}
	out := make([]FlowDetail, 0, len(flows))
	for _, flow := range flows {
		steps, err := s.repo.Steps(ctx, scope.TenantID, flow.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, FlowDetail{Flow: flow, Steps: steps})
	}
	return out, nil
}

func (s *Service) Flow(ctx context.Context, scope tenancy.Scope, id int64) (FlowDetail, error) {
	if err := s.requireEnabled(ctx, scope.TenantID); err != nil {
		return FlowDetail{}, err
	}
	flow, err := s.repo.FlowByID(ctx, scope.TenantID, id)
	if err != nil {
		return FlowDetail{}, err
	}
	steps, err := s.repo.Steps(ctx, scope.TenantID, id)
	if err != nil {
		return FlowDetail{}, err
	}
	return FlowDetail{Flow: flow, Steps: steps}, nil
}

// SaveFlow files a rule and its chain together. A flow with no steps would start a chain
// that asks nobody and holds nothing, so the two are written in one call or not at all.
//
// The match columns - type, category, department, site, value band - are fixed at creation.
// A chain already running read them when the pass arrived, and a tenant who wants different
// rules for the passes arriving now writes a new flow rather than retargeting the one that
// is halfway through somebody's approval.
func (s *Service) SaveFlow(ctx context.Context, scope tenancy.Scope, in NewFlow,
	steps []NewStep, id int64) (FlowDetail, error) {
	if err := s.requireEnabled(ctx, scope.TenantID); err != nil {
		return FlowDetail{}, err
	}
	if err := checkFlow(in, steps); err != nil {
		return FlowDetail{}, err
	}
	in.TenantID = scope.TenantID
	in.CreatedBy = scope.UserID

	var saved FlowDetail
	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		flowID := id
		if flowID == 0 {
			created, err := repo.CreateFlow(ctx, in)
			if err != nil {
				return err
			}
			flowID = created
		} else if err := repo.UpdateFlow(ctx, scope.TenantID, flowID, UpdateFlow{
			Name: in.Name, Priority: in.Priority, IsDefault: in.IsDefault,
			OutsideHoursOnly: in.OutsideHoursOnly, Status: in.Status, Description: in.Description,
		}); err != nil {
			return err
		}
		if err := repo.ReplaceSteps(ctx, scope.TenantID, flowID, steps); err != nil {
			return err
		}
		flow, err := repo.FlowByID(ctx, scope.TenantID, flowID)
		if err != nil {
			return err
		}
		defined, err := repo.Steps(ctx, scope.TenantID, flowID)
		if err != nil {
			return err
		}
		saved = FlowDetail{Flow: flow, Steps: defined}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "approval_flow.saved",
			SubjectType: "approval_flow", SubjectID: flowID,
			Before: map[string]any{"existed": id != 0},
			After: map[string]any{"name": flow.Name, "steps": len(steps), "priority": flow.Priority,
				"outside_hours_only": flow.OutsideHoursOnly},
		})
	})
	if err != nil {
		return FlowDetail{}, err
	}
	return saved, nil
}

// DeleteFlow retires a rule. The chains already working through it are left alone: they were
// started under a rule the tenant has not deleted, and a pass should not lose its approvers
// because the flow that asked them was switched off.
func (s *Service) DeleteFlow(ctx context.Context, scope tenancy.Scope, id int64) error {
	if err := s.requireEnabled(ctx, scope.TenantID); err != nil {
		return err
	}
	flow, err := s.repo.FlowByID(ctx, scope.TenantID, id)
	if err != nil {
		return err
	}
	return db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		if err := repo.SoftDeleteFlow(ctx, scope.TenantID, id); err != nil {
			return err
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "approval_flow.deleted",
			SubjectType: "approval_flow", SubjectID: id,
			Before: map[string]any{"name": flow.Name}, After: map[string]any{"deleted": true},
		})
	})
}

// checkFlow is the schema's own rules said in Go first, so a tenant who wrote a chain that
// cannot run hears which step is wrong instead of a database error number.
func checkFlow(in NewFlow, steps []NewStep) error {
	if strings.TrimSpace(in.Name) == "" {
		return errs.Invalid("a flow needs a name", errs.InvalidField("name", "required"))
	}
	if in.Status != "active" && in.Status != "inactive" {
		return errs.Invalid("status must be active or inactive", errs.InvalidField("status", "unknown"))
	}
	if in.MinValue != nil && in.MaxValue != nil && *in.MaxValue < *in.MinValue {
		return errs.Invalid("the value band ends below where it starts",
			errs.InvalidField("max_value", "below min_value"))
	}
	if len(steps) == 0 {
		return errs.Invalid("a flow needs at least one step to ask", errs.InvalidField("steps", "empty"))
	}

	nums := make(map[int]bool, len(steps))
	for i, step := range steps {
		if err := checkStep(step, i); err != nil {
			return err
		}
		nums[step.StepNo] = true
	}
	if !nums[lowestStep(steps)] {
		return errs.Invalid("the chain has to start at the lowest step number")
	}
	return nil
}

func checkStep(step NewStep, i int) error {
	field := "steps[" + itoa(i) + "]"
	if !knownApprover(step.ApproverType) {
		return errs.Invalid("approver type "+step.ApproverType+" is not one this system can find",
			errs.InvalidField(field+".approver_type", "unknown"))
	}
	if step.ApproverType == ApproverRole && strings.TrimSpace(step.ApproverRoleKey) == "" {
		return errs.Invalid("a role step has to name the role",
			errs.InvalidField(field+".approver_role_key", "required"))
	}
	if step.ApproverType == ApproverUser && step.ApproverUserID == 0 {
		return errs.Invalid("a named approver step has to say who",
			errs.InvalidField(field+".approver_user_id", "required"))
	}
	if !knownMode(step.Mode) {
		return errs.Invalid("mode "+step.Mode+" is not sequential, parallel or any_one",
			errs.InvalidField(field+".mode", "unknown"))
	}
	if step.StepNo <= 0 {
		return errs.Invalid("step numbers start at one", errs.InvalidField(field+".step_no", "range"))
	}
	if step.SLAHours != nil && *step.SLAHours <= 0 {
		return errs.Invalid("an SLA has to be a number of hours", errs.InvalidField(field+".sla_hours", "range"))
	}
	return nil
}

func knownApprover(kind string) bool {
	switch kind {
	case ApproverRole, ApproverUser, ApproverSupervisor, ApproverHost,
		ApproverDepartmentHead, ApproverRequesterManager:
		return true
	default:
		return false
	}
}

func knownMode(mode string) bool {
	switch mode {
	case ModeSequential, ModeParallel, ModeAnyOne:
		return true
	default:
		return false
	}
}

func lowestStep(steps []NewStep) int {
	lowest := steps[0].StepNo
	for _, step := range steps[1:] {
		if step.StepNo < lowest {
			lowest = step.StepNo
		}
	}
	return lowest
}

// ListDelegations is the cover board. fromUserID 0 means the whole tenant, which is what an
// administrator wants; a person passing their own hand asks it with their own id.
func (s *Service) ListDelegations(ctx context.Context, scope tenancy.Scope, fromUserID int64) ([]Delegation, error) {
	if err := s.requireEnabled(ctx, scope.TenantID); err != nil {
		return nil, err
	}
	return s.repo.ListDelegations(ctx, scope.TenantID, fromUserID)
}

// SaveDelegation files a cover. The window is checked here as well as in the schema because
// the schema can only say "ends after starts", and a person who delegates to themselves has
// written a chain that answers its own question.
func (s *Service) SaveDelegation(ctx context.Context, scope tenancy.Scope, in NewDelegation) (Delegation, error) {
	if err := s.requireEnabled(ctx, scope.TenantID); err != nil {
		return Delegation{}, err
	}
	in.TenantID = scope.TenantID
	if in.FromUserID == 0 {
		in.FromUserID = scope.UserID
	}
	if err := checkDelegation(in); err != nil {
		return Delegation{}, err
	}
	if _, err := s.repo.PersonByID(ctx, scope.TenantID, in.ToUserID); err != nil {
		return Delegation{}, err
	}

	var saved Delegation
	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		id, err := repo.CreateDelegation(ctx, in)
		if err != nil {
			return err
		}
		saved, err = repo.DelegationByID(ctx, scope.TenantID, id)
		if err != nil {
			return err
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "approval.delegated",
			SubjectType: "approval_delegation", SubjectID: id,
			After: map[string]any{"from_user_id": in.FromUserID, "to_user_id": in.ToUserID,
				"starts_at": in.StartsAt, "ends_at": in.EndsAt, "reason": in.Reason},
		})
	})
	if err != nil {
		return Delegation{}, err
	}
	return saved, nil
}

// EndDelegation cancels a cover early. Asks already handed over stay where they went: a
// cancellation changes who is asked next, not what has already been answered.
func (s *Service) EndDelegation(ctx context.Context, scope tenancy.Scope, id int64) error {
	if err := s.requireEnabled(ctx, scope.TenantID); err != nil {
		return err
	}
	delegation, err := s.repo.DelegationByID(ctx, scope.TenantID, id)
	if err != nil {
		return err
	}
	if !scope.IsSuperAdmin && scope.UserID != delegation.FromUserID {
		return errs.Forbidden("only the person who filed a cover can end it")
	}
	return db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		if err := repo.EndDelegation(ctx, scope.TenantID, id); err != nil {
			return err
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "approval.delegation_ended",
			SubjectType: "approval_delegation", SubjectID: id,
			Before: map[string]any{"status": delegation.Status}, After: map[string]any{"status": "cancelled"},
		})
	})
}

func checkDelegation(in NewDelegation) error {
	if in.FromUserID == in.ToUserID {
		return errs.Invalid("an approver cannot cover for themselves",
			errs.InvalidField("to_user_id", "self"))
	}
	if in.ToUserID == 0 {
		return errs.Invalid("a cover has to name who answers", errs.InvalidField("to_user_id", "required"))
	}
	if !in.EndsAt.After(in.StartsAt) {
		return errs.Invalid("the cover window ends before it starts",
			errs.InvalidField("ends_at", "before_starts_at"))
	}
	return nil
}
