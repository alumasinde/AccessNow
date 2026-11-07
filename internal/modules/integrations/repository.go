package integrations

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
)

type Repository struct{ q db.Conn }

func NewRepository(q db.Conn) *Repository { return &Repository{q: q} }

func (r *Repository) In(c db.Conn) *Repository { bound := *r; bound.q = c; return &bound }

// The keys.

const keyColumns = `id, tenant_id, name, key_prefix, key_hash, environment,
	COALESCE(scopes, '[]') AS scopes, rate_limit_per_minute,
	COALESCE(allowed_origins, '[]') AS allowed_origins, last_used_at, usage_count,
	expires_at, revoked_at, revoke_reason, status, created_by, created_at, updated_at`

func (r *Repository) InsertKey(ctx context.Context, k APIKey, at time.Time) (int64, error) {
	const query = `
		INSERT INTO api_keys
			(tenant_id, name, key_prefix, key_hash, environment, scopes, rate_limit_per_minute,
			 allowed_origins, expires_at, status, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 'active', ?, ?, ?)`
	result, err := r.q.ExecContext(ctx, query, k.TenantID, k.Name, k.KeyPrefix, k.KeyHash,
		k.Environment, k.Scopes, k.RateLimit, k.AllowedOrigins, k.ExpiresAt, k.CreatedBy, at, at)
	if err != nil {
		return 0, fmt.Errorf("issue api key: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read api key id: %w", err)
	}
	return id, nil
}

func (r *Repository) Keys(ctx context.Context, tenantID int64) ([]APIKey, error) {
	var out []APIKey
	if err := r.q.SelectContext(ctx, &out, `SELECT `+keyColumns+` FROM api_keys
		WHERE tenant_id = ? ORDER BY created_at DESC, id DESC`, tenantID); err != nil {
		return nil, fmt.Errorf("read api keys: %w", err)
	}
	return out, nil
}

func (r *Repository) Key(ctx context.Context, tenantID, id int64) (APIKey, error) {
	var out APIKey
	err := r.q.GetContext(ctx, &out, `SELECT `+keyColumns+` FROM api_keys
		WHERE tenant_id = ? AND id = ?`, tenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return APIKey{}, errs.NotFound("api key")
	}
	if err != nil {
		return APIKey{}, fmt.Errorf("read api key: %w", err)
	}
	return out, nil
}

// KeyByHash is the authentication lookup. The hash is the unique key, so the search is one
// index read and a key that is not there cannot be enumerated.
func (r *Repository) KeyByHash(ctx context.Context, hash string) (APIKey, error) {
	var out APIKey
	err := r.q.GetContext(ctx, &out, `SELECT `+keyColumns+` FROM api_keys WHERE key_hash = ?`, hash)
	if errors.Is(err, sql.ErrNoRows) {
		return APIKey{}, errs.Unauthenticated("api key is not recognised")
	}
	if err != nil {
		return APIKey{}, fmt.Errorf("read api key: %w", err)
	}
	return out, nil
}

func (r *Repository) RevokeKey(ctx context.Context, tenantID, id, by int64, reason string,
	at time.Time) (bool, error) {
	const query = `UPDATE api_keys
		SET status = 'revoked', revoked_at = ?, revoke_reason = ?, updated_at = ?
		WHERE tenant_id = ? AND id = ? AND status = 'active'`
	result, err := r.q.ExecContext(ctx, query, at, reason, at, tenantID, id)
	if err != nil {
		return false, fmt.Errorf("revoke api key: %w", err)
	}
	return db.RowsChanged(result)
}

func (r *Repository) TouchKey(ctx context.Context, id int64, at time.Time) error {
	if _, err := r.q.ExecContext(ctx, `UPDATE api_keys
		SET last_used_at = ?, usage_count = usage_count + 1 WHERE id = ?`, at, id); err != nil {
		return fmt.Errorf("count api key use: %w", err)
	}
	return nil
}

// The endpoints.

const webhookColumns = `id, tenant_id, name, url, secret_ref,
	COALESCE(events, '[]') AS events, subscribe_all, timeout_ms, max_retries, description,
	failure_count, consecutive_failures, last_success_at, last_attempt_at, disabled_reason,
	is_active, created_by, created_at, updated_at`

