// Package notifications is the module that turns somebody's action into somebody else's
// message. It listens on the outbox like any other reactor, decides who should hear about
// the event on which channel, renders the tenant's own wording, and queues it; a separate
// sweep hands the queue to a real provider and deals with what comes back.
//
// Nothing here is a business rule: a notification failing never stops a pass, and no gate
// ever reads this table to decide anything.
package notifications

import (
	"encoding/json"
	"time"
)

// ModuleKey is the catalogue entry. It ships enabled, because an in-app notice needs no
// provider and no credentials: a tenant that switches this off loses the inbox bell, not
// the workflow.
const ModuleKey = "notifications"

// Channel is how a notice travels. The set is the schema's chk_notifications_channel.
type Channel string

const (
	ChannelInApp    Channel = "in_app"
	ChannelEmail    Channel = "email"
	ChannelSMS      Channel = "sms"
	ChannelWhatsApp Channel = "whatsapp"
	ChannelPush     Channel = "push"
	ChannelWebhook  Channel = "webhook"
)

// Status is one notice's progress through the queue. The set is
// chk_notifications_status; 'sent' means the provider took it and 'delivered' means it has
// evidence of landing, which is why a provider that only answers "accepted" sets sent.
type Status string

const (
	StatusQueued    Status = "queued"
	StatusSending   Status = "sending"
	StatusSent      Status = "sent"
	StatusDelivered Status = "delivered"
	StatusFailed    Status = "failed"
	StatusBounced   Status = "bounced"
	StatusCancelled Status = "cancelled"
	StatusExpired   Status = "expired"
)

// channelsThatNeedAProvider is every channel except the one this application already owns:
// an in-app notice is delivered the moment it is queued, because the inbox IS the delivery.
func (c Channel) NeedsProvider() bool { return c != ChannelInApp }

func (s Status) Open() bool { return s == StatusQueued || s == StatusSending }

// Message is one queued notice: one row per recipient per channel, never one row per event.
// The contact columns are copied in at queue time rather than joined at read time, because
// the message has to survive the person leaving: an audit of what somebody was told cannot
// depend on their login still existing.
type Message struct {
	ID         int64  `db:"id"`
	TenantID   int64  `db:"tenant_id"`
	EventKey   string `db:"event_key"`
	TemplateID *int64 `db:"notification_template_id"`
	Channel    string `db:"channel"`
	Locale     string `db:"locale"`

	ToUserID  *int64  `db:"to_user_id"`
	ToAddress *string `db:"to_address"`

	Subject *string `db:"subject"`
	Body    *string `db:"body"`

	PassID      *int64  `db:"pass_id"`
	MovementID  *int64  `db:"movement_id"`
	RelatedType *string `db:"related_type"`
	RelatedID   *int64  `db:"related_id"`

	DedupeKey   *string    `db:"dedupe_key"`
	Status      string     `db:"status"`
	Attempts    int        `db:"attempts"`
	MaxAttempts int        `db:"max_attempts"`
	Available   time.Time  `db:"available_at"`
	QueuedAt    time.Time  `db:"queued_at"`
	SentAt      *time.Time `db:"sent_at"`
	Delivered   *time.Time `db:"delivered_at"`
	ReadAt      *time.Time `db:"read_at"`

	Provider          *string `db:"provider"`
	ProviderMessageID *string `db:"provider_message_id"`
	ErrorCode         *string `db:"error_code"`
	ErrorMessage      *string `db:"error_message"`

	CreatedAt time.Time `db:"created_at"`
}

// InInbox reports a notice a person can actually see in the app: the in-app copy, whatever
// its delivery status, is the inbox row. Email and SMS never appear there.
func (m Message) InInbox() bool { return Channel(m.Channel) == ChannelInApp }

func (m Message) Read() bool { return m.ReadAt != nil }

// Due reports whether the delivery sweep may take this row now. A queued row whose
// available_at is in the future is a retry cooling down, not work to do.
func (m Message) Due(at time.Time) bool {
	return Status(m.Status) == StatusQueued && !m.Available.After(at)
}

