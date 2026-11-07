package rollcall

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/httpx"
)

// StartIncidentRequest raises an alarm. The site comes from the session, never from the body: a
// guard raising a roll-call at Main must not be able to raise one at a site he is not standing
// in.
type StartIncidentRequest struct {
	Kind        string `json:"kind" validate:"required,oneof=fire medical security evacuation_drill natural_hazard chemical other"`
	MusterPoint string `json:"muster_point" validate:"omitempty,max=255"`
	Notes       string `json:"notes" validate:"omitempty,max=2000"`
}

func (req StartIncidentRequest) toRequest() StartRequest {
	return StartRequest{Kind: req.Kind, MusterPoint: req.MusterPoint, Notes: req.Notes}
}

// MarkPersonRequest is one answer at the muster point.
type MarkPersonRequest struct {
	Status      string `json:"status" validate:"required,oneof=unaccounted accounted evacuated injured missing not_on_site deceased"`
	MusterPoint string `json:"muster_point" validate:"omitempty,max=255"`
	Note        string `json:"note" validate:"omitempty,max=512"`
}

func (req MarkPersonRequest) toRequest(personID int64) MarkRequest {
	return MarkRequest{PersonID: personID, Status: req.Status, MusterPoint: req.MusterPoint,
		Note: req.Note}
}

// CloseIncidentRequest ends the incident. The summary is what makes a closed file answer the
// question an inspection asks about the people who were not found.
type CloseIncidentRequest struct {
	Summary string `json:"summary" validate:"omitempty,max=4000"`
}

type CancelIncidentRequest struct {
	Reason string `json:"reason" validate:"required,max=512"`
}

// IncidentView is the board's header: what kind of alarm it is, and how many people are still
// out.
type IncidentView struct {
	ID          int64      `json:"id"`
	UUID        string     `json:"uuid"`
	SiteID      int64      `json:"site_id"`
	SiteName    string     `json:"site_name,omitempty"`
	GateID      *int64     `json:"gate_id,omitempty"`
	Kind        string     `json:"kind"`
	Status      string     `json:"status"`
	StartedAt   time.Time  `json:"started_at"`
	StartedBy   *int64     `json:"started_by_user_id,omitempty"`
	FrozenAt    *time.Time `json:"roster_frozen_at,omitempty"`
	Expected    int        `json:"expected_person_count"`
	Accounted   int        `json:"accounted_count"`
	Unaccounted int        `json:"unaccounted_count"`
	MusterPoint string     `json:"muster_point,omitempty"`
	ClosedAt    *time.Time `json:"closed_at,omitempty"`
	ClosedBy    *int64     `json:"closed_by_user_id,omitempty"`
	Summary     string     `json:"summary,omitempty"`
	Notes       string     `json:"notes,omitempty"`
}

func viewOfIncident(i Incident) IncidentView {
	view := IncidentView{
		ID: i.ID, UUID: i.UUID, SiteID: i.SiteID, SiteName: i.SiteName, GateID: i.GateID,
		Kind: i.Kind, Status: i.Status, StartedAt: i.StartedAt, StartedBy: i.StartedBy,
		FrozenAt: i.RosterFrozenAt, Expected: i.Expected, Accounted: i.Accounted,
		Unaccounted: i.Unaccounted(), ClosedAt: i.ClosedAt, ClosedBy: i.ClosedBy,
	}
	if i.MusterPoint != nil {
		view.MusterPoint = *i.MusterPoint
	}
	if i.Summary != nil {
		view.Summary = *i.Summary
	}
	if i.Notes != nil {
		view.Notes = *i.Notes
	}
	return view
}

func viewsOfIncidents(rows []Incident) []IncidentView {
	views := make([]IncidentView, 0, len(rows))
	for _, row := range rows {
		views = append(views, viewOfIncident(row))
	}
	return views
}

// RosterView is one person on the board. The snapshots are shown as they were frozen, not as the
// pass reads now.
type RosterView struct {
	ID           int64      `json:"id"`
	PersonKey    string     `json:"person_key"`
	PersonType   string     `json:"person_type"`
	PassID       *int64     `json:"pass_id,omitempty"`
	PassNo       string     `json:"pass_no,omitempty"`
	MovementID   *int64     `json:"movement_id,omitempty"`
	Name         string     `json:"name"`
	Phone        string     `json:"phone,omitempty"`
	Company      string     `json:"company,omitempty"`
	Plate        string     `json:"plate,omitempty"`
	LastSeenAt   *time.Time `json:"last_seen_at,omitempty"`
	LastSeenGate *int64     `json:"last_seen_gate_id,omitempty"`
	Status       string     `json:"status"`
	MarkedBy     *int64     `json:"marked_by_user_id,omitempty"`
	MarkedAt     *time.Time `json:"marked_at,omitempty"`
	MarkedGate   *int64     `json:"marked_gate_id,omitempty"`
	MusterPoint  string     `json:"muster_point,omitempty"`
	Source       string     `json:"source"`
	Note         string     `json:"note,omitempty"`
}

