package rollcall

import (
	"context"

	"github.com/jmoiron/sqlx"

	"accessnow/internal/platform/audit"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/events"
	"accessnow/internal/platform/logging"
	"accessnow/internal/platform/tenancy"
)

// Service runs one roll-call at a time per site: raise it, freeze the roster, take marks against
// it, and close it with an answer for whoever was still on the board.
type Service struct {
	pool   *sqlx.DB
	repo   *Repository
	chain  audit.Writer
	events events.Publisher
}

func NewService(pool *sqlx.DB, repo *Repository, chain audit.Writer,
	bus events.Publisher) *Service {
	return &Service{pool: pool, repo: repo, chain: chain, events: bus}
}

// StartRequest is an alarm as the supervisor raises it.
type StartRequest struct {
	Kind        string
	MusterPoint string
	Notes       string
}

// Start raises an incident and freezes the roster in the same transaction. The freeze is what
// makes the board trustworthy: it is a snapshot taken at the moment of the alarm, not a live
// query that forgets the person who scanned out an hour later.
func (s *Service) Start(ctx context.Context, scope tenancy.Scope, req StartRequest) (Incident, error) {
	if scope.SiteID == 0 {
		return Incident{}, errs.Forbidden("no site on this session")
	}
	kind := Kind(req.Kind)
	if !kind.valid() {
		return Incident{}, errs.BadField("kind", "pick an incident kind")
	}
	if len(req.MusterPoint) > 255 {
		return Incident{}, errs.BadField("muster_point", "too long")
	}
	if len(req.Notes) > 2000 {
		return Incident{}, errs.BadField("notes", "too long")
	}
	if _, err := s.repo.OpenAt(ctx, scope.TenantID, scope.SiteID); err == nil {
		return Incident{}, errs.Conflict("a roll-call is already open at this site")
	} else if errs.CodeOf(err) != errs.CodeNotFound {
		return Incident{}, err
	}

	var id int64
	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		created, err := s.repo.Create(ctx, c, NewIncident{
			TenantID: scope.TenantID, SiteID: scope.SiteID, GateID: scope.GateID,
			StartedBy: scope.UserID, Kind: string(kind), MusterPoint: req.MusterPoint,
			Notes: req.Notes,
		})
		if err != nil {
			return err
		}
		id = created
		frozen, err := s.repo.Freeze(ctx, c, scope.TenantID, scope.SiteID, id)
		if err != nil {
			return err
		}
		counts, err := s.repo.Recount(ctx, c, scope.TenantID, id)
		if err != nil {
			return err
		}
		if err := s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "rollcall.started",
			SubjectType: aggregateType, SubjectID: id, SiteID: scope.SiteID,
			After: map[string]any{"kind": string(kind), "roster": frozen,
				"expected": counts.Expected, "muster_point": req.MusterPoint},
		}); err != nil {
			return err
		}
		return s.events.Publish(ctx, c, events.Event{
			TenantID: scope.TenantID, EventKey: "rollcall.incident_started",
			AggregateType: aggregateType, AggregateID: id, SiteID: scope.SiteID,
			GateID: scope.GateID, ActorUserID: scope.UserID,
			Payload: map[string]any{"kind": string(kind), "expected": counts.Expected,
				"muster_point": req.MusterPoint},
		})
	})
	if err != nil {
		return Incident{}, err
	}
	return s.repo.ByID(ctx, scope.TenantID, id)
}

// MarkRequest is one person's answer at the muster point.
type MarkRequest struct {
	PersonID    int64
	Status      string
	MusterPoint string
	Note        string
}

// Mark records what a warden saw. The row is locked, the transition is written with the status
// it came from, and the incident's totals are recomputed in the same transaction, so the board
// the next phone reads is the board the database holds.
func (s *Service) Mark(ctx context.Context, scope tenancy.Scope, incidentID int64,
	req MarkRequest) (Person, Counts, error) {
	if incidentID <= 0 || req.PersonID <= 0 {
		return Person{}, Counts{}, errs.Invalid("incident and roster entry are required")
	}
	to := PersonStatus(req.Status)
	if !to.valid() {
		return Person{}, Counts{}, errs.BadField("status", "pick a roll-call status")
	}
	if len(req.Note) > 512 {
		return Person{}, Counts{}, errs.BadField("note", "too long")
	}
	if len(req.MusterPoint) > 255 {
		return Person{}, Counts{}, errs.BadField("muster_point", "too long")
	}
	incident, err := s.repo.ByID(ctx, scope.TenantID, incidentID)
	if err != nil {
		return Person{}, Counts{}, err
	}
	if !incident.Live() {
		return Person{}, Counts{}, errs.Conflict("this roll-call is " + incident.Status)
	}

	var counts Counts
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		person, err := s.repo.PersonForUpdate(ctx, c, scope.TenantID, incidentID, req.PersonID)
		if err != nil {
			return err
		}
		source := string(SourceManual)
		if scope.DeviceID != 0 {
			source = string(SourceScan)
		}
		if err := s.repo.Mark(ctx, c, MarkInput{
			TenantID: scope.TenantID, IncidentID: incidentID, PersonID: req.PersonID,
			From: person.Status, To: string(to), MarkedBy: scope.UserID, GateID: scope.GateID,
			GateSession: scope.GateShiftID, MusterPoint: req.MusterPoint, Note: req.Note,
			Source: source,
		}); err != nil {
			return err
		}
		counts, err = s.repo.Recount(ctx, c, scope.TenantID, incidentID)
		if err != nil {
			return err
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "rollcall.marked",
			SubjectType: "evacuation_roster", SubjectID: req.PersonID, SiteID: incident.SiteID,
			Reason: req.Note,
			Before: map[string]any{"status": person.Status},
			After:  map[string]any{"status": string(to), "muster_point": req.MusterPoint},
		})
	})
	if err != nil {
		return Person{}, Counts{}, err
	}
	person, err := s.repo.Person(ctx, scope.TenantID, incidentID, req.PersonID)
	if err != nil {
		return Person{}, Counts{}, err
	}
	return person, counts, nil
}

