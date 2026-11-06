package approval

import (
	"context"
	"strings"
	"time"

	"accessnow/internal/core/passes"
	"accessnow/internal/platform/audit"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/tenancy"
)

// Decision is what an approver does with the ask in front of them. reject costs a reason
// always, and so does return, because a pass sent back for correction without saying what to
// correct comes back unchanged.
type Decision struct {
	Action  Action
	Comment string
}

// Answer is one approver's turn, taken.
//
// The chain is decided here and the pass is moved by Core afterwards, in that order and for
// one reason: Core refuses to approve a pass an open chain still holds. A crash between the
// two leaves a chain that has said yes and a pass still marked pending - which the desk can
// settle by hand, because the chain no longer holds it. The other order would let a refused
// chain own a pass a gate already waves through.
func (s *Service) Answer(ctx context.Context, scope tenancy.Scope, stepID int64,
	in Decision) (Request, passes.Pass, error) {
	if !in.Action.Valid() {
		return Request{}, passes.Pass{}, errs.Invalid("decision " + string(in.Action) +
			" is not one of approve, reject or return")
	}
	if err := s.requireEnabled(ctx, scope.TenantID); err != nil {
		return Request{}, passes.Pass{}, err
	}

	ask, err := s.repo.StepByID(ctx, scope.TenantID, stepID)
	if err != nil {
		return Request{}, passes.Pass{}, err
	}
	if !ask.Waiting() {
		return Request{}, passes.Pass{}, errs.Conflict("this approval was already answered")
	}
	req, err := s.openRequest(ctx, scope.TenantID, ask.ApprovalRequestID)
	if err != nil {
		return Request{}, passes.Pass{}, err
	}
	def, err := stepDefinition(req, ask.StepNo)
	if err != nil {
		return Request{}, passes.Pass{}, err
	}
	if err := s.mayAnswer(ctx, scope, ask); err != nil {
		return Request{}, passes.Pass{}, err
	}
	p, err := s.passes.Get(ctx, scope.TenantID, req.PassID)
	if err != nil {
		return Request{}, passes.Pass{}, err
	}
	if err := s.checkAsks(def, p, scope, in); err != nil {
		return Request{}, passes.Pass{}, err
	}

	to := StepApproved
	switch in.Action {
	case ActionReturn:
		to = StepReturned
	case ActionReject:
		to = StepRejected
	}
	answered := ask
	answered.Status = string(to)
	answered.Comment = ptr(in.Comment)
	answered.ActedByUserID = ptr(scope.UserID)

	var done Request
	var over bool
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		ok, err := repo.Answer(ctx, scope.TenantID, stepID, to, scope.UserID, in.Comment)
		if err != nil {
			return err
		}
		if !ok {
			// Two holders of one role, one click each, one second apart: the loser is told,
			// because the chain they thought they were moving has already moved.
			return errs.Conflict("somebody answered this approval first")
		}
		done, over, err = s.settle(ctx, c, req, p, answered, def, in)
		if err != nil {
			return err
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID,
			Action: "approval." + string(in.Action), SubjectID: done.ID, PassID: req.PassID,
			SiteID: p.SiteID, Reason: in.Comment,
			Before: map[string]any{"status": req.Status, "step_no": def.StepNo,
				"asked_of": nameOf(ask)},
			After: map[string]any{"status": done.Status, "step_no": done.CurrentStepNo,
				"decision": string(in.Action), "comment": in.Comment},
		})
	})
	if err != nil {
		return Request{}, passes.Pass{}, err
	}
	if !over {
		return done, p, nil
	}

	next, note := passDecision(done.Status, in)
	decided, err := s.passes.Decide(ctx, scope, req.PassID, next, note)
	if err != nil {
		return done, p, err
	}
	return done, decided, nil
}

