// It issues API keys (only hashes are stored), fans outbound webhooks from the outbox with a
// signed payload and a retry ladder, and queues actuator commands so a boom gate is lifted by a
// row somebody can audit rather than a side effect nobody recorded.
package integrations

import (
	"encoding/json"
	"strings"
	"time"
)

// ModuleKey is the tenant's switch for this surface.
const ModuleKey = "integrations_api"

// aggregateType names this module's rows on the outbox.
const aggregateType = "integration"

// deliveryBatch caps one worker pass.
const deliveryBatch = 20

// firstBackoff and worstBackoff frame the retry ladder for a webhook that did not answer.
const (
	firstBackoff = 30 * time.Second
	worstBackoff = 30 * time.Minute
)

// staleClaim is how long a delivery may sit in 'sending' before the next pass assumes the worker
// that claimed it is gone. The sender's own timeout is capped at ten seconds.
const staleClaim = 2 * time.Minute

// disableAfter is how many deliveries in a row must fail before the endpoint is switched off.
// A receiver that has been returning 500 for an hour is not a receiver with a transient problem.
const disableAfter = 20

// KeyStatus mirrors chk_api_keys_status.
const (
	KeyActive  = "active"
	KeyExpired = "expired"
	KeyRevoked = "revoked"
)

// DeliveryStatus mirrors chk_webhook_deliveries_status.
const (
	DeliveryQueued    = "queued"
	DeliverySending   = "sending"
	DeliverySucceeded = "succeeded"
	DeliveryFailed    = "failed"
	DeliveryDead      = "dead"
)

// CommandStatus mirrors chk_actuator_commands_status.
const (
	CommandQueued    = "queued"
	CommandSending   = "sending"
	CommandSent      = "sent"
	CommandConfirmed = "confirmed"
	CommandFailed    = "failed"
	CommandTimeout   = "timeout"
	CommandCancelled = "cancelled"
)

// CommandSource is why a gate was told to open. An approved scan is the only source this
// module produces itself; the rest are a person overriding, an emergency or a rule.
const (
	SourceManual    = "manual"
	SourceApproved  = "approved_scan"
	SourceEmergency = "emergency"
	SourceRule      = "rule"
)

// APIKey is one issued credential. The secret is not in this struct: the row keeps a SHA-256
// and a short prefix, and the plaintext is handed over once at issue.
type APIKey struct {
	ID             int64      `db:"id"`
	TenantID       int64      `db:"tenant_id"`
	Name           string     `db:"name"`
	KeyPrefix      string     `db:"key_prefix"`
	KeyHash        string     `db:"key_hash"`
	Environment    string     `db:"environment"`
	Scopes         string     `db:"scopes"`
	RateLimit      *int       `db:"rate_limit_per_minute"`
	AllowedOrigins string     `db:"allowed_origins"`
	LastUsedAt     *time.Time `db:"last_used_at"`
	UsageCount     int64      `db:"usage_count"`
	ExpiresAt      *time.Time `db:"expires_at"`
	RevokedAt      *time.Time `db:"revoked_at"`
	RevokeReason   *string    `db:"revoke_reason"`
	Status         string     `db:"status"`
	CreatedBy      *int64     `db:"created_by"`
	CreatedAt      time.Time  `db:"created_at"`
	UpdatedAt      time.Time  `db:"updated_at"`
}

func (k APIKey) ScopeList() []string { return decodeStrings(k.Scopes) }

// Allows answers whether a credential may use one permission key. '*' is a whole-scope
// wildcard; a scope is never matched by prefix, so "visitor.view" does not quietly grant
// "visitor.view_all".
func (k APIKey) Allows(scope string) bool {
	for _, granted := range k.ScopeList() {
		if granted == "*" || granted == scope {
			return true
		}
	}
	return false
}

// Live reports a key that may still authenticate. The status column is the authority; the
// expiry is checked as well because a key that ran out is dead whether or not a sweep said so.
func (k APIKey) Live(now time.Time) bool {
	if k.Status != KeyActive || k.RevokedAt != nil {
		return false
	}
	return k.ExpiresAt == nil || now.Before(*k.ExpiresAt)
}

