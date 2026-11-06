package approval

import (
	"testing"
	"time"

	"accessnow/internal/core/passes"
)

func TestFitsConditionalRouting(t *testing.T) {
	material := "material"
	otherType := int64(7)
	department := int64(4)
	high := 100000.0
	cap := 50000.0

	base := passes.Pass{SiteID: 1, PassTypeID: 6, CategoryKey: material, DepartmentID: &department}

	cases := []struct {
		name    string
		flow    Flow
		pass    passes.Pass
		outside bool
		want    bool
	}{
		{"a rule that says nothing matches anything", Flow{Status: "active"}, base, false, true},
		{"category narrows", Flow{Status: "active", CategoryKey: &material}, base, false, true},
		{"category of another kind does not", Flow{Status: "active", CategoryKey: strptr("visitor")},
			base, false, false},
		{"pass type narrows", Flow{Status: "active", PassTypeID: &otherType},
			base, false, false},
		{"site narrows", Flow{Status: "active", SiteID: int64ptr(2)}, base, false, false},
		{"department narrows", Flow{Status: "active", DepartmentID: int64ptr(9)}, base, false, false},
		{"after hours rule needs after hours", Flow{Status: "active", OutsideHoursOnly: true},
			base, false, false},
		{"after hours rule fires after hours", Flow{Status: "active", OutsideHoursOnly: true},
			base, true, true},
		{"value at the threshold is inside the band",
			Flow{Status: "active", MinValue: &high}, passes.Pass{ValueAmount: &high, SiteID: 1},
			false, true},
		{"value under the threshold is not",
			Flow{Status: "active", MinValue: &high},
			passes.Pass{SiteID: 1, ValueAmount: ptr(1200.0)}, false, false},
		{"an undeclared value never meets a band",
			Flow{Status: "active", MinValue: &high}, base, false, false},
		{"a ceiling excludes the expensive one",
			Flow{Status: "active", MaxValue: &cap}, passes.Pass{ValueAmount: &high}, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fits(tc.flow, tc.pass, tc.outside); got != tc.want {
				t.Errorf("fits() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestInactiveFlowsAreNotPickable covers the other half of the matcher: fit is about the
// rule's conditions, and whether the rule is still in force is a separate question the
// matcher asks first.
func TestInactiveFlowsAreNotPickable(t *testing.T) {
	if !(Flow{Status: "active"}).Active() {
		t.Error("an active flow must be pickable")
	}
	if (Flow{Status: "inactive"}).Active() {
		t.Error("an inactive flow must not be pickable")
	}
	if (Flow{Status: "active", DeletedAt: &time.Time{}}).Active() {
		t.Error("a deleted flow must not be pickable")
	}
}

func TestPassDecisionKeepsTheFourOutcomesApart(t *testing.T) {
	cases := []struct {
		name   string
		status RequestStatus
		in     Decision
		want   passes.Status
	}{
		{"approve", StatusApproved, Decision{Action: ActionApprove}, passes.StatusApproved},
		{"reject", StatusRejected, Decision{Action: ActionReject, Comment: "no"}, passes.StatusRejected},
		{"return for correction", StatusReturned,
			Decision{Action: ActionReturn, Comment: "add the ID"}, passes.StatusPendingReview},
		{"cancel by the requester", StatusCancelled, Decision{}, passes.StatusCancelled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, _ := passDecision(string(tc.status), tc.in)
			if status != tc.want {
				t.Errorf("passDecision(%q) = %q, want %q", tc.status, status, tc.want)
			}
		})
	}
}

func TestOutsideHours(t *testing.T) {
	// A SiteWindow built by hand carries no parsed location, so it reads its clock in UTC -
	// the same clock the window strings are written against here. SiteWindowFor is what turns
	// a site's timezone name into the location the comparison uses.
	day := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name  string
		open  string
		close string
		hour  int
		want  bool
	}{
		{"before the window", "08:00", "18:00", 7, true},
		{"inside the window", "08:00", "18:00", 12, false},
		{"the closing minute is outside", "08:00", "18:00", 18, true},
		{"a night shift is open at eleven", "20:00", "06:00", 23, false},
		{"and still open at three", "20:00", "06:00", 3, false},
		{"but not in its own afternoon", "20:00", "06:00", 13, true},
		{"an unparseable opening time means always open", "08.00", "18:00", 3, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			window := SiteWindow{SiteID: 1, Timezone: "UTC",
				OpensAt: strptr(tc.open), ClosesAt: strptr(tc.close)}
			if got := window.OutsideHours(day.Add(time.Duration(tc.hour) * time.Hour)); got != tc.want {
				t.Errorf("OutsideHours(%02d:00) = %v, want %v", tc.hour, got, tc.want)
			}
		})
	}

	// A site that never declared hours is treated as always open, so no after-hours rule
	// can quietly take over a tenant that has not configured one.
	if (SiteWindow{SiteID: 1}).OutsideHours(day) {
		t.Error("a site with no declared window must not read as after hours")
	}
}

func TestOutsideHoursHonoursTheWorkingDays(t *testing.T) {
	thursday := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	saturday := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		days *string
		at   time.Time
		want bool
	}{
		{"a weekday inside the window", strptr("mon-fri"), thursday, false},
		{"the same hour on a Saturday is shut", strptr("mon-fri"), saturday, true},
		{"six-day sites work Saturdays", strptr("mon-sat"), saturday, false},
		{"a weekend-only site is open Saturday", strptr("sat,sun"), saturday, false},
		{"and shut on Thursday", strptr("sat,sun"), thursday, true},
		{"a range through the weekend", strptr("fri-mon"), saturday, false},
		{"everyday needs no reading", strptr("all"), saturday, false},
		{"a blank spec is no restriction", strptr(""), saturday, false},
		{"a spec nobody can read is no restriction", strptr("weekdays"), saturday, false},
		{"no spec at all is no restriction", nil, saturday, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			window := SiteWindow{SiteID: 1, Timezone: "UTC", WorkingDays: tc.days,
				OpensAt: strptr("08:00"), ClosesAt: strptr("18:00")}
			if got := window.OutsideHours(tc.at); got != tc.want {
				t.Errorf("OutsideHours(%s %s) = %v, want %v",
					tc.at.Weekday(), tc.at.Format("15:04"), got, tc.want)
			}
		})
	}
}

