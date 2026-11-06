package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"

	"accessnow/internal/platform/db"
	"accessnow/internal/platform/numbering"
	"accessnow/internal/platform/security"
)

type outcome struct {
	TenantID     int64
	SiteID       int64
	GateID       int64
	AdminID      int64
	GuardID      int64
	SupervisorID int64
	ManagerID    int64
	DeviceToken  string
}

// provision writes in one transaction, so a half-seeded tenant never reaches the login
// screen: either every step lands or none does.
func provision(ctx context.Context, pool *sqlx.DB, s settings) (outcome, error) {
	adminHash, err := security.HashPassword(s.adminPassword)
	if err != nil {
		return outcome{}, err
	}
	guardHash, err := security.HashPassword(s.guardPassword)
	if err != nil {
		return outcome{}, err
	}
	token, err := security.NewToken()
	if err != nil {
		return outcome{}, err
	}

	var out outcome
	err = db.Transact(ctx, pool, func(c db.Conn) error {
		if out.TenantID, err = ensureTenant(ctx, c, s); err != nil {
			return err
		}
		tenant := out.TenantID

		if err := copyModules(ctx, c, tenant); err != nil {
			return err
		}
		if err := copyRoles(ctx, c, tenant); err != nil {
			return err
		}
		if err := copyRolePermissions(ctx, c, tenant); err != nil {
			return err
		}

		adminRole, err := roleID(ctx, c, tenant, "tenant_admin")
		if err != nil {
			return err
		}
		guardRole, err := roleID(ctx, c, tenant, "guard")
		if err != nil {
			return err
		}
		supervisorRole, err := roleID(ctx, c, tenant, "gate_supervisor")
		if err != nil {
			return err
		}
		managerRole, err := roleID(ctx, c, tenant, "site_manager")
		if err != nil {
			return err
		}

		if out.AdminID, err = ensureUser(ctx, c, tenant, "System", "Admin", s.adminEmail,
			s.slug+"-admin", "Tenant administrator", adminHash, adminRole); err != nil {
			return err
		}
		if out.GuardID, err = ensureUser(ctx, c, tenant, "Gate", "Guard", "",
			s.slug+"-guard", "Gate guard", guardHash, guardRole); err != nil {
			return err
		}
		// The two logins an approval chain needs: a supervisor who answers the queue, and a
		// site manager who signs the second step of the expensive ones. They share the
		// guard's password because a seeded tenant has no secrets in it.
		if out.SupervisorID, err = ensureUser(ctx, c, tenant, "Grace", "Njeri", "",
			s.slug+"-supervisor", "Gate supervisor", guardHash, supervisorRole); err != nil {
			return err
		}
		if out.ManagerID, err = ensureUser(ctx, c, tenant, "Samuel", "Otieno", "",
			s.slug+"-manager", "Site manager", guardHash, managerRole); err != nil {
			return err
		}

		if out.SiteID, err = ensureSite(ctx, c, tenant, s.name); err != nil {
			return err
		}
		if out.GateID, err = ensureGate(ctx, c, tenant, out.SiteID); err != nil {
			return err
		}
		if err := assignGuard(ctx, c, tenant, out.GateID, out.GuardID); err != nil {
			return err
		}
		if err := enrollDevice(ctx, c, tenant, out.SiteID, out.GateID, s.slug, token); err != nil {
			return err
		}
		// The directory comes last because it points at both the site and the login.
		if err := ensureStaff(ctx, c, tenant, out.SiteID, out.AdminID, s.slug); err != nil {
			return err
		}
		// Approval rules come after the people, because every step in one names a role that
		// has to exist and a user who has to hold it.
		if err := ensureFlows(ctx, c, tenant, out.SiteID, out.AdminID, out.SupervisorID); err != nil {
			return err
		}
		// Same reason for the notification routes: one of them names the supervisor by id.
		if err := ensureNotifySettings(ctx, c, tenant, out.SiteID, out.SupervisorID, s.slug); err != nil {
			return err
		}
		// The rulebook comes last of the three: a rule that names a role or a pass category
		// only means something once the desk surface and the flows it can require exist.
		if err := ensureRulebook(ctx, c, tenant, out.SiteID, out.AdminID); err != nil {
			return err
		}

		return numbering.EnsureDefaults(ctx, c, tenant)
	})
	if err != nil {
		return outcome{}, err
	}
	out.DeviceToken = token
	return out, nil
}

