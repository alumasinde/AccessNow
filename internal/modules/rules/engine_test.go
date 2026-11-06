package rules

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"

	"accessnow/internal/core/movements"
)

// The rulebook's whole promise is that a rule means what the sentence on the desk says it
// means, so these tests read like the sentences a site would write: nobody in after ten at
// night, five loads a day per plate, the loading bay gate only for goods.

// tenOnAThursday is the clock every case is judged against: Thursday 5 November 2026,
// 22:15 UTC. weekday 4 is Thursday, which is the number a working-week rule has to get right.
var tenOnAThursday = time.Date(2026, 11, 5, 22, 15, 0, 0, time.UTC)

func factsAt(change func(*Facts)) Facts {
	f := Facts{
		Now: tenOnAThursday, Loc: time.UTC,
		Direction: "in", Category: "visitor", Module: "visitor", Status: "approved",
		PassNo: "V-100", Subject: "Grace Njeri", Site: 1, Gate: 7,
	}
	if change != nil {
		change(&f)
	}
	return f
}

// leaf is one condition, written the way the desk writes it. empty and not_empty take no
// value, so the value is optional here too.
func leaf(field, op string, value ...string) string {
	if len(value) == 0 {
		return `{"field":"` + field + `","op":"` + op + `"}`
	}
	return `{"field":"` + field + `","op":"` + op + `","value":` + value[0] + `}`
}

