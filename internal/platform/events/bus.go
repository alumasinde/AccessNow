// Package events is the only channel between modules: a module writes a row here in
// its own transaction, and whoever cares reads it later. A module that is switched off
// is simply an event key with no subscriber.
package events

import (
	"context"
	"encoding/json"
	"fmt"

	"accessnow/internal/platform/db"
)

type Event struct {
	// EventID is the domain_events row this event was read from. A producer never sets it:
	// the relay fills it in, and a reactor that must hand out exactly one notice per event -
	// notifications - has nothing else to key on. Aggregate and pass ids repeat by design.
	EventID       int64
	TenantID      int64
	EventKey      string
	AggregateType string
	AggregateID   int64
	PassID        int64
	SiteID        int64
	GateID        int64
	ActorUserID   int64
	DedupeKey     string
	Payload       map[string]any
}

// Publisher is the seam services depend on; tests use a recording fake.
type Publisher interface {
	Publish(ctx context.Context, c db.Conn, e Event) error
}

// Str reads one string out of the payload. A handler gets the map back from JSON, so
// every value is an any and a missing key must not be a panic.
func (e Event) Str(key string) string {
	if value, ok := e.Payload[key].(string); ok {
		return value
	}
	return ""
}

// Bool reads one flag out of the payload. It is for the facts a producer worked out and
// does not want every handler to re-derive from a code word - whether a gate result let the
// subject through is the example, because the four modules whose rows follow a scan must all
// answer it the same way and none of them owns the result vocabulary.
func (e Event) Bool(key string) bool {
	if value, ok := e.Payload[key].(bool); ok {
		return value
	}
	return false
}

type Bus struct{}

func (Bus) Publish(ctx context.Context, c db.Conn, e Event) error {
	if e.EventKey == "" || e.TenantID == 0 {
		return fmt.Errorf("event %q: tenant_id and event_key are required", e.EventKey)
	}
	payload, err := json.Marshal(e.Payload)
	if err != nil {
		return fmt.Errorf("encode event payload: %w", err)
	}
	const query = `
		INSERT INTO domain_events
			(tenant_id, event_key, aggregate_type, aggregate_id, pass_id, site_id, gate_id,
			 actor_user_id, dedupe_key, payload)
		VALUES (?, ?, ?, NULLIF(?, 0), NULLIF(?, 0), NULLIF(?, 0), NULLIF(?, 0), NULLIF(?, 0), NULLIF(?, ''), ?)`
	_, err = c.ExecContext(ctx, query,
		e.TenantID, e.EventKey, e.AggregateType, e.AggregateID, e.PassID, e.SiteID, e.GateID,
		e.ActorUserID, e.DedupeKey, string(payload))
	if err != nil {
		return fmt.Errorf("publish event %s: %w", e.EventKey, err)
	}
	return nil
}