func ensureTenant(ctx context.Context, c db.Conn, s settings) (int64, error) {
	const query = `
		INSERT INTO tenants (slug, name, timezone, currency_code, status, contact_email)
		VALUES (?, ?, 'Africa/Nairobi', 'KES', 'active', ?)
		ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(id), name = VALUES(name)`
	return insert(ctx, c, "tenant", query, s.slug, s.name, s.adminEmail)
}

// enabled follows the catalogue's default_enabled, so flipping a module's default in 0025
// changes what the next tenant gets without touching this file.
func copyModules(ctx context.Context, c db.Conn, tenantID int64) error {
	const query = `
		INSERT INTO tenant_modules (tenant_id, module_key, enabled, activated_at)
		SELECT ?, module_key, default_enabled, CURRENT_TIMESTAMP(6)
		FROM   modules WHERE status = 'available'
		ON DUPLICATE KEY UPDATE tenant_id = tenant_id`
	_, err := c.ExecContext(ctx, query, tenantID)
	return wrap(err, "copy module flags")
}

func copyRoles(ctx context.Context, c db.Conn, tenantID int64) error {
	const query = `
		INSERT INTO roles (tenant_id, role_key, name, description, template_role_key,
		                   is_system, does_gate_work, sort_order)
		SELECT ?, role_key, name, description, role_key, 1, does_gate_work, sort_order
		FROM   role_templates WHERE is_default = 1
		ON DUPLICATE KEY UPDATE tenant_id = tenant_id`
	_, err := c.ExecContext(ctx, query, tenantID)
	return wrap(err, "clone role templates")
}

func copyRolePermissions(ctx context.Context, c db.Conn, tenantID int64) error {
	const query = `
		INSERT INTO role_permissions (role_id, tenant_id, permission_id)
		SELECT r.id, r.tenant_id, tp.permission_id
		FROM   roles r
		JOIN   role_templates t ON t.role_key = r.role_key
		JOIN   role_template_permissions tp ON tp.template_role_id = t.id
		WHERE  r.tenant_id = ? AND r.is_system = 1
		ON DUPLICATE KEY UPDATE role_id = role_id`
	_, err := c.ExecContext(ctx, query, tenantID)
	return wrap(err, "clone role permissions")
}

// A missing role means 0026 has not been applied, and seeding on top of that would create
// a user who can sign in and do nothing.
func roleID(ctx context.Context, c db.Conn, tenantID int64, roleKey string) (int64, error) {
	const query = `SELECT id FROM roles WHERE tenant_id = ? AND role_key = ? AND deleted_at IS NULL`
	var id int64
	err := c.GetContext(ctx, &id, query, tenantID, roleKey)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("role %q is not in this tenant; apply migration 0026 and re-run", roleKey)
	}
	return id, wrap(err, "read role "+roleKey)
}

func ensureUser(ctx context.Context, c db.Conn, tenantID int64, first, last, email,
	username, jobTitle, passwordHash string, role int64) (int64, error) {
	userID, err := insert(ctx, c, "user "+username, `
		INSERT INTO users (tenant_id, first_name, last_name, display_name, email, username,
		                   password_hash, job_title, status)
		VALUES (?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, 'active')
		ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(id)`,
		tenantID, first, last, first+" "+last, email, username, passwordHash, jobTitle)
	if err != nil {
		return 0, err
	}

	_, err = c.ExecContext(ctx, `
		INSERT INTO user_roles (user_id, tenant_id, role_id)
		VALUES (?, ?, ?)
		ON DUPLICATE KEY UPDATE user_id = user_id`, userID, tenantID, role)
	return userID, wrap(err, "grant role")
}

