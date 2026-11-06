package notifications

import (
	"net/http"
	"strings"
	"time"

	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/httpx"
	"accessnow/internal/platform/tenancy"
)

const defaultPage = 30

// NoticeView is one row of somebody's inbox. The address is deliberately absent: the person
// reading their own notices knows their own phone number, and a list endpoint that echoes
// contacts back is a list endpoint that leaks them.
type NoticeView struct {
	ID          int64      `json:"id"`
	EventKey    string     `json:"event_key"`
	Channel     string     `json:"channel"`
	Subject     string     `json:"subject,omitempty"`
	Body        string     `json:"body"`
	Status      string     `json:"status"`
	QueuedAt    time.Time  `json:"queued_at"`
	ReadAt      *time.Time `json:"read_at,omitempty"`
	PassID      int64      `json:"pass_id,omitempty"`
	RelatedType string     `json:"related_type,omitempty"`
	RelatedID   int64      `json:"related_id,omitempty"`
}

func viewOf(m Message) NoticeView {
	v := NoticeView{
		ID: m.ID, EventKey: m.EventKey, Channel: m.Channel, Status: m.Status,
		QueuedAt: m.QueuedAt, ReadAt: m.ReadAt, Body: derefString(m.Body),
		Subject: derefString(m.Subject), RelatedType: derefString(m.RelatedType),
	}
	if m.PassID != nil {
		v.PassID = *m.PassID
	}
	if m.RelatedID != nil {
		v.RelatedID = *m.RelatedID
	}
	return v
}

func viewsOf(rows []Message) []NoticeView {
	out := make([]NoticeView, 0, len(rows))
	for _, m := range rows {
		out = append(out, viewOf(m))
	}
	return out
}

// EventView is one line of the catalogue as a tenant sees it: what the notice is called, and
// what it currently does.
type EventView struct {
	EventKey       string   `json:"event_key"`
	Name           string   `json:"name"`
	ModuleKey      string   `json:"module_key"`
	Channels       []string `json:"default_channels"`
	RecipientTypes []string `json:"default_recipient_types"`
	IsOptOut       bool     `json:"is_opt_out"`
}

func viewOfEvent(e Event) EventView {
	return EventView{
		EventKey: e.EventKey, Name: e.Name, ModuleKey: e.ModuleKey,
		Channels: []string(e.Channels), RecipientTypes: []string(e.RecipientTypes),
		IsOptOut: e.IsOptOut,
	}
}

// RouteBody is one tenant decision about who hears about what. The target is not
// one-of-three on purpose: recipient_type says which field is required, and checkRoute says
// so in words rather than leaving a NULL to be resolved as silence at queue time.
type RouteBody struct {
	EventKey         string `json:"event_key" validate:"required,max=64"`
	Channel          string `json:"channel" validate:"required,max=16"`
	RecipientType    string `json:"recipient_type" validate:"required,max=24"`
	RecipientUserID  int64  `json:"recipient_user_id" validate:"omitempty,gt=0"`
	RecipientRoleKey string `json:"recipient_role_key" validate:"omitempty,max=64"`
	RecipientAddress string `json:"recipient_address" validate:"omitempty,max=255"`
	SiteID           int64  `json:"site_id" validate:"omitempty,gt=0"`
	IncludeActor     bool   `json:"include_actor"`
}

func (b RouteBody) toRoute(tenantID int64) Route {
	route := Route{
		TenantID: tenantID, EventKey: b.EventKey, Channel: b.Channel,
		RecipientType: b.RecipientType, IncludeActor: b.IncludeActor, IsActive: true,
	}
	if b.RecipientUserID != 0 {
		id := b.RecipientUserID
		route.RecipientUserID = &id
	}
	if key := trimSpace(b.RecipientRoleKey); key != "" {
		route.RecipientRoleKey = &key
	}
	if address := trimSpace(b.RecipientAddress); address != "" {
		route.RecipientAddress = &address
	}
	if b.SiteID != 0 {
		id := b.SiteID
		route.SiteID = &id
	}
	return route
}

