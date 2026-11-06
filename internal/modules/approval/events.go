package approval

import (
	"context"

	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/events"
	"accessnow/internal/platform/tenancy"
)

// OnPassIssued is the reaction that starts a chain.
//
// internal/server registers it against "pass.issued", which Core publishes inside the
// issuer's own transaction. So visitor, goods, vehicle and staff each get the tenant's
// workflow without any of them naming this module, and a flow a tenant writes this afternoon
// applies to the pass filed tomorrow morning without a deploy.
func (s *Service) OnPassIssued(ctx context.Context, e events.Event) error {
	if e.PassID == 0 {
		return nil
	}
	return s.Start(ctx, e.TenantID, e.PassID)
}

// settledKeys are the pass decisions that mean a chain has nothing left to decide. Core
// publishes one of them for every move it makes outside the asking states, and each is listed
// rather than caught with a prefix so a new pass status cannot quietly reach an approver's
// inbox unannounced.
var settledKeys = []string{
	"pass.approved", "pass.rejected", "pass.cancelled", "pass.completed", "pass.void",
}

// SettledKeys is the list internal/server subscribes to.
func SettledKeys() []string { return settledKeys }

// OnPassSettled is the reaction that stops a chain the desk has already made pointless.
//
// A pass can be settled away from its chain - withdrawn at the desk, refused, closed, or
// expired by a module that does not know this one exists - and the asks it leaves behind are
// a lie in somebody's inbox: an approver who arrives to answer them is told, at best, that the
// pass is history. This recalls them and tells the people still being asked, which is the same
// ending a withdrawal gets, because it is the same fact: the chain stopped without an answer.
func (s *Service) OnPassSettled(ctx context.Context, e events.Event) error {
	if e.PassID == 0 {
		return nil
	}
	p, err := s.passes.Get(ctx, e.TenantID, e.PassID)
	if err != nil {
		if errs.CodeOf(err) == errs.CodeNotFound {
			return nil
		}
		return err
	}
	if p.AwaitingDecision() {
		return nil
	}
	req, err := s.repo.OpenByPass(ctx, e.TenantID, e.PassID)
	if errs.CodeOf(err) == errs.CodeNotFound {
		// Either no chain was ever filed, or the chain decided this pass itself, which is the
		// ordinary route: the chain closes first and the pass follows.
		return nil
	}
	if err != nil {
		return err
	}
	scope := tenancy.Scope{TenantID: e.TenantID, UserID: e.ActorUserID}
	if _, err := s.close(ctx, scope, req, p, "cancelled",
		"the pass is "+p.Status+", so this chain has nothing left to decide"); err != nil {
		if errs.CodeOf(err) == errs.CodeConflict {
			// An approver or the desk closed it in the same moment the relay reached it.
			return nil
		}
		return err
	}
	return nil
}
