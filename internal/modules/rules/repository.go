package rules

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"accessnow/internal/core/movements"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/httpx"
)

type Repository struct{ q db.Conn }

func NewRepository(q db.Conn) *Repository { return &Repository{q: q} }

// In rebinds the repository to an open transaction, keeping one method set.
func (r *Repository) In(c db.Conn) *Repository { bound := *r; bound.q = c; return &bound }

const ruleColumns = `id, tenant_id, name, description, module_key, pass_type_id, category_key,
	site_id, gate_id, department_id, stage, action, action_payload, conditions, priority,
	severity, failure_message, override_allowed, effective_from, effective_until, version,
	is_active, created_by, created_at, updated_at, deleted_at`

const evaluationColumns = `e.id, e.tenant_id, e.rule_id, e.stage, e.outcome, e.matched,
	e.pass_id, e.movement_id, e.gate_id, e.evaluated_at, e.context, e.message,
	e.override_by_user_id, e.override_reason, e.latency_ms,
	r.name AS rule_name, r.action AS rule_action, r.severity AS rule_severity,
	r.priority AS rule_priority, p.pass_no, p.subject_name`

const evaluationFrom = `
	FROM   rule_evaluations e
	LEFT   JOIN rules r ON r.id = e.rule_id AND r.tenant_id = e.tenant_id
	LEFT   JOIN passes p ON p.id = e.pass_id`

// The rulebook.

func (r *Repository) Insert(ctx context.Context, in New, at time.Time) (int64, error) {
	const query = `
		INSERT INTO rules
			(tenant_id, name, description, module_key, pass_type_id, category_key, site_id,
			 gate_id, department_id, stage, action, action_payload, conditions, priority,
			 severity, failure_message, override_allowed, effective_from, effective_until,
			 version, is_active, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?)`
	result, err := r.q.ExecContext(ctx, query,
		in.TenantID, in.Name, text(in.Description), text(in.ModuleKey), nullable(in.PassTypeID),
		text(in.CategoryKey), nullable(in.SiteID), nullable(in.GateID),
		nullable(in.DepartmentID), string(in.Stage), string(in.Action), payloadJSON(in.Params),
		string(in.Conditions), in.Priority, string(in.Severity), text(in.FailureMsg),
		in.Override, nullableTime(in.EffectiveAt), nullableTime(in.UntilAt), in.IsActive,
		nullable(in.CreatedBy), at, at)
	if err != nil {
		return 0, fmt.Errorf("file rule: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read rule id: %w", err)
	}
	return id, nil
}

// Update rewrites a rule and raises its version. The version is not decoration: every
// evaluation row names a rule id, and "the rule as it now reads" is only an explanation of
// that log line if the desk can tell when it changed.
func (r *Repository) Update(ctx context.Context, tenantID, id int64, in New, at time.Time) (bool, error) {
	const query = `
		UPDATE rules
		SET    name = ?, description = ?, module_key = ?, pass_type_id = ?, category_key = ?,
		       site_id = ?, gate_id = ?, department_id = ?, stage = ?, action = ?,
		       action_payload = ?, conditions = ?, priority = ?, severity = ?,
		       failure_message = ?, override_allowed = ?, effective_from = ?,
		       effective_until = ?, is_active = ?, version = version + 1, updated_at = ?
		WHERE  tenant_id = ? AND id = ? AND deleted_at IS NULL`
	result, err := r.q.ExecContext(ctx, query,
		in.Name, text(in.Description), text(in.ModuleKey), nullable(in.PassTypeID),
		text(in.CategoryKey), nullable(in.SiteID), nullable(in.GateID),
		nullable(in.DepartmentID), string(in.Stage), string(in.Action),
		payloadJSON(in.Params), string(in.Conditions), in.Priority, string(in.Severity),
		text(in.FailureMsg), in.Override, nullableTime(in.EffectiveAt),
		nullableTime(in.UntilAt), in.IsActive, at, tenantID, id)
	if err != nil {
		return false, fmt.Errorf("rewrite rule: %w", err)
	}
	return changed(result)
}

func (r *Repository) ByID(ctx context.Context, tenantID, id int64) (Rule, error) {
	var out Rule
	err := r.q.GetContext(ctx, &out, `SELECT `+ruleColumns+` FROM rules
		WHERE tenant_id = ? AND id = ?`, tenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Rule{}, errs.NotFound("rule")
	}
	if err != nil {
		return Rule{}, fmt.Errorf("read rule: %w", err)
	}
	return out, nil
}

