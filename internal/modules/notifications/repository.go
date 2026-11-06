package notifications

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/httpx"
)

// Repository is the SQL for the queue, the inbox and the delivery ledger. It never decides
// who should be told - that is recipients.go - and never formats a message; it stores what
// it is handed and moves a row through its statuses with compare-and-set, because two
// delivery workers can reach the same row in the same second.
type Repository struct{ q db.Conn }

func NewRepository(pool *sqlx.DB) *Repository { return &Repository{q: pool} }

// In rebinds the repository to a transaction already in flight. Queueing notices happens
// inside the relay's reaction, and a half-written batch of recipients is worse than none.
func (r *Repository) In(c db.Conn) *Repository { return &Repository{q: c} }

const messageColumns = `id, tenant_id, event_key, notification_template_id, channel, locale,
	to_user_id, to_address, subject, body, pass_id, movement_id, related_type, related_id,
	dedupe_key, status, attempts, max_attempts, available_at, queued_at, sent_at, delivered_at,
	read_at, provider, provider_message_id, error_code, error_message, created_at`

// Dedupe builds the key that stops a person getting the same alert twice. It names the
// outbox row rather than the aggregate, so two genuinely different events about one pass -
// a step escalating twice, say - stay two notices, while the relay delivering one row
// three times stays one notice.
func Dedupe(eventID, recipientID int64, address string, channel Channel) string {
	// Addresses are folded for the same reason mail clients fold them: a person whose
	// address was stored twice with different spelling must not be told twice.
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d/%d/%s/%s", eventID, recipientID,
		strings.ToLower(strings.TrimSpace(address)), channel)))
	return hex.EncodeToString(sum[:])
}

