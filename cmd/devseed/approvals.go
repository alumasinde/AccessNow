package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"accessnow/internal/platform/db"
)

// starterFlow is one approval rule as the seed files it: the match, and the chain it
// starts. It is a plain record rather than the module's own DTO because cmd/devseed must
// not import a module - the seed writes the same rows the desk would, nothing more.
type starterFlow struct {
	name        string
	category    string
	minValue    *float64
	priority    int
	isDefault   bool
	afterHours  bool
	description string
	steps       []starterStep
}

// starterStep is one step of that chain. approverUser names a person instead of a role when
// it is not zero, which is the only way the seeded cover has anything to redirect.
type starterStep struct {
	no           int
	name         string
	roleKey      string
	approverUser int64
	slaHours     int
	escalates    string
	commentToo   bool
	memo         string
}

// starterFlows shows the three ways a tenant points a pass at a chain: one fallback for
// everything, one that only fires above a declared value, and one that only fires outside
// the site's own working hours.
func starterFlows(supervisorID int64) []starterFlow {
	high := 100000.0
	return []starterFlow{
		{
			name: "Every visit needs a supervisor", category: "visitor", priority: 20,
			isDefault:   true,
			description: "The fallback chain: any visitor pass is held until a gate supervisor says yes.",
			steps: []starterStep{
				{no: 1, name: "Gate supervisor approval", roleKey: "gate_supervisor",
					slaHours: 4, escalates: "tenant_admin",
					memo: "First supervisor to answer wins; the administrator picks it up after four hours."},
			},
		},
		{
			name: "High value goods", category: "material", minValue: &high, priority: 10,
			description: "Goods declared at 100,000 or more are signed twice: the supervisor, then the site manager.",
			steps: []starterStep{
				{no: 1, name: "Gate supervisor check", roleKey: "gate_supervisor",
					slaHours: 2, escalates: "site_manager", commentToo: true},
				{no: 2, name: "Site manager sign-off", roleKey: "site_manager",
					slaHours: 8, commentToo: true,
					memo: "A no here sends the goods back to the desk with the reason."},
			},
		},
		{
			name: "Out-of-hours staff exit", category: "staff", priority: 10,
			afterHours:  true,
			description: "A step-out after dark is asked of one named supervisor, whose cover answers it while they are away.",
			steps: []starterStep{
				{no: 1, name: "After-hours supervisor", approverUser: supervisorID,
					slaHours: 1, escalates: "tenant_admin",
					memo: "Asked of one person by name, so the seeded delegation has somewhere to land."},
			},
		},
	}
}

// ensureFlows files the starter chains, their steps, and one cover. Names are the key: a
// flow row has no unique index a seed can lean on, so each rule is looked up before it is
// written and a re-run changes nothing.
func ensureFlows(ctx context.Context, c db.Conn, tenantID, siteID, adminID, supervisorID int64) error {
	// The catalogue ships approval_workflow switched off, because a new tenant must get the
	// desk working before anybody has written a rule. This seed writes rules, so it switches
	// the module on too - otherwise the flows would be filed and never read.
	if _, err := c.ExecContext(ctx,
		`UPDATE tenant_modules SET enabled = 1 WHERE tenant_id = ? AND module_key = 'approval_workflow'`,
		tenantID); err != nil {
		return wrap(err, "enable approval_workflow")
	}

	for _, flow := range starterFlows(supervisorID) {
		id, err := flowID(ctx, c, tenantID, flow.name)
		if err != nil {
			return err
		}
		if id == 0 {
			result, err := c.ExecContext(ctx, `
				INSERT INTO approval_flows
					(tenant_id, name, category_key, site_id, min_value, currency, priority,
					 is_default, outside_hours_only, status, description, created_by)
				VALUES (?, ?, ?, ?, ?, 'KES', ?, ?, ?, 'active', ?, ?)`,
				tenantID, flow.name, flow.category, siteID,
				flow.minValue, flow.priority, boolInt(flow.isDefault), boolInt(flow.afterHours),
				flow.description, adminID)
			if err != nil {
				return wrap(err, "seed approval flow "+flow.name)
			}
			if id, err = result.LastInsertId(); err != nil {
				return wrap(err, "read approval flow id")
			}
		}
		if err := ensureSteps(ctx, c, tenantID, id, flow.steps); err != nil {
			return err
		}
	}

	// One cover on file proves the delegation path without anybody having to write it. The
	// window is the week around today, so it is in force whenever the seed is run.
	_, err := c.ExecContext(ctx, `
		INSERT INTO approval_delegations
			(tenant_id, from_user_id, to_user_id, starts_at, ends_at, reason, status, created_by)
		SELECT ?, ?, ?, UTC_TIMESTAMP(6) - INTERVAL 1 DAY, UTC_TIMESTAMP(6) + INTERVAL 6 DAY,
		       'Seeded cover: one week from the supervisor to the administrator.', 'active', ?
		WHERE  NOT EXISTS (SELECT 1 FROM approval_delegations
		                   WHERE tenant_id = ? AND from_user_id = ? AND status = 'active')`,
		tenantID, supervisorID, adminID, adminID, tenantID, supervisorID)
	return wrap(err, "seed approval delegation")
}

func ensureSteps(ctx context.Context, c db.Conn, tenantID, flowID int64, steps []starterStep) error {
	const query = `
		INSERT INTO approval_flow_steps
			(tenant_id, approval_flow_id, step_no, name, approver_type, approver_role_key,
			 approver_user_id, mode, requires_all, sla_hours, escalate_after_hours,
			 escalate_to_role_key, allow_self_approval, require_comment, memo)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'any_one', 0, ?, ?, ?, 0, ?, ?)
		ON DUPLICATE KEY UPDATE approver_role_key = VALUES(approver_role_key),
		                        approver_user_id = VALUES(approver_user_id),
		                        sla_hours = VALUES(sla_hours),
		                        escalate_to_role_key = VALUES(escalate_to_role_key),
		                        require_comment = VALUES(require_comment),
		                        memo = VALUES(memo)`
	for _, step := range steps {
		var userID any
		kind, roleKey := "role", step.roleKey
		if step.approverUser != 0 {
			kind, roleKey, userID = "user", "", step.approverUser
		}
		if _, err := c.ExecContext(ctx, query, tenantID, flowID, step.no, step.name, kind,
			nullString(roleKey), userID, step.slaHours, step.slaHours,
			nullString(step.escalates), boolInt(step.commentToo), nullString(step.memo)); err != nil {
			return wrap(err, fmt.Sprintf("seed step %d of approval flow %d", step.no, flowID))
		}
	}
	return nil
}

// flowID reads a flow by the only key the seed can use. No row is not an error: it means
// this tenant has not had this rule filed yet.
func flowID(ctx context.Context, c db.Conn, tenantID int64, name string) (int64, error) {
	const query = `SELECT id FROM approval_flows WHERE tenant_id = ? AND name = ? AND deleted_at IS NULL`
	var id int64
	err := c.GetContext(ctx, &id, query, tenantID, name)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return id, wrap(err, "read approval flow "+name)
}

// boolInt and the two NULL helpers are the shapes the approval tables want that seed.go
// does not already have: an empty escalation target is NULL, because a step that escalates
// to "" is a step that escalates to nobody.
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