func TestConditionLeaves(t *testing.T) {
	tests := []struct {
		name  string
		conds string
		facts func(*Facts)
		want  bool
	}{
		{"after nine", leaf("hour", "gt", "21"), nil, true},
		{"before six", leaf("hour", "lt", "6"), nil, false},
		{"weekday is thursday", leaf("weekday", "in", "[6,7]"), nil, false},
		{"clock compares as text", leaf("clock", "gte", `"22:00"`), nil, true},
		{"clock under", leaf("clock", "lt", `"22:00"`), nil, false},
		{"date of the scan", leaf("date", "eq", `"2026-11-05"`), nil, true},
		{"direction", leaf("direction", "eq", `"in"`), nil, true},
		{"direction wrong", leaf("direction", "ne", `"in"`), nil, false},
		{"category in list", leaf("category", "in", `["goods","visitor"]`), nil, true},
		{"category not in list", leaf("category", "not_in", `["goods","staff"]`), nil, true},
		{"name contains", leaf("subject", "contains", `"grace"`), nil, true},
		{"purpose does not contain", leaf("purpose", "not_contains", `"audit"`),
			func(f *Facts) { f.Purpose = "site tour" }, true},
		{"gate id", leaf("gate", "eq", "7"), nil, true},
		{"entries reached", leaf("entries", "gte", "3"),
			func(f *Facts) { f.Entries = 3 }, true},
		{"value under", leaf("value", "lt", "1000"),
			func(f *Facts) { v := 480.0; f.Value = &v }, true},
		{"status", leaf("status", "in", `["approved","on_site"]`), nil, true},

		// A field the scan never carried is not a field that was left empty. These two are the
		// difference between a rule about a plate and a rule about every pass.
		{"missing plate is not in a list", leaf("plate", "not_in", `["KCA111A"]`), nil, false},
		{"missing plate has nothing to contain", leaf("plate", "contains", `"kca"`), nil, false},
		{"missing plate is empty", leaf("plate", "empty"), nil, true},
		{"present plate is not empty", leaf("plate", "not_empty"),
			func(f *Facts) { f.Plate = "KCA111A" }, true},
		{"zero value is missing", leaf("value", "gt", "0"), nil, false},
		{"a field nobody wrote about", `{"field":"wingspan","op":"eq","value":2}`, nil, false},
		{"text against a number", leaf("hour", "eq", `"late"`), nil, false},
		{"unknown operator", leaf("hour", "between", "[1,2]"), nil, false},
	}
	for _, tc := range tests {
		var tree Cond
		if err := json.Unmarshal([]byte(tc.conds), &tree); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := tree.matches(factsAt(tc.facts)); got != tc.want {
			t.Errorf("%s: matched = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestConditionTrees(t *testing.T) {
	tests := []struct {
		name  string
		conds string
		facts func(*Facts)
		want  bool
	}{
		{"all of two", `{"all":[` + leaf("hour", "gt", "21") + `,` +
			leaf("direction", "eq", `"in"`) + `]}`, nil, true},
		{"all with one false", `{"all":[` + leaf("hour", "gt", "21") + `,` +
			leaf("direction", "eq", `"out"`) + `]}`, nil, false},
		{"any of two", `{"any":[` + leaf("hour", "gt", "23") + `,` +
			leaf("category", "eq", `"visitor"`) + `]}`, nil, true},
		{"any with none", `{"any":[` + leaf("hour", "gt", "23") + `,` +
			leaf("category", "eq", `"goods"`) + `]}`, nil, false},
		{"nested", `{"all":[` + leaf("direction", "eq", `"in"`) +
			`,{"any":[` + leaf("category", "eq", `"goods"`) + `,` +
			leaf("hour", "gte", "22") + `]}]}`, nil, true},

		// An empty branch is a mistake at the desk. Letting it match would file a rule that
		// stops every scan, which is the worst surprise a barrier can hold.
		{"empty all matches nothing", `{"all":[]}`, nil, false},
		{"empty any matches nothing", `{"any":[]}`, nil, false},
	}
	for _, tc := range tests {
		var tree Cond
		if err := json.Unmarshal([]byte(tc.conds), &tree); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := tree.matches(factsAt(tc.facts)); got != tc.want {
			t.Errorf("%s: matched = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// filed is one rule as the walk meets it: the row, and the tree the desk wrote.
func filed(t *testing.T, id int64, action Action, priority int, failure, payload,
	conds string) candidate {
	t.Helper()
	msg := failure
	r := Rule{
		ID: id, Name: "rule " + strconv.FormatInt(id, 10), Action: string(action),
		Priority: priority, Conditions: []byte(conds), Payload: []byte(payload),
		IsActive: true,
	}
	if failure != "" {
		r.FailureMsg = &msg
	}
	var tree Cond
	if err := json.Unmarshal([]byte(conds), &tree); err != nil {
		t.Fatalf("rule %d: %v", id, err)
	}
	return candidate{rule: r, tree: tree}
}

func nightRule(t *testing.T, id int64, action Action, priority int) candidate {
	t.Helper()
	return filed(t, id, action, priority, "No entry after 22:00", "",
		leaf("hour", "gte", "22"))
}

func TestEvaluateFirstMatchWins(t *testing.T) {
	night := nightRule(t, 11, ActionBlock, 10)
	loadingBay := filed(t, 12, ActionBlock, 20, "Use the delivery gate", "",
		leaf("gate", "eq", "9"))

	v := evaluate([]candidate{night, loadingBay}, factsAt(nil), nil)
	if v.refusal == nil {
		t.Fatal("two matching blocks and no refusal")
	}
	if v.refusal.rule.ID != 11 {
		t.Errorf("refused by rule %d, want the lowest priority (11)", v.refusal.rule.ID)
	}
	if v.refusal.message != "No entry after 22:00" {
		t.Errorf("reason = %q, want the rule's own sentence", v.refusal.message)
	}
	if len(v.firings) != 1 {
		t.Errorf("walked past the first block: %d firings, want 1", len(v.firings))
	}
}

func TestEvaluateActions(t *testing.T) {
	tests := []struct {
		name        string
		candidates  []candidate
		limits      Limits
		facts       func(*Facts)
		wantRefuse  int64
		wantWarn    int64
		wantFirs    int
		wantNotices int
	}{
		{
			name: "a warning lets the scan through",
			candidates: []candidate{
				nightRule(t, 1, ActionWarn, 10),
			},
			wantWarn: 1, wantFirs: 1,
		},
		{
			name: "a warning does not stop a later block",
			candidates: []candidate{
				nightRule(t, 1, ActionWarn, 10),
				filed(t, 2, ActionBlock, 20, "Goods only", "", leaf("category", "eq", `"visitor"`)),
			},
			wantRefuse: 2, wantWarn: 1, wantFirs: 2,
		},
		{
			name: "a notice never ends the walk",
			candidates: []candidate{
				nightRule(t, 1, ActionNotify, 10),
				nightRule(t, 2, ActionBlock, 20),
			},
			wantRefuse: 2, wantFirs: 2, wantNotices: 1,
		},
		{
			// This is the pair that makes priorities mean something. The walk runs lowest
			// number first and stops at the first rule that answers, so a waiver filed before
			// a block is how a site lifts its own restriction for its own staff.
			name: "an allow reached before a block lifts it",
			candidates: []candidate{
				nightRule(t, 2, ActionAllow, 5),
				nightRule(t, 1, ActionBlock, 10),
			},
			wantFirs: 1,
		},
		{
			name: "an allow reached after a block never gets there",
			candidates: []candidate{
				nightRule(t, 1, ActionBlock, 10),
				nightRule(t, 2, ActionAllow, 60),
			},
			wantRefuse: 1, wantFirs: 1,
		},
		{
			name: "inside the allowance spends it",
			candidates: []candidate{
				filed(t, 3, ActionLimit, 10, "Five loads a day",
					`{"limit":5,"period":"day","by":"plate"}`, leaf("category", "eq", `"goods"`)),
			},
			facts:    func(f *Facts) { f.Category = "goods"; f.Plate = "KCA111A" },
			limits:   Limits{3: 2},
			wantFirs: 1,
		},
		{
			name: "past the allowance refuses",
			candidates: []candidate{
				filed(t, 3, ActionLimit, 10, "Five loads a day",
					`{"limit":5,"period":"day","by":"plate"}`, leaf("category", "eq", `"goods"`)),
			},
			facts:      func(f *Facts) { f.Category = "goods"; f.Plate = "KCA111A" },
			limits:     Limits{3: 5},
			wantRefuse: 3,
			wantFirs:   1,
		},
		{
			// A counted rule with nothing to count is a mis-filed rule. It must not become a
			// gate that stops working because somebody left a limit box empty.
			name: "a counted rule with no plate lets the scan through",
			candidates: []candidate{
				filed(t, 3, ActionLimit, 10, "Five loads a day",
					`{"limit":5,"period":"day","by":"plate"}`, leaf("hour", "gte", "0")),
			},
			wantFirs: 1,
		},
		{
			name: "a rule the schema allows and this engine does not know is noted",
			candidates: []candidate{
				nightRule(t, 1, ActionField, 10),
				nightRule(t, 2, ActionBlock, 20),
			},
			wantRefuse: 2, wantFirs: 2,
		},
		{
			name:       "nothing matches and the rulebook says nothing",
			candidates: []candidate{nightRule(t, 1, ActionBlock, 10)},
			facts:      func(f *Facts) { f.Now = time.Date(2026, 11, 5, 10, 15, 0, 0, time.UTC) },
		},
	}

	for _, tc := range tests {
		v := evaluate(tc.candidates, factsAt(tc.facts), tc.limits)
		gotRefuse, gotWarn := int64(0), int64(0)
		if v.refusal != nil {
			gotRefuse = v.refusal.rule.ID
		}
		if v.warning != nil {
			gotWarn = v.warning.rule.ID
		}
		if gotRefuse != tc.wantRefuse {
			t.Errorf("%s: refused by %d, want %d", tc.name, gotRefuse, tc.wantRefuse)
		}
		if gotWarn != tc.wantWarn {
			t.Errorf("%s: warned by %d, want %d", tc.name, gotWarn, tc.wantWarn)
		}
		if len(v.firings) != tc.wantFirs {
			t.Errorf("%s: %d firings, want %d", tc.name, len(v.firings), tc.wantFirs)
		}
		if len(v.notices) != tc.wantNotices {
			t.Errorf("%s: %d notices, want %d", tc.name, len(v.notices), tc.wantNotices)
		}
	}
}

func TestQuotaBuckets(t *testing.T) {
	rule := Rule{ID: 3, Action: string(ActionLimit), Payload: []byte(`{"limit":5,"period":"day","by":"plate"}`)}

	quota, ok := quotaOf(rule, factsAt(func(f *Facts) { f.Plate = "KCA111A" }))
	if !ok {
		t.Fatal("a rule with a limit and a plate has something to count")
	}
	if quota.Key != "plate:KCA111A" || quota.Period != "day" || quota.Limit != 5 {
		t.Errorf("quota = %+v, want plate:KCA111A per day of 5", quota)
	}
	if want := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC); !quota.Start.Equal(want) {
		t.Errorf("bucket opens at %v, want midnight of the scan day", quota.Start)
	}

	// Nothing to count is a mis-filed rule, not a refusal: the walk has to keep answering.
	if _, ok := quotaOf(rule, factsAt(nil)); ok {
		t.Error("a counted rule with no plate got a bucket anyway")
	}

	byRequester := Rule{ID: 4, Action: string(ActionLimit),
		Payload: []byte(`{"limit":2,"period":"week","by":"requester"}`)}
	weekly, ok := quotaOf(byRequester, factsAt(func(f *Facts) { f.Requester = 18 }))
	if !ok {
		t.Fatal("a rule about a requester has something to count")
	}
	// Weeks open on Monday so that "ten visits a week" is the same week the site means.
	if want := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC); !weekly.Start.Equal(want) {
		t.Errorf("week bucket opens at %v, want the Monday", weekly.Start)
	}
	if weekly.Key != "requester:18" {
		t.Errorf("bucket key = %q, want requester:18", weekly.Key)
	}
}

func TestFactsFromTheGate(t *testing.T) {
	var value = 1500.0
	q := movements.PolicyQuery{
		ScreenQuery: movements.ScreenQuery{
			TenantID: 1, SiteID: 2, GateID: 7, ActorUserID: 3, PassNo: "V-100",
			CategoryKey: "goods", Direction: movements.DirectionOut, SubjectName: "Grace",
			Phone: "+254700111222", Plate: "KCA111A",
		},
		PassTypeID: 4, SourceModuleKey: "material", Status: "on_site", Origin: "pre_registered",
		Purpose: "deliveries", DepartmentID: 9, RequesterUserID: 18, HostUserID: 22,
		ValueAmount: &value, EntryCount: 2, At: tenOnAThursday, Timezone: "UTC",
	}
	f := factsOf(q, tenOnAThursday, time.UTC)
	if f.Direction != "out" || f.Module != "material" || f.Status != "on_site" {
		t.Errorf("facts = %+v, want the scan's own direction, module and status", f)
	}
	if f.Plate != "KCA111A" || f.Subject != "Grace" || f.PassNo != "V-100" {
		t.Errorf("facts lost what the guard was looking at: %+v", f)
	}
	if f.Gate != 7 || f.Department != 9 || f.Requester != 18 || f.Host != 22 || f.Entries != 2 {
		t.Errorf("facts lost the ids: %+v", f)
	}
	if got := f.lookup("clock"); got.text != "22:15" {
		t.Errorf("clock = %q, want the site's own 22:15", got.text)
	}
	if got := f.lookup("weekday"); got.number != 4 {
		t.Errorf("weekday = %v, want 4 for a Thursday", got.number)
	}
}

func TestValidateConditionTree(t *testing.T) {
	good := []string{
		leaf("hour", "gte", "22"),
		`{"all":[` + leaf("direction", "eq", `"in"`) + `,` + leaf("gate", "eq", "9") + `]}`,
		`{"any":[` + leaf("plate", "empty") + `,` + leaf("plate", "in", `["KCA111A"]`) + `]}`,
	}
	for _, raw := range good {
		if err := Validate(json.RawMessage(raw)); err != nil {
			t.Errorf("%s refused: %v", raw, err)
		}
	}

	tests := []struct {
		name  string
		conds string
	}{
		{"nothing to match", `{}`},
		{"not a tree", `[{"field":"hour"}]`},
		{"an empty branch", `{"all":[]}`},
		{"a field nobody carries", leaf("wingspan", "eq", "2")},
		{"an operator nobody has", leaf("hour", "between", "[1,2]")},
		{"in with nothing in it", leaf("category", "in", `[]`)},
		{"in with a value that is not a list", leaf("category", "in", `"goods"`)},
		{"contains with no text", leaf("subject", "contains", `""`)},
		{"eq with no value", `{"field":"hour","op":"eq"}`},
		{"a branch and a leaf in one node",
			`{"field":"hour","op":"gt","value":1,"all":[` + leaf("gate", "eq", "9") + `]}`},
		{"deeper than the engine walks", deeplyNested(maxDepth + 1)},
	}
	for _, tc := range tests {
		if err := Validate(json.RawMessage(tc.conds)); err == nil {
			t.Errorf("%s was accepted", tc.name)
		}
	}
}

// deeplyNested builds the smallest tree that is one level past the ceiling, so the ceiling is
// what fails and not the leaf inside it.
func deeplyNested(depth int) string {
	tree := leaf("gate", "eq", "9")
	for i := 1; i < depth; i++ {
		tree = `{"any":[` + tree + `]}`
	}
	return tree
}

func TestActionAndStagePredicates(t *testing.T) {
	if ActionField.Bites() || !ActionBlock.Bites() {
		t.Error("require_field is the one action a barrier cannot carry")
	}
	if !ActionLimit.Refuses() || ActionNotify.Refuses() || ActionWarn.Refuses() {
		t.Error("which actions refuse has to be the actions that stop a scan")
	}
	if !StageScan.StageAsked() || !StageBoth.StageAsked() {
		t.Error("the gate walks scan and both")
	}
	if StageRequest.StageAsked() || StageNightly.StageAsked() {
		t.Error("nothing evaluates request or nightly rules yet")
	}
	if !Action("require_field").Valid() || Action("detonate").Valid() {
		t.Error("action validity follows the schema's own CHECK")
	}
}

func TestRuleReasonAndLive(t *testing.T) {
	named := Rule{ID: 1, Name: "Night close", Action: string(ActionBlock), IsActive: true}
	if named.Reason() != "Night close" {
		t.Errorf("reason = %q, want the rule's name when it wrote no sentence", named.Reason())
	}
	written := "Use the delivery gate"
	shown := Rule{ID: 2, Name: "R2", FailureMsg: &written, IsActive: true}
	if shown.Reason() != written {
		t.Errorf("reason = %q, want the sentence the desk wrote", shown.Reason())
	}

	later := tenOnAThursday.Add(time.Hour)
	paused := Rule{ID: 3, IsActive: false}
	if paused.Live(later) {
		t.Error("a paused rule is not live")
	}
	notYet := Rule{ID: 4, IsActive: true, EffectiveAt: &later}
	if notYet.Live(tenOnAThursday) {
		t.Error("a rule whose own window has not opened is not live")
	}
	shut := Rule{ID: 5, IsActive: true, UntilAt: &tenOnAThursday}
	if shut.Live(tenOnAThursday) {
		t.Error("a rule whose window has shut is not live")
	}
	retired := Rule{ID: 6, IsActive: true, DeletedAt: &tenOnAThursday}
	if retired.Live(later) {
		t.Error("a retired rule is not live")
	}
}
