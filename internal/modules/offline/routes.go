package offline

import (
	"github.com/go-chi/chi/v5"

	"accessnow/internal/platform/router"
)

// Register mounts the desk surface. With offline_gate switched off these paths answer 404, the
// ledger files nothing, and the replay Core already does is the whole of the module's other
// half: a gate that cannot pull a cache simply needs the link.
//
// There is no replay endpoint here even though sync.replay is a seeded permission. The queue
// lives on the tablet, and the batch it resends is the same batch under the same client id, so
// a device that comes back is the only thing that can replay it. A server-side "replay" would
// have to invent a queue from the movements it already has.
func Register(r chi.Router, h *Handler, g router.Guard) {
	desk := g.Chain(g.Session, g.Module(ModuleKey))
	see := g.Permission("sync.view")
	resolve := g.Permission("sync.resolve_conflict")

	r.With(desk, see).Get("/offline/devices", h.Devices)

	r.With(desk, see).Get("/offline/batches", h.Batches)
	r.With(desk, see).Get("/offline/batches/{batchID}", h.Batch)

	r.With(desk, see).Get("/offline/conflicts", h.Conflicts)
	r.With(desk, see).Get("/offline/conflicts/{conflictID}", h.Conflict)
	r.With(desk, resolve).Post("/offline/conflicts/{conflictID}/resolve", h.Resolve)
}

// RegisterGate mounts the device surface, next to scan and sync. A tablet is authenticated by
// its own token, and the cache it is handed is its gate's and nothing else's.
func RegisterGate(r chi.Router, h *Handler, g router.Guard) {
	gate := g.Chain(g.Session, g.Device, g.Module(ModuleKey))
	r.With(gate).Get("/cache", h.Cache)
}
