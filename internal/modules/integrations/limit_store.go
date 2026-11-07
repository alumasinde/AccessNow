package integrations

import (
	"context"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// dbKeyLimiter keeps one fixed window per credential in MySQL. The spend is a single atomic
// upsert, so two API processes answering at the same moment advance the same counter; the read
// after it is what turns that counter into an answer.
type dbKeyLimiter struct {
	db *sqlx.DB
}

func newDBKeyLimiter(db *sqlx.DB) *dbKeyLimiter {
	return &dbKeyLimiter{db: db}
}

// spend rolls the window over when it is at least a minute old, otherwise it adds one. Both
// assignments read the old window_start, so the condition and the reset agree.
const spendWindow = `INSERT INTO api_key_rate_windows (api_key_id, window_start, hits)
	VALUES (?, ?, 1)
	ON DUPLICATE KEY UPDATE
		hits = IF(window_start <= ?, 1, hits + 1),
		window_start = IF(window_start <= ?, ?, window_start)`

func (l *dbKeyLimiter) allow(ctx context.Context, keyID int64, limit int,
	now time.Time) (bool, time.Duration, error) {
	if limit <= 0 {
		limit = defaultKeyRateLimit
	}
	now = now.UTC()
	cutoff := now.Add(-time.Minute)
	if _, err := l.db.ExecContext(ctx, spendWindow, keyID, now, cutoff, cutoff, now); err != nil {
		return false, 0, fmt.Errorf("spend api key rate window: %w", err)
	}
	var window struct {
		Start time.Time `db:"window_start"`
		Hits  int       `db:"hits"`
	}
	if err := l.db.GetContext(ctx, &window,
		`SELECT window_start, hits FROM api_key_rate_windows WHERE api_key_id = ?`,
		keyID); err != nil {
		return false, 0, fmt.Errorf("read api key rate window: %w", err)
	}
	if window.Hits > limit {
		return false, window.Start.Add(time.Minute).Sub(now), nil
	}
	return true, 0, nil
}
