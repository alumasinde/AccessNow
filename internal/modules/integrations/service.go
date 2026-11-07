package integrations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"accessnow/internal/core/access"
	"accessnow/internal/platform/audit"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/events"
	"accessnow/internal/platform/logging"
	"accessnow/internal/platform/security"
	"accessnow/internal/platform/tenancy"
)

// Service is the module's whole surface: credentials, receivers and the commands that move a
// barrier. A webhook is a notification to a machine, so it is signed, retried and attributed
// exactly like one; an actuator command is a physical action, so it is a row before it is a
// wire signal.
type Service struct {
	pool   *sqlx.DB
	repo   *Repository
	access *access.Service
	events events.Publisher
	chain  audit.Writer
	now    func() time.Time
}

func NewService(pool *sqlx.DB, repo *Repository, accessSvc *access.Service, bus events.Publisher,
	chain audit.Writer) *Service {
	return &Service{pool: pool, repo: repo, access: accessSvc, events: bus, chain: chain,
		now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) enabled(ctx context.Context, tenantID int64) (bool, error) {
	if s.access == nil {
		return true, nil
	}
	return s.access.Enabled(ctx, tenantID, ModuleKey)
}

// The keys.

// keyPrefixLength is how much of a secret is stored for display. The secret itself is a
// 256-bit base64url string; eight characters are enough to tell two keys apart and far too few
// to guess the rest.
const keyPrefixLength = 8

// IssueKey mints a credential and returns it once. The row keeps a SHA-256 and a prefix, so a
// database dump does not hand anybody a working key.
func (s *Service) IssueKey(ctx context.Context, scope tenancy.Scope, req KeyRequest) (APIKey, string, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return APIKey{}, "", errs.BadField("name", "is required")
	}
	environment := strings.TrimSpace(req.Environment)
	if environment == "" {
		environment = "live"
	}
	if environment != "live" && environment != "test" {
		return APIKey{}, "", errs.BadField("environment", "must be live or test")
	}
	scopes, err := scopeList(req.Scopes)
	if err != nil {
		return APIKey{}, "", err
	}
	encScopes, err := encodeStrings(scopes)
	if err != nil {
		return APIKey{}, "", errs.Internal(fmt.Errorf("encode scopes: %w", err))
	}
	origins, err := originList(req.AllowedOrigins)
	if err != nil {
		return APIKey{}, "", err
	}
	encOrigins, err := encodeStrings(origins)
	if err != nil {
		return APIKey{}, "", errs.Internal(fmt.Errorf("encode origins: %w", err))
	}
	if req.RateLimit != nil && (*req.RateLimit < 1 || *req.RateLimit > 6000) {
		return APIKey{}, "", errs.BadField("rate_limit_per_minute", "must be between 1 and 6000")
	}
	now := s.now()
	if req.ExpiresAt != nil && !req.ExpiresAt.After(now) {
		return APIKey{}, "", errs.BadField("expires_at", "must be in the future")
	}

	secret, err := security.NewToken()
	if err != nil {
		return APIKey{}, "", errs.Internal(err)
	}
	prefix := secret
	if len(prefix) > keyPrefixLength {
		prefix = prefix[:keyPrefixLength]
	}
	key := APIKey{
		TenantID: scope.TenantID, Name: name, KeyPrefix: prefix,
		KeyHash: security.HashToken(secret), Environment: environment, Scopes: encScopes,
		RateLimit: req.RateLimit, AllowedOrigins: encOrigins, ExpiresAt: req.ExpiresAt,
		CreatedBy: optionalID(scope.UserID),
	}

	var id int64
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		filed, err := s.repo.In(c).InsertKey(ctx, key, now)
		if err != nil {
			return err
		}
		id = filed
		// The secret is never in the audit row: the row says a key was issued, to whom and
		// with which scopes, and the plaintext exists only in the response.
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "integration.key_issued",
			SubjectType: "api_key", SubjectID: filed, SiteID: scope.SiteID,
			After: map[string]any{"name": name, "prefix": prefix, "environment": environment,
				"scopes": scopes, "expires_at": req.ExpiresAt},
		})
	})
	if err != nil {
		return APIKey{}, "", err
	}
	stored, err := s.repo.Key(ctx, scope.TenantID, id)
	if err != nil {
		return APIKey{}, "", err
	}
	return stored, secret, nil
}