// settle is the whole difference between one answer and a decision: an approve that still
// owes the step's siblings changes nothing anyone can see, an approve that clears the last
// step ends the chain, and any no ends it immediately.
func (s *Service) settle(ctx context.Context, c db.Conn, req Request, p passes.Pass,
	answered RequestStep, def FlowStep, in Decision) (Request, bool, error) {
	if in.Action == ActionApprove {
		moved, over, err := s.advance(ctx, c, req, p, answered, def, time.Now().UTC())
		if err != nil || !over {
			return moved, over, err
		}
		return moved, true, s.publish(ctx, c, moved, p, "approval.decided",
			answeredUserID(answered), map[string]any{"decision": "approved"})
	}

	repo := s.repo.In(c)
	// A no is a no for the whole chain, whoever was still being asked.
	to := StatusRejected
	key := "approval.rejected"
	if in.Action == ActionReturn {
		to, key = StatusReturned, "approval.returned"
	}
	if _, err := repo.CancelAsks(ctx, req.TenantID, req.ID); err != nil {
		return Request{}, false, err
	}
	ok, err := repo.FinishRequest(ctx, req.TenantID, req.ID, to, answeredUserID(answered),
		commentOf(answered))
	if err != nil {
		return Request{}, false, err
	}
	if !ok {
		return Request{}, false, errs.Conflict("this approval chain moved while it was being decided")
	}
	closed, err := repo.RequestByID(ctx, req.TenantID, req.ID)
	if err != nil {
		return Request{}, false, err
	}
	return closed, true, s.publish(ctx, c, closed, p, key, answeredUserID(answered),
		map[string]any{"decision": string(in.Action), "comment": commentOf(answered),
			"step_no": def.StepNo})
}

// passDecision reads a finished chain back into what the spine does with the pass.
func passDecision(status string, in Decision) (passes.Status, string) {
	switch status {
	case string(StatusApproved):
		return passes.StatusApproved, "approved by the approval chain"
	case string(StatusRejected):
		return passes.StatusRejected, answerOf(in)
	case string(StatusReturned):
		// Returned for correction is not a rejection: the pass goes back to the person who
		// filed it, to be fixed and filed again.
		return passes.StatusPendingReview, answerOf(in)
	case string(StatusCancelled):
		return passes.StatusCancelled, answerOf(in)
	default:
		return passes.StatusCancelled, answerOf(in)
	}
}

// mayAnswer is the rule that stops an approval chain being a joke: the ask is addressed
// either to you by name, or to a role you actually hold today.
func (s *Service) mayAnswer(ctx context.Context, scope tenancy.Scope, ask RequestStep) error {
	if ask.AssigneeUserID != nil {
		if *ask.AssigneeUserID != scope.UserID {
			return errs.Forbidden("this approval was asked of " + nameOf(ask))
		}
		return nil
	}
	role := str(ask.ApproverRoleKey)
	if role == "" {
		return errs.Forbidden("this approval names no approver")
	}
	keys, err := s.repo.RoleKeysOf(ctx, scope.TenantID, scope.UserID)
	if err != nil {
		return err
	}
	for _, key := range keys {
		if key == role {
			return nil
		}
	}
	return errs.Forbidden("role " + role + " is needed to answer this")
}

// checkAsks is the two promises the guide makes in the tenant's own words: the requester
// cannot approve their own pass, and a comment is a condition of the step, not a courtesy.
func (s *Service) checkAsks(def FlowStep, p passes.Pass, scope tenancy.Scope, in Decision) error {
	if in.Action != ActionApprove && strings.TrimSpace(in.Comment) == "" {
		return errs.Invalid(string(in.Action)+" needs a reason the requester can read",
			errs.InvalidField("comment", "required"))
	}
	if def.RequireComment && strings.TrimSpace(in.Comment) == "" {
		return errs.Invalid("this step requires a comment", errs.InvalidField("comment", "required"))
	}
	if def.AllowSelfApproval {
		return nil
	}
	if p.RequesterUserID != nil && *p.RequesterUserID == scope.UserID {
		return errs.Forbidden("you filed this request; the flow asks for somebody else to decide it")
	}
	return nil
}

// Cancel takes a pass out of the chain by the person who asked for it. The chain closes
// first and the spine follows, so a pass is never cancelled in one place and approved in the
// other.
func (s *Service) Cancel(ctx context.Context, scope tenancy.Scope, requestID int64,
	reason string) (Request, error) {
	req, err := s.openRequest(ctx, scope.TenantID, requestID)
	if err != nil {
		return Request{}, err
	}
	p, err := s.passes.Get(ctx, scope.TenantID, req.PassID)
	if err != nil {
		return Request{}, err
	}
	if !scope.IsSuperAdmin && (p.RequesterUserID == nil || *p.RequesterUserID != scope.UserID) {
		return Request{}, errs.Forbidden("only the person who filed this pass can withdraw it")
	}
	if strings.TrimSpace(reason) == "" {
		return Request{}, errs.Invalid("withdrawing a pass needs a reason",
			errs.InvalidField("reason", "required"))
	}
	closed, err := s.close(ctx, scope, req, p, "approval.cancelled", reason)
	if err != nil {
		return Request{}, err
	}
	if _, err := s.passes.Decide(ctx, scope, req.PassID, passes.StatusCancelled, reason); err != nil {
		return Request{}, err
	}
	return closed, nil
}

