package numbering

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
)

type Repository struct{ q db.Conn }

func NewRepository(q db.Conn) *Repository { return &Repository{q} }

// In rebinds the repository to an open transaction, keeping one method set.
func (r *Repository) In(c db.Conn) *Repository { bound := *r; bound.q = c; return &bound }

func (r *Repository) ActiveFormat(ctx context.Context, tenantID int64, formatKey string) (Format, error) {
	const query = `
		SELECT id, tenant_id, format_key, name, pattern, prefix, category_key, module_key,
		       sequence_width, separator_char, reset_period, gap_required
			FROM   pass_number_formats
		WHERE  tenant_id = ? AND format_key = ? AND is_active = 1`
	var f Format
	err := r.q.GetContext(ctx, &f, query, tenantID, formatKey)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Format{}, errs.NotFound("pass number format " + formatKey)
	case err != nil:
		return Format{}, fmt.Errorf("read pass number format: %w", err)
	default:
		return f, nil
	}
}

// Claim advances the counter and returns the number handed out.
//
// LAST_INSERT_ID() is scoped to the connection, so this has to run on a transaction
// that stays pinned to one connection; it is also what makes the increment atomic
// across two guards scanning at two gates at the same instant.
func (r *Repository) Claim(ctx context.Context, tenantID, formatID int64, periodKey string) (uint64, error) {
	const claim = `
		INSERT INTO pass_number_counters (tenant_id, format_id, period_key, last_value)
		VALUES (?, ?, ?, LAST_INSERT_ID(1))
		ON DUPLICATE KEY UPDATE last_value = LAST_INSERT_ID(last_value + step)`
	if _, err := r.q.ExecContext(ctx, claim, tenantID, formatID, periodKey); err != nil {
		return 0, fmt.Errorf("claim pass number: %w", err)
	}
	var value uint64
	if err := r.q.GetContext(ctx, &value, `SELECT LAST_INSERT_ID()`); err != nil {
		return 0, fmt.Errorf("read claimed pass number: %w", err)
	}
	return value, nil
}

// SaveFormat creates or replaces a tenant format. Existing pass_no values are never
// rewritten: a format change only applies to passes issued after it.
func (r *Repository) SaveFormat(ctx context.Context, f Format) error {
	const query = `
		INSERT INTO pass_number_formats
			(tenant_id, format_key, name, pattern, prefix, category_key, module_key,
			 sequence_width, separator_char, reset_period, gap_required, is_active)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)
		ON DUPLICATE KEY UPDATE
			name = VALUES(name), pattern = VALUES(pattern), prefix = VALUES(prefix),
			category_key = VALUES(category_key), module_key = VALUES(module_key),
			sequence_width = VALUES(sequence_width), separator_char = VALUES(separator_char),
			reset_period = VALUES(reset_period), gap_required = VALUES(gap_required),
			is_active = 1`
	_, err := r.q.ExecContext(ctx, query,
		f.TenantID, f.FormatKey, f.Name, f.Pattern, f.Prefix, f.CategoryKey, f.ModuleKey,
		f.SequenceWidth, f.Separator, f.ResetPeriod, f.GapRequired)
	if err != nil {
		return fmt.Errorf("save pass number format: %w", err)
	}
	return nil
}
