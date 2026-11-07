package rollcall

import (
	"net/http"

	"accessnow/internal/platform/httpx"
	"accessnow/internal/platform/tenancy"
)

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Start raises the alarm. The roster is frozen before this returns, so the screen the supervisor
// opens next is the roster the incident will be judged by.
func (h *Handler) Start(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req StartIncidentRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	incident, err := h.svc.Start(r.Context(), scope, req.toRequest())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.Created(w, viewOfIncident(incident))
}

func (h *Handler) ListIncidents(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter, page, err := ParseIncidents(r)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter.TenantID = scope.TenantID
	filter.Limit = page.Limit + 1
	filter.Before = page.Cursor

	rows, err := h.svc.Incidents(r.Context(), filter)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := viewsOfIncidents(rows)
	httpx.OK(w, httpx.Paginate(views, page.Limit, func(v IncidentView) httpx.Cursor {
		return httpx.Cursor{At: v.StartedAt, ID: v.ID}
	}))
}

// Open is what a gate screen polls: is there an alarm I should be working, and how many people
// are still out.
func (h *Handler) Open(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	incident, err := h.svc.Open(r.Context(), scope)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOfIncident(incident))
}

func (h *Handler) GetIncident(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r, "incidentID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	incident, err := h.svc.Incident(r.Context(), scope.TenantID, id)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOfIncident(incident))
}

func (h *Handler) Roster(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r, "incidentID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter, page, err := ParseRoster(r)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter.TenantID = scope.TenantID
	filter.IncidentID = id
	filter.Limit = page.Limit

	rows, err := h.svc.Roster(r.Context(), filter)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := viewsOfPeople(rows)
	httpx.OK(w, map[string]any{"items": views, "count": len(views)})
}

func (h *Handler) Mark(w http.ResponseWriter, r *http.Request) {
	scope, incidentID, err := scopeAndID(r, "incidentID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	personID, err := httpx.ID(r, "rosterID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req MarkPersonRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	person, counts, err := h.svc.Mark(r.Context(), scope, incidentID, req.toRequest(personID))
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, MarkResultView{Person: viewOfPerson(person), Counts: viewOfCounts(counts)})
}

func (h *Handler) Marks(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r, "incidentID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	rows, err := h.svc.Marks(r.Context(), scope.TenantID, id, 0, 200)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := viewsOfMarks(rows)
	httpx.OK(w, map[string]any{"items": views, "count": len(views)})
}

// Remind chases the people still unaccounted for. It sends nothing when the board is clear.
func (h *Handler) Remind(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r, "incidentID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	counts, err := h.svc.Remind(r.Context(), scope, id)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOfCounts(counts))
}

func (h *Handler) Close(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r, "incidentID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req CloseIncidentRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	incident, err := h.svc.Close(r.Context(), scope, id, req.Summary)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOfIncident(incident))
}

func (h *Handler) Cancel(w http.ResponseWriter, r *http.Request) {
	scope, id, err := scopeAndID(r, "incidentID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req CancelIncidentRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	incident, err := h.svc.Cancel(r.Context(), scope, id, req.Reason)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, viewOfIncident(incident))
}

func scopeAndID(r *http.Request, param string) (tenancy.Scope, int64, error) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		return scope, 0, err
	}
	id, err := httpx.ID(r, param)
	return scope, id, err
}