// checkRoute is the whole of what the database will not say for itself: the target has to
// match the kind, and the channel has to be one this build can send on.
func checkRoute(b RouteBody) error {
	if !knownChannel(b.Channel) {
		return errs.BadField("channel", "must be in_app, email, sms, whatsapp or push")
	}
	switch b.RecipientType {
	case "user":
		if b.RecipientUserID == 0 {
			return errs.BadField("recipient_user_id", "a user route must name a person")
		}
	case "role":
		if trimSpace(b.RecipientRoleKey) == "" {
			return errs.BadField("recipient_role_key", "a role route must name a role")
		}
	case "static":
		if trimSpace(b.RecipientAddress) == "" {
			return errs.BadField("recipient_address", "a static route must hold an address")
		}
	case "host", "requester", "approver", "gate_supervisor", "site_supervisor",
		"department_head", "tenant_admin":
		// These resolve from the event and the pass, so they need no target of their own.
	default:
		return errs.BadField("recipient_type", "not a recipient this system can address")
	}
	return nil
}

// TemplateBody is one tenant's own wording. Body is required and short_body is not: the SMS
// column exists so a tenant can write a text that fits instead of having one truncated.
type TemplateBody struct {
	EventKey  string `json:"event_key" validate:"required,max=64"`
	Channel   string `json:"channel" validate:"required,max=16"`
	Locale    string `json:"locale" validate:"omitempty,max=16"`
	Subject   string `json:"subject" validate:"omitempty,max=255"`
	Body      string `json:"body" validate:"required,max=65535"`
	ShortBody string `json:"short_body" validate:"omitempty,max=1024"`
	IsHTML    bool   `json:"is_html"`
}

func (b TemplateBody) toTemplate(tenantID int64) Template {
	t := Template{
		TenantID: &tenantID, EventKey: b.EventKey, Channel: b.Channel,
		Locale: trimSpace(b.Locale), Body: b.Body, IsHTML: b.IsHTML, Status: "active",
	}
	if t.Locale == "" {
		t.Locale = "en"
	}
	if b.Subject != "" {
		subject := b.Subject
		t.Subject = &subject
	}
	if b.ShortBody != "" {
		short := b.ShortBody
		t.ShortBody = &short
	}
	return t
}

func checkTemplate(b TemplateBody) error {
	if !knownChannel(b.Channel) {
		return errs.BadField("channel", "must be in_app, email, sms, whatsapp or push")
	}
	if len(trimSpace(b.Body)) == 0 {
		return errs.BadField("body", "a template has to say something")
	}
	return nil
}

func knownChannel(raw string) bool {
	switch Channel(raw) {
	case ChannelInApp, ChannelEmail, ChannelSMS, ChannelWhatsApp, ChannelPush:
		return true
	default:
		return false
	}
}

// LedgerView is the queue's health: one line per status. An operator reads this before they
// read any individual notice.
type LedgerView struct {
	Status string `json:"status"`
	Count  int    `json:"count"`
}

// inboxPage is the envelope plus the badge count, so one call paints the list and the bell
// without a second round trip a client would otherwise poll.
type inboxPage struct {
	httpx.Envelope[NoticeView]
	Unread int `json:"unread"`
}

// RouteView is one routing decision as its author wrote it. The three targets are pointers
// because which one is set is the whole meaning of the row, and a zero in the other two would
// read as "also send to user 0".
type RouteView struct {
	ID               int64   `json:"id"`
	EventKey         string  `json:"event_key"`
	Channel          string  `json:"channel"`
	RecipientType    string  `json:"recipient_type"`
	RecipientUserID  *int64  `json:"recipient_user_id,omitempty"`
	RecipientRoleKey *string `json:"recipient_role_key,omitempty"`
	RecipientAddress *string `json:"recipient_address,omitempty"`
	SiteID           *int64  `json:"site_id,omitempty"`
	IncludeActor     bool    `json:"include_actor"`
	IsActive         bool    `json:"is_active"`
}

func viewOfRoute(route Route) RouteView {
	return RouteView{
		ID: route.ID, EventKey: route.EventKey, Channel: route.Channel,
		RecipientType: route.RecipientType, RecipientUserID: route.RecipientUserID,
		RecipientRoleKey: route.RecipientRoleKey, RecipientAddress: route.RecipientAddress,
		SiteID: route.SiteID, IncludeActor: route.IncludeActor, IsActive: route.IsActive,
	}
}

func viewsOfRoutes(routes []Route) []RouteView {
	out := make([]RouteView, 0, len(routes))
	for _, route := range routes {
		out = append(out, viewOfRoute(route))
	}
	return out
}