func (r *Repository) Webhooks(ctx context.Context, tenantID int64) ([]Webhook, error) {
	var out []Webhook
	if err := r.q.SelectContext(ctx, &out, `SELECT `+webhookColumns+` FROM webhook_endpoints
		WHERE tenant_id = ? ORDER BY created_at DESC, id DESC`, tenantID); err != nil {
		return nil, fmt.Errorf("read webhook endpoints: %w", err)
	}
	return out, nil
}

func (r *Repository) Webhook(ctx context.Context, tenantID, id int64) (Webhook, error) {
	var out Webhook
	err := r.q.GetContext(ctx, &out, `SELECT `+webhookColumns+` FROM webhook_endpoints
		WHERE tenant_id = ? AND id = ?`, tenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Webhook{}, errs.NotFound("webhook endpoint")
	}
	if err != nil {
		return Webhook{}, fmt.Errorf("read webhook endpoint: %w", err)
	}
	return out, nil
}

func (r *Repository) InsertWebhook(ctx context.Context, w Webhook, at time.Time) (int64, error) {
	const query = `
		INSERT INTO webhook_endpoints
			(tenant_id, name, url, secret_ref, events, subscribe_all, timeout_ms, max_retries,
			 description, is_active, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?)`
	result, err := r.q.ExecContext(ctx, query, w.TenantID, w.Name, w.URL, w.SecretRef, w.Events,
		w.SubscribeAll, w.TimeoutMS, w.MaxRetries, w.Description, w.CreatedBy, at, at)
	if err != nil {
		return 0, fmt.Errorf("create webhook endpoint: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read webhook endpoint id: %w", err)
	}
	return id, nil
}

func (r *Repository) UpdateWebhook(ctx context.Context, w Webhook, at time.Time) (bool, error) {
	const query = `
		UPDATE webhook_endpoints
		SET name = ?, url = ?, secret_ref = ?, events = ?, subscribe_all = ?, timeout_ms = ?,
		    max_retries = ?, description = ?, is_active = ?, disabled_reason = CASE
		        WHEN ? = 1 THEN NULL ELSE disabled_reason END, updated_at = ?
		WHERE tenant_id = ? AND id = ?`
	result, err := r.q.ExecContext(ctx, query, w.Name, w.URL, w.SecretRef, w.Events,
		w.SubscribeAll, w.TimeoutMS, w.MaxRetries, w.Description, w.IsActive, w.IsActive, at,
		w.TenantID, w.ID)
	if err != nil {
		return false, fmt.Errorf("update webhook endpoint: %w", err)
	}
	return db.RowsChanged(result)
}

// RetireWebhook switches an endpoint off rather than deleting it: the deliveries behind it are
// the record of what a tenant's system was told, and a cascade would erase that history.
func (r *Repository) RetireWebhook(ctx context.Context, tenantID, id int64, reason string,
	at time.Time) (bool, error) {
	const query = `UPDATE webhook_endpoints
		SET is_active = 0, disabled_reason = ?, updated_at = ?
		WHERE tenant_id = ? AND id = ? AND is_active = 1`
	result, err := r.q.ExecContext(ctx, query, reason, at, tenantID, id)
	if err != nil {
		return false, fmt.Errorf("retire webhook endpoint: %w", err)
	}
	return db.RowsChanged(result)
}

// EndpointsFor is the fan-out question: which of this tenant's live receivers asked for this
// event. JSON_CONTAINS is the database's own array membership, so the decision stays in one
// indexable read instead of a Go loop over every tenant's subscriptions.
func (r *Repository) EndpointsFor(ctx context.Context, tenantID int64,
	eventKey string) ([]Webhook, error) {
	const query = `SELECT ` + webhookColumns + ` FROM webhook_endpoints
		WHERE tenant_id = ? AND is_active = 1
		  AND (subscribe_all = 1 OR JSON_CONTAINS(events, JSON_QUOTE(?)))`
	var out []Webhook
	if err := r.q.SelectContext(ctx, &out, query, tenantID, eventKey); err != nil {
		return nil, fmt.Errorf("read subscribed endpoints: %w", err)
	}
	return out, nil
}

// The deliveries.

const deliveryColumns = `d.id, d.tenant_id, d.webhook_endpoint_id, d.domain_event_id, d.event_key,
	d.attempt, d.payload_sha256, d.request_body, d.response_status, d.response_snippet,
	d.error_message, d.status, d.queued_at, d.available_at, d.sent_at, d.duration_ms, d.created_at,
	e.name AS endpoint_name, e.url AS endpoint_url`

