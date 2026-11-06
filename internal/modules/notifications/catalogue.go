package notifications

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"accessnow/internal/platform/errs"
)

const eventColumns = `event_key, name, module_key, default_channels, default_recipient_types,
	is_opt_out, status`

const routeColumns = `id, tenant_id, event_key, channel, recipient_type, recipient_user_id,
	recipient_role_key, recipient_address, site_id, gate_id, include_actor, is_active`

const templateColumns = `id, tenant_id, event_key, channel, locale, subject, body, short_body,
	is_html, version, status`

// COALESCE because a provider with no gateway settings stores SQL NULL, and a NULL cannot be
// scanned into a json.RawMessage; optionsOf already reads '{}' as "no knobs".
const providerColumns = `id, tenant_id, channel, provider, is_primary, sender_id,
	credentials_ref, COALESCE(options, '{}') AS options, is_active, updated_at`

// Catalogue reads what one outbox event means to this system. It is global rather than
// per-tenant on purpose: an event key is a fact about the code, and a tenant tunes who hears
// it through routes and templates, not by renaming it.
func (r *Repository) Catalogue(ctx context.Context, eventKey string) (Event, error) {
	const query = `SELECT ` + eventColumns + ` FROM notification_events WHERE event_key = ?`
	var e Event
	if err := r.q.GetContext(ctx, &e, query, eventKey); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Event{}, errs.NotFound("notification event " + eventKey)
		}
		return Event{}, fmt.Errorf("read notification event: %w", err)
	}
	return e, nil
}

// ActiveEvents is the screen a tenant configures from: every notice the system can send,
// with the defaults it currently gets.
func (r *Repository) ActiveEvents(ctx context.Context) ([]Event, error) {
	const query = `SELECT ` + eventColumns + ` FROM notification_events
		WHERE status = 'active' ORDER BY module_key, event_key`
	var out []Event
	if err := r.q.SelectContext(ctx, &out, query); err != nil {
		return nil, fmt.Errorf("list notification events: %w", err)
	}
	return out, nil
}

// Routes are the tenant's own decisions about one event. An empty list is not "muted", it
// means "use the catalogue defaults", which is what keeps a fresh tenant working.
func (r *Repository) Routes(ctx context.Context, tenantID int64, eventKey string) ([]Route, error) {
	const query = `SELECT ` + routeColumns + ` FROM notification_routes
		WHERE tenant_id = ? AND event_key = ? AND is_active = 1 ORDER BY channel, id`
	var out []Route
	if err := r.q.SelectContext(ctx, &out, query, tenantID, eventKey); err != nil {
		return nil, fmt.Errorf("list notification routes: %w", err)
	}
	return out, nil
}

// AllRoutes is the configuration screen's list, optionally narrowed to one event.
func (r *Repository) AllRoutes(ctx context.Context, tenantID int64, eventKey string) ([]Route, error) {
	query := `SELECT ` + routeColumns + ` FROM notification_routes WHERE tenant_id = ?`
	args := []any{tenantID}
	if eventKey != "" {
		query += ` AND event_key = ?`
		args = append(args, eventKey)
	}
	query += ` ORDER BY event_key, channel, id`

	var out []Route
	if err := r.q.SelectContext(ctx, &out, query, args...); err != nil {
		return nil, fmt.Errorf("list notification routes: %w", err)
	}
	return out, nil
}

// SaveRoute is an upsert done by hand. The unique key over (user, role, address) cannot do
// it: a role route has two NULL columns, and MySQL lets an index full of NULLs repeat
// forever, so a second identical insert would file a duplicate rather than update.
func (r *Repository) SaveRoute(ctx context.Context, in Route) (Route, error) {
	const update = `UPDATE notification_routes
		SET site_id = ?, gate_id = ?, include_actor = ?, is_active = 1
		WHERE tenant_id = ? AND event_key = ? AND channel = ? AND recipient_type = ?
		  AND recipient_user_id IS NOT NULL = ? AND COALESCE(recipient_user_id, 0) = ?
		  AND COALESCE(recipient_role_key, '') = ? AND COALESCE(recipient_address, '') = ?`
	res, err := r.q.ExecContext(ctx, update, nullableID(in.SiteID), nullableID(in.GateID),
		in.IncludeActor, in.TenantID, in.EventKey, in.Channel, in.RecipientType,
		in.RecipientUserID != nil, derefID(in.RecipientUserID),
		derefString(in.RecipientRoleKey), derefString(in.RecipientAddress))
	if err != nil {
		return Route{}, fmt.Errorf("update notification route: %w", err)
	}
	if changed, err := res.RowsAffected(); err == nil && changed > 0 {
		return r.routeByMatch(ctx, in)
	}

	const insert = `INSERT INTO notification_routes
		(tenant_id, event_key, channel, recipient_type, recipient_user_id, recipient_role_key,
		 recipient_address, site_id, gate_id, include_actor, is_active)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1)`
	if _, err := r.q.ExecContext(ctx, insert, in.TenantID, in.EventKey, in.Channel,
		in.RecipientType, in.RecipientUserID, in.RecipientRoleKey, in.RecipientAddress,
		nullableID(in.SiteID), nullableID(in.GateID), in.IncludeActor); err != nil {
		return Route{}, fmt.Errorf("insert notification route: %w", err)
	}
	return r.routeByMatch(ctx, in)
}

