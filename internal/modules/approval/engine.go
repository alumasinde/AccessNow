package approval

import (
	"context"
	"time"

	"accessnow/internal/core/passes"
	"accessnow/internal/platform/audit"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
)

// Start opens the chain a pass arrived under. It is a reaction, not an endpoint: the module
// that issued the pass does not know this one exists, and a chain started from inside the
// issuer would make every module depend on the workflow.
//
// A pass that needs no chain, or belongs to a tenant with this module switched off, is a
// successful no-op. Only a flow that asks for approvers and cannot find one is an error,
// because that is a tenant's misconfiguration and the outbox is where it gets said.
func (s *Service) Start(ctx context.Context, tenantID, passID int64) error {
	on, err := s.enabled(ctx, tenantID)
	if err != nil || !on {
		return err
	}
	p, err := s.passes.Get(ctx, tenantID, passID)
	if err != nil {
		return err
	}
	// pending, draft and pending_review are the three states that mean somebody still wants
	// a decision - the last one because a returned pass comes back for a fresh chain.
	if !needsDecision(p.Status) {
		return nil
	}

	at := time.Now().UTC()
	route, err := s.match(ctx, p, at)
	if err != nil {
		return err
	}
	if route.None() {
		return nil
	}
	first := route.Steps[0]
	asked, err := s.resolve(ctx, first, route.Flow.ID, p, at)
	if err != nil {
		return err
	}

	freeze, err := snapshotOf(route.Steps)
	if err != nil {
		return err
	}
	return db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		req, fresh, err := repo.CreateRequest(ctx, NewRequest{
			TenantID: tenantID, PassID: passID, FlowID: route.Flow.ID, Snapshot: freeze,
			Status: StatusPending, TotalSteps: len(distinctStepNos(route.Steps)),
			ValueAmount: p.ValueAmount, Currency: str(p.ValueCurrency), DeadlineAt: first.Deadline(at),
		})
		if err != nil {
			return err
		}
		if !fresh {
			// A redelivered event for a chain already running: somebody else already asked.
			return nil
		}
		if _, err := repo.MoveRequest(ctx, tenantID, req.ID, StatusInProgress, first.StepNo,
			first.Deadline(at)); err != nil {
			return err
		}
		// The asks are written after the chain has moved, so the notice an approver is
		// handed describes the chain as it actually stands at that moment.
		req.Status = string(StatusInProgress)
		req.CurrentStepNo = first.StepNo
		if err := s.ask(ctx, c, req, p, first, asked, 0, at); err != nil {
			return err
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: tenantID, Action: "approval.started", SubjectID: req.ID, PassID: passID,
			SiteID: p.SiteID, ActorUserID: requesterOf(p),
			After: map[string]any{"flow": route.Flow.Name, "pass_no": p.PassNo,
				"steps": len(distinctStepNos(route.Steps)), "outside_hours": route.Outside},
		})
	})
}

// ask writes the asks for one step and announces them. Every row lands in the caller's
// transaction, so an approver is never told about a chain that then failed to start.
//
// escalatedFrom is the step an escalation was taken from, and it is what makes a sweep
// idempotent: Overdue refuses a step that already has an escalated sibling.
func (s *Service) ask(ctx context.Context, c db.Conn, req Request, p passes.Pass, def FlowStep,
	asked []assignee, escalatedFrom int, at time.Time) error {
	repo := s.repo.In(c)
	for _, who := range asked {
		if err := repo.InsertStep(ctx, NewStepRow{
			TenantID: req.TenantID, RequestID: req.ID, StepNo: def.StepNo, Name: def.Name,
			AssigneeKey: who.key, AssigneeUserID: who.userID, ApproverRoleKey: who.roleKey,
			AssigneeName: who.name, Mode: def.Mode, DueAt: def.Deadline(at),
			DelegatedFromUser: who.fromUser, DelegationID: who.delegID,
			EscalatedFrom: escalatedFrom,
		}); err != nil {
			return err
		}
		if err := s.publish(ctx, c, req, p, "approval.step.assigned", requesterOf(p), map[string]any{
			"step_no": def.StepNo, "step_name": def.Name, "mode": def.Mode,
			"role_key": who.roleKey, "assignee": who.name, "to_user_id": who.userID,
			"delegated_from": who.fromUser, "notify_user_ids": personIDs(who.people),
		}); err != nil {
			return err
		}
	}
	return nil
}

