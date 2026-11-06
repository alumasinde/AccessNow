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
	"accessnow/internal/modules/material"
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

// Deps is the assembled application: one guard bundle and the handlers the router mounts.
type Deps struct {
	Guard     router.Guard
	Auth      *auth.Handler
	Gates     *gates.Handler
	Passes    *passes.Handler
	Movements *movements.Handler
	Visitor   *visitor.Handler
	Material  *material.Handler
	Vehicle   *vehicle.Handler
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
	gateSvc := gates.NewService(pool, gates.NewRepository(pool), authRepo, accessSvc, chain)
	passRepo := passes.NewRepository(pool)
	passSvc := passes.NewService(pool, passRepo, numbers, chain, bus)
	movementsSvc := movements.NewService(pool, movements.NewRepository(pool), passRepo, chain, bus, relay)
	visitorSvc := visitor.NewService(pool, visitor.NewRepository(pool), authRepo,
		passSvc, passRepo, movementsSvc, bus)
	materialSvc := material.NewService(pool, material.NewRepository(pool), passSvc, passRepo, bus)
	vehicleSvc := vehicle.NewService(pool, vehicle.NewRepository(pool), passSvc, passRepo, bus)

	// All three reactions key off the same scan: the gate decides, the module's own row
	// follows. A module switched off for a tenant hides its surface, not these handlers:
	// letting the register drift while the desk is closed is worse than keeping it true, and
	// turning the module back on would otherwise resurrect passes stuck at "approved".
	registry.On("movement.recorded", visitorSvc.OnMovement)
	registry.On("movement.recorded", materialSvc.OnMovement)
	registry.On("movement.recorded", vehicleSvc.OnMovement)

	guard := router.Guard{
		Session:    auth.NewMiddleware(authSvc).Session,
		Module:     accessSvc.Module,
		Permission: accessSvc.Permission,
		GateWork:   accessSvc.GateWork(),
		Device:     gates.NewDeviceMiddleware(gateSvc).Require,
	}

	return &Deps{
		Guard:     guard,
		Auth:      auth.NewHandler(authSvc, cfg),
		Gates:     gates.NewHandler(gateSvc),
		Passes:    passes.NewHandler(passSvc),
		Movements: movements.NewHandler(movementsSvc),
		Visitor:   visitor.NewHandler(visitorSvc),
		Material:  material.NewHandler(materialSvc),
		Vehicle:   vehicle.NewHandler(vehicleSvc),
	}
}