// Reset is the edit-after-approval rule. A pass whose paper changed since it was signed -
// its value, its host, the goods on it - is not the pass that was signed. The live chain
// becomes history, the pass goes back to asking, and a fresh chain starts under whatever the
// flow says today.
func (s *Service) Reset(ctx context.Context, scope tenancy.Scope, passID int64,
	reason string) (Request, error) {
	if err := s.requireEnabled(ctx, scope.TenantID); err != nil {
		return Request{}, err
	}
	p, err := s.passes.Get(ctx, scope.TenantID, passID)
	if err != nil {
		return Request{}, err
	}
	if strings.TrimSpace(reason) == "" {
		return Request{}, errs.Invalid("restarting an approval needs a reason",
			errs.InvalidField("reason", "required"))
	}

	req, err := s.repo.OpenByPass(ctx, scope.TenantID, passID)
	switch {
	case err == nil:
		if _, err := s.close(ctx, scope, req, p, "approval.reset", reason); err != nil {
			return Request{}, err
		}
	case errs.CodeOf(err) != errs.CodeNotFound:
		return Request{}, err
	}
	if needsDecision(p.Status) {
		return s.restart(ctx, scope, passID)
	}
	// The pass has been signed already, so the only way back to asking is through Core.
	if _, err := s.passes.Decide(ctx, scope, passID, passes.StatusPending, reason); err != nil {
		return Request{}, err
	}
	return s.restart(ctx, scope, passID)
}

// restart files the pass again with whoever the flow names now. A tenant with no flow that
// matches ends up with the pass sitting pending for its desk, which is exactly what the same
// pass would have had before this module was switched on.
func (s *Service) restart(ctx context.Context, scope tenancy.Scope, passID int64) (Request, error) {
	if err := s.Start(ctx, scope.TenantID, passID); err != nil {
		return Request{}, err
	}
	req, err := s.repo.OpenByPass(ctx, scope.TenantID, passID)
	if errs.CodeOf(err) == errs.CodeNotFound {
		return Request{}, errs.Conflict("this pass needs no approval chain; the desk can decide it")
	}
	return req, err
}

// close is the one way a chain stops without an approver's answer: the asks stop, the chain
// becomes history, and the trail says who closed it and why.
func (s *Service) close(ctx context.Context, scope tenancy.Scope, req Request, p passes.Pass,
	action, reason string) (Request, error) {
	var closed Request
	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		if _, err := repo.CancelAsks(ctx, scope.TenantID, req.ID); err != nil {
			return err
		}
		ok, err := repo.FinishRequest(ctx, scope.TenantID, req.ID, StatusCancelled, scope.UserID, reason)
		if err != nil {
			return err
		}
		if !ok {
			return errs.Conflict("this approval was decided while you were closing it")
		}
		closed, err = repo.RequestByID(ctx, scope.TenantID, req.ID)
		if err != nil {
			return err
		}
		if err := s.publish(ctx, c, closed, p, "approval."+action, scope.UserID,
			map[string]any{"reason": reason}); err != nil {
			return err
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: action,
			SubjectID: req.ID, PassID: req.PassID, SiteID: p.SiteID, Reason: reason,
			Before: map[string]any{"status": req.Status},
			After:  map[string]any{"status": closed.Status},
		})
	})
	if err != nil {
		return Request{}, err
	}
	return closed, nil
}

// Escalation is what one sweep moved. It is a count, not a page of rows: the caller is a
// cron job, and a person who wants the detail has the queue.
type Escalation struct {
	Escalated int       `json:"escalated"`
	At        time.Time `json:"at"`
}

// Escalate is what an absent approver costs the tenant: every ask whose clock has run out is
// taken off the person who did not answer and put in front of whoever the flow names above
// them. A step with no escalation target is left where it is, because a pass nobody answered
// is still a pass somebody is meant to answer, not a pass to lose.
func (s *Service) Escalate(ctx context.Context, scope tenancy.Scope) (Escalation, error) {
	if err := s.requireEnabled(ctx, scope.TenantID); err != nil {
		return Escalation{}, err
	}
	at := time.Now().UTC()
	overdue, err := s.repo.Overdue(ctx, scope.TenantID, at, inboxBatch)
	if err != nil {
		return Escalation{}, err
	}

	out := Escalation{At: at}
	for _, ask := range overdue {
		moved, err := s.escalateOne(ctx, scope, ask, at)
		if err != nil {
			return out, err
		}
		if moved {
			out.Escalated++
		}
	}
	return out, nil
}

