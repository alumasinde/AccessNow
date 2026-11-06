package server

import (
	"testing"

	"github.com/jmoiron/sqlx"

	"accessnow/internal/platform/config"
)

// TestEveryMountedHandlerIsBuilt guards the one wiring mistake a compiler cannot see: a
// Deps field declared, mounted by Register, and never assigned. Build only constructs, so a
// nil pool is enough to prove every handler the router reaches for exists.
func TestEveryMountedHandlerIsBuilt(t *testing.T) {
	deps := Build((*sqlx.DB)(nil), config.Config{})

	if deps.Backlog == nil {
		t.Error("backlog loop is not wired")
	}
	for _, h := range []struct {
		name    string
		present bool
	}{
		{"Auth", deps.Auth != nil},
		{"Gates", deps.Gates != nil},
		{"Passes", deps.Passes != nil},
		{"Movements", deps.Movements != nil},
		{"Visitor", deps.Visitor != nil},
		{"Material", deps.Material != nil},
		{"Vehicle", deps.Vehicle != nil},
		{"Staff", deps.Staff != nil},
		{"Approval", deps.Approval != nil},
	} {
		if !h.present {
			t.Errorf("%s handler is not wired; its routes would panic", h.name)
		}
	}
}
