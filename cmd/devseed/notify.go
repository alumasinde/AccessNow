package main

import (
	"context"
	"fmt"

	"accessnow/internal/platform/db"
)

// starterProvider is one gateway row. Every seeded channel uses the "log" provider, which
// writes the message into the application log and reports it landed: that keeps the queue,
// the sweep and the failures real before anybody has an SMTP password.
type starterProvider struct {
	channel, senderID string
}

func starterProviders(slug string) []starterProvider {
	return []starterProvider{
		{channel: "email", senderID: "desk@" + slug + ".example"},
		{channel: "sms", senderID: slug},
	}
}

const starterProviderKind = "log"

// starterRoute is one row of the tenant's routing table, in the shapes the schema allows: a
// kind derived from the pass with nobody named, an address that is not a person, or one
// named user. userID and address are the nullable columns, so they arrive as any and string.
type starterRoute struct {
	eventKey, channel, recipientType string
	userID                           any
	address                          string
}

// starterRoutes is deliberately mixed. The first turns on a notice the catalogue marks
// opt-in, so nothing at all is sent for it until a tenant asks: a fresh tenant would file
// goods releases to nobody. The second sends an overdue step-out to a duty inbox rather than
// to a login, and the third names the supervisor who is meant to chase it - which also
// replaces that event's role defaults, the documented price of writing any route at all.
func starterRoutes(supervisorID int64, slug string) []starterRoute {
	return []starterRoute{
		{eventKey: "material.released", channel: "in_app", recipientType: "requester"},
		{eventKey: "outpass.overdue", channel: "email", recipientType: "static",
			address: "duty@" + slug + ".example"},
		{eventKey: "outpass.overdue", channel: "in_app", recipientType: "user",
			userID: supervisorID},
	}
}

// ensureNotifySettings files the notification rows that let a seeded tenant show a notice
// end to end. The route writes are guarded rather than upserts: that table's unique key
// spans three nullable columns, and MySQL lets an index full of NULLs repeat forever, so
// "is this row already here" has to be asked instead of assumed.
func ensureNotifySettings(ctx context.Context, c db.Conn, tenantID, siteID, supervisorID int64, slug string) error {
	// A tenant seeded before this file existed has providers to write but the module flag
	// from the catalogue; switching it on here is what makes the routes below readable.
	if _, err := c.ExecContext(ctx,
		`UPDATE tenant_modules SET enabled = 1 WHERE tenant_id = ? AND module_key = 'notifications'`,
		tenantID); err != nil {
		return wrap(err, "enable notifications")
	}

	for _, p := range starterProviders(slug) {
		if _, err := c.ExecContext(ctx, `
			INSERT INTO notification_providers
				(tenant_id, channel, provider, is_primary, sender_id, is_active)
			VALUES (?, ?, ?, 1, ?, 1)
			ON DUPLICATE KEY UPDATE is_primary = 1, is_active = 1, sender_id = VALUES(sender_id)`,
			tenantID, p.channel, starterProviderKind, p.senderID); err != nil {
			return wrap(err, "seed "+p.channel+" provider")
		}
	}

	for _, route := range starterRoutes(supervisorID, slug) {
		if _, err := c.ExecContext(ctx, `
			INSERT INTO notification_routes
				(tenant_id, event_key, channel, recipient_type, recipient_user_id,
				 recipient_role_key, recipient_address, site_id, include_actor, is_active)
			SELECT ?, ?, ?, ?, ?, NULL, ?, ?, 0, 1
			WHERE  NOT EXISTS (SELECT 1 FROM notification_routes
			                   WHERE tenant_id = ? AND event_key = ? AND channel = ?
			                     AND recipient_type = ?
			                     AND COALESCE(recipient_user_id, 0) = COALESCE(?, 0)
			                     AND COALESCE(recipient_address, '') = COALESCE(?, ''))`,
			tenantID, route.eventKey, route.channel, route.recipientType,
			route.userID, nullString(route.address), siteID,
			tenantID, route.eventKey, route.channel, route.recipientType,
			route.userID, nullString(route.address)); err != nil {
			return wrap(err, fmt.Sprintf("seed route %s on %s", route.eventKey, route.channel))
		}
	}

	// One override in the tenant's own voice. It is the email wording for an overdue
	// step-out, because the catalogue ships only an in-app line for that event - so this row
	// is not merely a demo of precedence, it is the copy the seeded email route sends.
	// Insert-if-absent rather than an upsert: notification_templates carries a version column
	// that means "which wording ran", and a seed that bumped it on every run would inflate it
	// into noise.
	if _, err := c.ExecContext(ctx, `
		INSERT INTO notification_templates
			(tenant_id, event_key, channel, locale, subject, body, is_html, version, status)
		SELECT ?, 'outpass.overdue', 'email', 'en',
		       'Still out: {{employee_no}} on {{pass_no}}',
		       '{{employee_no}} has been out {{minutes_out}} minutes past the time on pass {{pass_no}}. Duty desk: ring the number on the pass, then mark them back in at the gate.',
		       0, 1, 'active'
		WHERE  NOT EXISTS (SELECT 1 FROM notification_templates
		                   WHERE tenant_id = ? AND event_key = 'outpass.overdue'
		                     AND channel = 'email' AND locale = 'en')`,
		tenantID, tenantID); err != nil {
		return wrap(err, "seed notification template")
	}
	return nil
}
