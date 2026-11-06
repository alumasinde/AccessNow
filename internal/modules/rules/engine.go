package rules

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"accessnow/internal/core/movements"
	"accessnow/internal/platform/errs"
)

// Cond is one node of a rule's condition tree. A node is either a branch - all of these, any
// of these - or a leaf that asks one field one thing. The tree is the whole reason a new kind
// of restriction is a row rather than a release: nothing about a rule's shape is schema.
type Cond struct {
	Field string          `json:"field,omitempty"`
	Op    string          `json:"op,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
	All   []Cond          `json:"all,omitempty"`
	Any   []Cond          `json:"any,omitempty"`
}

// The operators a leaf may use. The set is small on purpose: every one of them is a sentence a
// person writes on a whiteboard, and anything that needs arithmetic belongs in a new field the
// engine reads, not in an expression the desk has to type.
const (
	opEq          = "eq"
	opNe          = "ne"
	opIn          = "in"
	opNotIn       = "not_in"
	opGt          = "gt"
	opGte         = "gte"
	opLt          = "lt"
	opLte         = "lte"
	opContains    = "contains"
	opNotContains = "not_contains"
	opEmpty       = "empty"
	opNotEmpty    = "not_empty"
	maxDepth      = 6
	maxLeaves     = 40
)

var operators = []string{
	opEq, opNe, opIn, opNotIn, opGt, opGte, opLt, opLte, opContains, opNotContains,
	opEmpty, opNotEmpty,
}

// Fields is everything the engine can be asked about, in the words a condition uses for it.
// A field a scan does not carry is not the same as a field with nothing in it - see operand.
var fields = map[string]string{
	"direction":  "in or out, as the guard pointed the scanner",
	"category":   "the pass category key, e.g. visitor or goods",
	"module":     "which module filed the pass",
	"status":     "the pass status, e.g. approved or on_site",
	"origin":     "how the pass came to exist, e.g. walk_in or pre_registered",
	"pass_no":    "the number printed on the pass",
	"subject":    "the name on the scan, or on the pass when the scan has none",
	"phone":      "the subject's phone as stored on the pass",
	"email":      "the subject's email as stored on the pass",
	"plate":      "the plate read at the barrier",
	"purpose":    "what the pass says it is for",
	"site":       "the site id the pass belongs to",
	"gate":       "the gate id the scan is happening at",
	"department": "the department id on the pass",
	"type":       "the pass type id",
	"requester":  "the user id of whoever asked for the pass",
	"host":       "the user id of the host on the pass",
	"actor":      "the user id of the guard holding the scanner",
	"entries":    "how many times this pass has already been let in",
	"value":      "the declared value on the pass, in the tenant's currency",
	"hour":       "hour of the scan, 0-23, in the site's own clock",
	"weekday":    "day of the scan, 1 for Monday through 7 for Sunday",
	"date":       "the scan date as YYYY-MM-DD in the site's own clock",
	"clock":      "the scan time as HH:MM in the site's own clock",
}

// FieldNames is the desk's list of what it may write about.
func FieldNames() []string {
	out := make([]string, 0, len(fields))
	for name := range fields {
		out = append(out, name)
	}
	return sorted(out)
}

// FieldSpec is one field with the sentence that tells a desk what is in it. The descriptions
// are served rather than written into a form because a field nobody can name is a field
// nobody uses, and the alternative is a rule editor that guesses.
type FieldSpec struct {
	Key  string `json:"key"`
	What string `json:"what"`
}

// FieldSpecs is the whole vocabulary a condition may be written in, in a fixed order so the
// list a desk reads is the list it read last time.
func FieldSpecs() []FieldSpec {
	out := make([]FieldSpec, 0, len(fields))
	for name, what := range fields {
		out = append(out, FieldSpec{Key: name, What: what})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// OperatorNames is the set a leaf may use, in the order a form would group them.
func OperatorNames() []string { return append([]string(nil), operators...) }

// sorted is a copy that does not alias the caller's slice.
func sorted(names []string) []string {
	out := append([]string(nil), names...)
	sort.Strings(out)
	return out
}

// Facts is everything the engine is allowed to ask one scan about. It is a snapshot of the
// pass row and the scan as the gate saw them, not a live read: a rule about a declared value
// judges the value the guard was shown.
type Facts struct {
	Now time.Time
	Loc *time.Location

	Direction, Category, Module, Status, Origin          string
	PassNo, Subject, Phone, Email, Plate, Purpose        string
	Site, Gate, Department, Type, Requester, Host, Actor int64
	Entries                                              int
	Value                                                *float64
}

// factsOf turns the question Core asked into the vocabulary a condition is written in. The
// scan's own plate and name are already inside q.ScreenQuery, which is why they are read from
// there rather than from the pass.
func factsOf(q movements.PolicyQuery, now time.Time, loc *time.Location) Facts {
	return Facts{
		Now: now, Loc: loc,
		Direction: string(q.Direction), Category: q.CategoryKey, Module: q.SourceModuleKey,
		Status: q.Status, Origin: q.Origin, PassNo: q.PassNo, Subject: q.SubjectName,
		Phone: q.Phone, Email: q.Email, Plate: q.Plate, Purpose: q.Purpose,
		Site: q.SiteID, Gate: q.GateID, Department: q.DepartmentID, Type: q.PassTypeID,
		Requester: q.RequesterUserID, Host: q.HostUserID, Actor: q.ActorUserID,
		Entries: q.EntryCount, Value: q.ValueAmount,
	}
}

// operand is one field read out of the facts. kindMissing is not zero: a rule has to be able
// to tell "this scan carried no plate" from "the plate was read as KCA000A", and a comparison
// against a field the scan never carried must match nothing at all.
type kind int

const (
	kindMissing kind = iota
	kindText
	kindNumber
)

type operand struct {
	kind   kind
	text   string
	number float64
}

func textOf(value string) operand {
	if strings.TrimSpace(value) == "" {
		return operand{kind: kindMissing}
	}
	return operand{kind: kindText, text: value}
}

func numOf(value int64) operand {
	if value == 0 {
		return operand{kind: kindMissing}
	}
	return operand{kind: kindNumber, number: float64(value)}
}

func (f Facts) lookup(name string) operand {
	switch name {
	case "direction":
		return textOf(f.Direction)
	case "category":
		return textOf(f.Category)
	case "module":
		return textOf(f.Module)
	case "status":
		return textOf(f.Status)
	case "origin":
		return textOf(f.Origin)
	case "pass_no":
		return textOf(f.PassNo)
	case "subject":
		return textOf(f.Subject)
	case "phone":
		return textOf(f.Phone)
	case "email":
		return textOf(f.Email)
	case "plate":
		return textOf(f.Plate)
	case "purpose":
		return textOf(f.Purpose)
	case "site":
		return numOf(f.Site)
	case "gate":
		return numOf(f.Gate)
	case "department":
		return numOf(f.Department)
	case "type":
		return numOf(f.Type)
	case "requester":
		return numOf(f.Requester)
	case "host":
		return numOf(f.Host)
	case "actor":
		return numOf(f.Actor)
	case "entries":
		return numOf(int64(f.Entries))
	case "value":
		if f.Value == nil {
			return operand{kind: kindMissing}
		}
		return operand{kind: kindNumber, number: *f.Value}
	case "hour":
		return operand{kind: kindNumber, number: float64(f.clock().Hour())}
	case "weekday":
		// Monday is 1 because that is how a rule about a working week reads. Sunday is 7 for
		// the same reason: a site that closes at the weekend writes [6, 7], not [0, 6].
		return operand{kind: kindNumber, number: float64((int(f.clock().Weekday())+6)%7 + 1)}
	case "date":
		return textOf(f.clock().Format("2006-01-02"))
	case "clock":
		return textOf(f.clock().Format("15:04"))
	default:
		return operand{kind: kindMissing}
	}
}

// clock is the scan in the site's own clock, which is the only clock a rule about "after ten
// at night" can mean.
func (f Facts) clock() time.Time {
	loc := f.Loc
	if loc == nil {
		loc = time.UTC
	}
	return f.Now.In(loc)
}

// matches walks the tree. A branch with no children matches nothing rather than everything:
// an empty {"all": []} is a mistake at the desk, and a mistake that lets every scan through
// under a rule that was meant to stop some of them is the worst kind of surprise.
func (c Cond) matches(f Facts) bool {
	if len(c.All) > 0 {
		for _, child := range c.All {
			if !child.matches(f) {
				return false
			}
		}
		return true
	}
	if len(c.Any) > 0 {
		for _, child := range c.Any {
			if child.matches(f) {
				return true
			}
		}
		return false
	}
	return c.leaf(f)
}

func (c Cond) leaf(f Facts) bool {
	left := f.lookup(c.Field)
	switch c.Op {
	case opEmpty:
		return left.kind == kindMissing
	case opNotEmpty:
		return left.kind != kindMissing
	}
	if left.kind == kindMissing {
		return false
	}

	switch c.Op {
	case opEq, opNe:
		order, ok := compare(left, c.Value)
		if !ok {
			return false
		}
		return order == 0 == (c.Op == opEq)
	case opGt, opGte, opLt, opLte:
		order, ok := compare(left, c.Value)
		if !ok {
			return false
		}
		switch c.Op {
		case opGt:
			return order > 0
		case opGte:
			return order >= 0
		case opLt:
			return order < 0
		default:
			return order <= 0
		}
	case opIn, opNotIn:
		var items []json.RawMessage
		if json.Unmarshal(c.Value, &items) != nil {
			return false
		}
		found := false
		for _, item := range items {
			if order, ok := compare(left, item); ok && order == 0 {
				found = true
				break
			}
		}
		return found == (c.Op == opIn)
	case opContains, opNotContains:
		needle, ok := asText(left, c.Value)
		if !ok || left.kind != kindText {
			return false
		}
		found := strings.Contains(strings.ToLower(left.text), strings.ToLower(needle))
		return found == (c.Op == opContains)
	default:
		return false
	}
}

// compare orders a field against a literal. Numbers compare as numbers - so a value of 9 is
// under a value of 10 - and everything else compares case-folded as text. A literal that
// cannot take the field's type is a condition that does not match, never a scan that is wrong.
func compare(left operand, raw json.RawMessage) (int, bool) {
	if left.kind == kindNumber {
		right, ok := asNumber(raw)
		if !ok {
			return 0, false
		}
		switch {
		case left.number < right:
			return -1, true
		case left.number > right:
			return 1, true
		default:
			return 0, true
		}
	}
	right, ok := asText(left, raw)
	if !ok {
		return 0, false
	}
	return strings.Compare(strings.ToLower(left.text), strings.ToLower(right)), true
}

func asNumber(raw json.RawMessage) (float64, bool) {
	var out float64
	if json.Unmarshal(raw, &out) == nil {
		return out, true
	}
	// A number typed as a string still counts, because a desk that quotes its values should
	// not get a rule that never fires.
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return 0, false
	}
	value, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil {
		return 0, false
	}
	return value, true
}

// asText is the other direction: the literal as the field's own type of text.
func asText(left operand, raw json.RawMessage) (string, bool) {
	if left.kind == kindNumber {
		return "", false
	}
	var out string
	if json.Unmarshal(raw, &out) == nil {
		return out, true
	}
	// JSON that is not a quoted string - a number or a bare token - still has a text form.
	var anyValue any
	if json.Unmarshal(raw, &anyValue) != nil {
		return "", false
	}
	return stringify(anyValue), true
}

// candidate is one rule that reached the walk: the row and the tree it parsed into.
type candidate struct {
	rule Rule
	tree Cond
}

// Limits is the counted part of the rulebook, read out of the buckets before the walk runs:
// how much of its allowance each counted rule has already spent on whoever is at the gate.
// Keeping it an argument is what lets the engine stay a function of its inputs, and lets a
// test hand it a map instead of a database.
type Limits map[int64]int

// firing is one rule that matched, in the order the walk met it.
type firing struct {
	rule    Rule
	outcome string
	message string
	quota   *movements.Quota
}

// verdict is the walk's answer for one scan.
type verdict struct {
	firings []firing
	// refusal is the rule that stopped the scan. A waiver can still be asked for it, which is
	// why it travels separately from the reason the gate is shown.
	refusal *firing
	warning *firing
	// notices are the rules that only asked for somebody to be told. They change nothing at
	// the barrier, and they are the only firings with no other way to be noticed.
	notices []firing
}

// evaluate walks the candidates in the order they were fetched - lowest priority number
// first, then most specific - and returns what they asked for.
//
// "First blocking action wins" is the schema's own rule, and it is what makes the ordering mean
// something: a block stops the walk, and an allow stops it too - so the rule reached first is
// the one that answers, and a waiver the desk wants to count has to be filed at a lower
// priority number than the block it lifts. A warn or a notice is noted and walked past,
// because neither is an answer to the question the guard asked.
func evaluate(candidates []candidate, f Facts, limits Limits) verdict {
	out := verdict{}
	for _, cand := range candidates {
		if !cand.tree.matches(f) {
			continue
		}
		rule := cand.rule
		hit := firing{rule: rule, message: rule.Reason()}

		switch rule.ActionKind() {
		case ActionAllow:
			out.clear(hit)
			return out
		case ActionBlock:
			hit.outcome = OutcomeBlocked
			return out.refuse(hit)
		case ActionRequire:
			hit.outcome = OutcomeRequired
			return out.refuse(hit)
		case ActionWarn:
			hit.outcome = OutcomeWarned
			out.record(hit)
			if out.warning == nil {
				warned := hit
				out.warning = &warned
			}
		case ActionNotify:
			hit.outcome = OutcomePassed
			out.record(hit)
			out.notices = append(out.notices, hit)
		case ActionLimit:
			quota, ok := quotaOf(rule, f)
			if !ok {
				// A counted rule with nothing to count is a mis-filed rule. It lets the scan
				// through and is logged as passed, because the alternative is a gate that
				// stops working because somebody left a limit box empty.
				out.record(firing{rule: rule, outcome: OutcomePassed})
				continue
			}
			quota.Spent = limits[rule.ID] < limitOf(rule)
			hit.quota = &quota
			if !quota.Spent {
				hit.outcome = OutcomeLimit
				return out.refuse(hit)
			}
			// Inside the allowance the log owes one fact - this scan took one from the bucket -
			// and not the sentence the gate would have shown had it not.
			hit.message = ""
			out.clear(hit)
		default:
			// require_field never reaches here: the write path will not file it for a stage
			// the gate walks. Anything else is a row the schema allows and this engine does
			// not know, so it is noted and skipped rather than guessed at.
			out.record(firing{rule: rule, outcome: OutcomePassed, message: rule.Reason()})
		}
	}
	return out
}

// record appends one firing to the walk's own log, which is all a rule that changed nothing
// owes the desk.
func (v *verdict) record(hit firing) {
	v.firings = append(v.firings, hit)
}

// refuse records the rule that stopped the walk and names it as the refusal.
func (v *verdict) refuse(hit firing) verdict {
	v.record(hit)
	refused := hit
	v.refusal = &refused
	return *v
}

// clear records a rule that said yes, and is the reason the walk stops there: an allow filed
// at a lower priority number than a block is reached first, which is how a desk waives its own
// restriction without deleting it.
func (v *verdict) clear(hit firing) {
	hit.outcome = OutcomePassed
	v.record(hit)
}

func limitOf(rule Rule) int {
	if params := rule.params(); params.Limit > 0 {
		return params.Limit
	}
	return 0
}

// quotaOf names the bucket this scan belongs to and whether it may spend from it. The subject
// of the count is one of the four facts a gate always has, and the bucket opens on the site's
// own calendar so that "five per day" means five per day in the place the rule was written.
func quotaOf(rule Rule, f Facts) (movements.Quota, bool) {
	params := rule.params()
	if params.Limit <= 0 {
		return movements.Quota{}, false
	}
	period := params.Period
	if period == "" {
		period = PeriodDay
	}
	key, ok := bucketKey(params.By, f)
	if !ok {
		return movements.Quota{}, false
	}
	return movements.Quota{
		RuleID: rule.ID, Key: key, Period: string(period), Start: startOf(f.clock(), period),
		Limit: params.Limit,
	}, true
}

func bucketKey(by string, f Facts) (string, bool) {
	switch by {
	case "", "pass":
		if f.PassNo == "" {
			return "", false
		}
		return "pass:" + f.PassNo, true
	case "plate":
		if f.Plate == "" {
			return "", false
		}
		return "plate:" + f.Plate, true
	case "phone":
		if f.Phone == "" {
			return "", false
		}
		return "phone:" + f.Phone, true
	case "subject":
		if f.Subject == "" {
			return "", false
		}
		return "subject:" + f.Subject, true
	case "department":
		if f.Department == 0 {
			return "", false
		}
		return "department:" + stringify(f.Department), true
	case "requester":
		if f.Requester == 0 {
			return "", false
		}
		return "requester:" + stringify(f.Requester), true
	default:
		return "", false
	}
}

// startOf is where a bucket opens. Week buckets open on Monday and month buckets on the first,
// both in the site's own clock, so a shift's worth of counting does not move when the tenant
// changes which server answers.
func startOf(clock time.Time, period Period) time.Time {
	switch period {
	case PeriodHour:
		return time.Date(clock.Year(), clock.Month(), clock.Day(), clock.Hour(), 0, 0, 0,
			clock.Location())
	case PeriodWeek:
		day := clock.AddDate(0, 0, -((int(clock.Weekday()) + 6) % 7))
		return time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, clock.Location())
	case PeriodMonth:
		return time.Date(clock.Year(), clock.Month(), 1, 0, 0, 0, 0, clock.Location())
	default:
		return time.Date(clock.Year(), clock.Month(), clock.Day(), 0, 0, 0, 0, clock.Location())
	}
}

// Validate checks a condition tree before the desk is allowed to file it. Every reason it
// refuses is a way a rule would silently stop meaning what its author intended, which at a
// barrier is the same as a lock that looks fitted.
func Validate(raw json.RawMessage) error {
	if len(raw) == 0 {
		return errs.BadField("conditions", "a rule needs at least one condition")
	}
	var root Cond
	if err := json.Unmarshal(raw, &root); err != nil {
		return errs.BadField("conditions", "is not a condition tree: "+trimmed(err.Error()))
	}
	leaves := 0
	if err := walk(&root, 1, &leaves); err != nil {
		return err
	}
	if leaves == 0 {
		return errs.BadField("conditions", "a rule needs at least one condition")
	}
	if leaves > maxLeaves {
		return errs.BadField("conditions", "has more than "+strconv.Itoa(maxLeaves)+" conditions")
	}
	return nil
}

func walk(c *Cond, depth int, leaves *int) error {
	if depth > maxDepth {
		return errs.BadField("conditions", "is nested deeper than "+strconv.Itoa(maxDepth)+" levels")
	}
	branching := len(c.All) > 0 || len(c.Any) > 0
	if branching && c.Field != "" {
		return errs.BadField("conditions", "mixes a branch with a condition in one node")
	}
	if !branching {
		if err := checkLeaf(c); err != nil {
			return err
		}
		*leaves++
		return nil
	}
	if len(c.All) > 0 && len(c.Any) > 0 {
		return errs.BadField("conditions", "cannot have both all and any in one node")
	}
	children := c.All
	if len(c.Any) > 0 {
		children = c.Any
	}
	if len(children) == 0 {
		return errs.BadField("conditions", "has an empty branch")
	}
	for i := range children {
		if err := walk(&children[i], depth+1, leaves); err != nil {
			return err
		}
	}
	return nil
}

func checkLeaf(c *Cond) error {
	if _, known := fields[c.Field]; !known {
		return errs.BadField("conditions.field",
			c.Field+" is not something a scan carries; try one of "+strings.Join(FieldNames(), ", "))
	}
	if !knownOperator(c.Op) {
		return errs.BadField("conditions.op", c.Op+" is not an operator")
	}
	switch c.Op {
	case opEmpty, opNotEmpty:
		return nil
	case opIn, opNotIn:
		var items []json.RawMessage
		if json.Unmarshal(c.Value, &items) != nil || len(items) == 0 {
			return errs.BadField("conditions.value", c.Op+" needs a list of values")
		}
		return nil
	case opContains, opNotContains:
		var text string
		if json.Unmarshal(c.Value, &text) != nil || strings.TrimSpace(text) == "" {
			return errs.BadField("conditions.value", c.Op+" needs some text to look for")
		}
		return nil
	default:
		if len(c.Value) == 0 {
			return errs.BadField("conditions.value", c.Op+" needs a value")
		}
		return nil
	}
}

func knownOperator(op string) bool {
	for _, candidate := range operators {
		if candidate == op {
			return true
		}
	}
	return false
}

// stringify is the text form of a JSON value nobody wrote as a string, which is what lets a
// rule about a plate say "KCA000A" and a rule about entries say 22 with the same operator set.
func stringify(value any) string { return fmt.Sprint(value) }

// trimmed keeps the part of a decoder complaint a desk can act on rather than the whole of
// what Go thought about its bytes.
func trimmed(value string) string {
	runes := []rune(value)
	if len(runes) <= 80 {
		return value
	}
	return string(runes[:80]) + "…"
}
