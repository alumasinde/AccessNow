package tenancy

import (
	"context"
	"testing"

	"accessnow/internal/platform/errs"
)

func TestRequire(t *testing.T) {
	if _, err := Require(context.Background()); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Errorf("bare context = %v, want unauthenticated", err)
	}

	// A scope with no tenant would let a query run unfiltered across every tenant.
	scoped := Put(context.Background(), Scope{UserID: 9})
	if _, err := Require(scoped); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Errorf("tenantless scope = %v, want unauthenticated", err)
	}

	full := Put(context.Background(), Scope{TenantID: 42, UserID: 9, GateID: 3})
	got, err := Require(full)
	if err != nil {
		t.Fatalf("Require: %v", err)
	}
	if got.TenantID != 42 || got.UserID != 9 || got.SessionID != 0 {
		t.Errorf("scope = %+v", got)
	}
}

func TestGetMissesWhenAbsent(t *testing.T) {
	if _, ok := Get(context.Background()); ok {
		t.Error("scope found where none was put")
	}
}

func TestHasGate(t *testing.T) {
	if (Scope{SiteID: 4}).HasGate() {
		t.Error("a site alone is not a gate")
	}
	if !(Scope{GateID: 4}).HasGate() {
		t.Error("GateID 4 reported no gate")
	}
}
