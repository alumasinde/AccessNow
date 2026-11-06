// Package server owns the object graph and the two public surfaces. Nothing in here
// knows a business rule: it wires Core and the modules together and hangs guards on them.
package server

import (
	"time"

	"github.com/jmoiron/sqlx"

	"accessnow/internal/core/access"
	"accessnow/internal/core/auth"
	"accessnow/internal/core/gates"
	"accessnow/internal/core/movements"
	"accessnow/internal/core/passes"
	"accessnow/internal/modules/approval"
	"accessnow/internal/modules/blocklist"
	"accessnow/internal/modules/material"
	"accessnow/internal/modules/notifications"
	"accessnow/internal/modules/offline"
	"accessnow/internal/modules/rules"
	"accessnow/internal/modules/staff"
	"accessnow/internal/modules/vehicle"
	"accessnow/internal/modules/visitor"
	"accessnow/internal/platform/audit"
	"accessnow/internal/platform/config"
	"accessnow/internal/platform/events"
	"accessnow/internal/platform/numbering"
	"accessnow/internal/platform/router"
)

// cacheTTL is how long a permission or module flag may be stale. Thirty seconds is the
// compromise an operator accepts: a revoked guard stops working on the next refresh, and
// a busy gate does not query RBAC on every scan.
const cacheTTL = 30 * time.Second

// backlogEvery is how long an event may wait for a reaction that no request relayed. Five
// seconds is under a human noticing, and one pass per tenant per tick is cheap.
const backlogEvery = 5 * time.Second

// sweepEvery is how long a notice may wait for a gateway. It is looser than the relay tick
// because a message that takes eleven seconds is late in a way nobody notices, while a queue
// that dials a provider on every tick is a tenant paying for a burst.
const sweepEvery = 10 * time.Second

// Deps is the assembled application: one guard bundle and the handlers the router mounts.
type Deps struct {
	Guard     router.Guard
	Backlog   *events.Backlog
	Delivery  *notifications.DeliveryLoop
	Auth      *auth.Handler
	Gates     *gates.Handler
	Passes    *passes.Handler
	Movements *movements.Handler
	Visitor   *visitor.Handler
	Material  *material.Handler
	Vehicle   *vehicle.Handler
	Staff     *staff.Handler
	Approval  *approval.Handler
	Blocklist *blocklist.Handler
	Rules     *rules.Handler
	Offline   *offline.Handler

	Notifications *notifications.Handler
}

