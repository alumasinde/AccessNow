// Package movements' seam for the rules a module keeps about its own passes. It sits next to
// Screener and Policy and is bound the same way: Core holds an interface, the server hands it
// an implementation, and a tenant with the module off gets an empty answer and the gate it had
// before.
package movements

import (
	"context"
	"time"

	"accessnow/internal/core/passes"
	"accessnow/internal/platform/tenancy"
)

// Eligibility is asked about a pass before a barrier opens for it. It exists for the rules that
// are neither Core's facts about the pass row nor the tenant's general rulebook: a contractor's
// rota and the expiry date on the permit that lets them on site are facts about one module's
// pass, and only that module can read them.
//
// An error is not "allow", for the same reason it is not for the rulebook: a module that cannot
// answer is a gate that must stay shut, and the guard is told which of the two it is looking at.
// A module with no opinion - because the pass is not its own - answers with an empty string.
type Eligibility interface {
	Check(ctx context.Context, q EligibilityQuery) (string, error)
}

// EligibilityQuery is one pass presented at one gate, with the barrier's own facts on it. It is
// narrow on purpose: a module must not have to ask Core for anything else to answer, or the
// seam becomes a second copy of the repository.
type EligibilityQuery struct {
	TenantID      int64
	SiteID        int64
	GateID        int64
	GateSessionID int64
	ActorUserID   int64

	PassID          int64
	PassNo          string
	SourceModuleKey string
	CategoryKey     string
	Direction       Direction

	// At is when the barrier asked, in UTC, and Timezone is the site's own clock. A rota that
	// says "07:00" means seven where the gate is.
	At       time.Time
	Timezone string
}

// EligibilityGuard is the handle Core holds while the module that will answer is still being
// built. Empty it says nothing, and the barrier is judged on Core's own facts alone.
type EligibilityGuard struct{ check Eligibility }

func NewEligibilityGuard() *EligibilityGuard { return &EligibilityGuard{} }

// Bind hands the guard the module that answers for its own passes.
func (g *EligibilityGuard) Bind(check Eligibility) { g.check = check }

// Check reports the module's refusal, or an empty string when it has no objection. The question
// is only worth a query for the module the pass belongs to, so the source module key travels
// with it and an implementation can return at once for anything it did not issue.
func (g *EligibilityGuard) Check(ctx context.Context, q EligibilityQuery) (string, error) {
	if g.check == nil {
		return "", nil
	}
	return g.check.Check(ctx, q)
}

// eligibilityQueryOf builds the module's question from the pass and the scan.
func eligibilityQueryOf(scope tenancy.Scope, pass passes.Pass, scan Scan) EligibilityQuery {
	at := time.Now().UTC()
	if scan.OccurredAt.After(time.Time{}) && !scan.OccurredAt.After(at.Add(time.Minute)) {
		// A tablet that replayed a queue says when it really scanned, and a rota has to be
		// judged against that clock rather than the moment the link came back.
		at = scan.OccurredAt.UTC()
	}
	return EligibilityQuery{
		TenantID: scope.TenantID, SiteID: pass.SiteID, GateID: scope.GateID,
		GateSessionID: scope.GateShiftID, ActorUserID: scope.UserID,
		PassID: pass.ID, PassNo: pass.PassNo, SourceModuleKey: pass.SourceModuleKey,
		CategoryKey: pass.CategoryKey, Direction: scan.Direction,
		At: at, Timezone: scope.Timezone,
	}
}