func (s *Service) Keys(ctx context.Context, tenantID int64) ([]APIKey, error) {
	return s.repo.Keys(ctx, tenantID)
}

// RevokeKey ends a credential. The reason is required: a key that stops working at 3am is a
// question somebody will ask, and "revoked" alone does not answer it.
func (s *Service) RevokeKey(ctx context.Context, scope tenancy.Scope, id int64,
	reason string) (APIKey, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return APIKey{}, errs.BadField("reason", "is required to revoke a key")
	}
	now := s.now()
	var revoked bool
	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		moved, err := s.repo.In(c).RevokeKey(ctx, scope.TenantID, id, scope.UserID, reason, now)
		if err != nil {
			return err
		}
		revoked = moved
		if !moved {
			return nil
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "integration.key_revoked",
			SubjectType: "api_key", SubjectID: id, SiteID: scope.SiteID, Reason: reason,
			After: map[string]any{"status": KeyRevoked},
		})
	})
	if err != nil {
		return APIKey{}, err
	}
	if !revoked {
		key, err := s.repo.Key(ctx, scope.TenantID, id)
		if err != nil {
			return APIKey{}, err
		}
		return APIKey{}, errs.Conflict("key is already " + key.Status)
	}
	return s.repo.Key(ctx, scope.TenantID, id)
}

// Authenticate resolves a presented secret into the key it names. A key that ran out is
// refused here even if no sweep has marked it expired, and a revoked key is refused whether or
// not its expiry has passed.
func (s *Service) Authenticate(ctx context.Context, raw string) (APIKey, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return APIKey{}, errs.Unauthenticated("api key is required")
	}
	key, err := s.repo.KeyByHash(ctx, security.HashToken(raw))
	if err != nil {
		return APIKey{}, err
	}
	if !key.Live(s.now()) {
		return APIKey{}, errs.Unauthenticated("api key is no longer valid")
	}
	if err := s.repo.TouchKey(ctx, key.ID, s.now()); err != nil {
		logging.From(ctx).ErrorContext(ctx, "could not count api key use", "key_id", key.ID, "error", err)
	}
	return key, nil
}

// The endpoints.

func (s *Service) Webhooks(ctx context.Context, tenantID int64) ([]Webhook, error) {
	return s.repo.Webhooks(ctx, tenantID)
}

func (s *Service) Webhook(ctx context.Context, tenantID, id int64) (Webhook, error) {
	return s.repo.Webhook(ctx, tenantID, id)
}

func (s *Service) CreateWebhook(ctx context.Context, scope tenancy.Scope,
	req WebhookRequest) (Webhook, error) {
	hook, err := s.buildWebhook(req, Webhook{TenantID: scope.TenantID})
	if err != nil {
		return Webhook{}, err
	}
	hook.CreatedBy = optionalID(scope.UserID)
	now := s.now()

	var id int64
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		filed, err := s.repo.In(c).InsertWebhook(ctx, hook, now)
		if err != nil {
			return err
		}
		id = filed
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "integration.webhook_created",
			SubjectType: "webhook_endpoint", SubjectID: filed, SiteID: scope.SiteID,
			After: map[string]any{"name": hook.Name, "url": hook.URL,
				"events": hook.EventList(), "subscribe_all": hook.SubscribeAll},
		})
	})
	if err != nil {
		if db.IsDuplicate(err) {
			return Webhook{}, errs.Conflict("an endpoint with that name already exists")
		}
		return Webhook{}, err
	}
	return s.repo.Webhook(ctx, scope.TenantID, id)
}