// escalateOne is one ask taken up the chain, in its own transaction so a sweep that fails
// halfway leaves everything it had already moved moved for good.
func (s *Service) escalateOne(ctx context.Context, scope tenancy.Scope, ask RequestStep,
	at time.Time) (bool, error) {
	req, err := s.repo.RequestByID(ctx, scope.TenantID, ask.ApprovalRequestID)
	if err != nil {
		return false, err
	}
	if !req.Open() {
		return false, nil
	}
	def, err := stepDefinition(req, ask.StepNo)
	if err != nil {
		return false, nil
	}
	p, err := s.passes.Get(ctx, scope.TenantID, req.PassID)
	if err != nil {
		return false, err
	}
	// A chain can outlive the decision it existed to make - the desk settled the pass while
	// this module was switched off, for instance. Chasing an answer nobody is waiting for
	// would only move a stale ask into somebody else's inbox.
	if !needsDecision(p.Status) {
		return false, nil
	}
	target, ok := s.escalationTarget(ctx, def, p)
	if !ok {
		return false, nil
	}

	moved := false
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		recalled, err := repo.Recall(ctx, scope.TenantID, ask.ID)
		if err != nil {
			return err
		}
		if !recalled {
			// The ask was answered in the same minute the sweep read it.
			return nil
		}
		if err := repo.InsertStep(ctx, NewStepRow{
			TenantID: scope.TenantID, RequestID: req.ID, StepNo: ask.StepNo, Name: def.Name,
			AssigneeKey: target.key, AssigneeUserID: target.userID, ApproverRoleKey: target.roleKey,
			AssigneeName: target.name, Mode: def.Mode, DueAt: def.Deadline(at),
			EscalatedFrom: ask.StepNo,
		}); err != nil {
			return err
		}
		if _, err := repo.MoveRequest(ctx, scope.TenantID, req.ID, StatusEscalated, ask.StepNo,
			def.Deadline(at)); err != nil {
			return err
		}
		if err := repo.CountEscalation(ctx, scope.TenantID, req.ID); err != nil {
			return err
		}
		if err := s.publish(ctx, c, req, p, "approval.escalated", scope.UserID, map[string]any{
			"step_no": ask.StepNo, "from": nameOf(ask), "to": target.name,
			"to_user_id": target.userID, "notify_user_ids": personIDs(target.people),
		}); err != nil {
			return err
		}
		if err := s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "approval.escalated",
			SubjectID: req.ID, PassID: req.PassID, SiteID: p.SiteID,
			Before: map[string]any{"asked_of": nameOf(ask), "due_at": ask.DueAt},
			After:  map[string]any{"asked_of": target.name, "step_no": ask.StepNo},
		}); err != nil {
			return err
		}
		moved = true
		return nil
	})
	return moved, err
}

// escalationTarget is the person or the role the step names above itself. No target, or a
// target the directory cannot find, means the ask stays where it is: an escalation to nobody
// is how a pass quietly becomes nobody's problem.
func (s *Service) escalationTarget(ctx context.Context, def FlowStep, p passes.Pass) (assignee, bool) {
	if user := derefID(def.EscalateToUserID); user != 0 {
		who, err := s.userAsk(ctx, p.TenantID, user, "escalation target")
		return who, err == nil
	}
	if role := str(def.EscalateToRoleKey); role != "" {
		who, err := s.roleAsk(ctx, p, role)
		return who, err == nil
	}
	return assignee{}, false
}

// requireEnabled is the module switch, asked the same way by every entry point so a tenant
// that turned this off is never told their pass is held by a queue they cannot see.
func (s *Service) requireEnabled(ctx context.Context, tenantID int64) error {
	on, err := s.enabled(ctx, tenantID)
	if err != nil {
		return err
	}
	if !on {
		return errs.Conflict(ModuleKey + " is switched off for this tenant")
	}
	return nil
}

func nameOf(step RequestStep) string {
	if n := str(step.AssigneeNameSnapshot); n != "" {
		return n
	}
	if r := str(step.ApproverRoleKey); r != "" {
		return "role " + r
	}
	return "nobody"
}

func answerOf(in Decision) string {
	if strings.TrimSpace(in.Comment) != "" {
		return in.Comment
	}
	return string(in.Action)
}

func ptr[T any](value T) *T { return &value }