const deliveryFrom = ` FROM webhook_deliveries d
	JOIN webhook_endpoints e ON e.id = d.webhook_endpoint_id`

// Enqueue is idempotent per (event, endpoint): the relay is at-least-once, and a tenant must
// not receive the same movement twice because a reaction ran again.
func (r *Repository) Enqueue(ctx context.Context, d Delivery, at time.Time) error {
	const query = `
		INSERT INTO webhook_deliveries
			(tenant_id, webhook_endpoint_id, domain_event_id, event_key, attempt,
			 payload_sha256, request_body, status, queued_at, available_at, created_at)
		VALUES (?, ?, ?, ?, 1, ?, ?, 'queued', ?, ?, ?)
		ON DUPLICATE KEY UPDATE id = id`
	if _, err := r.q.ExecContext(ctx, query, d.TenantID, d.EndpointID, d.DomainEventID,
		d.EventKey, d.PayloadSHA, d.RequestBody, at, at, at); err != nil {
		return fmt.Errorf("queue webhook delivery: %w", err)
	}
	return nil
}

// Due is the worker's queue: queued rows whose backoff has passed.
func (r *Repository) Due(ctx context.Context, now time.Time, limit int) ([]Delivery, error) {
	var out []Delivery
	if err := r.q.SelectContext(ctx, &out, `SELECT `+deliveryColumns+deliveryFrom+`
		WHERE d.status = 'queued' AND d.available_at <= ?
		ORDER BY d.available_at, d.id LIMIT ?`, now, limit); err != nil {
		return nil, fmt.Errorf("read due webhook deliveries: %w", err)
	}
	return out, nil
}

// Claim takes one row out of the queue and leases it until lease. The lease is what stops a
// second worker reclaiming a delivery that is still in flight: queued_at is when the row was
// first filed, which can be an hour old by the time a retry is attempted.
func (r *Repository) Claim(ctx context.Context, id int64, lease time.Time) (bool, error) {
	result, err := r.q.ExecContext(ctx, `UPDATE webhook_deliveries
		SET status = 'sending', available_at = ? WHERE id = ? AND status = 'queued'`, lease, id)
	if err != nil {
		return false, fmt.Errorf("claim webhook delivery: %w", err)
	}
	return db.RowsChanged(result)
}

// ReclaimStale puts rows back in the queue when the process that claimed them died mid-send.
// The new available_at is now, so the retry is immediate: the attempt never happened.
func (r *Repository) ReclaimStale(ctx context.Context, now time.Time, limit int) (int64, error) {
	const query = `UPDATE webhook_deliveries SET status = 'queued', available_at = ?
		WHERE status = 'sending' AND available_at < ? LIMIT ?`
	result, err := r.q.ExecContext(ctx, query, now, now, limit)
	if err != nil {
		return 0, fmt.Errorf("reclaim webhook deliveries: %w", err)
	}
	n, err := result.RowsAffected()
	return n, err
}

func (r *Repository) Succeeded(ctx context.Context, d Delivery, status int, snippet string,
	duration int, at time.Time) error {
	const query = `UPDATE webhook_deliveries
		SET status = 'succeeded', response_status = ?, response_snippet = ?, sent_at = ?,
		    duration_ms = ?, error_message = NULL
		WHERE id = ?`
	if _, err := r.q.ExecContext(ctx, query, status, snippet, at, duration, d.ID); err != nil {
		return fmt.Errorf("record webhook success: %w", err)
	}
	return nil
}

// Retry leaves the row queued with a new attempt number and a later available_at. The unique
// key is on (event, endpoint, attempt) and this is an update, so a retry cannot collide with
// itself.
func (r *Repository) Retry(ctx context.Context, d Delivery, status int, snippet, message string,
	next time.Time, duration int) error {
	const query = `UPDATE webhook_deliveries
		SET status = 'queued', attempt = attempt + 1, available_at = ?, response_status = ?,
		    response_snippet = ?, error_message = ?, sent_at = ?, duration_ms = ?
		WHERE id = ?`
	if _, err := r.q.ExecContext(ctx, query, next, nullable(status), snippet, message, next,
		duration, d.ID); err != nil {
		return fmt.Errorf("requeue webhook delivery: %w", err)
	}
	return nil
}

