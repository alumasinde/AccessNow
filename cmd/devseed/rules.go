package main

import (
	"context"
	"fmt"

	"accessnow/internal/platform/db"
)

// starterRule is one written-down restriction, filed exactly as the desk would file it. Like
// the approval seed this is a plain record rather than the module's own DTO, because
// cmd/devseed must not import a module: it writes the same rows, nothing more.
type starterRule struct {
	name        string
	description string
	category    string
	stage       string
	action      string
	payload     string
	conditions  string
	priority    int
	severity    string
	failure     string
	overridable bool
}

// starterRules is the five sentences a site actually writes down, in the order the gate walks
// them. They are deliberately a mix: a waiver that has to be reached first to mean anything,
// a restriction that names the condition in the guard's own words, a counted allowance, a
// note for the duty roster and a closing time - the five things a rulebook is for.
func starterRules() []starterRule {
	return []starterRule{
		{
			name:        "Board meetings are admitted at any hour",
			description: "The waiver rule: reached before everything below it, so a board visit is never held by the night close or the audit ban.",
			stage:       "scan", action: "allow", priority: 5, severity: "info",
			conditions: `{"all":[{"field":"direction","op":"eq","value":"in"},` +
				`{"field":"purpose","op":"contains","value":"board"}]}`,
			failure: "Admitted under the board-meeting waiver.",
		},
		{
			name:        "Two entries a day on a visitor pass",
			description: "The counted allowance: one bucket per pass number per site day, and only for an inward scan, so a pass that keeps coming and going is stopped on the third entry.",
			stage:       "scan", action: "limit", priority: 20, severity: "warning",
			payload: `{"limit":2,"period":"day","by":"pass"}`,
			conditions: `{"all":[{"field":"category","op":"eq","value":"visitor"},` +
				`{"field":"direction","op":"eq","value":"in"}]}`,
			failure:     "This pass has used its two entries today.",
			overridable: true,
		},
		{
			name:        "Audit visits are booked through the office",
			description: "The refusal the guard is shown in the visitor's own language: not an error, a reason.",
			stage:       "scan", action: "block", priority: 30, severity: "warning",
			conditions:  `{"field":"purpose","op":"contains","value":"audit"}`,
			failure:     "Audit visits must be booked through the office, not the gate.",
			overridable: true,
		},
		{
			name:        "The visitor gate closes at ten",
			description: "The time rule the old radio call used to carry, written in the site's own clock.",
			stage:       "scan", action: "block", priority: 40, severity: "info",
			conditions: `{"all":[{"field":"category","op":"eq","value":"visitor"},` +
				`{"field":"hour","op":"gte","value":"22"}]}`,
			failure: "The visitor gate is closed after 22:00.",

			// An overridable restriction is the difference between a rule and a locked door at
			// the wrong hour, so the seed files both: this one a supervisor may waive, and the
			// goods note below nobody can.
			overridable: true,
		},
		{
			name:        "Goods moves go to the duty roster",
			description: "The notice: it changes nothing at the barrier and asks for nobody to be chased, it only writes the sentence down.",
			stage:       "scan", action: "notify", priority: 50, severity: "info",
			conditions: `{"field":"category","op":"eq","value":"material"}`,
			failure:    "A goods move was recorded at the gate.",
		},
	}
}

// ensureRulebook files the starter restrictions and switches the module on. The catalogue
// ships rules_policy off, because a tenant must get its gates working before anybody writes
// a rule down - and a seed that writes rules without turning them on would leave a rulebook
// nobody reads.
func ensureRulebook(ctx context.Context, c db.Conn, tenantID, siteID, adminID int64) error {
	if _, err := c.ExecContext(ctx,
		`UPDATE tenant_modules SET enabled = 1 WHERE tenant_id = ? AND module_key = 'rules_policy'`,
		tenantID); err != nil {
		return wrap(err, "enable rules_policy")
	}

	for _, rule := range starterRules() {
		exists, err := ruleOnFile(ctx, c, tenantID, rule.name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := c.ExecContext(ctx, `
			INSERT INTO rules
				(tenant_id, name, description, site_id, stage, action, action_payload,
				 conditions, priority, severity, failure_message, override_allowed, created_by)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			tenantID, rule.name, rule.description, siteID, rule.stage, rule.action,
			jsonOrNull(rule.payload), rule.conditions, rule.priority, rule.severity,
			rule.failure, boolInt(rule.overridable), adminID); err != nil {
			return wrap(err, fmt.Sprintf("seed rule %q", rule.name))
		}
	}
	return nil
}

// ruleOnFile is the seed's own idempotence: a name is not unique in the schema, but it is
// unique enough for a starter rulebook that must not double up when the seed is re-run.
func ruleOnFile(ctx context.Context, c db.Conn, tenantID int64, name string) (bool, error) {
	var filed int
	if err := c.GetContext(ctx, &filed,
		`SELECT COUNT(*) FROM rules WHERE tenant_id = ? AND name = ? AND deleted_at IS NULL`,
		tenantID, name); err != nil {
		return false, wrap(err, "look up seeded rule "+name)
	}
	return filed > 0, nil
}

// jsonOrNull keeps an empty action_payload NULL rather than an empty string, which MariaDB's
// JSON column would refuse.
func jsonOrNull(value string) any {
	if value == "" {
		return nil
	}
	return value
}
