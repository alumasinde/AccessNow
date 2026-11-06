package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jmoiron/sqlx"

	"accessnow/internal/platform/logging"
)

// relayLimit is how many pending rows one relay pass reads. A scan relays its own event,
// so the normal case is one row; the limit only matters when a module handler was broken
// for a while and the backlog has to drain across several requests.
const relayLimit = 20

// Relay is the seam Core depends on: a service publishes inside its transaction, then
// asks for a drain once that transaction has committed. Naming an interface is what keeps
// core from ever importing a module.
type Relay interface {
	Relay(ctx context.Context, tenantID int64) error
}

// Handler reacts to one outbox row. Delivery is at-least-once and runs outside the
// transaction that wrote the row, so a handler must be safe to run twice for one event.
type Handler func(ctx context.Context, e Event) error

// Registry is the subscriber list. Wiring happens in internal/server, the only layer
// allowed to know Core and a module at the same time.
type Registry struct{ handlers map[string][]Handler }

func NewRegistry() *Registry { return &Registry{handlers: make(map[string][]Handler)} }

// On subscribes a handler to one event key. A key nobody subscribed to is settled as
// no_subscriber instead of being delivered, which is what "the module is switched off"
// means: the event is still on the record, nothing reacted to it.
func (r *Registry) On(eventKey string, h Handler) {
	r.handlers[eventKey] = append(r.handlers[eventKey], h)
}

