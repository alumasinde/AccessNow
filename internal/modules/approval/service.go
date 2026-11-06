package approval

import (
	"context"
	"strconv"

	"github.com/jmoiron/sqlx"

	"accessnow/internal/core/access"
	"accessnow/internal/core/passes"
	"accessnow/internal/platform/audit"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/events"
)

// inboxBatch caps one escalation sweep per tenant. A tenant with a thousand overdue asks
// escalates fifty of them per run and catches up; it does not spend a minute holding rows
// while a desk waits.
const inboxBatch = 50

// Service runs the chains. It holds Core's pass service rather than Core's table: a pass is
// not this module's row to write, and the one place a pass becomes scannable is the decision
// Core itself records.
type Service struct {
	pool   *sqlx.DB
	repo   *Repository
	passes *passes.Service
	chain  audit.Writer
	events events.Publisher
	access *access.Service
}

func NewService(pool *sqlx.DB, repo *Repository, passSvc *passes.Service, chain audit.Writer,
	bus events.Publisher, accessSvc *access.Service) *Service {
	return &Service{pool: pool, repo: repo, passes: passSvc, chain: chain, events: bus,
		access: accessSvc}
}

// enabled is the switch the rest of the system obeys. A tenant that turned this module off
// gets no new chains and is not held by the old ones: their desk decides passes the way it
// did before, and the surface they no longer have simply is not there.
func (s *Service) enabled(ctx context.Context, tenantID int64) (bool, error) {
	if s.access == nil {
		return true, nil
	}
	return s.access.Enabled(ctx, tenantID, ModuleKey)
}

// openRequest is the chain a caller wants to act on, checked to still be waiting.
func (s *Service) openRequest(ctx context.Context, tenantID, requestID int64) (Request, error) {
	req, err := s.repo.RequestByID(ctx, tenantID, requestID)
	if err != nil {
		return Request{}, err
	}
	if !req.Open() {
		return Request{}, errs.Conflict("this approval is already " + req.Status)
	}
	return req, nil
}

// stepDefinition is the frozen settings of one numbered step. Reading the snapshot rather
// than the live flow is the whole point of keeping it: the person who approved a pass last
// week approved it under the rule that was true last week, and a tenant who has since
// deleted that step must not be able to change what happened.
func stepDefinition(req Request, stepNo int) (FlowStep, error) {
	steps, err := req.Definition()
	if err != nil {
		return FlowStep{}, err
	}
	step, ok := stepAt(steps, stepNo)
	if !ok {
		return FlowStep{}, errs.Conflict("this approval chain has no step " + itoa(stepNo) +
			"; the flow it started under was edited mid-flight")
	}
	return step, nil
}

// publish is the notification seam. Every state change here writes one row into the outbox
// in the same transaction as the change, so an approver who was asked is announced even if
// the process dies before the mail goes out - and the module that sends the mail is free to
// be switched on later without any of these events being replayed by hand.
func (s *Service) publish(ctx context.Context, c db.Conn, req Request, p passes.Pass,
	key string, actor int64, payload map[string]any) error {
	if payload == nil {
		payload = map[string]any{}
	}
	payload["pass_no"] = p.PassNo
	payload["pass_id"] = p.ID
	payload["chain_status"] = req.Status
	return s.events.Publish(ctx, c, events.Event{
		TenantID: req.TenantID, EventKey: key,
		AggregateType: aggregateType, AggregateID: req.ID, PassID: req.PassID, SiteID: p.SiteID,
		ActorUserID: actor, Payload: payload,
	})
}

// assignee is one entity being asked. A role assignee has no user id, which is what lets the
// ask be answered by whoever holds that role when the pass actually arrives; people carries
// the humans it reaches today, which is who the notification is for.
type assignee struct {
	key      string
	userID   int64
	roleKey  string
	name     string
	people   []Person
	fromUser int64
	delegID  int64
}

func (a assignee) isRole() bool { return a.userID == 0 && a.roleKey != "" }

func itoa(n int) string { return strconv.Itoa(n) }
