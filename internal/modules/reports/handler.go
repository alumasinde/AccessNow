package reports

import (
	"net/http"
	"strconv"

	"accessnow/internal/platform/httpx"
	"accessnow/internal/platform/tenancy"
)

// Handler is the desk surface: the catalogue, the run button and the schedules.
type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Definitions lists what this tenant may run.
func (h *Handler) Definitions(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	definitions, err := h.svc.Definitions(r.Context(), scope.TenantID)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := make([]DefinitionView, 0, len(definitions))
	for _, def := range definitions {
		views = append(views, definitionView(def))
	}
	httpx.OK(w, map[string]any{"items": views})
}

// Run produces one export and streams it. The file is the response body; the run id is on a
// header so a client can look the run up afterwards without parsing a filename.
func (h *Handler) Run(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req RunRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	artifact, err := h.svc.Run(r.Context(), scope, req)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	writeArtifact(w, artifact)
}

// writeArtifact sends the file with the headers a browser needs to save it under the name the
// run recorded, rather than under the endpoint's path.
func writeArtifact(w http.ResponseWriter, artifact Artifact) {
	w.Header().Set("Content-Type", artifact.ContentType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+artifact.FileName+`"`)
	w.Header().Set("X-Report-Run-ID", strconv.FormatInt(artifact.Run.ID, 10))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if artifact.Run.RowCount != nil {
		w.Header().Set("X-Report-Rows", strconv.Itoa(*artifact.Run.RowCount))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(artifact.Bytes)
}

// Runs is the register of exports, newest first.
func (h *Handler) Runs(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter, page, err := ParseRuns(r)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	filter.TenantID = scope.TenantID
	filter.Limit = page.Limit + 1
	if page.Cursor != nil {
		filter.Before = &page.Cursor.At
		filter.BeforeID = page.Cursor.ID
	}

	runs, err := h.svc.Runs(r.Context(), filter)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := runViews(runs)
	httpx.OK(w, httpx.Paginate(views, page.Limit, func(v RunView) httpx.Cursor {
		return httpx.Cursor{At: v.CreatedAt, ID: v.ID}
	}))
}

func (h *Handler) GetRun(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	id, err := httpx.ID(r, "runID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	run, err := h.svc.RunByID(r.Context(), scope.TenantID, id)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, runView(run))
}

func (h *Handler) Schedules(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	schedules, err := h.svc.Schedules(r.Context(), scope.TenantID)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, map[string]any{"items": scheduleViews(schedules)})
}

func (h *Handler) GetSchedule(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	id, err := httpx.ID(r, "scheduleID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	schedule, err := h.svc.Schedule(r.Context(), scope.TenantID, id)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, scheduleView(schedule))
}

func (h *Handler) CreateSchedule(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req ScheduleRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	schedule, err := h.svc.CreateSchedule(r.Context(), scope, req)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.Created(w, scheduleView(schedule))
}

func (h *Handler) UpdateSchedule(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	id, err := httpx.ID(r, "scheduleID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req ScheduleRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	schedule, err := h.svc.UpdateSchedule(r.Context(), scope, id, req)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, scheduleView(schedule))
}

func (h *Handler) DeleteSchedule(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	id, err := httpx.ID(r, "scheduleID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	if err := h.svc.DeleteSchedule(r.Context(), scope, id); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.NoContent(w)
}

// RunSchedule sends one schedule's export now, over the same lookback the schedule uses.
func (h *Handler) RunSchedule(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	id, err := httpx.ID(r, "scheduleID")
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	artifact, err := h.svc.RunSchedule(r.Context(), scope, id)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	writeArtifact(w, artifact)
}
