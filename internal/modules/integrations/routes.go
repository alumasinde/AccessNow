package integrations

import (
	"github.com/go-chi/chi/v5"

	"accessnow/internal/platform/router"
)

// Register mounts the integrations surface. With integrations_api switched off these paths
// answer 404 and the webhook worker has no endpoints to send to; nothing else changes.
func Register(r chi.Router, h *Handler, g router.Guard) {
	desk := g.Chain(g.Session, g.Module(ModuleKey))
	keys := g.Permission("api_key.manage")
	hooks := g.Permission("webhook.manage")
	actuate := g.Permission("actuator.trigger")
	syncView := g.Permission("sync.view")
	syncRun := g.Permission("sync.replay")

	// API keys. The secret appears exactly once, in the 201.
	r.With(desk, keys).Post("/integrations/api-keys", h.IssueKey)
	r.With(desk, keys).Get("/integrations/api-keys", h.Keys)
	r.With(desk, keys).Post("/integrations/api-keys/{keyID}/revoke", h.RevokeKey)

	// Webhooks.
	r.With(desk, hooks).Get("/integrations/webhooks", h.Webhooks)
	r.With(desk, hooks).Get("/integrations/webhooks/{endpointID}", h.GetWebhook)
	r.With(desk, hooks).Post("/integrations/webhooks", h.CreateWebhook)
	r.With(desk, hooks).Put("/integrations/webhooks/{endpointID}", h.UpdateWebhook)
	r.With(desk, hooks).Delete("/integrations/webhooks/{endpointID}", h.RetireWebhook)
	r.With(desk, hooks).Post("/integrations/webhooks/{endpointID}/test", h.TestWebhook)
	r.With(desk, hooks).Get("/integrations/webhook-deliveries", h.Deliveries)

	// Actuators.
	r.With(desk, actuate).Post("/integrations/actuators/commands", h.EnqueueCommand)
	r.With(desk, actuate).Get("/integrations/actuators/commands", h.Commands)
	r.With(desk, actuate).Post("/integrations/actuators/commands/{commandID}/ack", h.AckCommand)

	// HR, ERP and other systems. The links a sync files are read here; the worker that makes
	// the calls runs in the process, not in a request.
	r.With(desk, syncView).Get("/integrations/external-systems", h.Systems)
	r.With(desk, syncView).Get("/integrations/external-systems/{systemID}", h.System)
	r.With(desk, syncRun).Post("/integrations/external-systems", h.CreateSystem)
	r.With(desk, syncRun).Put("/integrations/external-systems/{systemID}", h.UpdateSystem)
	r.With(desk, syncRun).Delete("/integrations/external-systems/{systemID}", h.SuspendSystem)
	r.With(desk, syncRun).Post("/integrations/external-systems/{systemID}/sync", h.StartSync)
	r.With(desk, syncView).Get("/integrations/sync-jobs", h.SyncJobs)
	r.With(desk, syncView).Get("/integrations/entity-links", h.Links)
	r.With(desk, syncRun).Delete("/integrations/entity-links/{linkID}", h.DeleteLink)

	// The machine surface. It carries a key instead of a session, so the session middleware does
	// not run: a machine has no cookie to present. The key comes first because it is what puts a
	// tenant scope on the request, and the module guard has nothing to check without one. Each
	// data route also checks the key's own scope, so a credential issued for one register cannot
	// read another.
	machine := g.Chain(h.RequireKey, g.Module(ModuleKey), h.RateLimit)
	r.With(machine).Get("/integrations/machine/me", h.WhoAmI)
	r.With(machine).Get("/integrations/machine/visitors", h.MachineVisitors)
	r.With(machine).Get("/integrations/machine/passes/{passNo}", h.MachinePass)
	r.With(machine).Get("/integrations/machine/movements", h.MachineMovements)
}
