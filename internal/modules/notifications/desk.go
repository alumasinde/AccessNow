package notifications

import (
	"context"

	"accessnow/internal/platform/audit"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/tenancy"
)

// This file is the desk surface: the inbox a person reads and the configuration an
// operations manager writes. Neither of them is a business rule - a pass never waits on a
// notice, and no gate reads what is in here.

// Inbox is one person's own notices. Reading is scoped by session user id in
// Repository.Inbox, which is the only reason this handler can be a plain list.
func (s *Service) Inbox(ctx context.Context, f InboxFilter) ([]Message, error) {
	return s.repo.Inbox(ctx, f)
}

func (s *Service) Unread(ctx context.Context, tenantID, userID int64) (int, error) {
	return s.repo.UnreadCount(ctx, tenantID, userID)
}

// MarkRead answers 404 for a notice that is not this person's, rather than 200 for an update
// that changed nothing. The difference is what a client can act on.
func (s *Service) MarkRead(ctx context.Context, scope tenancy.Scope, id int64) error {
	changed, err := s.repo.MarkRead(ctx, scope.TenantID, scope.UserID, id)
	if err != nil {
		return err
	}
	if !changed {
		return errs.NotFound("notice")
	}
	return nil
}

func (s *Service) MarkAllRead(ctx context.Context, scope tenancy.Scope) (int, error) {
	return s.repo.MarkAllRead(ctx, scope.TenantID, scope.UserID)
}

// Events is the catalogue the routes and templates screens are written against. It is global
// by design: an event key is a fact about the code, and a tenant tunes it rather than
// renaming it.
func (s *Service) Events(ctx context.Context) ([]Event, error) {
	return s.repo.ActiveEvents(ctx)
}

func (s *Service) Routes(ctx context.Context, tenantID int64, eventKey string) ([]Route, error) {
	return s.repo.AllRoutes(ctx, tenantID, eventKey)
}

// SaveRoute files one tenant's decision about who hears about what. The catalogue row is
// checked first because a route for a key nothing publishes is dead configuration, and the
// person who wrote it deserves to be told now rather than in six months when the notice they
// expected did not come.
func (s *Service) SaveRoute(ctx context.Context, scope tenancy.Scope, in Route) (Route, error) {
	if err := s.knownEvent(ctx, in.EventKey); err != nil {
		return Route{}, err
	}
	var saved Route
	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		row, err := repo.SaveRoute(ctx, in)
		if err != nil {
			return err
		}
		saved = row
		return s.record(ctx, c, scope, "notification.route.saved", "notification_route", row.ID,
			"route saved", map[string]any{"channel": row.Channel, "recipient_type": row.RecipientType,
				"event_key": row.EventKey})
	})
	if err != nil {
		return Route{}, err
	}
	return saved, nil
}

func (s *Service) DeleteRoute(ctx context.Context, scope tenancy.Scope, id int64) error {
	return db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		changed, err := repo.DeleteRoute(ctx, scope.TenantID, id)
		if err != nil {
			return err
		}
		if !changed {
			return errs.NotFound("notification route")
		}
		return s.record(ctx, c, scope, "notification.route.deleted", "notification_route", id,
			"route deleted", nil)
	})
}

// Templates is the wording screen: the tenant's own rows and the system defaults underneath.
func (s *Service) Templates(ctx context.Context, tenantID int64, eventKey string) ([]Template, error) {
	return s.repo.Templates(ctx, tenantID, eventKey)
}

func (s *Service) SaveTemplate(ctx context.Context, scope tenancy.Scope, in Template) (Template, error) {
	if err := s.knownEvent(ctx, in.EventKey); err != nil {
		return Template{}, err
	}
	var saved Template
	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		row, err := repo.SaveTemplate(ctx, in)
		if err != nil {
			return err
		}
		saved = row
		return s.record(ctx, c, scope, "notification.template.saved", "notification_template", row.ID,
			"wording saved", map[string]any{"channel": row.Channel, "locale": row.Locale,
				"version": row.Version})
	})
	if err != nil {
		return Template{}, err
	}
	return saved, nil
}

// DeleteTemplate removes the tenant's own override, which puts the system wording back. A
// tenant cannot delete a system default, and the repository's tenant_id predicate is what
// makes that true rather than a check in here.
func (s *Service) DeleteTemplate(ctx context.Context, scope tenancy.Scope, id int64) error {
	return db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		changed, err := repo.DeleteTemplate(ctx, scope.TenantID, id)
		if err != nil {
			return err
		}
		if !changed {
			return errs.NotFound("notification template")
		}
		return s.record(ctx, c, scope, "notification.template.deleted", "notification_template", id,
			"wording deleted", nil)
	})
}

// Outbox is the queue as an operations list: the newest rows, optionally in one status.
func (s *Service) Outbox(ctx context.Context, f OutboxFilter) ([]Message, error) {
	return s.repo.Outbox(ctx, f)
}

// Ledger is the same queue counted, which is the question asked before the rows are.
func (s *Service) Ledger(ctx context.Context, tenantID int64) ([]LedgerCount, error) {
	return s.repo.Ledger(ctx, tenantID)
}

// knownEvent refuses a configuration row for a key the catalogue does not know. NotFound is
// turned into an invalid field here because the person being told has not asked for a record
// that was deleted - they typed a key into a form.
func (s *Service) knownEvent(ctx context.Context, eventKey string) error {
	_, err := s.repo.Catalogue(ctx, eventKey)
	if errs.CodeOf(err) == errs.CodeNotFound {
		return errs.Invalid("event_key is not a published notification",
			errs.InvalidField("event_key", "nothing is sent under that key"))
	}
	return err
}

// record is the audit trail of a configuration change. A tenant's notification settings decide
// who was told about an incident, so "who turned the supervisor's copy off, and when" is a
// question with an answer in this table.
func (s *Service) record(ctx context.Context, c db.Conn, scope tenancy.Scope, action,
	subjectType string, subjectID int64, reason string, after map[string]any) error {
	return s.chain.Write(ctx, c, audit.Entry{
		TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: action,
		SubjectType: subjectType, SubjectID: subjectID, Reason: reason, After: after,
	})
}