type ListFilter struct {
	TenantID     int64
	Action       string
	Stage        string
	SiteID       int64
	CategoryKey  string
	IncludeQuiet bool
	Limit        int
}

// List reads the rulebook in the order the gate walks it, not the order it was filed: a desk
// reading priorities 10, 40 and 60 in that order can see which of its rules wins. There is no
// cursor here because a tenant files dozens of rules, not thousands, and the whole point of
// the list is to see them in one screen.
func (r *Repository) List(ctx context.Context, in ListFilter) ([]Rule, error) {
	where, args := tenantOnly(in.TenantID)
	add(&where, &args, in.Action, "action")
	add(&where, &args, in.Stage, "stage")
	add(&where, &args, in.CategoryKey, "category_key")
	if in.SiteID != 0 {
		where = append(where, "(site_id IS NULL OR site_id = ?)")
		args = append(args, in.SiteID)
	}
	if !in.IncludeQuiet {
		where = append(where, "deleted_at IS NULL")
	}

	query := `SELECT ` + ruleColumns + ` FROM rules WHERE ` + strings.Join(where, " AND ") +
		` ORDER BY priority ASC, id ASC LIMIT ?`
	args = append(args, in.Limit)

	var out []Rule
	if err := r.q.SelectContext(ctx, &out, query, args...); err != nil {
		return nil, fmt.Errorf("read the rulebook: %w", err)
	}
	return out, nil
}

// Summary counts what a desk leads with: how many rules are on the books, how many refuse a
// scan, and how many of them bit today.
type Count struct {
	Action string `db:"action"`
	Active int    `db:"n"`
}

func (r *Repository) Counts(ctx context.Context, tenantID int64) ([]Count, error) {
	const query = `SELECT action, COUNT(*) AS n FROM rules
		WHERE tenant_id = ? AND deleted_at IS NULL AND is_active = 1 GROUP BY action`
	rows, err := r.q.QueryxContext(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("count the rulebook: %w", err)
	}
	defer rows.Close()
	var out []Count
	for rows.Next() {
		var one Count
		if err := rows.Scan(&one.Action, &one.Active); err != nil {
			return nil, fmt.Errorf("count the rulebook: %w", err)
		}
		out = append(out, one)
	}
	return out, rows.Err()
}

// Pause takes a rule off the books without deleting it. A rule that bit in March and is
// paused in April still has to explain the March evaluations, and a delete would take its
// name out from under them.
func (r *Repository) Pause(ctx context.Context, tenantID, id int64, active bool,
	at time.Time) (bool, error) {
	const query = `UPDATE rules SET is_active = ?, updated_at = ?
		WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL AND is_active <> ?`
	result, err := r.q.ExecContext(ctx, query, active, at, tenantID, id, active)
	if err != nil {
		return false, fmt.Errorf("pause rule: %w", err)
	}
	return changed(result)
}

// Retire is a soft delete, and the only delete this module does.
func (r *Repository) Retire(ctx context.Context, tenantID, id int64, at time.Time) (bool, error) {
	const query = `UPDATE rules SET deleted_at = ?, is_active = 0, updated_at = ?
		WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL`
	result, err := r.q.ExecContext(ctx, query, at, at, tenantID, id)
	if err != nil {
		return false, fmt.Errorf("retire rule: %w", err)
	}
	return changed(result)
}

// The walk.

// Match is the scope a scan falls inside. A rule left a column empty means "any", which is why
// every test below is a two-way OR rather than an equality: this is the one question the
// engine cannot answer for itself, because it is the reason the candidate list is short.
type Match struct {
	TenantID     int64
	SiteID       int64
	GateID       int64
	ModuleKey    string
	CategoryKey  string
	DepartmentID int64
	PassTypeID   int64
	Stages       []string
}

