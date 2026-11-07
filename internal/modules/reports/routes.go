package reports

import (
	"github.com/go-chi/chi/v5"

	"accessnow/internal/platform/router"
)

// Register mounts the reports surface. With reports_exports switched off these paths answer
// 404 and the worker skips the tenant's schedules; nothing else in the system changes.
func Register(r chi.Router, h *Handler, g router.Guard) {
	desk := g.Chain(g.Session, g.Module(ModuleKey))
	see := g.Permission("report.view")
	export := g.Permission("report.export")
	manage := g.Permission("report.schedule_manage")

	r.With(desk, see).Get("/reports/definitions", h.Definitions)

	r.With(desk, export).Post("/reports/run", h.Run)

	r.With(desk, see).Get("/reports/runs", h.Runs)
	r.With(desk, see).Get("/reports/runs/{runID}", h.GetRun)

	r.With(desk, see).Get("/reports/schedules", h.Schedules)
	r.With(desk, see).Get("/reports/schedules/{scheduleID}", h.GetSchedule)
	r.With(desk, manage).Post("/reports/schedules", h.CreateSchedule)
	r.With(desk, manage).Put("/reports/schedules/{scheduleID}", h.UpdateSchedule)
	r.With(desk, manage).Delete("/reports/schedules/{scheduleID}", h.DeleteSchedule)
	r.With(desk, export).Post("/reports/schedules/{scheduleID}/run", h.RunSchedule)
}
