package passes

import (
	"testing"
	"time"
)

func TestWithinWindow(t *testing.T) {
	from := time.Date(2026, 11, 5, 6, 0, 0, 0, time.UTC)
	until := from.Add(2 * time.Hour)
	openEnded := Pass{ValidFrom: from}
	bounded := Pass{ValidFrom: from, ValidUntil: &until}

	tests := []struct {
		name   string
		pass   Pass
		at     time.Time
		expect bool
	}{
		{"at the start", bounded, from, true},
		{"before the start", bounded, from.Add(-time.Minute), false},
		{"inside", bounded, from.Add(time.Hour), true},
		{"the last instant before expiry", bounded, until.Add(-time.Nanosecond), true},
		{"expiry is exclusive", bounded, until, false},
		{"after expiry", bounded, until.Add(time.Minute), false},
		{"open ended has no end", openEnded, until.Add(365 * 24 * time.Hour), true},
		{"open ended still has a start", openEnded, from.Add(-time.Nanosecond), false},
	}

	for _, tc := range tests {
		if got := tc.pass.WithinWindow(tc.at); got != tc.expect {
			t.Errorf("%s: WithinWindow(%v) = %v, want %v", tc.name, tc.at, got, tc.expect)
		}
	}
}

// A closed or voided pass is history: the desk must not offer a decision button for it.
func TestDecidable(t *testing.T) {
	closed := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)

	tests := []struct {
		name   string
		pass   Pass
		expect bool
	}{
		{"pending", Pass{Status: string(StatusPending)}, true},
		{"approved", Pass{Status: string(StatusApproved)}, true},
		{"on site", Pass{Status: string(StatusOnSite)}, true},
		{"void", Pass{Status: string(StatusVoid)}, false},
		{"completed", Pass{Status: string(StatusCompleted)}, false},
		{"closed at", Pass{Status: string(StatusActive), ClosedAt: &closed}, false},
	}
	for _, tc := range tests {
		if got := tc.pass.Decidable(); got != tc.expect {
			t.Errorf("%s: Decidable() = %v, want %v", tc.name, got, tc.expect)
		}
	}
}

// AwaitingDecision is the resurrection rule: an ask left unanswered in an inbox must not
// approve a pass that the desk has already cancelled, refused or closed.
func TestAwaitingDecision(t *testing.T) {
	tests := []struct {
		name   string
		status Status
		expect bool
	}{
		{"draft", StatusDraft, true},
		{"pending", StatusPending, true},
		{"pending review", StatusPendingReview, true},
		{"cancelled", StatusCancelled, false},
		{"expired", StatusExpired, false},
		{"rejected", StatusRejected, false},
		{"completed", StatusCompleted, false},
		{"already approved", StatusApproved, false},
	}
	for _, tc := range tests {
		if got := (Pass{Status: string(tc.status)}).AwaitingDecision(); got != tc.expect {
			t.Errorf("%s: AwaitingDecision() = %v, want %v", tc.name, got, tc.expect)
		}
	}
}

// Usable is the barrier rule: pending is a request and completed is history, so a gate
// must turn red for both, in either direction. Otherwise an unapproved lorry drives out
// and a closed pass leaves the site twice.
func TestUsable(t *testing.T) {
	tests := []struct {
		status Status
		want   bool
	}{
		{StatusApproved, true},
		{StatusActive, true},
		{StatusOnSite, true},
		{StatusDraft, false},
		{StatusPending, false},
		{StatusRejected, false},
		{StatusCancelled, false},
		{StatusExpired, false},
		{StatusCompleted, false},
		{StatusReturned, false},
		{StatusOverdue, false},
		{StatusVoid, false},
	}
	for _, tc := range tests {
		pass := Pass{Status: string(tc.status)}
		if got := pass.Usable(); got != tc.want {
			t.Errorf("%q: Usable() = %v, want %v", tc.status, got, tc.want)
		}
	}
}

func TestValid(t *testing.T) {
	for _, status := range Allowed {
		if !Valid(status) {
			t.Errorf("%q rejected but it is in Allowed", status)
		}
	}
	for _, status := range []Status{"", "on_side", "Approved"} {
		if Valid(status) {
			t.Errorf("%q accepted but chk_passes_status would reject it", status)
		}
	}
}