// Candidates is the rulebook the walk reads: live, in scope, and in the order first-match-wins
// needs. The window and the flags are checked in SQL so a paused or expired rule never reaches
// the engine, and the condition tree is checked in Go, where a mistake cannot corrupt a query.
//
// Specificity comes second to priority because that is the pair the desk thinks in: priority
// says which rule the tenant meant to be read first, and specificity breaks a tie between two
// rules that say the same thing about a gate and about everything.
func (r *Repository) Candidates(ctx context.Context, in Match, now time.Time, limit int) ([]Rule, error) {
	where, args := tenantOnly(in.TenantID)
	where = append(where, `is_active = 1`, `deleted_at IS NULL`,
		`(effective_from IS NULL OR effective_from <= ?)`,
		`(effective_until IS NULL OR effective_until > ?)`)
	args = append(args, now, now)
	where = append(where, "stage IN ("+marks(len(in.Stages))+")")
	for _, stage := range in.Stages {
		args = append(args, stage)
	}
	scope(&where, &args, "site_id", in.SiteID)
	scope(&where, &args, "gate_id", in.GateID)
	scope(&where, &args, "department_id", in.DepartmentID)
	scope(&where, &args, "pass_type_id", in.PassTypeID)
	if in.CategoryKey != "" {
		where = append(where, "(category_key IS NULL OR category_key = ?)")
		args = append(args, in.CategoryKey)
	}
	if in.ModuleKey != "" {
		where = append(where, "(module_key IS NULL OR module_key = ?)")
		args = append(args, in.ModuleKey)
	}

	// The tie-breaker is written into ORDER BY rather than selected as a column, because the
	// row shape the walk scans is exactly ruleColumns and nothing else.
	query := `SELECT ` + ruleColumns + `
		FROM rules WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY priority ASC,
			((site_id IS NOT NULL) + (gate_id IS NOT NULL) + (department_id IS NOT NULL) +
			 (pass_type_id IS NOT NULL) + (category_key IS NOT NULL) +
			 (module_key IS NOT NULL)) DESC,
			id ASC
		LIMIT ?`
	args = append(args, limit)

	var out []Rule
	if err := r.q.SelectContext(ctx, &out, query, args...); err != nil {
		return nil, fmt.Errorf("read the rules for this scan: %w", err)
	}
	return out, nil
}

// The counted rules.

func (r *Repository) Used(ctx context.Context, quota movements.Quota, tenantID int64) (int, error) {
	const query = `SELECT used_count FROM rule_counter_buckets
		WHERE tenant_id = ? AND rule_id = ? AND bucket_key = ? AND period = ? AND period_start = ?`
	var used int
	err := r.q.GetContext(ctx, &used, query, tenantID, quota.RuleID, quota.Key, quota.Period,
		quota.Start.UTC())
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read the allowance: %w", err)
	}
	return used, nil
}

// Spend opens the bucket if this subject has not been counted in it yet. The increment is one
// statement rather than a read-modify-write, because two guards scanning the same driver at
// the same second are the case the limit exists for.
func (r *Repository) Spend(ctx context.Context, quota movements.Quota, tenantID int64,
	limit int, at time.Time) error {
	const query = `
		INSERT INTO rule_counter_buckets
			(tenant_id, rule_id, bucket_key, period, period_start, used_count, limit_value,
			 updated_at, created_at)
		VALUES (?, ?, ?, ?, ?, 1, ?, ?, ?)
		ON DUPLICATE KEY UPDATE used_count = used_count + 1, updated_at = VALUES(updated_at)`
	if _, err := r.q.ExecContext(ctx, query, tenantID, quota.RuleID, quota.Key, quota.Period,
		quota.Start.UTC(), limit, at, at); err != nil {
		return fmt.Errorf("spend the allowance: %w", err)
	}
	return nil
}

// The log.

type Evaluation struct {
	ID          int64     `db:"id"`
	TenantID    int64     `db:"tenant_id"`
	RuleID      *int64    `db:"rule_id"`
	Stage       string    `db:"stage"`
	Outcome     string    `db:"outcome"`
	Matched     bool      `db:"matched"`
	PassID      *int64    `db:"pass_id"`
	MovementID  *int64    `db:"movement_id"`
	GateID      *int64    `db:"gate_id"`
	EvaluatedAt time.Time `db:"evaluated_at"`
	Context     []byte    `db:"context"`
	Message     *string   `db:"message"`
	OverrideBy  *int64    `db:"override_by_user_id"`
	OverrideWhy *string   `db:"override_reason"`
	Latency     *int64    `db:"latency_ms"`
	RuleName    *string   `db:"rule_name"`
	RuleAction  *string   `db:"rule_action"`
	RuleSev     *string   `db:"rule_severity"`
	RulePrio    *int      `db:"rule_priority"`
	PassNo      *string   `db:"pass_no"`
	SubjectName *string   `db:"subject_name"`
}

