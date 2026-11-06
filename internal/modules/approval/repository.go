package approval

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"

	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
)

// Repository is the SQL for one tenant's workflows. A flow is read whole - a flow without
// its steps can answer nothing - and every write to a live chain is compare-and-set,
// because two holders of the same role can open the same step in the same second.
type Repository struct{ q db.Conn }

func NewRepository(pool *sqlx.DB) *Repository { return &Repository{q: pool} }

// In rebinds the repository to a transaction already in flight. Starting a chain writes a
// request, its steps and an audit row, and none of it is useful on its own.
func (r *Repository) In(c db.Conn) *Repository { return &Repository{q: c} }

const flowColumns = `id, tenant_id, name, pass_type_id, category_key, department_id, site_id,
	min_value, max_value, currency, priority, is_default, outside_hours_only, status,
	description, created_by, created_at, updated_at, deleted_at`

const stepColumns = `id, tenant_id, approval_flow_id, step_no, name, approver_type,
	approver_role_key, approver_user_id, mode, requires_all, sla_hours, escalate_after_hours,
	escalate_to_role_key, escalate_to_user_id, allow_self_approval, require_comment, memo,
	is_active, created_at, updated_at`

// ListFlows is the matcher's whole input: a tenant has a handful of these, and choosing
// which one applies is clearer in Go than in one SQL predicate.
func (r *Repository) ListFlows(ctx context.Context, tenantID int64) ([]Flow, error) {
	const query = `SELECT ` + flowColumns + ` FROM approval_flows
		WHERE tenant_id = ? AND deleted_at IS NULL ORDER BY priority, id`
	var out []Flow
	if err := r.q.SelectContext(ctx, &out, query, tenantID); err != nil {
		return nil, fmt.Errorf("list approval flows: %w", err)
	}
	return out, nil
}

func (r *Repository) FlowByID(ctx context.Context, tenantID, id int64) (Flow, error) {
	const query = `SELECT ` + flowColumns + ` FROM approval_flows
		WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`
	var f Flow
	if err := r.q.GetContext(ctx, &f, query, id, tenantID); err != nil {
		return Flow{}, flowReadErr(err)
	}
	return f, nil
}

// Steps reads a flow's definition as it stands now. Inactive steps are skipped: a tenant
// that retires a step retires it for the passes arriving from that moment.
func (r *Repository) Steps(ctx context.Context, tenantID, flowID int64) ([]FlowStep, error) {
	const query = `SELECT ` + stepColumns + ` FROM approval_flow_steps
		WHERE tenant_id = ? AND approval_flow_id = ? AND is_active = 1 ORDER BY step_no, id`
	var out []FlowStep
	if err := r.q.SelectContext(ctx, &out, query, tenantID, flowID); err != nil {
		return nil, fmt.Errorf("read flow steps: %w", err)
	}
	return out, nil
}

// NewFlow is a flow as the tenant filed it, before it has a step list.
type NewFlow struct {
	TenantID         int64
	Name             string
	PassTypeID       int64
	CategoryKey      string
	DepartmentID     int64
	SiteID           int64
	MinValue         *float64
	MaxValue         *float64
	Currency         string
	Priority         int
	IsDefault        bool
	OutsideHoursOnly bool
	Status           string
	Description      string
	CreatedBy        int64
}

