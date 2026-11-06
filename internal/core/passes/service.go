package passes

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"

	"accessnow/internal/platform/audit"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/events"
	"accessnow/internal/platform/httpx"
	"accessnow/internal/platform/numbering"
	"accessnow/internal/platform/tenancy"
)

type Service struct {
	pool     *sqlx.DB
	repo     *Repository
	numbers  *numbering.Allocator
	chain    audit.Writer
	events   events.Publisher
	workflow Workflow
}

// Workflow is the seam an approval chain plugs into. Core asks whether a pass is already
// being decided by a queue of approvers before it lets one click approve it; internal/server
// hands over the implementation, which is how core stays ignorant of the module.
type Workflow interface {
	Holds(ctx context.Context, tenantID, passID int64) (bool, error)
}

func NewService(pool *sqlx.DB, repo *Repository, numbers *numbering.Allocator, chain audit.Writer,
	bus events.Publisher, workflow Workflow) *Service {
	return &Service{pool: pool, repo: repo, numbers: numbers, chain: chain, events: bus, workflow: workflow}
}

type Issue struct {
	New
	FormatKey   string
	Timezone    string
	ActorUserID int64
}

// Issue allocates the tenant's pass number and inserts the spine row on the caller's
// connection, so a module can write its own detail row in the same transaction.
func (s *Service) Issue(ctx context.Context, c db.Conn, in Issue) (Pass, error) {
	passNo, err := s.numbers.Next(ctx, c, numbering.Request{
		TenantID: in.TenantID, FormatKey: in.FormatKey, Category: in.CategoryKey,
		Module: in.SourceModuleKey, Timezone: in.Timezone, At: in.ValidFrom,
	})
	if err != nil {
		return Pass{}, err
	}
	in.PassNo = passNo

	repo := s.repo.In(c)
	id, err := repo.Create(ctx, in.New, in.ActorUserID)
	if err != nil {
		return Pass{}, err
	}
	if err := s.chain.Write(ctx, c, audit.Entry{
		TenantID: in.TenantID, ActorUserID: in.ActorUserID, Action: "pass.issued",
		SubjectType: "pass", SubjectID: id, PassID: id, SiteID: in.SiteID,
		After: map[string]any{"pass_no": passNo, "status": string(in.Status), "type": in.CategoryKey},
	}); err != nil {
		return Pass{}, err
	}
	// The chain starts here rather than in a module: the pass is the thing being approved,
	// and every module that issues one - visitor, goods, vehicle, staff - then gets the
	// tenant's workflow for free without any of them knowing the approval module exists.
	if err := s.events.Publish(ctx, c, events.Event{
		TenantID: in.TenantID, EventKey: "pass.issued",
		AggregateType: "pass", AggregateID: id, PassID: id, SiteID: in.SiteID,
		ActorUserID: in.ActorUserID,
		Payload: map[string]any{
			"pass_no": passNo, "pass_type_id": in.PassTypeID, "category_key": in.CategoryKey,
			"module_key": in.SourceModuleKey, "value_amount": in.ValueAmount,
			"requester_user_id": in.RequesterUserID,
		},
	}); err != nil {
		return Pass{}, err
	}
	return repo.ByID(ctx, in.TenantID, id)
}

func (s *Service) Get(ctx context.Context, tenantID, passID int64) (Pass, error) {
	return s.repo.ByID(ctx, tenantID, passID)
}

// List returns one page of the register plus the row that decides has_more. The caller
// asks for limit+1 so the extra row is a signal, never a result.
func (s *Service) List(ctx context.Context, tenantID int64, filter Filter, limit int, before *httpx.Cursor) ([]Pass, error) {
	if limit <= 0 {
		return nil, errs.Invalid("limit must be a positive number")
	}
	return s.repo.List(ctx, ListFilter{TenantID: tenantID, Filter: filter, Limit: limit, Before: before})
}

// Decide moves a pass between states, publishes the event other modules may react to,
// and refuses a second writer that got there first.
func (s *Service) Decide(ctx context.Context, scope tenancy.Scope, passID int64, to Status, note string) (Pass, error) {
	if !Valid(to) {
		return Pass{}, errs.Invalid("status " + string(to) + " is not recognised")
	}
	current, err := s.repo.ByID(ctx, scope.TenantID, passID)
	if err != nil {
		return Pass{}, err
	}
	if !current.Decidable() {
		return Pass{}, errs.Conflict("pass " + current.PassNo + " is " + current.Status)
	}
	// Approving is a decision about a request, not a repair of history: a cancelled pass must
	// not turn green at a barrier because an ask sat unanswered in somebody's inbox.
	if to == StatusApproved && !current.AwaitingDecision() {
		return Pass{}, errs.Conflict("pass " + current.PassNo + " is " + current.Status +
			"; only a pass still waiting to be decided can be approved")
	}
	// Only an approval is guarded: refusing, cancelling and closing stay open to the desk,
	// or a chain that nobody answers would strand the pass in both directions.
	if to == StatusApproved && s.workflow != nil {
		held, err := s.workflow.Holds(ctx, scope.TenantID, passID)
		if err != nil {
			return Pass{}, err
		}
		if held {
			return Pass{}, errs.Conflict("pass " + current.PassNo +
				" is in an approval chain; decide it in the approval queue")
		}
	}

	from := Status(current.Status)
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		ok, err := repo.MoveStatus(ctx, Decision{
			TenantID: scope.TenantID, PassID: passID, From: from, To: to,
			ByUserID: scope.UserID, Note: note, Issued: to == StatusApproved,
		})
		if err != nil {
			return err
		}
		if !ok {
			return errs.Conflict("pass " + current.PassNo + " changed while this request was in flight")
		}

		if err := s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "pass.decided",
			SubjectType: "pass", SubjectID: passID, PassID: passID, SiteID: current.SiteID,
			Reason: note,
			Before: map[string]any{"status": string(from)},
			After:  map[string]any{"status": string(to)},
		}); err != nil {
			return err
		}
		return s.events.Publish(ctx, c, events.Event{
			TenantID: scope.TenantID, EventKey: "pass." + string(to),
			AggregateType: "pass", AggregateID: passID, PassID: passID, SiteID: current.SiteID,
			ActorUserID: scope.UserID,
			Payload:     map[string]any{"pass_no": current.PassNo, "from": string(from), "note": note},
		})
	})
	if err != nil {
		return Pass{}, err
	}
	return s.repo.ByID(ctx, scope.TenantID, passID)
}

// Window converts a pass type's default validity in hours into a close time, or nil for
// unlimited within the issued window.
func Window(open time.Time, hours *int) *time.Time {
	if hours == nil || *hours <= 0 {
		return nil
	}
	until := open.UTC().Add(time.Duration(*hours) * time.Hour)
	return &until
}