func (r *Repository) Dead(ctx context.Context, d Delivery, status int, snippet, message string,
	duration int, at time.Time) error {
	const query = `UPDATE webhook_deliveries
		SET status = 'dead', response_status = ?, response_snippet = ?, error_message = ?,
		    sent_at = ?, duration_ms = ?
		WHERE id = ?`
	if _, err := r.q.ExecContext(ctx, query, nullable(status), snippet, message, at, duration,
		d.ID); err != nil {
		return fmt.Errorf("bury webhook delivery: %w", err)
	}
	return nil
}

// RecordFailure counts a failed hand-off against the endpoint and switches it off after a run
// of them. It is one statement so a worker pass cannot leave the count and the disabled reason
// disagreeing.
func (r *Repository) RecordFailure(ctx context.Context, endpointID int64, reason string,
	at time.Time) error {
	const query = `UPDATE webhook_endpoints
		SET failure_count = failure_count + 1,
		    consecutive_failures = consecutive_failures + 1,
		    last_attempt_at = ?,
		    is_active = CASE WHEN consecutive_failures + 1 >= ? THEN 0 ELSE is_active END,
		    disabled_reason = CASE WHEN consecutive_failures + 1 >= ?
		        THEN ? ELSE disabled_reason END
		WHERE id = ?`
	if _, err := r.q.ExecContext(ctx, query, at, disableAfter, disableAfter, reason,
		endpointID); err != nil {
		return fmt.Errorf("count webhook failure: %w", err)
	}
	return nil
}

func (r *Repository) RecordSuccess(ctx context.Context, endpointID int64, at time.Time) error {
	const query = `UPDATE webhook_endpoints
		SET consecutive_failures = 0, last_success_at = ?, last_attempt_at = ?
		WHERE id = ?`
	if _, err := r.q.ExecContext(ctx, query, at, at, endpointID); err != nil {
		return fmt.Errorf("record webhook success: %w", err)
	}
	return nil
}

func (r *Repository) Deliveries(ctx context.Context, tenantID, endpointID int64,
	limit int) ([]Delivery, error) {
	where := "d.tenant_id = ?"
	args := []any{tenantID}
	if endpointID > 0 {
		where += " AND d.webhook_endpoint_id = ?"
		args = append(args, endpointID)
	}
	args = append(args, limit)
	var out []Delivery
	if err := r.q.SelectContext(ctx, &out, `SELECT `+deliveryColumns+deliveryFrom+
		` WHERE `+where+` ORDER BY d.queued_at DESC, d.id DESC LIMIT ?`, args...); err != nil {
		return nil, fmt.Errorf("read webhook deliveries: %w", err)
	}
	return out, nil
}

// The actuators.

const commandColumns = `c.id, c.tenant_id, c.gate_device_id, c.gate_id, c.pass_id, c.movement_id,
	c.command, c.source, c.status, c.attempts, c.idempotency_key, c.available_at, c.queued_at,
	c.sent_at, c.acked_at, c.error_message, c.issued_by_user_id, c.created_at, g.name AS gate_name`

const commandFrom = ` FROM actuator_commands c JOIN gates g ON g.id = c.gate_id`

