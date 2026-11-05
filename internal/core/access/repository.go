// Package access answers the two questions every route asks: is this module switched on
// for this tenant, and may this user do this thing.
package access

import (
	"context"
	"fmt"

	"accessnow/internal/platform/db"
)

type Repository struct{ q db.Conn }

func NewRepository(q db.Conn) *Repository { return &Repository{q: q} }

func (r *Repository) EnabledModules(ctx context.Context, tenantID int64) (map[string]bool, error) {
	const query = `SELECT module_key, enabled = 1 FROM tenant_modules WHERE tenant_id = ?`
	rows, err := r.q.QueryxContext(ctx, query, tenantID)
	if err != nil {
		return nil, fmt.Errorf("read tenant modules: %w", err)
	}
	defer rows.Close()

	enabled := make(map[string]bool)
	for rows.Next() {
		var key string
		var on bool
		if err := rows.Scan(&key, &on); err != nil {
			return nil, fmt.Errorf("scan tenant module: %w", err)
		}
		enabled[key] = on
	}
	return enabled, rows.Err()
}

func (r *Repository) Permissions(ctx context.Context, tenantID, userID int64) (map[string]bool, error) {
	const query = `
		SELECT DISTINCT p.permission_key
		FROM   user_roles ur
		JOIN   roles r            ON r.id = ur.role_id AND r.tenant_id = ur.tenant_id
		JOIN   role_permissions rp ON rp.role_id = r.id
		JOIN   permissions p     ON p.id = rp.permission_id
		WHERE  ur.user_id = ? AND ur.tenant_id = ? AND r.status = 'active'`
	rows, err := r.q.QueryxContext(ctx, query, userID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("read user permissions: %w", err)
	}
	defer rows.Close()

	granted := make(map[string]bool)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("scan permission: %w", err)
		}
		granted[key] = true
	}
	return granted, rows.Err()
}

func (r *Repository) GateWork(ctx context.Context, tenantID, userID int64) (bool, error) {
	const query = `
		SELECT COUNT(*) > 0
		FROM   user_roles ur
		JOIN   roles r ON r.id = ur.role_id AND r.tenant_id = ur.tenant_id
		WHERE  ur.user_id = ? AND ur.tenant_id = ? AND r.does_gate_work = 1 AND r.status = 'active'`
	var works bool
	if err := r.q.GetContext(ctx, &works, query, userID, tenantID); err != nil {
		return false, fmt.Errorf("read gate role: %w", err)
	}
	return works, nil
}