// Queue files one notice. It reports false when the dedupe key says this recipient has
// already been handed this event, which is the normal outcome of a relayed-at-least-once
// event and never an error. The ON DUPLICATE clause is what makes that a quiet no-op: the
// unique key over (tenant_id, dedupe_key) would otherwise come back as a MySQL error.
func (r *Repository) Queue(ctx context.Context, m Message) (bool, error) {
	budget := m.MaxAttempts
	if budget == 0 {
		budget = maxAttempts
	}
	const query = `
		INSERT INTO notifications
			(tenant_id, event_key, notification_template_id, channel, locale, to_user_id,
			 to_address, subject, body, pass_id, related_type, related_id, dedupe_key,
			 status, attempts, max_attempts, available_at, sent_at, delivered_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE id = id`
	res, err := r.q.ExecContext(ctx, query,
		m.TenantID, m.EventKey, m.TemplateID, m.Channel, m.Locale, m.ToUserID, m.ToAddress,
		m.Subject, m.Body, m.PassID, m.RelatedType, m.RelatedID, m.DedupeKey,
		m.Status, budget, m.Available, m.SentAt, m.Delivered)
	if err != nil {
		return false, fmt.Errorf("queue %s notice for %s: %w", m.Channel, m.EventKey, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count queued notices: %w", err)
	}
	return changed > 0, nil
}

// InboxFilter is one person's notice list. UnreadOnly is the bell; EventKey lets a screen
// show only what a workflow did with a pass.
type InboxFilter struct {
	TenantID   int64
	UserID     int64
	UnreadOnly bool
	EventKey   string
	Limit      int
	Before     *httpx.Cursor
}

func (r *Repository) Inbox(ctx context.Context, f InboxFilter) ([]Message, error) {
	query := `SELECT ` + messageColumns + ` FROM notifications
		WHERE tenant_id = ? AND to_user_id = ? AND channel = 'in_app'`
	args := []any{f.TenantID, f.UserID}

	if f.UnreadOnly {
		query += ` AND read_at IS NULL`
	}
	if f.EventKey != "" {
		query += ` AND event_key = ?`
		args = append(args, f.EventKey)
	}
	if f.Before != nil {
		query += ` AND (queued_at < ? OR (queued_at = ? AND id < ?))`
		args = append(args, f.Before.At.UTC(), f.Before.At.UTC(), f.Before.ID)
	}
	query += ` ORDER BY queued_at DESC, id DESC LIMIT ?`
	args = append(args, f.Limit)

	var out []Message
	if err := r.q.SelectContext(ctx, &out, query, args...); err != nil {
		return nil, fmt.Errorf("read inbox: %w", err)
	}
	return out, nil
}

func (r *Repository) UnreadCount(ctx context.Context, tenantID, userID int64) (int, error) {
	const query = `SELECT COUNT(*) FROM notifications
		WHERE tenant_id = ? AND to_user_id = ? AND channel = 'in_app' AND read_at IS NULL`
	var n int
	if err := r.q.GetContext(ctx, &n, query, tenantID, userID); err != nil {
		return 0, fmt.Errorf("count unread notices: %w", err)
	}
	return n, nil
}

// MarkRead answers false when the row was already read or was never this person's. The
// handler turns that into a 404 rather than reporting a read that did not happen.
func (r *Repository) MarkRead(ctx context.Context, tenantID, userID, id int64) (bool, error) {
	const query = `UPDATE notifications SET read_at = UTC_TIMESTAMP(6)
		WHERE id = ? AND tenant_id = ? AND to_user_id = ? AND channel = 'in_app'
		  AND read_at IS NULL`
	res, err := r.q.ExecContext(ctx, query, id, tenantID, userID)
	if err != nil {
		return false, fmt.Errorf("mark notice read: %w", err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count read notices: %w", err)
	}
	return changed > 0, nil
}

func (r *Repository) MarkAllRead(ctx context.Context, tenantID, userID int64) (int, error) {
	const query = `UPDATE notifications SET read_at = UTC_TIMESTAMP(6)
		WHERE tenant_id = ? AND to_user_id = ? AND channel = 'in_app' AND read_at IS NULL`
	res, err := r.q.ExecContext(ctx, query, tenantID, userID)
	if err != nil {
		return 0, fmt.Errorf("mark notices read: %w", err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count read notices: %w", err)
	}
	return int(changed), nil
}

// Due reads the rows the delivery sweep may take. tenantID 0 means every tenant, which is
// what the background loop needs; a request from the desk names its own tenant.
func (r *Repository) Due(ctx context.Context, tenantID int64, at time.Time, limit int) ([]Message, error) {
	query := `SELECT ` + messageColumns + ` FROM notifications
		WHERE status = 'queued' AND available_at <= ?`
	args := []any{at.UTC()}
	if tenantID != 0 {
		query += ` AND tenant_id = ?`
		args = append(args, tenantID)
	}
	query += ` ORDER BY available_at, id LIMIT ?`
	args = append(args, limit)

	var out []Message
	if err := r.q.SelectContext(ctx, &out, query, args...); err != nil {
		return nil, fmt.Errorf("read due notices: %w", err)
	}
	return out, nil
}

// ReclaimStale re-queues rows a worker claimed and never finished - the process was killed
// mid-send, or the sweep died on the way to the gateway. Without it a notice sits in
// 'sending' for ever, which is the one queue failure an operator cannot see coming. The
// compare-and-set on 'sending' in every write is what keeps a slow live worker and this
// repair from both settling the same row.
func (r *Repository) ReclaimStale(ctx context.Context, tenantID int64, now, cutoff time.Time,
	limit int) (int, error) {
	query := `UPDATE notifications
		SET status = 'queued', available_at = ?, error_code = 'claim_abandoned',
		    error_message = 'the worker that claimed this notice never finished it'
		WHERE status = 'sending' AND updated_at < ?`
	args := []any{now.UTC(), cutoff.UTC()}
	if tenantID != 0 {
		query += ` AND tenant_id = ?`
		args = append(args, tenantID)
	}
	query += ` ORDER BY id LIMIT ?`
	args = append(args, limit)

	res, err := r.q.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("reclaim abandoned notices: %w", err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count reclaimed notices: %w", err)
	}
	return int(changed), nil
}

// Claim takes one row out of the queue for this worker. Losing the claim is normal - a
// second worker, or a request that already sent it - and reports false so the caller moves on.
func (r *Repository) Claim(ctx context.Context, id int64) (bool, error) {
	const query = `UPDATE notifications
		SET status = 'sending', attempts = attempts + 1, error_code = NULL, error_message = NULL
		WHERE id = ? AND status = 'queued'`
	res, err := r.q.ExecContext(ctx, query, id)
	if err != nil {
		return false, fmt.Errorf("claim notice %d: %w", id, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count claimed notices: %w", err)
	}
	return changed > 0, nil
}

// Sent is the provider's answer written back: 'sent' when it took the message, 'delivered'
// when it can prove it landed. Both close the row; nothing retries a message that got here.
func (r *Repository) Sent(ctx context.Context, id int64, provider, messageID string, delivered bool) error {
	status := string(StatusSent)
	if delivered {
		status = string(StatusDelivered)
	}
	const query = `UPDATE notifications
		SET status = ?, provider = ?, provider_message_id = NULLIF(?, ''),
		    sent_at = UTC_TIMESTAMP(6), delivered_at = CASE WHEN ? THEN UTC_TIMESTAMP(6) ELSE delivered_at END
		WHERE id = ? AND status = 'sending'`
	_, err := r.q.ExecContext(ctx, query, status, provider, messageID, delivered, id)
	if err != nil {
		return fmt.Errorf("record %s for notice %d: %w", status, id, err)
	}
	return nil
}

// Retry puts a row back for another attempt later. The row never goes back to attempts 0:
// max_attempts is the tenant's budget for one notice, not per try.
func (r *Repository) Retry(ctx context.Context, id int64, at time.Time, code, message string) error {
	const query = `UPDATE notifications
		SET status = 'queued', available_at = ?, error_code = ?, error_message = LEFT(?, 512)
		WHERE id = ? AND status = 'sending'`
	if _, err := r.q.ExecContext(ctx, query, at.UTC(), code, message, id); err != nil {
		return fmt.Errorf("requeue notice %d: %w", id, err)
	}
	return nil
}

// Fail closes a row that will not be sent: the budget is spent, or the notice was unsendable
// in the first place (nobody has an address, no provider is configured). The reason stays on
// the row, because "notifications are broken" is only actionable if it says which ones.
func (r *Repository) Fail(ctx context.Context, id int64, code, message string) error {
	const query = `UPDATE notifications
		SET status = 'failed', error_code = ?, error_message = LEFT(?, 512)
		WHERE id = ? AND status = 'sending'`
	if _, err := r.q.ExecContext(ctx, query, code, message, id); err != nil {
		return fmt.Errorf("fail notice %d: %w", id, err)
	}
	return nil
}

// Bounce is a delivered-to-the-gateway-then-refused message. The schema reserves the status
// for exactly that, and it is terminal: retrying an address the server refuses is how an
// operator gets a complaint.
func (r *Repository) Bounce(ctx context.Context, id int64, code, message string) error {
	const query = `UPDATE notifications
		SET status = 'bounced', error_code = ?, error_message = LEFT(?, 512), delivered_at = NULL
		WHERE id = ? AND status = 'sending'`
	if _, err := r.q.ExecContext(ctx, query, code, message, id); err != nil {
		return fmt.Errorf("bounce notice %d: %w", id, err)
	}
	return nil
}

func (r *Repository) ByID(ctx context.Context, tenantID, id int64) (Message, error) {
	const query = `SELECT ` + messageColumns + ` FROM notifications WHERE id = ? AND tenant_id = ?`
	var m Message
	if err := r.q.GetContext(ctx, &m, query, id, tenantID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Message{}, errs.NotFound("notification")
		}
		return Message{}, fmt.Errorf("read notice %d: %w", id, err)
	}
	return m, nil
}

// Reset hands a spent notice back to the queue for an operator who has fixed the cause -
// the provider they have just configured, the address they have just corrected.
func (r *Repository) Reset(ctx context.Context, tenantID, id int64, at time.Time) (bool, error) {
	const query = `UPDATE notifications
		SET status = 'queued', attempts = 0, available_at = ?, sent_at = NULL,
		    delivered_at = NULL, provider = NULL, provider_message_id = NULL,
		    error_code = NULL, error_message = NULL
		WHERE id = ? AND tenant_id = ? AND status IN ('failed', 'bounced', 'cancelled')`
	res, err := r.q.ExecContext(ctx, query, at.UTC(), id, tenantID)
	if err != nil {
		return false, fmt.Errorf("requeue notice %d: %w", id, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count requeued notices: %w", err)
	}
	return changed > 0, nil
}

// OutboxFilter is the operations list: what the queue is holding, narrowed however the screen
// is set. Unlike the inbox it is not a person's own view, so a channel and an event key do the
// filtering and the reason columns come back with the rows.
type OutboxFilter struct {
	TenantID int64
	Status   string
	Channel  string
	EventKey string
	Limit    int
	Before   *httpx.Cursor
}

func (r *Repository) Outbox(ctx context.Context, f OutboxFilter) ([]Message, error) {
	query := `SELECT ` + messageColumns + ` FROM notifications WHERE tenant_id = ?`
	args := []any{f.TenantID}

	if f.Status != "" {
		query += ` AND status = ?`
		args = append(args, f.Status)
	}
	if f.Channel != "" {
		query += ` AND channel = ?`
		args = append(args, f.Channel)
	}
	if f.EventKey != "" {
		query += ` AND event_key = ?`
		args = append(args, f.EventKey)
	}
	if f.Before != nil {
		query += ` AND (queued_at < ? OR (queued_at = ? AND id < ?))`
		args = append(args, f.Before.At.UTC(), f.Before.At.UTC(), f.Before.ID)
	}
	query += ` ORDER BY queued_at DESC, id DESC LIMIT ?`
	args = append(args, f.Limit)

	var out []Message
	if err := r.q.SelectContext(ctx, &out, query, args...); err != nil {
		return nil, fmt.Errorf("read notification outbox: %w", err)
	}
	return out, nil
}

// LedgerCount is the queue's health in one pass: how many notices sit in each status. An
// operations screen reads this before it reads any individual message.
type LedgerCount struct {
	Status string `db:"status"`
	N      int    `db:"n"`
}

func (r *Repository) Ledger(ctx context.Context, tenantID int64) ([]LedgerCount, error) {
	const query = `SELECT status, COUNT(*) AS n FROM notifications WHERE tenant_id = ? GROUP BY status`
	var out []LedgerCount
	if err := r.q.SelectContext(ctx, &out, query, tenantID); err != nil {
		return nil, fmt.Errorf("read notification ledger: %w", err)
	}
	return out, nil
}