func Build(pool *sqlx.DB, cfg config.Config) *Deps {
	chain := audit.Writer{}
	bus := events.Bus{}

	// The registry is what the outbox relay reads. Registering a module's reaction here is
	// the only way a module ever hears about another module's event, and this file is the
	// only place allowed to name both.
	registry := events.NewRegistry()
	relay := events.NewDispatcher(pool, registry)

	authRepo := auth.NewRepository(pool)
	accessRepo := access.NewRepository(pool)
	accessSvc := access.NewService(accessRepo, cacheTTL)
	numbers := numbering.NewAllocator(numbering.NewRepository(pool))

	authSvc := auth.NewService(pool, authRepo, chain, cfg)
	gateRepo := gates.NewRepository(pool)
	gateSvc := gates.NewService(pool, gateRepo, authRepo, accessSvc, chain)
	passRepo := passes.NewRepository(pool)
	// The workflow plugs into Core through an empty handle, because Core needs the guard to
	// build and the guard needs Core's service to answer. Bind completes it below; nothing
	// reaches the desk surface in between.
	flow := approval.NewGuard()
	// The screening list plugs into Core through the same empty handle, for the same reason:
	// a barrier has to be built before the module that answers it exists, and a tenant
	// without the module gets an empty verdict rather than an error.
	screen := blocklist.NewGuard()
	// The tenant's own rulebook plugs in the same way, as the second seam the barrier holds:
	// a rulebook that is not there says nothing, and saying nothing is the gate it had before.
	policy := rules.NewGuard()
	// The offline ledger plugs in the same way, and it is the seam with the least promise in it:
	// a replayed queue that nobody files still lands, row for row, exactly as it did before this
	// module existed.
	ledger := movements.NewLedgerGuard()
	passSvc := passes.NewService(pool, passRepo, numbers, chain, bus, flow)
	movementsSvc := movements.NewService(pool, movements.NewRepository(pool), passRepo, chain,
		bus, relay, screen, policy, ledger)
	visitorSvc := visitor.NewService(pool, visitor.NewRepository(pool), authRepo,
		passSvc, passRepo, movementsSvc, bus)
	materialSvc := material.NewService(pool, material.NewRepository(pool), passSvc, passRepo,
		bus, chain)
	vehicleSvc := vehicle.NewService(pool, vehicle.NewRepository(pool), passSvc, passRepo, bus)
	staffSvc := staff.NewService(pool, staff.NewRepository(pool), passSvc, passRepo, bus)
	approvalSvc := approval.NewService(pool, approval.NewRepository(pool), passSvc, chain, bus, accessSvc)
	flow.Bind(approvalSvc)
	blocklistSvc := blocklist.NewService(pool, blocklist.NewRepository(pool), accessSvc, bus,
		chain)
	screen.Bind(blocklistSvc)
	rulesSvc := rules.NewService(pool, rules.NewRepository(pool), accessSvc, bus, chain)
	policy.Bind(rulesSvc)
	// Offline gate mode is the ledger behind Core's seam, and the cache a device pulls. The
	// screening list reaches it through the wiring rather than as a dependency: a tenant that
	// runs gates offline without the list gets a cache with the passes in it.
	offlineSvc := offline.NewService(pool, offline.NewRepository(pool), gateRepo, passRepo,
		screeningCache{svc: blocklistSvc}, accessSvc, bus, chain)
	ledger.Bind(offlineSvc)
	// The notification provider's credentials come from the environment by name, so nothing
	// in this wiring ever holds a secret it could log.
	notificationsSvc := notifications.NewService(pool, notifications.NewRepository(pool),
		accessSvc, chain, nil)

	// All four reactions key off the same scan: the gate decides, the module's own row
	// follows. A module switched off for a tenant hides its surface, not these handlers:
	// letting the register drift while the desk is closed is worse than keeping it true, and
	// turning the module back on would otherwise resurrect passes stuck at "approved".
	registry.On("movement.recorded", visitorSvc.OnMovement)
	registry.On("movement.recorded", materialSvc.OnMovement)
	registry.On("movement.recorded", vehicleSvc.OnMovement)
	registry.On("movement.recorded", staffSvc.OnMovement)
	// One trigger starts a chain, and it is Core's rather than any module's: a pass is
	// issued in exactly one place, so a chain cannot be filed twice by two issuers, and a
	// module never has to know the workflow exists.
	registry.On("pass.issued", approvalSvc.OnPassIssued)
	// And the same trigger ends one: whenever the pass leaves the asking states by any route,
	// the chain that was asking about it is recalled and the people still holding an ask are
	// told. An approver should never sit on a task that a desk already made moot.
	for _, eventKey := range approval.SettledKeys() {
		registry.On(eventKey, approvalSvc.OnPassSettled)
	}

	// Notifications is the one reactor that subscribes to a long list of keys rather than to
	// the scan, because its whole job is what everybody else's events mean to a human. The
	// list is the module's own, so adding an event is a change in one place.
	for _, eventKey := range notifications.EventKeys() {
		registry.On(eventKey, notificationsSvc.OnEvent)
	}

	guard := router.Guard{
		Session:    auth.NewMiddleware(authSvc).Session,
		Module:     accessSvc.Module,
		Permission: accessSvc.Permission,
		GateWork:   accessSvc.GateWork(),
		Device:     gates.NewDeviceMiddleware(gateSvc).Require,
	}

	return &Deps{
		Guard:     guard,
		Backlog:   events.NewBacklog(relay, backlogEvery),
		Delivery:  notifications.NewDeliveryLoop(notificationsSvc, sweepEvery),
		Auth:      auth.NewHandler(authSvc, cfg),
		Gates:     gates.NewHandler(gateSvc),
		Passes:    passes.NewHandler(passSvc),
		Movements: movements.NewHandler(movementsSvc),
		Visitor:   visitor.NewHandler(visitorSvc),
		Material:  material.NewHandler(materialSvc),
		Vehicle:   vehicle.NewHandler(vehicleSvc),
		Staff:     staff.NewHandler(staffSvc),
		Approval:  approval.NewHandler(approvalSvc),
		Blocklist: blocklist.NewHandler(blocklistSvc),
		Rules:     rules.NewHandler(rulesSvc),
		Offline:   offline.NewHandler(offlineSvc),

		Notifications: notifications.NewHandler(notificationsSvc),
	}
}