func (r *Registry) keys() []string {
	keys := make([]string, 0, len(r.handlers))
	for key := range r.handlers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Dispatcher is the in-request outbox relay.
type Dispatcher struct {
	pool *sqlx.DB
	reg  *Registry
}

func NewDispatcher(pool *sqlx.DB, reg *Registry) *Dispatcher {
	return &Dispatcher{pool: pool, reg: reg}
}

var _ Relay = (*Dispatcher)(nil)

// Relay drains this tenant's pending rows. It runs after the producing transaction
// committed, so a handler failure is reported but never unwinds the gate's answer: the
// movement is history either way and the row stays on file for the next pass.
func (d *Dispatcher) Relay(ctx context.Context, tenantID int64) error {
	keys := d.reg.keys()
	if len(keys) == 0 {
		return nil
	}
	pending, err := d.pending(ctx, tenantID, keys)
	if err != nil {
		return err
	}

	log := logging.From(ctx)
	var failed []string
	for _, row := range pending {
		e, err := row.event()
		if err == nil {
			err = d.run(ctx, e)
		}
		if err != nil {
			log.ErrorContext(ctx, "event reaction failed",
				"event_id", row.ID, "event_key", row.EventKey, "error", err)
			failed = append(failed, fmt.Sprintf("%s#%d", row.EventKey, row.ID))
			if markErr := d.mark(ctx, row.ID, "failed", err.Error()); markErr != nil {
				log.ErrorContext(ctx, "could not mark event failed", "event_id", row.ID, "error", markErr)
			}
			continue
		}
		if err := d.mark(ctx, row.ID, "dispatched", ""); err != nil {
			return err
		}
	}
	// A key nobody subscribed to will never be read again, so its rows are settled here.
	// Without this the backlog grows forever and stops meaning "work still owed".
	if changed, err := d.settle(ctx, tenantID, keys); err != nil {
		log.ErrorContext(ctx, "could not settle unsubscribed events", "error", err)
	} else if changed > 0 {
		log.InfoContext(ctx, "settled unsubscribed events", "count", changed)
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d of %d reactions failed: %s", len(failed), len(pending), strings.Join(failed, ", "))
	}
	return nil
}

// Drain relays every tenant that owes work. A request only relays its own tenant, and only
// on the paths that ask for it — the gate scan does, a desk write does not — so without this
// a decision taken at the desk would wait for the next badge to be scanned before anything
// reacted to it.
func (d *Dispatcher) Drain(ctx context.Context) error {
	if len(d.reg.keys()) == 0 {
		return nil
	}
	const query = `SELECT DISTINCT tenant_id FROM domain_events WHERE status = 'pending'`
	var tenants []int64
	if err := d.pool.SelectContext(ctx, &tenants, query); err != nil {
		return fmt.Errorf("read tenants owing events: %w", err)
	}

	var errs []error
	for _, tenantID := range tenants {
		if err := d.Relay(ctx, tenantID); err != nil {
			// One tenant's broken handler must not stop the next tenant from being served.
			errs = append(errs, fmt.Errorf("tenant %d: %w", tenantID, err))
		}
	}
	return errors.Join(errs...)
}

func (d *Dispatcher) run(ctx context.Context, e Event) error {
	for _, h := range d.reg.handlers[e.EventKey] {
		if err := h(ctx, e); err != nil {
			return err
		}
	}
	return nil
}

// stored is a domain_events row as the database gives it. The nullable columns are read
// through COALESCE so the relay never needs a sql.Null* wrapper per field.
type stored struct {
	ID            int64  `db:"id"`
	EventKey      string `db:"event_key"`
	AggregateType string `db:"aggregate_type"`
	AggregateID   int64  `db:"aggregate_id"`
	PassID        int64  `db:"pass_id"`
	SiteID        int64  `db:"site_id"`
	GateID        int64  `db:"gate_id"`
	ActorUserID   int64  `db:"actor_user_id"`
	DedupeKey     string `db:"dedupe_key"`
	Payload       string `db:"payload"`
	TenantID      int64  `db:"tenant_id"`
}

func (s stored) event() (Event, error) {
	e := Event{
		TenantID: s.TenantID, EventKey: s.EventKey, AggregateType: s.AggregateType,
		AggregateID: s.AggregateID, PassID: s.PassID, SiteID: s.SiteID, GateID: s.GateID,
		ActorUserID: s.ActorUserID, DedupeKey: s.DedupeKey,
	}
	if err := json.Unmarshal([]byte(s.Payload), &e.Payload); err != nil {
		return Event{}, fmt.Errorf("decode payload for %s#%d: %w", s.EventKey, s.ID, err)
	}
	return e, nil
}

// pending reads the backlog in one go and closes the cursor before any handler runs: a
// handler writes on the same pool, and a held cursor would compete with it for
// connections. keys is passed to sqlx.In as one argument so it expands into the IN list
// rather than binding one key per placeholder.
func (d *Dispatcher) pending(ctx context.Context, tenantID int64, keys []string) ([]stored, error) {
	const query = `
		SELECT id, tenant_id, event_key, aggregate_type, COALESCE(aggregate_id, 0) AS aggregate_id,
		       COALESCE(pass_id, 0) AS pass_id, COALESCE(site_id, 0) AS site_id,
		       COALESCE(gate_id, 0) AS gate_id, COALESCE(actor_user_id, 0) AS actor_user_id,
		       COALESCE(dedupe_key, '') AS dedupe_key, payload
		FROM   domain_events
		WHERE  tenant_id = ? AND status = 'pending' AND event_key IN (?)
		ORDER  BY id
		LIMIT  ?`
	expanded, args, err := sqlx.In(query, tenantID, keys, relayLimit)
	if err != nil {
		return nil, fmt.Errorf("expand event keys: %w", err)
	}
	var rows []stored
	if err := d.pool.SelectContext(ctx, &rows, d.pool.Rebind(expanded), args...); err != nil {
		return nil, fmt.Errorf("read pending events: %w", err)
	}
	return rows, nil
}

// mark moves one row out of the backlog. A row that already left is not an error: two
// concurrent requests can relay the same event, and the second one simply loses.
func (d *Dispatcher) mark(ctx context.Context, id int64, status, reason string) error {
	const query = `
		UPDATE domain_events
		SET    status = ?, attempts = attempts + 1,
		       dispatched_at = CASE WHEN ? = 'dispatched' THEN UTC_TIMESTAMP(6) ELSE dispatched_at END,
		       last_error = NULLIF(?, '')
		WHERE  id = ? AND status = 'pending'`
	if _, err := d.pool.ExecContext(ctx, query, status, status, reason, id); err != nil {
		return fmt.Errorf("mark event %d %s: %w", id, status, err)
	}
	return nil
}

// settle closes off rows no handler can ever see. 'no_subscriber' is the status the
// schema already reserves for exactly this: the event is on the record, it just has
// nobody listening yet.
func (d *Dispatcher) settle(ctx context.Context, tenantID int64, keys []string) (int64, error) {
	const query = `
		UPDATE domain_events
		SET    status = 'no_subscriber'
		WHERE  tenant_id = ? AND status = 'pending' AND event_key NOT IN (?)`
	expanded, args, err := sqlx.In(query, tenantID, keys)
	if err != nil {
		return 0, fmt.Errorf("expand event keys: %w", err)
	}
	result, err := d.pool.ExecContext(ctx, d.pool.Rebind(expanded), args...)
	if err != nil {
		return 0, fmt.Errorf("settle unsubscribed events: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("count settled events: %w", err)
	}
	return changed, nil
}
