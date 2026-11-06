package passes

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// Cacheable is one pass a device may be handed for a window: usable, overlapping the window,
// and of a category the gate it works is allowed to honour. It is Core's own answer rather
// than a caller's guess, because "scannable here, then" is the same rule the barrier applies
// live, and an offline cache stocked by a second opinion is a cache that disagrees with the
// gate it belongs to.
type Cacheable struct {
	PassID      int64      `db:"pass_id"`
	PassNo      string     `db:"pass_no"`
	CategoryKey string     `db:"category_key"`
	ModuleKey   string     `db:"source_module_key"`
	Status      string     `db:"status"`
	SubjectName string     `db:"subject_name"`
	ValidFrom   time.Time  `db:"valid_from"`
	ValidUntil  *time.Time `db:"valid_until"`
	EntryCount  int        `db:"entry_count"`
}

// CacheQuery is the window a device is being stocked for, and the categories that window is
// limited to. No categories means the gate admits every category, which is what a site with
// one entrance and no category rows has always meant.
type CacheQuery struct {
	TenantID   int64
	Categories []string
	From       time.Time
	To         time.Time
	Limit      int
}

// usableStatuses is Pass.Usable() as a list, so the read that stocks a device's cache asks
// the same question the barrier answers rather than a second opinion of it.
var usableStatuses = []string{
	string(StatusApproved), string(StatusActive), string(StatusOnSite),
}

// Cacheable reads the register for a window. It is a keyset-free read on purpose: the whole
// set is what a device has to hold, so a page of it would be a cache with holes in it, and
// the caller's limit is a guard against a tenant whose register has grown past anything a
// tablet can hold, not a way through it.
func (r *Repository) Cacheable(ctx context.Context, q CacheQuery) ([]Cacheable, error) {
	where := []string{
		"tenant_id = ?", "deleted_at IS NULL",
		// Approved has permission, active has used it and may have another entry in it,
		// on_site is inside and has to be able to leave. Everything else is a request or
		// history, and neither of those belongs on a barrier.
		"p.status IN (" + placeholders(len(usableStatuses)) + ")",
		"p.valid_from < ?",
		"(p.valid_until IS NULL OR (p.valid_until > p.valid_from AND p.valid_until > ?))",
	}
	args := []any{q.TenantID}
	for _, status := range usableStatuses {
		args = append(args, status)
	}
	args = append(args, q.To.UTC(), q.From.UTC())

	if len(q.Categories) > 0 {
		where = append(where, "category_key IN ("+placeholders(len(q.Categories))+")")
		for _, key := range q.Categories {
			args = append(args, key)
		}
	}
	args = append(args, q.Limit)

	const columns = `p.id AS pass_id, p.pass_no, p.category_key, p.source_module_key, p.status,
		p.subject_name, p.valid_from, p.valid_until, p.entry_count`
	query := `SELECT ` + columns + ` FROM passes p WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY p.valid_until IS NULL, p.valid_until ASC, p.id ASC
		LIMIT ?`

	var out []Cacheable
	if err := r.q.SelectContext(ctx, &out, query, args...); err != nil {
		return nil, fmt.Errorf("read the cacheable register: %w", err)
	}
	return out, nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}