// Remind asks the notification module to chase the people still on the board. It refuses to
// send a notice that says nobody is missing.
func (s *Service) Remind(ctx context.Context, scope tenancy.Scope, incidentID int64) (Counts, error) {
	incident, err := s.repo.ByID(ctx, scope.TenantID, incidentID)
	if err != nil {
		return Counts{}, err
	}
	if !incident.Live() {
		return Counts{}, errs.Conflict("this roll-call is " + incident.Status)
	}
	counts := Counts{Expected: incident.Expected, Accounted: incident.Accounted}
	if counts.Unaccounted() == 0 {
		return Counts{}, errs.Conflict("everybody on the roster is accounted for")
	}
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		if err := s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "rollcall.reminded",
			SubjectType: aggregateType, SubjectID: incidentID, SiteID: incident.SiteID,
			After: map[string]any{"unaccounted": counts.Unaccounted()},
		}); err != nil {
			return err
		}
		return s.events.Publish(ctx, c, events.Event{
			TenantID: scope.TenantID, EventKey: "rollcall.still_unaccounted",
			AggregateType: aggregateType, AggregateID: incidentID, SiteID: incident.SiteID,
			GateID: scope.GateID, ActorUserID: scope.UserID,
			Payload: map[string]any{"unaccounted": counts.Unaccounted(),
				"expected": counts.Expected, "accounted": counts.Accounted,
				"muster_point": deref(incident.MusterPoint), "kind": incident.Kind},
		})
	})
	if err != nil {
		return Counts{}, err
	}
	return counts, nil
}

// Close ends an incident. It will not sign off with people unaccounted and nothing written about
// them: the summary is the question the next inspection asks.
func (s *Service) Close(ctx context.Context, scope tenancy.Scope, incidentID int64,
	summary string) (Incident, error) {
	incident, err := s.repo.ByID(ctx, scope.TenantID, incidentID)
	if err != nil {
		return Incident{}, err
	}
	counts := Counts{Expected: incident.Expected, Accounted: incident.Accounted}
	if err := closeRefusal(counts, summary); err != nil {
		return Incident{}, err
	}
	return s.settle(ctx, scope, incident, StatusClosed, summary, "rollcall.closed")
}

// Cancel ends an incident that should not have been raised - a drill called off, an alarm
// tripped by dust. It still holds the reason.
func (s *Service) Cancel(ctx context.Context, scope tenancy.Scope, incidentID int64,
	reason string) (Incident, error) {
	if reason == "" {
		return Incident{}, errs.BadField("reason", "say why the incident is being cancelled")
	}
	incident, err := s.repo.ByID(ctx, scope.TenantID, incidentID)
	if err != nil {
		return Incident{}, err
	}
	return s.settle(ctx, scope, incident, StatusCancelled, reason, "rollcall.cancelled")
}

func (s *Service) settle(ctx context.Context, scope tenancy.Scope, incident Incident, to Status,
	summary, action string) (Incident, error) {
	if !incident.Live() {
		return Incident{}, errs.Conflict("this roll-call is already " + incident.Status)
	}
	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		settled, err := s.repo.Settle(ctx, c, scope.TenantID, incident.ID, string(to), summary,
			scope.UserID)
		if err != nil {
			return err
		}
		if !settled {
			return errs.Conflict("this roll-call was settled by someone else")
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: action,
			SubjectType: aggregateType, SubjectID: incident.ID, SiteID: incident.SiteID,
			Reason: summary,
			Before: map[string]any{"status": incident.Status},
			After: map[string]any{"status": string(to),
				"expected": incident.Expected, "accounted": incident.Accounted},
		})
	})
	if err != nil {
		return Incident{}, err
	}
	settled, err := s.repo.ByID(ctx, scope.TenantID, incident.ID)
	if err != nil {
		return Incident{}, err
	}
	logging.From(ctx).InfoContext(ctx, "roll-call settled", "incident_id", settled.ID,
		"status", settled.Status, "unaccounted", settled.Unaccounted())
	return settled, nil
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