// UpdateWebhook replaces the receiver. An inactive endpoint stays inactive until the caller
// says otherwise: switching a broken receiver back on should be a decision, not a side effect
// of editing its events.
func (s *Service) UpdateWebhook(ctx context.Context, scope tenancy.Scope, id int64,
	req WebhookRequest) (Webhook, error) {
	existing, err := s.repo.Webhook(ctx, scope.TenantID, id)
	if err != nil {
		return Webhook{}, err
	}
	hook, err := s.buildWebhook(req, existing)
	if err != nil {
		return Webhook{}, err
	}
	hook.ID = id
	hook.IsActive = existing.IsActive
	if req.IsActive != nil {
		hook.IsActive = *req.IsActive
	}
	now := s.now()

	var changed bool
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		moved, err := s.repo.In(c).UpdateWebhook(ctx, hook, now)
		if err != nil {
			return err
		}
		changed = moved
		if !moved {
			return nil
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "integration.webhook_updated",
			SubjectType: "webhook_endpoint", SubjectID: id, SiteID: scope.SiteID,
			After: map[string]any{"name": hook.Name, "url": hook.URL,
				"events": hook.EventList(), "is_active": hook.IsActive},
		})
	})
	if err != nil {
		if db.IsDuplicate(err) {
			return Webhook{}, errs.Conflict("an endpoint with that name already exists")
		}
		return Webhook{}, err
	}
	if !changed {
		return Webhook{}, errs.NotFound("webhook endpoint")
	}
	return s.repo.Webhook(ctx, scope.TenantID, id)
}

func (s *Service) RetireWebhook(ctx context.Context, scope tenancy.Scope, id int64) error {
	var retired bool
	now := s.now()
	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		moved, err := s.repo.In(c).RetireWebhook(ctx, scope.TenantID, id,
			"retired by the desk", now)
		if err != nil {
			return err
		}
		retired = moved
		if !moved {
			return nil
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "integration.webhook_retired",
			SubjectType: "webhook_endpoint", SubjectID: id, SiteID: scope.SiteID,
			After: map[string]any{"is_active": false},
		})
	})
	if err != nil {
		return err
	}
	if !retired {
		return errs.NotFound("webhook endpoint")
	}
	return nil
}

// TestWebhook queues one synthetic delivery so a tenant can see their receiver answer without
// waiting for the event they care about.
func (s *Service) TestWebhook(ctx context.Context, scope tenancy.Scope, id int64) (Delivery, error) {
	hook, err := s.repo.Webhook(ctx, scope.TenantID, id)
	if err != nil {
		return Delivery{}, err
	}
	body := []byte(`{"event":"webhook.test","message":"This is a test delivery from AccessNow."}`)
	sum := sha256.Sum256(body)
	payload := string(body)
	digest := hex.EncodeToString(sum[:])
	delivery := Delivery{
		TenantID: scope.TenantID, EndpointID: hook.ID, EventKey: "webhook.test",
		PayloadSHA: &digest, RequestBody: &payload,
	}
	if err := s.repo.Enqueue(ctx, delivery, s.now()); err != nil {
		return Delivery{}, err
	}
	rows, err := s.repo.Deliveries(ctx, scope.TenantID, hook.ID, 1)
	if err != nil {
		return Delivery{}, err
	}
	if len(rows) == 0 {
		return Delivery{}, errs.NotFound("delivery")
	}
	return rows[0], nil
}

func (s *Service) Deliveries(ctx context.Context, tenantID, endpointID int64) ([]Delivery, error) {
	return s.repo.Deliveries(ctx, tenantID, endpointID, 100)
}