// LogEvaluation is the row that explains a red light after the fact. It is written after the
// movement, never inside it: the gate's answer is already given, and a log that could not be
// written is a reason to alarm an operator, not to strand a visitor.
func (r *Repository) LogEvaluation(ctx context.Context, in Evaluation) error {
	const query = `
		INSERT INTO rule_evaluations
			(tenant_id, rule_id, stage, outcome, matched, pass_id, movement_id, gate_id,
			 evaluated_at, context, message, override_by_user_id, override_reason, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := r.q.ExecContext(ctx, query,
		in.TenantID, nullable(in.ruleID()), string(in.Stage), in.Outcome, in.Matched,
		nullable(in.passID()), nullable(in.movementID()), nullable(in.gateID()), in.EvaluatedAt,
		contextJSON(in.Context), text(deref(in.Message)), in.OverrideBy,
		text(deref(in.OverrideWhy)), in.EvaluatedAt)
	if err != nil {
		return fmt.Errorf("write the evaluation log: %w", err)
	}
	return nil
}

type EvalFilter struct {
	TenantID int64
	RuleID   int64
	Outcome  string
	PassID   int64
	Matched  *bool
	Limit    int
	Before   *httpx.Cursor
}

// Evaluations is the feed: what the rulebook did, to which pass, and whether anybody waived
// it. Newest first, because the question it is opened with is "what happened this morning".
func (r *Repository) Evaluations(ctx context.Context, in EvalFilter) ([]Evaluation, error) {
	where, args := tableTenantOnly("e", in.TenantID)
	if in.RuleID != 0 {
		where = append(where, "e.rule_id = ?")
		args = append(args, in.RuleID)
	}
	if in.PassID != 0 {
		where = append(where, "e.pass_id = ?")
		args = append(args, in.PassID)
	}
	add(&where, &args, in.Outcome, "e.outcome")
	if in.Matched != nil {
		where = append(where, "e.matched = ?")
		args = append(args, *in.Matched)
	}
	if in.Before != nil {
		where = append(where, "(e.evaluated_at < ? OR (e.evaluated_at = ? AND e.id < ?))")
		args = append(args, in.Before.At, in.Before.At, in.Before.ID)
	}

	query := `SELECT ` + evaluationColumns + evaluationFrom + ` WHERE ` +
		strings.Join(where, " AND ") + ` ORDER BY e.evaluated_at DESC, e.id DESC LIMIT ?`
	args = append(args, in.Limit)

	var out []Evaluation
	if err := r.q.SelectContext(ctx, &out, query, args...); err != nil {
		return nil, fmt.Errorf("read the evaluation log: %w", err)
	}
	return out, nil
}

// Overrides is the count a supervisor is asked for at the end of a shift: how many refusals
// this tenant waived.
func (r *Repository) Overrides(ctx context.Context, tenantID int64, since time.Time) (int, error) {
	const query = `SELECT COUNT(*) FROM rule_evaluations
		WHERE tenant_id = ? AND outcome = 'override_granted' AND evaluated_at >= ?`
	var n int
	if err := r.q.GetContext(ctx, &n, query, tenantID, since); err != nil {
		return 0, fmt.Errorf("count the waivers: %w", err)
	}
	return n, nil
}

func (e Evaluation) ruleID() int64 {
	return derefInt64(e.RuleID)
}
func (e Evaluation) passID() int64     { return derefInt64(e.PassID) }
func (e Evaluation) movementID() int64 { return derefInt64(e.MovementID) }
func (e Evaluation) gateID() int64     { return derefInt64(e.GateID) }

// The small helpers this repository shares with itself.

func tenantOnly(tenantID int64) ([]string, []any) {
	return []string{"tenant_id = ?"}, []any{tenantID}
}

func tableTenantOnly(alias string, tenantID int64) ([]string, []any) {
	return []string{alias + ".tenant_id = ?"}, []any{tenantID}
}

// scope adds a column that means "any" when it is empty, which is the whole trick of a rule
// that is written once for every gate and site the tenant has.
func scope(where *[]string, args *[]any, column string, value int64) {
	if value == 0 {
		return
	}
	*where = append(*where, "("+column+" IS NULL OR "+column+" = ?)")
	*args = append(*args, value)
}

func add(where *[]string, args *[]any, value, column string) {
	if value == "" {
		return
	}
	*where = append(*where, column+" = ?")
	*args = append(*args, value)
}

func marks(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }

func changed(result sql.Result) (bool, error) {
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count the rows: %w", err)
	}
	return n > 0, nil
}

func text(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullable(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC()
}

func payloadJSON(in Params) any {
	if in.Limit == 0 && in.Period == "" && in.By == "" {
		return nil
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return nil
	}
	return string(raw)
}

func contextJSON(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func derefInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}