// The working window is seeded because the after-hours flow reads it: a site that never
// declared an opening time is a site that is always open, and its out-of-hours rule would
// then match nothing.
func ensureSite(ctx context.Context, c db.Conn, tenantID int64, tenantName string) (int64, error) {
	return insert(ctx, c, "site", `
		INSERT INTO sites (tenant_id, code, name, timezone, is_headquarters, status, settings)
		VALUES (?, 'HQ', ?, 'Africa/Nairobi', 1, 'active',
		        JSON_OBJECT('business_hours', JSON_OBJECT('days', 'mon-fri',
		                                                  'open', '08:00', 'close', '18:00')))
		ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(id),
		                        settings = COALESCE(settings, VALUES(settings))`,
		tenantID, tenantName+" headquarters")
}

func ensureGate(ctx context.Context, c db.Conn, tenantID, siteID int64) (int64, error) {
	return insert(ctx, c, "gate", `
		INSERT INTO gates (tenant_id, site_id, code, name, gate_type, direction,
		                   is_default, offline_capable, status)
		VALUES (?, ?, 'MAIN', 'Main gate', 'main', 'both', 1, 1, 'active')
		ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(id)`, tenantID, siteID)
}

func assignGuard(ctx context.Context, c db.Conn, tenantID, gateID, userID int64) error {
	_, err := c.ExecContext(ctx, `
		INSERT INTO gate_users (gate_id, tenant_id, user_id, is_supervisor)
		VALUES (?, ?, ?, 0)
		ON DUPLICATE KEY UPDATE gate_id = gate_id`, gateID, tenantID, userID)
	return wrap(err, "assign guard to gate")
}

// The raw token is never stored, so re-running replaces the tablet's secret and prints the
// only copy that will ever exist.
func enrollDevice(ctx context.Context, c db.Conn, tenantID, siteID, gateID int64, slug, token string) error {
	_, err := c.ExecContext(ctx, `
		INSERT INTO gate_devices (tenant_id, site_id, gate_id, device_token_hash, short_code,
		                          label, kind, is_shared, is_active, enrolled_at)
		VALUES (?, ?, ?, ?, ?, ?, 'tablet', 1, 1, CURRENT_TIMESTAMP(6))
		ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(id), device_token_hash = VALUES(device_token_hash)`,
		tenantID, siteID, gateID, security.HashToken(token), slug+"-TAB1", slug+" gate tablet")
	return wrap(err, "enroll gate device")
}

// Two directory rows, because the outpass desk has two callers: an employee with a login,
// who can ask for themselves, and one without, whom a guard signs out by name.
func ensureStaff(ctx context.Context, c db.Conn, tenantID, siteID, userID int64, slug string) error {
	const query = `
		INSERT INTO staff (tenant_id, user_id, home_site_id, employee_no, first_name, last_name,
		                   job_title, shift_pattern, status, started_on)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'day', 'active', CURRENT_DATE - INTERVAL 1 YEAR)
		ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(id)`
	for _, employee := range []struct {
		user                   any
		no, first, last, title string
	}{
		{userID, slug + "-E001", "Wanjiru", "Kamau", "Accounts clerk"},
		{nil, slug + "-E002", "Peter", "Otieno", "Storekeeper"},
	} {
		if _, err := c.ExecContext(ctx, query, tenantID, employee.user, siteID, employee.no,
			employee.first, employee.last, employee.title); err != nil {
			return wrap(err, "seed staff "+employee.no)
		}
	}
	return nil
}

func insert(ctx context.Context, c db.Conn, what, query string, args ...any) (int64, error) {
	result, err := c.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, wrap(err, "insert "+what)
	}
	id, err := result.LastInsertId()
	return id, wrap(err, "read id for "+what)
}

func wrap(err error, action string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", action, err)
}