// OnEvent is the fan-out. It runs for every outbox row; the tenant's endpoints decide whether
// that means anything, and a tenant without the module has none.
func (s *Service) OnEvent(ctx context.Context, e events.Event) error {
	on, err := s.enabled(ctx, e.TenantID)
	if err != nil {
		return err
	}
	if !on {
		return nil
	}
	endpoints, err := s.repo.EndpointsFor(ctx, e.TenantID, e.EventKey)
	if err != nil {
		return err
	}
	if len(endpoints) == 0 {
		return nil
	}
	body, err := marshalEvent(e, s.now())
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	digest := hex.EncodeToString(sum[:])
	payload := string(body)
	now := s.now()
	for _, endpoint := range endpoints {
		if !endpoint.Wants(e.EventKey) {
			continue
		}
		if err := s.repo.Enqueue(ctx, Delivery{
			TenantID: e.TenantID, EndpointID: endpoint.ID, DomainEventID: eventID(e),
			EventKey: e.EventKey, PayloadSHA: &digest, RequestBody: &payload,
		}, now); err != nil {
			return err
		}
	}
	return nil
}

// DeliverDue is one worker pass: stale claims back in the queue, then every due delivery.
func (s *Service) DeliverDue(ctx context.Context) (int, error) {
	now := s.now()
	if _, err := s.repo.ReclaimStale(ctx, now, deliveryBatch); err != nil {
		return 0, err
	}
	due, err := s.repo.Due(ctx, now, deliveryBatch)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, delivery := range due {
		claimed, err := s.repo.Claim(ctx, delivery.ID, now.Add(staleClaim))
		if err != nil {
			return sent, err
		}
		if !claimed {
			continue
		}
		if err := s.deliver(ctx, delivery); err != nil {
			return sent, err
		}
		sent++
	}
	return sent, nil
}

// deliver hands one row to its endpoint and records what happened. A receiver that answers with
// any 2xx succeeded; anything else is a retry until the endpoint's budget runs out.
func (s *Service) deliver(ctx context.Context, delivery Delivery) error {
	hook, err := s.repo.Webhook(ctx, delivery.TenantID, delivery.EndpointID)
	if err != nil {
		// The endpoint was deleted between the queue read and the send. That is not a failure
		// of the receiver, so the delivery is buried without counting against anybody.
		return s.repo.Dead(ctx, delivery, 0, "", "endpoint no longer exists", 0, s.now())
	}
	if !hook.IsActive {
		return s.repo.Dead(ctx, delivery, 0, "", "endpoint is switched off", 0, s.now())
	}
	body := []byte("{}")
	if delivery.RequestBody != nil {
		body = []byte(*delivery.RequestBody)
	}

	started := s.now()
	status, snippet, sendErr := post(ctx, hook, body)
	duration := int(s.now().Sub(started).Milliseconds())
	at := s.now()

	if sendErr == nil && status >= 200 && status < 300 {
		if err := s.repo.Succeeded(ctx, delivery, status, snippet, duration, at); err != nil {
			return err
		}
		return s.repo.RecordSuccess(ctx, hook.ID, at)
	}

	message := "receiver answered " + fmt.Sprint(status)
	if sendErr != nil {
		message = sendErr.Error()
	}
	logging.From(ctx).WarnContext(ctx, "webhook delivery failed",
		"endpoint_id", hook.ID, "delivery_id", delivery.ID, "status", status, "error", message)

	if delivery.Attempt >= hook.MaxRetries {
		if err := s.repo.Dead(ctx, delivery, status, snippet, message, duration, at); err != nil {
			return err
		}
	} else {
		next := at.Add(backoff(delivery.Attempt))
		if err := s.repo.Retry(ctx, delivery, status, snippet, message, next, duration); err != nil {
			return err
		}
	}
	return s.repo.RecordFailure(ctx, hook.ID, message, at)
}

// The actuators.