// advance is what one answer costs the chain: the next step gets its asks, or the chain
// ends. It runs inside the answering transaction so a step cannot be answered twice while
// the chain sits between two states.
//
// The returned bool is "the chain is over", and the caller has to move the pass afterwards:
// Core will not approve a pass an open chain still holds, so closing the chain is what makes
// the pass scannable at all.
func (s *Service) advance(ctx context.Context, c db.Conn, req Request, p passes.Pass,
	answered RequestStep, def FlowStep, at time.Time) (Request, bool, error) {
	repo := s.repo.In(c)
	from := StepStatus(answered.Status)
	if from != StepApproved {
		return req, false, nil
	}

	// An any-one step, and a parallel step that does not need everybody, settle on the first
	// yes. A step that does need everybody keeps its number until the last ask answers.
	if !def.WaitsForEveryone() {
		if _, err := repo.CloseSiblings(ctx, req.TenantID, req.ID, def.StepNo, answered.ID); err != nil {
			return Request{}, false, err
		}
	}
	waiting, err := repo.WaitingAtStep(ctx, req.TenantID, req.ID, def.StepNo)
	if err != nil {
		return Request{}, false, err
	}
	if len(waiting) > 0 {
		return req, false, nil
	}

	next, hasNext := nextStep(req, def.StepNo)
	if !hasNext {
		ok, err := repo.FinishRequest(ctx, req.TenantID, req.ID, StatusApproved,
			answeredUserID(answered), commentOf(answered))
		if err != nil {
			return Request{}, false, err
		}
		if !ok {
			return Request{}, false, errs.Conflict(
				"this approval chain moved while it was being decided")
		}
		closed, err := repo.RequestByID(ctx, req.TenantID, req.ID)
		return closed, true, err
	}

	if _, err := repo.MoveRequest(ctx, req.TenantID, req.ID, StatusInProgress, next.StepNo,
		next.Deadline(at)); err != nil {
		return Request{}, false, err
	}
	moved, err := repo.RequestByID(ctx, req.TenantID, req.ID)
	if err != nil {
		return Request{}, false, err
	}
	asked, err := s.resolve(ctx, next, derefID(req.ApprovalFlowID), p, at)
	if err != nil {
		return Request{}, false, err
	}
	if err := s.ask(ctx, c, moved, p, next, asked, 0, at); err != nil {
		return Request{}, false, err
	}
	return moved, false, nil
}

// resolve is who an approver type points at, found against the pass in front of us. Every
// branch answers with a real person or a real role; a step that points at nobody is a
// conflict naming the kind, because the alternative is a chain nobody can finish.
func (s *Service) resolve(ctx context.Context, def FlowStep, flowID int64, p passes.Pass,
	at time.Time) ([]assignee, error) {
	var who assignee
	var err error

	switch def.ApproverType {
	case ApproverRole:
		who, err = s.roleAsk(ctx, p, str(def.ApproverRoleKey))
	case ApproverUser:
		who, err = s.userAsk(ctx, p.TenantID, derefID(def.ApproverUserID), "named approver")
	case ApproverHost:
		who, err = s.userAsk(ctx, p.TenantID, derefID(p.HostUserID), "host")
	case ApproverSupervisor, ApproverRequesterManager:
		who, err = s.managerAsk(ctx, def, p)
	case ApproverDepartmentHead:
		who, err = s.departmentAsk(ctx, p)
	default:
		return nil, errs.Conflict("approver type " + def.ApproverType +
			" is not something this system can find")
	}
	if err != nil {
		return nil, err
	}
	return s.applyDelegations(ctx, []assignee{who}, flowID, p, at)
}

// roleAsk is one ask for a whole role: whoever holds it answers, including a person hired
// after this pass arrived. The holders read now are only who the notice goes to.
func (s *Service) roleAsk(ctx context.Context, p passes.Pass, roleKey string) (assignee, error) {
	if roleKey == "" {
		return assignee{}, errs.Conflict("a role step with no role named")
	}
	people, err := s.repo.RoleHolders(ctx, p.TenantID, roleKey, p.SiteID)
	if err != nil {
		return assignee{}, err
	}
	return assignee{key: roleAssigneeKey(roleKey), roleKey: roleKey, name: roleKey,
		people: people}, nil
}

func (s *Service) userAsk(ctx context.Context, tenantID, userID int64, label string) (assignee, error) {
	if userID == 0 {
		return assignee{}, errs.Conflict(missingApprover(label))
	}
	person, err := s.repo.PersonByID(ctx, tenantID, userID)
	if err != nil {
		return assignee{}, err
	}
	if !person.Active() {
		return assignee{}, errs.Conflict(missingApprover(label))
	}
	return assignee{key: assigneeKey(userID), userID: userID, name: person.Name,
		people: []Person{person}}, nil
}