func (r *Repository) InsertCommand(ctx context.Context, c Command, at time.Time) (int64, error) {
	const query = `
		INSERT INTO actuator_commands
			(tenant_id, gate_device_id, gate_id, pass_id, movement_id, command, source, status,
			 idempotency_key, available_at, queued_at, issued_by_user_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'queued', ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE id = LAST_INSERT_ID(id)`
	result, err := r.q.ExecContext(ctx, query, c.TenantID, c.GateDeviceID, c.GateID, c.PassID,
		c.MovementID, c.Command, c.Source, c.IdempotencyKey, at, at, c.IssuedBy, at)
	if err != nil {
		return 0, fmt.Errorf("queue actuator command: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("read actuator command id: %w", err)
	}
	return id, nil
}

func (r *Repository) Command(ctx context.Context, tenantID, id int64) (Command, error) {
	var out Command
	err := r.q.GetContext(ctx, &out, `SELECT `+commandColumns+commandFrom+`
		WHERE c.tenant_id = ? AND c.id = ?`, tenantID, id)
	if errors.Is(err, sql.ErrNoRows) {
		return Command{}, errs.NotFound("actuator command")
	}
	if err != nil {
		return Command{}, fmt.Errorf("read actuator command: %w", err)
	}
	return out, nil
}

func (r *Repository) Commands(ctx context.Context, tenantID int64, status string,
	limit int) ([]Command, error) {
	where := "c.tenant_id = ?"
	args := []any{tenantID}
	if status != "" {
		where += " AND c.status = ?"
		args = append(args, status)
	}
	args = append(args, limit)
	var out []Command
	if err := r.q.SelectContext(ctx, &out, `SELECT `+commandColumns+commandFrom+`
		WHERE `+where+` ORDER BY c.queued_at DESC, c.id DESC LIMIT ?`, args...); err != nil {
		return nil, fmt.Errorf("read actuator commands: %w", err)
	}
	return out, nil
}

// Ack closes a command with what the device said. Only a command that was sent can be
// confirmed, and only once: a second acknowledgement is a conflict, not a new fact.
func (r *Repository) Ack(ctx context.Context, tenantID, id int64, status, message string,
	at time.Time) (bool, error) {
	const query = `UPDATE actuator_commands
		SET status = ?, acked_at = ?, error_message = NULLIF(?, '')
		WHERE tenant_id = ? AND id = ? AND status IN ('queued', 'sending', 'sent')`
	result, err := r.q.ExecContext(ctx, query, status, at, message, tenantID, id)
	if err != nil {
		return false, fmt.Errorf("acknowledge actuator command: %w", err)
	}
	return db.RowsChanged(result)
}

// GateCommands is the tablet's pull: work queued for this gate that has not been confirmed.
// A sent row is still returned because a device that crashed after pulling must be able to
// pick the instruction up again; the ack is what closes it.
func (r *Repository) GateCommands(ctx context.Context, tenantID, gateID int64,
	limit int) ([]Command, error) {
	var out []Command
	if err := r.q.SelectContext(ctx, &out, `SELECT `+commandColumns+commandFrom+`
		WHERE c.tenant_id = ? AND c.gate_id = ? AND c.status IN ('queued', 'sent')
		ORDER BY c.available_at ASC, c.id ASC LIMIT ?`, tenantID, gateID, limit); err != nil {
		return nil, fmt.Errorf("read gate actuator commands: %w", err)
	}
	return out, nil
}

// AckAtGate closes a command only if it belongs to the gate the device is standing at. The
// desk's ack path is tenant-wide; this one is deliberately narrower.
func (r *Repository) AckAtGate(ctx context.Context, tenantID, gateID, id int64, status,
	message string, at time.Time) (bool, error) {
	const query = `UPDATE actuator_commands
		SET status = ?, acked_at = ?, error_message = NULLIF(?, '')
		WHERE tenant_id = ? AND gate_id = ? AND id = ? AND status IN ('queued', 'sending', 'sent')`
	result, err := r.q.ExecContext(ctx, query, status, at, message, tenantID, gateID, id)
	if err != nil {
		return false, fmt.Errorf("acknowledge gate actuator command: %w", err)
	}
	return db.RowsChanged(result)
}

// GateKnown checks the gate belongs to the tenant before a command names it. The foreign key
// would accept another tenant's gate id; this is what stops that becoming a row.
func (r *Repository) GateKnown(ctx context.Context, tenantID, gateID int64) (bool, error) {
	var n int
	if err := r.q.GetContext(ctx, &n, `SELECT COUNT(*) FROM gates
		WHERE tenant_id = ? AND id = ? AND deleted_at IS NULL`, tenantID, gateID); err != nil {
		return false, fmt.Errorf("read gate: %w", err)
	}
	return n > 0, nil
}

func (r *Repository) ModuleEnabled(ctx context.Context, tenantID int64) (bool, error) {
	var n int
	if err := r.q.GetContext(ctx, &n, `SELECT COUNT(*) FROM tenant_modules
		WHERE tenant_id = ? AND module_key = ? AND enabled = 1`, tenantID, ModuleKey); err != nil {
		return false, fmt.Errorf("read integrations module flag: %w", err)
	}
	return n > 0, nil
}

func nullable(value int) any {
	if value <= 0 {
		return nil
	}
	return value
}

// contains is used by the payload builder for a quick subscription sanity check in tests.
func contains(values []string, want string) bool {
	for _, value := range values {
		if strings.EqualFold(value, want) {
			return true
		}
	}
	return false
}