func (s *Service) EnqueueCommand(ctx context.Context, scope tenancy.Scope,
	req CommandRequest) (Command, error) {
	on, err := s.enabled(ctx, scope.TenantID)
	if err != nil {
		return Command{}, err
	}
	if !on {
		return Command{}, errs.ModuleDisabled(ModuleKey)
	}
	if req.GateID <= 0 {
		return Command{}, errs.BadField("gate_id", "is required")
	}
	if !knownCommand(req.Command) {
		return Command{}, errs.BadField("command", "is not an actuator command")
	}
	known, err := s.repo.GateKnown(ctx, scope.TenantID, req.GateID)
	if err != nil {
		return Command{}, err
	}
	if !known {
		return Command{}, errs.NotFound("gate")
	}
	source := req.Source
	if source == "" {
		source = SourceManual
	}
	if !knownSource(source) {
		return Command{}, errs.BadField("source", "is not a command source")
	}
	idempotency := strings.TrimSpace(req.IdempotencyKey)
	if len(idempotency) > 64 {
		return Command{}, errs.BadField("idempotency_key", "must be 64 characters or fewer")
	}

	now := s.now()
	command := Command{
		TenantID: scope.TenantID, GateID: req.GateID, PassID: optionalPtr(req.PassID),
		MovementID: optionalPtr(req.MovementID), Command: req.Command, Source: source,
		IdempotencyKey: optionalText(idempotency), IssuedBy: optionalID(scope.UserID),
	}

	var id int64
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		filed, err := s.repo.In(c).InsertCommand(ctx, command, now)
		if err != nil {
			return err
		}
		id = filed
		// A repeated command with the same idempotency key returns the original row; the audit
		// entry says it was asked for again, which is the difference between a retry and a
		// second gate lift.
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "integration.actuator_queued",
			SubjectType: "actuator_command", SubjectID: filed, PassID: valueOrZero(command.PassID),
			SiteID: scope.SiteID, After: map[string]any{
				"gate_id": req.GateID, "command": req.Command, "source": source,
				"idempotency_key": idempotency,
			},
		})
	})
	if err != nil {
		return Command{}, err
	}
	return s.repo.Command(ctx, scope.TenantID, id)
}

func (s *Service) Commands(ctx context.Context, tenantID int64, status string) ([]Command, error) {
	return s.repo.Commands(ctx, tenantID, status, 100)
}

// AckCommand closes a queued command with what happened at the barrier. A command already
// confirmed cannot be confirmed again: the second acknowledgement is a conflict, not a fact.
func (s *Service) AckCommand(ctx context.Context, scope tenancy.Scope, id int64,
	req AckRequest) (Command, error) {
	status := strings.TrimSpace(req.Status)
	switch status {
	case CommandConfirmed, CommandFailed, CommandTimeout:
	default:
		return Command{}, errs.BadField("status", "must be confirmed, failed or timeout")
	}
	var closed bool
	now := s.now()
	err := db.Transact(ctx, s.pool, func(c db.Conn) error {
		moved, err := s.repo.In(c).Ack(ctx, scope.TenantID, id, status, req.Message, now)
		if err != nil {
			return err
		}
		closed = moved
		if !moved {
			return nil
		}
		return s.chain.Write(ctx, c, audit.Entry{
			TenantID: scope.TenantID, ActorUserID: scope.UserID, Action: "integration.actuator_acked",
			SubjectType: "actuator_command", SubjectID: id, SiteID: scope.SiteID,
			After: map[string]any{"status": status, "message": req.Message},
		})
	})
	if err != nil {
		return Command{}, err
	}
	if !closed {
		command, err := s.repo.Command(ctx, scope.TenantID, id)
		if err != nil {
			return Command{}, err
		}
		return Command{}, errs.Conflict("command is already " + command.Status)
	}
	return s.repo.Command(ctx, scope.TenantID, id)
}