func viewOfPerson(p Person) RosterView {
	view := RosterView{
		ID: p.ID, PersonKey: p.PersonKey, PersonType: p.PersonType, PassID: p.PassID,
		PassNo: p.PassNo, MovementID: p.MovementID, Name: p.Name, LastSeenAt: p.LastSeenAt,
		LastSeenGate: p.LastSeenGate, Status: p.Status, MarkedBy: p.MarkedBy,
		MarkedAt: p.MarkedAt, MarkedGate: p.MarkedGate, Source: p.Source,
	}
	if p.Phone != nil {
		view.Phone = *p.Phone
	}
	if p.Company != nil {
		view.Company = *p.Company
	}
	if p.Plate != nil {
		view.Plate = *p.Plate
	}
	if p.MusterPoint != nil {
		view.MusterPoint = *p.MusterPoint
	}
	if p.Note != nil {
		view.Note = *p.Note
	}
	return view
}

func viewsOfPeople(rows []Person) []RosterView {
	views := make([]RosterView, 0, len(rows))
	for _, row := range rows {
		views = append(views, viewOfPerson(row))
	}
	return views
}

// MarkView is one transition, shown in the order it happened.
type MarkView struct {
	ID         int64     `json:"id"`
	PersonID   int64     `json:"roster_id"`
	PersonName string    `json:"name,omitempty"`
	From       string    `json:"from_status,omitempty"`
	To         string    `json:"to_status"`
	MarkedBy   *int64    `json:"marked_by_user_id,omitempty"`
	GateID     *int64    `json:"gate_id,omitempty"`
	Muster     string    `json:"muster_point,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
	Note       string    `json:"note,omitempty"`
}

func viewOfMark(m Mark) MarkView {
	view := MarkView{
		ID: m.ID, PersonID: m.PersonID, PersonName: m.PersonName, To: m.ToStatus,
		MarkedBy: m.MarkedBy, GateID: m.GateID, OccurredAt: m.OccurredAt,
	}
	if m.FromStatus != nil {
		view.From = *m.FromStatus
	}
	if m.MusterPoint != nil {
		view.Muster = *m.MusterPoint
	}
	if m.Note != nil {
		view.Note = *m.Note
	}
	return view
}

func viewsOfMarks(rows []Mark) []MarkView {
	views := make([]MarkView, 0, len(rows))
	for _, row := range rows {
		views = append(views, viewOfMark(row))
	}
	return views
}

// CountsView is the pair of numbers a mark answers with, so a board can redraw without a second
// request.
type CountsView struct {
	Expected    int `json:"expected"`
	Accounted   int `json:"accounted"`
	Unaccounted int `json:"unaccounted"`
}

func viewOfCounts(c Counts) CountsView {
	return CountsView{Expected: c.Expected, Accounted: c.Accounted,
		Unaccounted: c.Unaccounted()}
}

// MarkResultView is one person's new row plus the board's new totals.
type MarkResultView struct {
	Person RosterView `json:"person"`
	Counts CountsView `json:"counts"`
}

// ParseIncidents reads the register's query string.
func ParseIncidents(r *http.Request) (IncidentFilter, httpx.Page, error) {
	query := r.URL.Query()
	siteID, err := optionalID(query, "site_id")
	if err != nil {
		return IncidentFilter{}, httpx.Page{}, err
	}
	filter := IncidentFilter{SiteID: siteID, Status: query.Get("status")}
	if filter.Status != "" && !Status(filter.Status).valid() {
		return filter, httpx.Page{}, errs.BadField("status", filter.Status+" is not an incident status")
	}
	page, err := httpx.ParsePage(r, defaultPage, 200)
	return filter, page, err
}

// ParseRoster reads one board's query string.
func ParseRoster(r *http.Request) (RosterFilter, httpx.Page, error) {
	query := r.URL.Query()
	filter := RosterFilter{Status: query.Get("status"), Search: query.Get("search")}
	if filter.Status != "" && !PersonStatus(filter.Status).valid() {
		return filter, httpx.Page{}, errs.BadField("status",
			filter.Status+" is not a roll-call status")
	}
	if len(filter.Search) > 128 {
		return filter, httpx.Page{}, errs.BadField("search", "too long")
	}
	page, err := httpx.ParsePage(r, defaultPage, 500)
	return filter, page, err
}

func optionalID(query url.Values, name string) (int64, error) {
	raw := query.Get(name)
	if raw == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, errs.BadField(name, "expected a positive id")
	}
	return value, nil
}