func (r *Repository) routeByMatch(ctx context.Context, in Route) (Route, error) {
	const query = `SELECT ` + routeColumns + ` FROM notification_routes
		WHERE tenant_id = ? AND event_key = ? AND channel = ? AND recipient_type = ?
		  AND COALESCE(recipient_user_id, 0) = ? AND COALESCE(recipient_role_key, '') = ?
		  AND COALESCE(recipient_address, '') = ?
		ORDER BY id DESC LIMIT 1`
	var out Route
	err := r.q.GetContext(ctx, &out, query, in.TenantID, in.EventKey, in.Channel,
		in.RecipientType, derefID(in.RecipientUserID), derefString(in.RecipientRoleKey),
		derefString(in.RecipientAddress))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Route{}, errs.NotFound("notification route")
		}
		return Route{}, fmt.Errorf("read saved notification route: %w", err)
	}
	return out, nil
}

func (r *Repository) DeleteRoute(ctx context.Context, tenantID, id int64) (bool, error) {
	const query = `DELETE FROM notification_routes WHERE id = ? AND tenant_id = ?`
	res, err := r.q.ExecContext(ctx, query, id, tenantID)
	if err != nil {
		return false, fmt.Errorf("delete notification route: %w", err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count deleted routes: %w", err)
	}
	return changed > 0, nil
}

// Template resolves the wording with tenant precedence first, then the locale the recipient
// actually asked for. A tenant that writes its own English copy owns that copy even for a
// reader whose locale is swahili, because the alternative is a tenant's override silently
// not applying to most of its people.
//
// errs.NotFound here is the normal answer for an event key nobody has written copy for; the
// queue turns it into its fallback sentence rather than an error.
func (r *Repository) Template(ctx context.Context, tenantID int64, eventKey string,
	channel Channel, locale string) (Template, error) {
	const query = `SELECT ` + templateColumns + ` FROM notification_templates
		WHERE event_key = ? AND channel = ? AND status = 'active'
		  AND (tenant_id = ? OR tenant_id IS NULL)
		ORDER BY (tenant_id IS NULL), (locale = ?) DESC, locale
		LIMIT 1`
	var t Template
	if err := r.q.GetContext(ctx, &t, query, eventKey, string(channel), tenantID, locale); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Template{}, errs.NotFound("notification template")
		}
		return Template{}, fmt.Errorf("read notification template: %w", err)
	}
	return t, nil
}

// Templates is the wording screen: the tenant's own rows and the system defaults they sit on
// top of, so a tenant can see what it is overriding before it overrides it.
func (r *Repository) Templates(ctx context.Context, tenantID int64, eventKey string) ([]Template, error) {
	query := `SELECT ` + templateColumns + ` FROM notification_templates
		WHERE (tenant_id = ? OR tenant_id IS NULL) AND status = 'active'`
	args := []any{tenantID}
	if eventKey != "" {
		query += ` AND event_key = ?`
		args = append(args, eventKey)
	}
	query += ` ORDER BY event_key, channel, (tenant_id IS NULL), locale`

	var out []Template
	if err := r.q.SelectContext(ctx, &out, query, args...); err != nil {
		return nil, fmt.Errorf("list notification templates: %w", err)
	}
	return out, nil
}

// SaveTemplate files or replaces one of the tenant's own rows. This unique key really is
// unique - tenant_scope_key turns the system default's NULL into 0 - so the database can do
// the upsert, and the version bump tells an operator which wording ran when a notice is
// queried later.
func (r *Repository) SaveTemplate(ctx context.Context, in Template) (Template, error) {
	const query = `INSERT INTO notification_templates
		(tenant_id, event_key, channel, locale, subject, body, short_body, is_html, version, status)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, 'active')
		ON DUPLICATE KEY UPDATE
			subject = VALUES(subject), body = VALUES(body), short_body = VALUES(short_body),
			is_html = VALUES(is_html), status = 'active', version = version + 1`
	if _, err := r.q.ExecContext(ctx, query, in.TenantID, in.EventKey, in.Channel, in.Locale,
		in.Subject, in.Body, in.ShortBody, in.IsHTML); err != nil {
		return Template{}, fmt.Errorf("save notification template: %w", err)
	}

	const read = `SELECT ` + templateColumns + ` FROM notification_templates
		WHERE event_key = ? AND channel = ? AND locale = ?
		  AND COALESCE(tenant_id, 0) = COALESCE(?, 0)`
	var out Template
	if err := r.q.GetContext(ctx, &out, read, in.EventKey, in.Channel, in.Locale, in.TenantID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Template{}, errs.NotFound("notification template")
		}
		return Template{}, fmt.Errorf("read saved notification template: %w", err)
	}
	return out, nil
}

// DeleteTemplate takes a tenant's override away, which puts the system wording back for
// them. Removing a system default is not a tenant's call, and the tenant_id predicate is
// what enforces that here rather than in a handler.
func (r *Repository) DeleteTemplate(ctx context.Context, tenantID, id int64) (bool, error) {
	const query = `DELETE FROM notification_templates WHERE id = ? AND tenant_id = ?`
	res, err := r.q.ExecContext(ctx, query, id, tenantID)
	if err != nil {
		return false, fmt.Errorf("delete notification template: %w", err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count deleted templates: %w", err)
	}
	return changed > 0, nil
}

// Providers lists the gateways a tenant has configured for one channel, the primary first.
// More than one is deliberate: a tenant can keep a second provider listed and inactive, so
// switching over is a flag rather than a re-typing of credentials.
func (r *Repository) Providers(ctx context.Context, tenantID int64, channel Channel) ([]Provider, error) {
	const query = `SELECT ` + providerColumns + ` FROM notification_providers
		WHERE tenant_id = ? AND channel = ? ORDER BY is_primary DESC, id`
	var out []Provider
	if err := r.q.SelectContext(ctx, &out, query, tenantID, string(channel)); err != nil {
		return nil, fmt.Errorf("list notification providers: %w", err)
	}
	return out, nil
}

func nullableID(id *int64) any {
	if id == nil {
		return nil
	}
	return *id
}

func derefID(id *int64) int64 {
	if id == nil {
		return 0
	}
	return *id
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