// buildWebhook validates a receiver. An https endpoint with a public address is the rule: a
// webhook URL is a request this server makes, and pointing it at 127.0.0.1 is how a tenant
// probes the network the API runs in.
func (s *Service) buildWebhook(req WebhookRequest, existing Webhook) (Webhook, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return Webhook{}, errs.BadField("name", "is required")
	}
	if err := validWebhookURL(req.URL); err != nil {
		return Webhook{}, err
	}
	events, err := eventList(req.Events)
	if err != nil {
		return Webhook{}, err
	}
	encEvents, err := encodeStrings(events)
	if err != nil {
		return Webhook{}, errs.Internal(fmt.Errorf("encode events: %w", err))
	}
	if len(events) == 0 && !req.SubscribeAll {
		return Webhook{}, errs.BadField("events", "name at least one event or subscribe to all")
	}
	secretRef := strings.TrimSpace(req.SecretRef)
	if secretRef != "" && !validSecretRef(secretRef) {
		return Webhook{}, errs.BadField("secret_ref",
			"must be an environment variable name written in capitals")
	}
	timeout := 5000
	if req.TimeoutMS != nil {
		timeout = *req.TimeoutMS
	}
	if timeout < 500 || timeout > 10000 {
		return Webhook{}, errs.BadField("timeout_ms", "must be between 500 and 10000")
	}
	retries := 5
	if req.MaxRetries != nil {
		retries = *req.MaxRetries
	}
	if retries < 0 || retries > 10 {
		return Webhook{}, errs.BadField("max_retries", "must be between 0 and 10")
	}

	hook := Webhook{
		TenantID: existing.TenantID, ID: existing.ID, Name: name, URL: strings.TrimSpace(req.URL),
		SecretRef: optionalText(secretRef), Events: encEvents, SubscribeAll: req.SubscribeAll,
		TimeoutMS: timeout, MaxRetries: retries, Description: optionalText(req.Description),
		IsActive: existing.IsActive, CreatedBy: existing.CreatedBy,
	}
	if existing.ID == 0 {
		hook.IsActive = true
	}
	if req.IsActive != nil {
		hook.IsActive = *req.IsActive
	}
	return hook, nil
}

// validWebhookURL refuses anything that is not a public https address. The dialer refuses a
// private address at connect time too, because a name can resolve differently by then.
func validWebhookURL(raw string) error {
	value := strings.TrimSpace(raw)
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return errs.BadField("url", "must be an https URL")
	}
	if parsed.User != nil {
		return errs.BadField("url", "must not carry credentials")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return errs.BadField("url", "must not point at this host")
	}
	if ip := net.ParseIP(host); ip != nil && !publicIP(ip) {
		return errs.BadField("url", "must not point at a private address")
	}
	return nil
}

func knownCommand(value string) bool {
	switch value {
	case "open_boom_gate", "close_boom_gate", "release_turnstile", "barrier_up",
		"door_unlock", "light_on", "siren", "log_only":
		return true
	}
	return false
}

func knownSource(value string) bool {
	switch value {
	case SourceManual, SourceApproved, SourceEmergency, SourceRule:
		return true
	}
	return false
}

func optionalID(id int64) *int64 { return optionalPtr(id) }

func optionalPtr(id int64) *int64 {
	if id <= 0 {
		return nil
	}
	return &id
}

func optionalText(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func valueOrZero(id *int64) int64 {
	if id == nil {
		return 0
	}
	return *id
}

func eventID(e events.Event) *int64 {
	if e.EventID == 0 {
		return nil
	}
	id := e.EventID
	return &id
}

func backoff(attempt int) time.Duration {
	wait := firstBackoff
	for i := 1; i < attempt; i++ {
		wait *= 2
		if wait >= worstBackoff {
			return worstBackoff
		}
	}
	return wait
}

// marshalEvent is the body a receiver gets: who, what, and the payload the producing module
// wrote. The time is the hand-off time; the event's own instant is in its payload.
func marshalEvent(e events.Event, at time.Time) ([]byte, error) {
	body := map[string]any{
		"event": e.EventKey, "event_id": e.EventID, "tenant_id": e.TenantID,
		"aggregate_type": e.AggregateType, "aggregate_id": e.AggregateID,
		"pass_id": e.PassID, "site_id": e.SiteID, "gate_id": e.GateID,
		"delivered_at": at.UTC().Format(time.RFC3339), "data": e.Payload,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("encode webhook payload: %w", err)
	}
	return raw, nil
}
