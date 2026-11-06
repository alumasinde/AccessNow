package approval

import (
	"context"
	"time"

	"accessnow/internal/core/passes"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/tenancy"
)

// Inbox is what is waiting on the caller: the asks addressed to them by name, and the asks
// addressed to a role they hold. An empty list is the answer a start-of-shift screen wants,
// not an error.
func (s *Service) Inbox(ctx context.Context, tenantID, userID int64, limit int) ([]InboxRow, error) {
	if err := s.requireEnabled(ctx, tenantID); err != nil {
		return nil, err
	}
	if limit <= 0 {
		return nil, errs.Invalid("limit must be a positive number")
	}
	roles, err := s.repo.RoleKeysOf(ctx, tenantID, userID)
	if err != nil {
		return nil, err
	}
	return s.repo.Inbox(ctx, tenantID, userID, roles, limit)
}

// Chain is one pass's whole approval story: the chain, the definitions it was frozen under,
// every ask with its answer, and the pass it is about. It is read as one object because an
// approver who cannot see the step above theirs has no idea what they are agreeing to.
type Chain struct {
	Request Request       `json:"request"`
	Steps   []FlowStep    `json:"steps,omitempty"`
	Asks    []RequestStep `json:"asks"`
	Pass    passes.Pass   `json:"pass"`
}

// WaitingOn is who owes the chain an answer right now, which is the one question a queue
// screen and a reminder both ask.
func (c Chain) WaitingOn() []RequestStep {
	var out []RequestStep
	for _, ask := range c.Asks {
		if ask.Waiting() {
			out = append(out, ask)
		}
	}
	return out
}

// ChainByPass is the chain a pass is working through. A pass with no live chain is a
// NotFound, which is the truth an approver linking out from a pass screen needs.
func (s *Service) ChainByPass(ctx context.Context, scope tenancy.Scope, passID int64) (Chain, error) {
	if err := s.requireEnabled(ctx, scope.TenantID); err != nil {
		return Chain{}, err
	}
	req, err := s.repo.OpenByPass(ctx, scope.TenantID, passID)
	if err != nil {
		return Chain{}, err
	}
	return s.chainOf(ctx, req)
}

// ChainByID is the same story read from the approval side, and it is the one the audit trail
// links to: a closed chain stays here, because who decided what is the thing a tenant is
// asked about months later.
func (s *Service) ChainByID(ctx context.Context, scope tenancy.Scope, requestID int64) (Chain, error) {
	if err := s.requireEnabled(ctx, scope.TenantID); err != nil {
		return Chain{}, err
	}
	req, err := s.repo.RequestByID(ctx, scope.TenantID, requestID)
	if err != nil {
		return Chain{}, err
	}
	return s.chainOf(ctx, req)
}

func (s *Service) chainOf(ctx context.Context, req Request) (Chain, error) {
	asks, err := s.repo.StepsForRequest(ctx, req.TenantID, req.ID)
	if err != nil {
		return Chain{}, err
	}
	p, err := s.passes.Get(ctx, req.TenantID, req.PassID)
	if err != nil {
		return Chain{}, err
	}
	steps, err := req.Definition()
	if err != nil {
		return Chain{}, err
	}
	return Chain{Request: req, Steps: steps, Asks: asks, Pass: p}, nil
}

// ListRequests is the register of chains: the page a supervisor opens instead of the inbox,
// when they want what is late rather than what is theirs.
func (s *Service) ListRequests(ctx context.Context, scope tenancy.Scope, f ListFilter) ([]Request, error) {
	if err := s.requireEnabled(ctx, scope.TenantID); err != nil {
		return nil, err
	}
	if f.Limit <= 0 {
		return nil, errs.Invalid("limit must be a positive number")
	}
	f.TenantID = scope.TenantID
	return s.repo.List(ctx, f)
}

// Detail is what one chain looks like to the person reading it, including how long the
// current step has left and which of the open asks this caller may answer. The handler hands
// this to the DTO layer rather than a bare Request so a response can name the pass without a
// second round trip.
type Detail struct {
	Chain Chain
	// HeldRoles is what the caller holds right now. The ask screen uses it to say "this one
	// is yours"; Answer re-checks everything, so it is a hint and never a permission.
	HeldRoles []string
	Overdue   bool
	Waiting   []RequestStep
	Deadline  *time.Time
}

// DetailByPass is the live chain of a pass, from the pass screen.
func (s *Service) DetailByPass(ctx context.Context, scope tenancy.Scope, passID int64) (Detail, error) {
	chain, err := s.ChainByPass(ctx, scope, passID)
	if err != nil {
		return Detail{}, err
	}
	return s.describe(ctx, scope, chain)
}

// DetailByID is the same story read from the approval side, which is how a closed chain -
// the one the audit trail links to - gets opened months later.
func (s *Service) DetailByID(ctx context.Context, scope tenancy.Scope, requestID int64) (Detail, error) {
	chain, err := s.ChainByID(ctx, scope, requestID)
	if err != nil {
		return Detail{}, err
	}
	return s.describe(ctx, scope, chain)
}

// StartChain files a chain for a pass that is still asking. It exists for the day a tenant
// switches this module on: the passes already in the register were issued under no flow, and
// without this they would sit pending forever with a routing table ready to work them.
func (s *Service) StartChain(ctx context.Context, scope tenancy.Scope, passID int64) (Detail, error) {
	if err := s.Start(ctx, scope.TenantID, passID); err != nil {
		return Detail{}, err
	}
	detail, err := s.DetailByPass(ctx, scope, passID)
	if errs.CodeOf(err) == errs.CodeNotFound {
		return Detail{}, errs.Conflict("no active flow matches this pass; the desk can decide it")
	}
	return detail, err
}

func (s *Service) describe(ctx context.Context, scope tenancy.Scope, chain Chain) (Detail, error) {
	roles, err := s.repo.RoleKeysOf(ctx, scope.TenantID, scope.UserID)
	if err != nil {
		return Detail{}, err
	}
	at := time.Now().UTC()
	out := Detail{Chain: chain, HeldRoles: roles, Waiting: chain.WaitingOn()}
	for _, ask := range out.Waiting {
		if ask.DueAt == nil {
			continue
		}
		if ask.DueAt.Before(at) {
			out.Overdue = true
		}
		if out.Deadline == nil || ask.DueAt.Before(*out.Deadline) {
			due := *ask.DueAt
			out.Deadline = &due
		}
	}
	return out, nil
}
