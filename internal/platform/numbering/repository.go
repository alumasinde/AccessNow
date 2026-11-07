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
// The read is a locked one and the lock is held until the caller's transaction commits,
// which is what makes a number exclusive: two guards at two gates compute the same next
// value only if they are allowed to read the same row at the same time, and this stops it.
// It therefore has to run on the transaction that inserts the pass, so a rolled back issue
// hands the number back instead of burning it - exactly what a gap_required format needs.
//
// The increment is read and written in two statements on purpose. The single-statement
// trick (last_value = LAST_INSERT_ID(last_value + step), then SELECT LAST_INSERT_ID())
// loses on the very first claim of a period, because that claim also inserts the row, and
// an inserted AUTO_INCREMENT id overwrites the sequence value in the same session state:
// the pass would be numbered with the counter row's own id.
func (r *Repository) Claim(ctx context.Context, tenantID, formatID int64, periodKey string) (uint64, error) {
	// Opening a period is idempotent, so two claims that both find no row do not race: the
	// second one is ignored and both then read and move the same locked row.
	const open = `
		INSERT IGNORE INTO pass_number_counters (tenant_id, format_id, period_key, last_value)
		VALUES (?, ?, ?, 0)`
	if _, err := r.q.ExecContext(ctx, open, tenantID, formatID, periodKey); err != nil {
		return 0, fmt.Errorf("open pass number period: %w", err)
	}

	var next uint64
	const lock = `
		SELECT last_value + step FROM pass_number_counters
		WHERE  tenant_id = ? AND format_id = ? AND period_key = ? FOR UPDATE`
	if err := r.q.GetContext(ctx, &next, lock, tenantID, formatID, periodKey); err != nil {
		return 0, fmt.Errorf("claim pass number: %w", err)
	}

	const advance = `
		UPDATE pass_number_counters SET last_value = ?
		WHERE  tenant_id = ? AND format_id = ? AND period_key = ? AND last_value < ?`
	result, err := r.q.ExecContext(ctx, advance, next, tenantID, formatID, periodKey, next)
	if err != nil {
		return 0, fmt.Errorf("advance pass number counter: %w", err)
	}
	// The row is locked, so nothing should have moved it under us. If it somehow did, the
	// number this claim read is somebody else's already, and saying so is better than
	// handing out a duplicate that dies on uk_passes_no.
	if moved, err := db.RowsChanged(result); err != nil {
		return 0, err
	} else if !moved {
		return 0, errs.Conflict("pass number counter moved while this request was in flight")
	}
	return next, nil
}

// Format reads one tenant format by key, whatever its active flag. It is the admin read;
// allocation uses ActiveFormat and refuses a paused series.
func (r *Repository) Format(ctx context.Context, tenantID int64, formatKey string) (Format, error) {
	const query = `
		SELECT id, tenant_id, format_key, name, pattern, prefix, category_key, module_key,
		       sequence_width, separator_char, reset_period, gap_required
		FROM   pass_number_formats
		WHERE  tenant_id = ? AND format_key = ?`
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

// Formats is the desk's whole numbering scheme, ordered by the key a module names so the
// list is stable between reads.
func (r *Repository) Formats(ctx context.Context, tenantID int64) ([]Format, error) {
	const query = `
		SELECT id, tenant_id, format_key, name, pattern, prefix, category_key, module_key,
		       sequence_width, separator_char, reset_period, gap_required
		FROM   pass_number_formats
		WHERE  tenant_id = ?
		ORDER  BY format_key`
	var out []Format
	if err := r.q.SelectContext(ctx, &out, query, tenantID); err != nil {
		return nil, fmt.Errorf("read pass number formats: %w", err)
	}
	return out, nil
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
