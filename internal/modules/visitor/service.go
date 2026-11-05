package visitor

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"

	"accessnow/internal/core/auth"
	"accessnow/internal/core/movements"
	"accessnow/internal/core/passes"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/events"
	"accessnow/internal/platform/tenancy"
)

const (
	ModuleKey    = "visitor_management"
	numberFormat = "visitor"
)

// Service is the module's whole business call. It owns the visit row, asks Core for the
// pass and the movement, and never reaches into another module.
type Service struct {
	pool      *sqlx.DB
	repo      *Repository
	users     *auth.Repository
	passes    *passes.Service
	passRepo  *passes.Repository
	scans     *movements.Service
	publisher events.Publisher
}

func NewService(
	pool *sqlx.DB,
	repo *Repository,
	users *auth.Repository,
	passService *passes.Service,
	passRepo *passes.Repository,
	scans *movements.Service,
	publisher events.Publisher,
) *Service {
	return &Service{pool: pool, repo: repo, users: users, passes: passService,
		passRepo: passRepo, scans: scans, publisher: publisher}
}

// Create is a walk-in or a booked visit: one visitor row, one Core pass, one visit.
type Create struct {
	Visitor        NewVisitor
	Purpose        string
	PeopleCount    int
	EscortRequired bool
	BadgeNo        string
	DepartmentID   int64
	ExpectedFrom   *time.Time
	ExpectedUntil  *time.Time
	Origin         string
}

func (s *Service) Create(ctx context.Context, scope tenancy.Scope, in Create) (Visit, error) {
	if scope.SiteID == 0 {
		return Visit{}, errs.Forbidden("no site on this session")
	}
	origin := in.Origin
	if origin == "" {
		origin = "walk_in"
	}
	passType, err := s.passRepo.TypeByKey(ctx, typeKeyFor(origin))
	if err != nil {
		return Visit{}, err
	}

	status, passStatus := StatusApproved, passes.StatusApproved
	if passType.RequiresApproval {
		status, passStatus = StatusPending, passes.StatusPending
	}
	now := time.Now().UTC()
	until := passes.Window(now, passType.DefaultValidityHours)
	host, err := s.host(ctx, in.Visitor.HostUserID)
	if err != nil {
		return Visit{}, err
	}

	var visit Visit
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		person, err := repo.FindOrCreateVisitor(ctx, scope.TenantID, in.Visitor, scope.UserID)
		if err != nil {
			return err
		}

		pass, err := s.passes.Issue(ctx, c, passes.Issue{
			New: passes.New{
				TenantID: scope.TenantID, SiteID: scope.SiteID, PassTypeID: passType.ID,
				CategoryKey: passType.CategoryKey, SourceModuleKey: ModuleKey,
				Status: passStatus, Origin: origin, SubjectName: person.Name(),
				SubjectPhone: person.Phone, SubjectEmail: person.Email, Purpose: text(in.Purpose),
				RequesterUserID: num(scope.UserID), HostUserID: host.ID,
				ValidFrom: now, ValidUntil: until,
			},
			FormatKey: numberFormat, Timezone: scope.Timezone, ActorUserID: scope.UserID,
		})
		if err != nil {
			return err
		}

		visitID, err := repo.CreateVisit(ctx, NewVisit{
			TenantID: scope.TenantID, PassID: pass.ID, VisitorID: person.ID,
			CompanyID: person.CompanyID, HostUserID: host.ID, HostName: host.Name,
			DepartmentID: num(in.DepartmentID), Purpose: in.Purpose,
			PeopleCount: in.PeopleCount, EscortRequire: in.EscortRequired, BadgeNo: in.BadgeNo,
			IsWalkIn: origin == "walk_in", Status: status,
			ExpectedFrom: in.ExpectedFrom, ExpectedUntil: in.ExpectedUntil,
		})
		if err != nil {
			return err
		}

		payload := map[string]any{
			"visitor": person.Name(), "purpose": in.Purpose, "pass_no": pass.PassNo,
			"status": string(status), "host_user_id": in.Visitor.HostUserID,
		}
		if err := s.publisher.Publish(ctx, c, event(scope, "visit.created", visitID, pass.ID, payload)); err != nil {
			return err
		}
		if status == StatusPending {
			if err := s.publisher.Publish(ctx, c, event(scope, "approval.requested", visitID, pass.ID, payload)); err != nil {
				return err
			}
		}
		visit, err = repo.VisitByID(ctx, scope.TenantID, visitID)
		return err
	})
	if err != nil {
		return Visit{}, err
	}
	return visit, nil
}

func typeKeyFor(origin string) string {
	if origin == "walk_in" {
		return "visitor_walk_in"
	}
	return "visitor_pre_registered"
}

func event(scope tenancy.Scope, key string, visitID, passID int64, payload map[string]any) events.Event {
	return events.Event{
		TenantID: scope.TenantID, EventKey: key, AggregateType: "visit", AggregateID: visitID,
		PassID: passID, SiteID: scope.SiteID, GateID: scope.GateID,
		ActorUserID: scope.UserID, Payload: payload,
	}
}

func text(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func num(value int64) *int64 {
	if value == 0 {
		return nil
	}
	return &value
}