// TemplateView is one wording. is_system tells a tenant which rows it is reading that it does
// not own, which is the difference between a screen that explains itself and one that offers
// to delete somebody else's default.
type TemplateView struct {
	ID        int64   `json:"id"`
	EventKey  string  `json:"event_key"`
	Channel   string  `json:"channel"`
	Locale    string  `json:"locale"`
	Subject   *string `json:"subject,omitempty"`
	Body      string  `json:"body"`
	ShortBody *string `json:"short_body,omitempty"`
	IsHTML    bool    `json:"is_html"`
	Version   int     `json:"version"`
	IsSystem  bool    `json:"is_system"`
}

func viewOfTemplate(t Template) TemplateView {
	return TemplateView{
		ID: t.ID, EventKey: t.EventKey, Channel: t.Channel, Locale: t.Locale,
		Subject: t.Subject, Body: t.Body, ShortBody: t.ShortBody,
		IsHTML: t.IsHTML, Version: t.Version, IsSystem: t.TenantID == nil,
	}
}

func viewsOfTemplates(rows []Template) []TemplateView {
	out := make([]TemplateView, 0, len(rows))
	for _, t := range rows {
		out = append(out, viewOfTemplate(t))
	}
	return out
}

// OutboxView is the narrowing list an operations screen asks for: the rows in one status,
// with the reason column that explains them.
type OutboxView struct {
	ID           int64      `json:"id"`
	EventKey     string     `json:"event_key"`
	Channel      string     `json:"channel"`
	ToUserID     int64      `json:"to_user_id,omitempty"`
	Status       string     `json:"status"`
	Attempts     int        `json:"attempts"`
	MaxAttempts  int        `json:"max_attempts"`
	AvailableAt  time.Time  `json:"available_at"`
	Provider     string     `json:"provider,omitempty"`
	MessageID    string     `json:"provider_message_id,omitempty"`
	ErrorCode    string     `json:"error_code,omitempty"`
	ErrorMessage string     `json:"error_message,omitempty"`
	QueuedAt     time.Time  `json:"queued_at"`
	SentAt       *time.Time `json:"sent_at,omitempty"`
}

func viewOfOutbox(m Message) OutboxView {
	v := OutboxView{
		ID: m.ID, EventKey: m.EventKey, Channel: m.Channel, Status: m.Status,
		Attempts: m.Attempts, MaxAttempts: m.MaxAttempts, AvailableAt: m.Available,
		QueuedAt: m.QueuedAt, SentAt: m.SentAt, Provider: derefString(m.Provider),
		MessageID: derefString(m.ProviderMessageID), ErrorCode: derefString(m.ErrorCode),
		ErrorMessage: derefString(m.ErrorMessage),
	}
	if m.ToUserID != nil {
		v.ToUserID = *m.ToUserID
	}
	return v
}

// inboxQuery reads the page and the narrowing filters for a person's own list. The user id
// comes from the session and never from the query: an inbox you can point at somebody else is
// not an inbox, it is a surveillance endpoint.
func inboxQuery(r *http.Request, scope tenancy.Scope) (InboxFilter, httpx.Page, error) {
	page, err := httpx.ParsePage(r, defaultPage, 100)
	if err != nil {
		return InboxFilter{}, page, err
	}
	return InboxFilter{
		TenantID:   scope.TenantID,
		UserID:     scope.UserID,
		UnreadOnly: r.URL.Query().Get("unread") == "true",
		EventKey:   r.URL.Query().Get("event_key"),
		Limit:      page.Limit,
		Before:     page.Cursor,
	}, page, nil
}

// eventKeyQuery is the optional ?event_key= narrowing the routes and templates screens share.
func eventKeyQuery(r *http.Request) string {
	return trimSpace(r.URL.Query().Get("event_key"))
}

// statusQuery is the outbox narrowing, checked against the status column's own vocabulary.
func statusQuery(r *http.Request) (Status, error) {
	raw := trimSpace(r.URL.Query().Get("status"))
	if raw == "" {
		return "", nil
	}
	switch Status(raw) {
	case StatusQueued, StatusSending, StatusSent, StatusDelivered, StatusFailed,
		StatusBounced, StatusCancelled, StatusExpired:
		return Status(raw), nil
	default:
		return "", errs.BadField("status", "not a notification status")
	}
}

// trimSpace is the one writing rule the configuration surfaces share: a role key or an
// address with a trailing space is a typo, and a typo in a route is a notice that never
// arrives.
func trimSpace(s string) string { return strings.TrimSpace(s) }