// managerAsk is the requester's own line management: the supervisor the directory knows, and
// failing that the head of their department. A pass a guard filed for somebody else still
// asks that person's manager, not the guard's.
func (s *Service) managerAsk(ctx context.Context, def FlowStep, p passes.Pass) (assignee, error) {
	requester := derefID(p.RequesterUserID)
	if requester == 0 {
		return assignee{}, errs.Conflict(missingApprover("requester's manager"))
	}
	supervisor, err := s.repo.SupervisorOf(ctx, p.TenantID, requester)
	if err != nil {
		return assignee{}, err
	}
	if supervisor.Active() {
		return assignee{key: assigneeKey(supervisor.UserID), userID: supervisor.UserID,
			name: supervisor.Name, people: []Person{supervisor}}, nil
	}
	if def.ApproverType == ApproverSupervisor || p.DepartmentID == nil {
		return assignee{}, errs.Conflict(missingApprover("supervisor"))
	}
	return s.departmentAsk(ctx, p)
}

func (s *Service) departmentAsk(ctx context.Context, p passes.Pass) (assignee, error) {
	head, err := s.repo.DepartmentHead(ctx, p.TenantID, derefID(p.DepartmentID))
	if err != nil {
		return assignee{}, err
	}
	if !head.Active() {
		return assignee{}, errs.Conflict(missingApprover("department head"))
	}
	return assignee{key: assigneeKey(head.UserID), userID: head.UserID, name: head.Name,
		people: []Person{head}}, nil
}

// applyDelegations hands a named ask to whoever is covering for that person. A role ask is
// left alone: any holder may answer it, so a cover is already inside that, and rewriting the
// row would lose the role the flow named.
func (s *Service) applyDelegations(ctx context.Context, asked []assignee, flowID int64,
	p passes.Pass, at time.Time) ([]assignee, error) {
	out := make([]assignee, 0, len(asked))
	for _, who := range asked {
		if who.isRole() || who.userID == 0 {
			out = append(out, who)
			continue
		}
		delegation, found, err := s.repo.DelegationFor(ctx, DelegationScope{
			TenantID: p.TenantID, FromUserID: who.userID, FlowID: flowID,
			PassTypeID: p.PassTypeID, DepartmentID: derefID(p.DepartmentID), At: at,
		})
		if err != nil {
			return nil, err
		}
		if !found {
			out = append(out, who)
			continue
		}
		to, err := s.repo.PersonByID(ctx, p.TenantID, delegation.ToUserID)
		if err != nil {
			return nil, err
		}
		if !to.Active() {
			return nil, errs.Conflict("the approver covering for " + who.name +
				" cannot sign in; fix the delegation")
		}
		out = append(out, assignee{key: assigneeKey(to.UserID), userID: to.UserID, name: to.Name,
			people: []Person{to}, fromUser: who.userID, delegID: delegation.ID})
	}
	return out, nil
}

// nextStep is the step after the one just answered. Parallel siblings share a number, so the
// frozen list is walked by step_no rather than by index.
func nextStep(req Request, after int) (FlowStep, bool) {
	steps, err := req.Definition()
	if err != nil {
		return FlowStep{}, false
	}
	var best *FlowStep
	for i, step := range steps {
		if step.StepNo <= after {
			continue
		}
		if best == nil || step.StepNo < best.StepNo {
			best = &steps[i]
		}
	}
	if best == nil {
		return FlowStep{}, false
	}
	return *best, true
}

func distinctStepNos(steps []FlowStep) []int {
	seen := make(map[int]bool, len(steps))
	out := make([]int, 0, len(steps))
	for _, step := range steps {
		if seen[step.StepNo] {
			continue
		}
		seen[step.StepNo] = true
		out = append(out, step.StepNo)
	}
	return out
}

// needsDecision is the only reason a chain starts: the pass is still asking for a decision.
func needsDecision(status string) bool {
	switch status {
	case string(passes.StatusPending), string(passes.StatusDraft),
		string(passes.StatusPendingReview):
		return true
	default:
		return false
	}
}

func decidedStatus(from StepStatus) RequestStatus {
	if from == StepRejected {
		return StatusRejected
	}
	return StatusApproved
}

func requesterOf(p passes.Pass) int64 { return derefID(p.RequesterUserID) }

func answeredUserID(step RequestStep) int64 { return derefID(step.ActedByUserID) }

func commentOf(step RequestStep) string { return str(step.Comment) }

func derefID(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}

func missingApprover(label string) string {
	return "the " + label + " for this pass cannot be found, so nobody can be asked"
}

func personIDs(people []Person) []int64 {
	out := make([]int64, 0, len(people))
	for _, person := range people {
		out = append(out, person.UserID)
	}
	return out
}
