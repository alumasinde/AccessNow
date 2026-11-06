package movements

import (
	"context"

	"accessnow/internal/core/passes"
	"accessnow/internal/platform/tenancy"
)

// Screener is the seam a screening list plugs into. Core asks it the one question a barrier
// cannot answer for itself - is this person or vehicle on a list that says no - and never
// learns which module, table or list type is behind the answer.
//
// The dependency runs one way only: the module reads Core's pass row and writes its own
// rows, while Core holds an interface and no import. That is what keeps the module switch
// true: a tenant with screening switched off gets an empty verdict from an unbound guard,
// and every barrier on their site keeps working exactly as it did the day before.
type Screener interface {
	// Screen is asked before the movement is written, because its answer is the colour of
	// the light. An error is not "allow": a list that could not be read is a gate that must
	// stay shut, and the guard is told which of the two it is looking at.
	Screen(ctx context.Context, q ScreenQuery) (Verdict, error)

	// Note is told what the gate finally did, after the movement is committed. It is where
	// a list keeps its own record of the hit and raises the quiet alarm, so it happens once
	// the guard already has his answer and an error here is logged, never shown.
	Note(ctx context.Context, verdict Verdict, ref HitRef) error
}

// ScreenQuery is the whole of what a barrier knows about the thing in front of it. It is
// deliberately narrow: only facts that are on the pass row or on the scan itself. An
// identifier that lives in another module's table - a national number in the visitor
// register, a badge in the staff one - belongs to that module's own filing screen, not here,
// because reaching across for it would tie two module lifecycles together.
type ScreenQuery struct {
	TenantID      int64
	SiteID        int64
	GateID        int64
	GateSessionID int64
	ActorUserID   int64

	PassID      int64
	PassNo      string
	CategoryKey string
	Direction   Direction

	SubjectName string
	Phone       string
	Email       string
	Plate       string
}

// Match is one list entry that the query hit, and only the address of it: the list it sits
// on, the kind of identifier that matched and the value that matched. Note re-reads the
// entry for everything else, which keeps this type about the match rather than about the
// module's schema.
type Match struct {
	EntryID    int64
	ListType   string
	MatchType  string
	MatchValue string
}

// Verdict is the answer a screener gives the gate. Blocked and Reason are the only two
// fields Core reads; Matches is carried so that the same screener can write its own record
// of the hit without being asked a second time.
type Verdict struct {
	Blocked bool
	Reason  string
	Matches []Match
}

// HitRef is what the gate did with a verdict, and the rows that say so. It carries the
// whole scan identity rather than just the movement id because the list keeps its own
// record of the hit, and a record that cannot name the gate or the pass is not evidence.
type HitRef struct {
	MovementID    int64
	TenantID      int64
	SiteID        int64
	GateID        int64
	GateSessionID int64
	ActorUserID   int64
	PassID        int64
	PassNo        string
	SubjectName   string
	Result        Result
	Reason        string
}

// queryOf builds the screening question out of the pass and the scan. The name on the scan
// wins over the name on the pass because the guard is the one reading a face or a document,
// and the plate only ever comes from the scan.
func queryOf(scope tenancy.Scope, pass passes.Pass, scan Scan) ScreenQuery {
	name := firstOf(scan.SubjectName, pass.SubjectName)
	return ScreenQuery{
		TenantID: scope.TenantID, SiteID: pass.SiteID, GateID: scope.GateID,
		GateSessionID: scope.GateShiftID, ActorUserID: scope.UserID,
		PassID: pass.ID, PassNo: pass.PassNo, CategoryKey: pass.CategoryKey,
		Direction: scan.Direction, SubjectName: name,
		Phone: derefString(pass.SubjectPhone), Email: derefString(pass.SubjectEmail),
		Plate: scan.Plate,
	}
}

func derefString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