// Webhook is one receiver. The secret itself is never stored: SecretRef names an environment
// value, so a leaked table dump does not hand over the signing key.
type Webhook struct {
	ID                  int64      `db:"id"`
	TenantID            int64      `db:"tenant_id"`
	Name                string     `db:"name"`
	URL                 string     `db:"url"`
	SecretRef           *string    `db:"secret_ref"`
	Events              string     `db:"events"`
	SubscribeAll        bool       `db:"subscribe_all"`
	TimeoutMS           int        `db:"timeout_ms"`
	MaxRetries          int        `db:"max_retries"`
	Description         *string    `db:"description"`
	FailureCount        int64      `db:"failure_count"`
	ConsecutiveFailures int        `db:"consecutive_failures"`
	LastSuccessAt       *time.Time `db:"last_success_at"`
	LastAttemptAt       *time.Time `db:"last_attempt_at"`
	DisabledReason      *string    `db:"disabled_reason"`
	IsActive            bool       `db:"is_active"`
	CreatedBy           *int64     `db:"created_by"`
	CreatedAt           time.Time  `db:"created_at"`
	UpdatedAt           time.Time  `db:"updated_at"`
}

func (w Webhook) EventList() []string { return decodeStrings(w.Events) }

// Wants is the subscription question. subscribe_all beats the list rather than extending it,
// because the list on such an endpoint is history nobody maintains.
func (w Webhook) Wants(eventKey string) bool {
	if w.SubscribeAll {
		return true
	}
	for _, key := range w.EventList() {
		if key == eventKey {
			return true
		}
	}
	return false
}

// Delivery is one attempt to hand one event to one endpoint.
type Delivery struct {
	ID             int64      `db:"id"`
	TenantID       int64      `db:"tenant_id"`
	EndpointID     int64      `db:"webhook_endpoint_id"`
	DomainEventID  *int64     `db:"domain_event_id"`
	EventKey       string     `db:"event_key"`
	Attempt        int        `db:"attempt"`
	PayloadSHA     *string    `db:"payload_sha256"`
	RequestBody    *string    `db:"request_body"`
	ResponseStatus *int       `db:"response_status"`
	Response       *string    `db:"response_snippet"`
	ErrorMessage   *string    `db:"error_message"`
	Status         string     `db:"status"`
	QueuedAt       time.Time  `db:"queued_at"`
	AvailableAt    time.Time  `db:"available_at"`
	SentAt         *time.Time `db:"sent_at"`
	DurationMS     *int       `db:"duration_ms"`
	CreatedAt      time.Time  `db:"created_at"`

	EndpointName string `db:"endpoint_name"`
	EndpointURL  string `db:"endpoint_url"`
}

// Command is one instruction for a device: a boom gate, a turnstile, a siren. It is a row
// because an action taken on a barrier has to be attributable, idempotent and safe to retry.
type Command struct {
	ID             int64      `db:"id"`
	TenantID       int64      `db:"tenant_id"`
	GateDeviceID   *int64     `db:"gate_device_id"`
	GateID         int64      `db:"gate_id"`
	PassID         *int64     `db:"pass_id"`
	MovementID     *int64     `db:"movement_id"`
	Command        string     `db:"command"`
	Source         string     `db:"source"`
	Status         string     `db:"status"`
	Attempts       int        `db:"attempts"`
	IdempotencyKey *string    `db:"idempotency_key"`
	AvailableAt    time.Time  `db:"available_at"`
	QueuedAt       time.Time  `db:"queued_at"`
	SentAt         *time.Time `db:"sent_at"`
	AckedAt        *time.Time `db:"acked_at"`
	ErrorMessage   *string    `db:"error_message"`
	IssuedBy       *int64     `db:"issued_by_user_id"`
	CreatedAt      time.Time  `db:"created_at"`

	GateName string `db:"gate_name"`
}

// decodeStrings reads a JSON array column. A value that cannot be read is an empty list: a
// subscription nobody can parse should send nothing, never everything.
func decodeStrings(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out == nil {
		return []string{}
	}
	return out
}

// encodeStrings stores a list as JSON, rejecting a value the database would accept but the
// worker could never match against.
func encodeStrings(values []string) (string, error) {
	if values == nil {
		values = []string{}
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
