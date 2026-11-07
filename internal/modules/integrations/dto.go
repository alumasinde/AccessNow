package integrations

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"accessnow/internal/platform/errs"
)

// keyRequest is the name used by the DTO below; the service takes the same fields.
type KeyRequest struct {
	Name           string     `json:"name" validate:"required,max=128"`
	Environment    string     `json:"environment" validate:"omitempty,oneof=live test"`
	Scopes         []string   `json:"scopes" validate:"omitempty,max=50,dive,max=64"`
	RateLimit      *int       `json:"rate_limit_per_minute" validate:"omitempty,min=1,max=6000"`
	AllowedOrigins []string   `json:"allowed_origins" validate:"omitempty,max=20,dive,max=255"`
	ExpiresAt      *time.Time `json:"expires_at"`
}

type RevokeKeyRequest struct {
	Reason string `json:"reason" validate:"required,max=255"`
}

type WebhookRequest struct {
	Name         string   `json:"name" validate:"required,max=128"`
	URL          string   `json:"url" validate:"required,max=1024"`
	SecretRef    string   `json:"secret_ref" validate:"omitempty,max=64"`
	Events       []string `json:"events" validate:"omitempty,max=200,dive,max=64"`
	SubscribeAll bool     `json:"subscribe_all"`
	TimeoutMS    *int     `json:"timeout_ms" validate:"omitempty,min=500,max=10000"`
	MaxRetries   *int     `json:"max_retries" validate:"omitempty,min=0,max=10"`
	Description  string   `json:"description" validate:"omitempty,max=512"`
	IsActive     *bool    `json:"is_active"`
}

type CommandRequest struct {
	GateID         int64  `json:"gate_id" validate:"required,gt=0"`
	PassID         int64  `json:"pass_id" validate:"omitempty,gt=0"`
	MovementID     int64  `json:"movement_id" validate:"omitempty,gt=0"`
	Command        string `json:"command" validate:"required,max=24"`
	Source         string `json:"source" validate:"omitempty,oneof=approved_scan manual api emergency rule"`
	IdempotencyKey string `json:"idempotency_key" validate:"omitempty,max=64"`
}

type AckRequest struct {
	Status  string `json:"status" validate:"required,oneof=confirmed failed timeout"`
	Message string `json:"message" validate:"omitempty,max=512"`
}