func (r *Repository) CreateFlow(ctx context.Context, in NewFlow) (int64, error) {
	const query = `
		INSERT INTO approval_flows
			(tenant_id, name, pass_type_id, category_key, department_id, site_id,
			 min_value, max_value, currency, priority, is_default, outside_hours_only,
			 status, description, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	result, err := r.q.ExecContext(ctx, query, in.TenantID, in.Name, nullable(in.PassTypeID),
		text(in.CategoryKey), nullable(in.DepartmentID), nullable(in.SiteID), in.MinValue,
		in.MaxValue, text(in.Currency), in.Priority, boolInt(in.IsDefault),
		boolInt(in.OutsideHoursOnly), in.Status, text(in.Description), nullable(in.CreatedBy))
	if err != nil {
		return 0, fmt.Errorf("create approval flow: %w", err)
	}
	id, err := result.LastInsertId()
	return id, wrap(err, "read flow id")
}

// UpdateFlow moves the settings a tenant can change without redefining the chain. The match
// columns stay put on purpose: retargeting a live flow would rewrite which passes a running
// chain was started under.
func (r *Repository) UpdateFlow(ctx context.Context, tenantID, id int64, in UpdateFlow) error {
	const query = `
		UPDATE approval_flows
		SET    name = ?, priority = ?, is_default = ?, outside_hours_only = ?, status = ?,
		       description = ?
		WHERE  id = ? AND tenant_id = ? AND deleted_at IS NULL`
	_, err := r.q.ExecContext(ctx, query, in.Name, in.Priority, boolInt(in.IsDefault),
		boolInt(in.OutsideHoursOnly), in.Status, text(in.Description), id, tenantID)
	return wrap(err, "update approval flow")
}

// UpdateFlow is the PATCH body of a flow, already defaulted by the handler.
type UpdateFlow struct {
	Name             string
	Priority         int
	IsDefault        bool
	OutsideHoursOnly bool
	Status           string
	Description      string
}

// SoftDeleteFlow retires a rule. Requests already working through it keep their snapshot and
// keep moving, so switching a flow off never strands a pass mid-chain.
func (r *Repository) SoftDeleteFlow(ctx context.Context, tenantID, id int64) error {
	const query = `UPDATE approval_flows SET deleted_at = UTC_TIMESTAMP(6)
		WHERE id = ? AND tenant_id = ? AND deleted_at IS NULL`
	_, err := r.q.ExecContext(ctx, query, id, tenantID)
	return wrap(err, "delete approval flow")
}

// ReplaceSteps swaps a flow's definition. Live chains are untouched: they carry the steps as
// they were when the pass arrived, which is the whole reason flow_snapshot exists.
func (r *Repository) ReplaceSteps(ctx context.Context, tenantID, flowID int64, steps []NewStep) error {
	if _, err := r.q.ExecContext(ctx,
		`DELETE FROM approval_flow_steps WHERE tenant_id = ? AND approval_flow_id = ?`,
		tenantID, flowID); err != nil {
		return fmt.Errorf("clear flow steps: %w", err)
	}
	for _, s := range steps {
		if err := r.insertStep(ctx, tenantID, flowID, s); err != nil {
			return err
		}
	}
	return nil
}

// NewStep is one step of a flow as the tenant defined it.
type NewStep struct {
	StepNo             int
	Name               string
	ApproverType       string
	ApproverRoleKey    string
	ApproverUserID     int64
	Mode               string
	RequiresAll        bool
	SLAHours           *int
	EscalateAfterHours *int
	EscalateToRoleKey  string
	EscalateToUserID   int64
	AllowSelfApproval  bool
	RequireComment     bool
	Memo               string
}

func (r *Repository) insertStep(ctx context.Context, tenantID, flowID int64, s NewStep) error {
	const query = `
		INSERT INTO approval_flow_steps
			(tenant_id, approval_flow_id, step_no, name, approver_type, approver_role_key,
			 approver_user_id, mode, requires_all, sla_hours, escalate_after_hours,
			 escalate_to_role_key, escalate_to_user_id, allow_self_approval, require_comment,
			 memo, is_active)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`
	_, err := r.q.ExecContext(ctx, query, tenantID, flowID, s.StepNo, s.Name, s.ApproverType,
		text(s.ApproverRoleKey), nullable(s.ApproverUserID), s.Mode, boolInt(s.RequiresAll),
		s.SLAHours, s.EscalateAfterHours, text(s.EscalateToRoleKey), nullable(s.EscalateToUserID),
		boolInt(s.AllowSelfApproval), boolInt(s.RequireComment), text(s.Memo))
	return wrap(err, "insert flow step "+s.Name)
}

func flowReadErr(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return errs.NotFound("approval flow")
	}
	return fmt.Errorf("read approval flow: %w", err)
}

func changed(result sql.Result) (bool, error) { return db.RowsChanged(result) }

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// text turns an absent string into NULL. A flow that says "" matches nothing, which is not
// the same thing as a flow that does not filter on that column at all.
func text(value string) any {
	if value == "" {
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

func wrap(err error, action string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", action, err)
}