// Spent reports a row the sweep must stop touching: either it landed, or it has used up the
// tenant's retries and now sits there with its error for whoever configures providers to fix.
func (m Message) Spent() bool {
	return !Status(m.Status).Open() || m.Attempts >= m.MaxAttempts
}

func (m Message) Address() string {
	if m.ToAddress == nil {
		return ""
	}
	return *m.ToAddress
}

// Event is the catalogue row for one outbox key: what it means, who is told by default,
// and on which channels. A tenant that never touches routes still gets these defaults,
// which is what makes the module useful the day it is switched on.
type Event struct {
	EventKey       string `db:"event_key"`
	Name           string `db:"name"`
	ModuleKey      string `db:"module_key"`
	Channels       list   `db:"default_channels"`
	RecipientTypes list   `db:"default_recipient_types"`
	IsOptOut       bool   `db:"is_opt_out"`
	Status         string `db:"status"`
}

func (e Event) Active() bool { return e.Status == "active" }

// Route is one tenant's decision about who hears one event on one channel. Writing any row
// for an event replaces that event's catalogue defaults rather than adding to them, which is
// the only way the schema can say "not by SMS" - see plansFor.
type Route struct {
	ID               int64   `db:"id"`
	TenantID         int64   `db:"tenant_id"`
	EventKey         string  `db:"event_key"`
	Channel          string  `db:"channel"`
	RecipientType    string  `db:"recipient_type"`
	RecipientUserID  *int64  `db:"recipient_user_id"`
	RecipientRoleKey *string `db:"recipient_role_key"`
	RecipientAddress *string `db:"recipient_address"`
	SiteID           *int64  `db:"site_id"`
	GateID           *int64  `db:"gate_id"`
	IncludeActor     bool    `db:"include_actor"`
	IsActive         bool    `db:"is_active"`
}

// Template is the wording for one event on one channel in one language. tenant_id NULL is
// the system default every tenant inherits; a tenant row for the same key, channel and
// locale wins over it.
type Template struct {
	ID        int64   `db:"id"`
	TenantID  *int64  `db:"tenant_id"`
	EventKey  string  `db:"event_key"`
	Channel   string  `db:"channel"`
	Locale    string  `db:"locale"`
	Subject   *string `db:"subject"`
	Body      string  `db:"body"`
	ShortBody *string `db:"short_body"`
	IsHTML    bool    `db:"is_html"`
	Version   int     `db:"version"`
	Status    string  `db:"status"`
}

// Provider is one tenant's choice of gateway for one channel. The secret is never here:
// credentials_ref names it, and the sender looks the value up in the environment at send
// time, which is how the same row works in staging and in production.
type Provider struct {
	ID             int64           `db:"id"`
	TenantID       int64           `db:"tenant_id"`
	Channel        string          `db:"channel"`
	Provider       string          `db:"provider"`
	IsPrimary      bool            `db:"is_primary"`
	SenderID       *string         `db:"sender_id"`
	CredentialsRef *string         `db:"credentials_ref"`
	Options        json.RawMessage `db:"options"`
	IsActive       bool            `db:"is_active"`
	UpdatedAt      time.Time       `db:"updated_at"`
}

// Sendable reports a row the queue may use. An inactive row is a tenant keeping a gateway
// configured but switched off, so it must not be picked as a fallback.
func (p Provider) Sendable() bool { return p.IsActive }

// list is a JSON array column read as Go strings. MySQL hands back the raw document, and a
// queue that dies because a tenant stored JSON null rather than JSON_ARRAY() would be a bad
// trade, so anything unreadable is simply an empty list.
type list []string

func (l *list) Scan(src any) error {
	switch value := src.(type) {
	case nil:
		*l = nil
	case []byte:
		return l.unmarshal(value)
	case string:
		return l.unmarshal([]byte(value))
	default:
		*l = nil
	}
	return nil
}

func (l *list) unmarshal(raw []byte) error {
	if len(raw) == 0 || string(raw) == "null" {
		*l = nil
		return nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		*l = nil
		return nil
	}
	*l = list(out)
	return nil
}