func TestSnapshotSurvivesTheRoundTrip(t *testing.T) {
	steps := []FlowStep{
		{ID: 3, TenantID: 1, ApprovalFlowID: 9, StepNo: 1, Name: "Supervisor",
			ApproverType: ApproverRole, ApproverRoleKey: strptr("gate_supervisor"), Mode: ModeAnyOne},
		{ID: 4, TenantID: 1, ApprovalFlowID: 9, StepNo: 2, Name: "Manager",
			ApproverType: ApproverRole, ApproverRoleKey: strptr("site_manager"), Mode: ModeAnyOne,
			RequireComment: true},
	}
	raw, err := snapshotOf(steps)
	if err != nil {
		t.Fatalf("snapshotOf: %v", err)
	}
	req := Request{FlowSnapshot: []byte(raw)}
	back, err := req.Definition()
	if err != nil {
		t.Fatalf("Definition: %v", err)
	}
	if len(back) != 2 || back[1].Name != "Manager" || !back[1].RequireComment {
		t.Fatalf("the frozen steps came back wrong: %+v", back)
	}
	// Row metadata is not part of the story: a flow edited later must not move the chain.
	if back[0].ID != 0 || back[0].ApprovalFlowID != 0 || back[0].TenantID != 0 {
		t.Errorf("snapshot leaked live row ids: %+v", back[0])
	}
}

func TestAskCanAnswerIsOnlyAHintAboutTheAssignee(t *testing.T) {
	now := time.Now().UTC()
	role := "gate_supervisor"
	named := int64(17)

	cases := []struct {
		name   string
		ask    RequestStep
		viewer int64
		roles  []string
		want   bool
	}{
		{"a holder of the role asked", RequestStep{Status: string(StepPending), ApproverRoleKey: &role},
			1, []string{"tenant_admin", "gate_supervisor"}, true},
		{"somebody who does not hold it", RequestStep{Status: string(StepPending), ApproverRoleKey: &role},
			1, []string{"tenant_admin"}, false},
		{"the person it names", RequestStep{Status: string(StepPending), AssigneeUserID: &named},
			named, nil, true},
		{"not the person it names", RequestStep{Status: string(StepPending), AssigneeUserID: &named},
			1, []string{"gate_supervisor", "site_manager"}, false},
		{"an answered ask", RequestStep{Status: string(StepApproved), ApproverRoleKey: &role},
			1, []string{"gate_supervisor"}, false},
		{"an ask that names nobody", RequestStep{Status: string(StepPending)}, 1, []string{"guard"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			view := askOf(tc.ask, tc.viewer, tc.roles, now)
			if view.CanAnswer != tc.want {
				t.Errorf("can_answer = %v, want %v", view.CanAnswer, tc.want)
			}
			if !view.Waiting && view.CanAnswer {
				t.Error("an ask nobody is waiting on cannot be answerable")
			}
		})
	}
}

func strptr(value string) *string { return &value }

func int64ptr(value int64) *int64 { return &value }
