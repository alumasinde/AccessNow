package vehicle

import (
	"github.com/go-chi/chi/v5"

	"accessnow/internal/platform/router"
)

// Register mounts the vehicle surface. With vehicle_tracking switched off these paths
// answer 404 and the gate keeps scanning: entry and exit are movements, and the reaction
// that keeps the register true needs no desk endpoint to be reachable.
//
// A plate is PATCHed, not POSTed again: the register row is the one thing a pass points at,
// so an edit that keeps the id is what keeps the history intact.
func Register(r chi.Router, h *Handler, g router.Guard) {
	desk := g.Chain(g.Session, g.Module(ModuleKey))

	r.With(desk, g.Permission("vehicle_pass.create")).Post("/vehicle-passes", h.Log)
	r.With(desk, g.Permission("vehicle.view")).Get("/vehicle-passes", h.ListPasses)
	// Static before the wildcard: chi prefers the exact segment, so this never swallows a
	// numeric id.
	r.With(desk, g.Permission("vehicle.view")).Get("/vehicle-passes/on-site", h.OnSite)
	r.With(desk, g.Permission("vehicle.view")).Get("/vehicle-passes/{vehiclePassID}", h.GetPass)
	r.With(desk, g.Permission("vehicle_pass.approve")).Post("/vehicle-passes/{vehiclePassID}/decision", h.Decide)
	// Cancelling is the create side of the desk: the operator who logged a journey that has
	// not been driven is the one allowed to take it back.
	r.With(desk, g.Permission("vehicle_pass.create")).Post("/vehicle-passes/{vehiclePassID}/cancel", h.Cancel)

	r.With(desk, g.Permission("vehicle.manage")).Post("/vehicles", h.Register)
	r.With(desk, g.Permission("vehicle.view")).Get("/vehicles", h.ListVehicles)
	r.With(desk, g.Permission("vehicle.view")).Get("/vehicles/{vehicleID}", h.GetVehicle)
	r.With(desk, g.Permission("vehicle.manage")).Patch("/vehicles/{vehicleID}", h.UpdateVehicle)
}
