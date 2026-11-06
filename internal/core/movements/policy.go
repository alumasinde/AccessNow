package movements

import (
	"context"
	"time"

	"accessnow/internal/core/passes"
	"accessnow/internal/platform/tenancy"
)

// Policy is the seam a rules engine plugs into. Core asks it what the tenant's own policy
// says about a scan that is about to be written, and never learns which rules, conditions or
// counters are behind the answer.
//
// It is a second seam next to Screener rather than a field on it because the two answers are
// not the same kind of thing. A list says "this person is refused"; a rulebook says "nobody
// enters this gate after ten at night", and it can also say the scan is fine but worth a
// warning. The dependency runs one way, exactly as it does for screening: the module reads
// Core's pass row and writes its own, Core holds an interface and no import, and a tenant
// with rules switched off gets a zero Decision from an unbound guard and is judged on the
// rules Core has always owned.
type Policy interface {
	// Decide is asked before the movement is written. Like screening, an error is not
	// "allow": a rulebook that could not be read is a gate that stays shut, because the
	// alternative is a tenant whose restrictions stop working the moment their database
	// has a bad afternoon.
	Decide(ctx context.Context, q PolicyQuery) (Decision, error)

	// Note is told what the gate finally did, after the movement is committed. This is where
	// the rulebook keeps its own log of every rule that fired, spends any quota it counted,
	// and raises the notice about a waiver, so it happens once the guard already has his
	// colour and an error here is logged, never shown.
	Note(ctx context.Context, q PolicyQuery, d Decision, ref HitRef) error
}

// PolicyQuery is one scan asked of the rulebook. It carries the barrier's own facts inside a
// ScreenQuery and adds what the pass row knows, because a condition about a category, a
// declared value or a department is a fact about the pass, not about the gate.
//
// It is a snapshot of the scan rather than a live row: a rule that reads a value must read the
// value the guard was looking at, not whatever a desk wrote to the pass two seconds later.
type PolicyQuery struct {
	ScreenQuery

	PassTypeID      int64
	SourceModuleKey string
	Status          string
	Origin          string
	Purpose         string
	DepartmentID    int64
	RequesterUserID int64
	HostUserID      int64
	ValueAmount     *float64
	EntryCount      int

	// At is when the decision was asked for, and Timezone is the name of the place it was
	// asked in. A rule that says "after ten at night" means ten at night in Nairobi, not ten
	// in the hours of whichever server answered.
	At       time.Time
	Timezone string

	// Override is the guard's second attempt. Requested is true when the tablet has asked for
	// the refusal to be waived and Reason is the sentence it had to type; whether the person
	// holding the scanner is allowed to ask at all is not Core's business, it is the rule
	// book's, because the rule that refused is the one that says whether it can be waived.
	Override struct {
		Requested bool
		Reason    string
	}
}

// Decision is the rulebook's answer, and only the three fields Core reads. A zero Decision
// means the rules had nothing to say, which is the common case: a tenant with the module
// switched on and no rules filed gets the gate it had the day before.
type Decision struct {
	// Result is the colour the movement row is filed under. It is left empty when the rules
	// agree with what Core already decided; Denied, Warned and Override are the three ways
	// they do not. Override is the module's answer to a waiver it checked and accepted: the
	// words that waived it are already on the scan, so the movement row keeps the human's
	// reason and the rulebook's log keeps the rule's.
	Result Result
	Reason string

	// Fired is the audit the module owes itself: which rules matched, in what order, and what
	// each one asked for. Core reads none of it.
	Fired []Firing
}

// Firing is one rule that matched, named by what it is rather than by how it is stored, so
// the gate's answer never has to know about condition trees or counter buckets.
type Firing struct {
	RuleID   int64
	RuleName string
	Action   string
	Outcome  string
	Message  string
	// Quota is the counted rule's bucket, when the rule counts anything at all. Note spends it
	// only after the movement it decided is on file.
	Quota *Quota
}

// Quota is the one stateful thing a rule can ask for: how many of something a subject is
// allowed inside a period, and which bucket of that allowance this scan belongs to. Core
// carries it without reading it - it is the rulebook's own bookkeeping, and Note is where it
// gets spent.
type Quota struct {
	RuleID int64
	Key    string
	Period string
	Start  time.Time
	// Limit is how big the allowance was when the bucket was named, so the row the log opens
	// records the rule as it read at the time rather than as it reads after the next edit.
	Limit int
	// Spent is true when the scan is inside the allowance and so owes the bucket an increment.
	Spent bool
}

// policyQueryOf builds the rulebook's question out of the pass, the scan and the session. The
// name on the scan wins over the name on the pass for the same reason it does in queryOf: the
// guard is the one reading a face or a document.
func policyQueryOf(scope tenancy.Scope, pass passes.Pass, scan Scan, at time.Time) PolicyQuery {
	q := PolicyQuery{
		ScreenQuery:     queryOf(scope, pass, scan),
		PassTypeID:      pass.PassTypeID,
		SourceModuleKey: pass.SourceModuleKey,
		Status:          pass.Status,
		Origin:          pass.Origin,
		Purpose:         derefString(pass.Purpose),
		DepartmentID:    derefInt64(pass.DepartmentID),
		RequesterUserID: derefInt64(pass.RequesterUserID),
		HostUserID:      derefInt64(pass.HostUserID),
		ValueAmount:     pass.ValueAmount,
		EntryCount:      pass.EntryCount,
		At:              at,
		Timezone:        scope.Timezone,
	}
	q.Override.Requested = scan.Override
	q.Override.Reason = scan.Reason
	return q
}

func derefInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}