type KeyView struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	Prefix      string     `json:"key_prefix"`
	Environment string     `json:"environment"`
	Scopes      []string   `json:"scopes"`
	RateLimit   *int       `json:"rate_limit_per_minute,omitempty"`
	UsageCount  int64      `json:"usage_count"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	Live        bool       `json:"is_live"`
}

func keyView(k APIKey, now time.Time) KeyView {
	return KeyView{ID: k.ID, Name: k.Name, Prefix: k.KeyPrefix, Environment: k.Environment,
		Scopes: k.ScopeList(), RateLimit: k.RateLimit, UsageCount: k.UsageCount,
		LastUsedAt: k.LastUsedAt, ExpiresAt: k.ExpiresAt, RevokedAt: k.RevokedAt,
		Status: k.Status, CreatedAt: k.CreatedAt, Live: k.Live(now)}
}

// IssuedKeyView is the one response that carries a secret, and it is never logged.
type IssuedKeyView struct {
	KeyView
	Secret string `json:"secret"`
}

type WebhookView struct {
	ID                  int64      `json:"id"`
	Name                string     `json:"name"`
	URL                 string     `json:"url"`
	SecretRef           *string    `json:"secret_ref,omitempty"`
	Events              []string   `json:"events"`
	SubscribeAll        bool       `json:"subscribe_all"`
	TimeoutMS           int        `json:"timeout_ms"`
	MaxRetries          int        `json:"max_retries"`
	Description         *string    `json:"description,omitempty"`
	FailureCount        int64      `json:"failure_count"`
	ConsecutiveFailures int        `json:"consecutive_failures"`
	LastSuccessAt       *time.Time `json:"last_success_at,omitempty"`
	LastAttemptAt       *time.Time `json:"last_attempt_at,omitempty"`
	DisabledReason      *string    `json:"disabled_reason,omitempty"`
	IsActive            bool       `json:"is_active"`
	Signed              bool       `json:"is_signed"`
	CreatedAt           time.Time  `json:"created_at"`
}

func webhookView(w Webhook) WebhookView {
	return WebhookView{ID: w.ID, Name: w.Name, URL: w.URL, SecretRef: w.SecretRef,
		Events: w.EventList(), SubscribeAll: w.SubscribeAll, TimeoutMS: w.TimeoutMS,
		MaxRetries: w.MaxRetries, Description: w.Description, FailureCount: w.FailureCount,
		ConsecutiveFailures: w.ConsecutiveFailures, LastSuccessAt: w.LastSuccessAt,
		LastAttemptAt: w.LastAttemptAt, DisabledReason: w.DisabledReason, IsActive: w.IsActive,
		Signed: w.SecretRef != nil && *w.SecretRef != "", CreatedAt: w.CreatedAt}
}

type DeliveryView struct {
	ID            int64      `json:"id"`
	EndpointID    int64      `json:"webhook_endpoint_id"`
	EndpointName  string     `json:"endpoint_name"`
	EventKey      string     `json:"event_key"`
	DomainEventID *int64     `json:"domain_event_id,omitempty"`
	Attempt       int        `json:"attempt"`
	Status        string     `json:"status"`
	ResponseCode  *int       `json:"response_status,omitempty"`
	Response      *string    `json:"response_snippet,omitempty"`
	Error         *string    `json:"error_message,omitempty"`
	DurationMS    *int       `json:"duration_ms,omitempty"`
	QueuedAt      time.Time  `json:"queued_at"`
	SentAt        *time.Time `json:"sent_at,omitempty"`
}

func deliveryView(d Delivery) DeliveryView {
	return DeliveryView{ID: d.ID, EndpointID: d.EndpointID, EndpointName: d.EndpointName,
		EventKey: d.EventKey, DomainEventID: d.DomainEventID, Attempt: d.Attempt,
		Status: d.Status, ResponseCode: d.ResponseStatus, Response: d.Response,
		Error: d.ErrorMessage, DurationMS: d.DurationMS, QueuedAt: d.QueuedAt,
		SentAt: d.SentAt}
}

type CommandView struct {
	ID             int64      `json:"id"`
	GateID         int64      `json:"gate_id"`
	GateName       string     `json:"gate_name"`
	PassID         *int64     `json:"pass_id,omitempty"`
	MovementID     *int64     `json:"movement_id,omitempty"`
	Command        string     `json:"command"`
	Source         string     `json:"source"`
	Status         string     `json:"status"`
	Attempts       int        `json:"attempts"`
	IdempotencyKey *string    `json:"idempotency_key,omitempty"`
	QueuedAt       time.Time  `json:"queued_at"`
	SentAt         *time.Time `json:"sent_at,omitempty"`
	AckedAt        *time.Time `json:"acked_at,omitempty"`
	Error          *string    `json:"error_message,omitempty"`
	IssuedBy       *int64     `json:"issued_by_user_id,omitempty"`
}

func commandView(c Command) CommandView {
	return CommandView{ID: c.ID, GateID: c.GateID, GateName: c.GateName, PassID: c.PassID,
		MovementID: c.MovementID, Command: c.Command, Source: c.Source, Status: c.Status,
		Attempts: c.Attempts, IdempotencyKey: c.IdempotencyKey, QueuedAt: c.QueuedAt,
		SentAt: c.SentAt, AckedAt: c.AckedAt, Error: c.ErrorMessage, IssuedBy: c.IssuedBy}
}

func keyViews(keys []APIKey, now time.Time) []KeyView {
	out := make([]KeyView, 0, len(keys))
	for _, key := range keys {
		out = append(out, keyView(key, now))
	}
	return out
}

func webhookViews(hooks []Webhook) []WebhookView {
	out := make([]WebhookView, 0, len(hooks))
	for _, hook := range hooks {
		out = append(out, webhookView(hook))
	}
	return out
}

func deliveryViews(rows []Delivery) []DeliveryView {
	out := make([]DeliveryView, 0, len(rows))
	for _, row := range rows {
		out = append(out, deliveryView(row))
	}
	return out
}

func commandViews(rows []Command) []CommandView {
	out := make([]CommandView, 0, len(rows))
	for _, row := range rows {
		out = append(out, commandView(row))
	}
	return out
}

// The context key the key-auth middleware stamps, so WhoAmI does not authenticate twice.
type keyContextKey struct{}

func withKey(ctx context.Context, key APIKey) context.Context {
	return context.WithValue(ctx, keyContextKey{}, key)
}

func keyFrom(ctx context.Context) (APIKey, bool) {
	key, ok := ctx.Value(keyContextKey{}).(APIKey)
	return key, ok
}

var (
	scopePattern  = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+)+$`)
	secretPattern = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
)

// scopeList validates the permission keys a credential may use. A wildcard is allowed as a
// whole scope; anything that is not a dotted permission name is refused rather than stored and
// silently never matched.
func scopeList(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		scope := strings.TrimSpace(value)
		if scope == "" {
			continue
		}
		if scope != "*" && !scopePattern.MatchString(scope) {
			return nil, errs.BadField("scopes", scope+" is not a permission key")
		}
		if seen[scope] {
			continue
		}
		seen[scope] = true
		out = append(out, scope)
	}
	return out, nil
}

func originList(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	for _, value := range values {
		origin := strings.TrimSpace(value)
		if origin == "" {
			continue
		}
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return nil, errs.BadField("allowed_origins", origin+" is not an origin")
		}
		out = append(out, origin)
	}
	return out, nil
}

func eventList(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		key := strings.TrimSpace(value)
		if key == "" {
			continue
		}
		if len(key) > 64 || strings.ContainsAny(key, " \t") {
			return nil, errs.BadField("events", key+" is not an event key")
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, key)
	}
	return out, nil
}

func validSecretRef(value string) bool { return secretPattern.MatchString(value) }

// ParseCommandStatus reads ?status= on the command list.
func ParseCommandStatus(r *http.Request) (string, error) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	switch status {
	case "", CommandQueued, CommandSending, CommandSent, CommandConfirmed, CommandFailed,
		CommandTimeout, CommandCancelled:
		return status, nil
	default:
		return "", errs.BadField("status", status+" is not a command status")
	}
}
