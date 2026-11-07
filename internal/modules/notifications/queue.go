package notifications

import (
	"context"
	"encoding/json"
	"time"

	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/events"
	"accessnow/internal/platform/logging"
)

// subscribedKeys is every outbox event this module listens to: each one is published by code
// in this repository AND has a catalogue row to send from. It is a list rather than a
// wildcard because the relay's registry is exact-key, and being explicit is what makes "we
// tell people about this one" a decision somebody can read.
//
// movement.recorded is deliberately absent: a gate scan is data, and every module that cares
// about one already publishes its own event about it. pass.issued and pass.completed are
// absent because a draft is not news and every module says its own goodbye (visit.checked_out,
// material.returned, outpass.returned), and approval.started and approval.delegation_ended
// because nothing has written their wording yet.
var subscribedKeys = []string{
	// The workflow engine.
	"approval.step.assigned", "approval.escalated", "approval.decided", "approval.rejected",
	"approval.returned", "approval.cancelled", "approval.reset", "approval.delegated",
	// The pass spine, whatever module filed it.
	"pass.approved", "pass.rejected", "pass.cancelled", "pass.expired", "pass.pending_review",
	// Visitors.
	"visit.created", "approval.requested",
	// The two ways a visit ends with nobody having come, both written by the sweep.
	"visit.no_show", "visit.expired",
	// The gate's own account of a visitor: arrived, left, or called off before either.
	"visit.checked_in", "visit.checked_out", "visit.cancelled",
	// Goods.
	"material.approval_needed", "material.declared", "material.released", "material.received",
	"material.returned", "material.verified",
	// The owed-back sweep and the desk's dispute of a count.
	"material.overdue", "material.disputed",
	// Staff step-outs.
	"outpass.approval_needed", "outpass.requested", "outpass.departed", "outpass.returned",
	"outpass.overdue", "outpass.unreturned",
	// Vehicles.
	"vehicle.approval_needed", "vehicle.logged", "vehicle.entered", "vehicle.exited",
	"vehicle.overstaying",
	// The screening lists. A watchlist hit is the quiet alarm the guard is not shown, and a
	// denied entry is the one the supervisor has to hear about before the visitor walks off.
	"blocklist.watched_hit", "blocklist.denied_entry",
	// The rulebook. A rule that refused or flagged a scan, and the waiver somebody signed
	// over it - the second one is why the supervisor reads the first.
	"rule.matched", "gate.override_used",
	// The offline ledger. A batch that came back with arguments on it is the one thing a
	// supervisor has to do about a tablet that kept working without the server.
	"sync.conflicts_found",
	// A scheduled export finished. Reports publishes it with its own name, row count and
	// recipients; the catalogue row for the key is seeded, so the notice has words already.
	"report.ready",
}

// maxAttempts is the retry budget one notice gets before it stops and shows its error. It is
// a constant rather than a setting because the sweep's backoff is written against it; a
// tenant that wants a different budget changes the provider, not this number.
const maxAttempts = 5

// EventKeys is what internal/server registers the relay with.
func EventKeys() []string { return append([]string(nil), subscribedKeys...) }

// plan is one channel and the recipient kinds that should be told on it.
type plan struct {
	channel Channel
	kinds   []string
	route   Route
}

// OnEvent is the module's whole reaction to an outbox row: work out who hears about it, in
// what words, on which channel, and file one notice per person per channel.
//
// It returns an error only when the queue itself could not be written, because that is the
// one case where trying again later helps. A missing catalogue row, a role with nobody in
// it and an approver with no phone number are all answers, not failures.
func (s *Service) OnEvent(ctx context.Context, e events.Event) error {
	on, err := s.enabled(ctx, e.TenantID)
	if err != nil {
		return err
	}
	if !on {
		// The tenant switched this off, so nothing is owed. Dropping it quietly is the
		// documented meaning of a module being off: the event stays on the record.
		return nil
	}

	catalogue, err := s.repo.Catalogue(ctx, e.EventKey)
	if errs.CodeOf(err) == errs.CodeNotFound {
		// A key with no catalogue row is an event nobody has decided how to announce yet.
		return nil
	}
	if err != nil {
		return err
	}
	if !catalogue.Active() {
		return nil
	}

	routes, err := s.repo.Routes(ctx, e.TenantID, e.EventKey)
	if err != nil {
		return err
	}
	plans := plansFor(catalogue, routes)
	if len(plans) == 0 {
		return nil
	}

	at := s.now()
	who, names, err := s.audienceOf(ctx, e)
	if err != nil {
		return err
	}
	base := eventData{Event: e, CatalogueName: catalogue.Name}
	words := &templateCache{repo: s.repo, tenantID: e.TenantID, eventKey: e.EventKey}

	var queued, skipped int
	for _, p := range plans {
		recipients, err := s.recipientsFor(ctx, p, who)
		if err != nil {
			return err
		}
		for _, a := range recipients {
			// The reader's own clock and language decide the wording, so a notice says
			// "expired at 09:12" to the person who will act on it at 09:12.
			ev := base
			ev.ReaderZone = firstNonEmpty(a.Timezone, who.Pass.SiteTimezone)
			tpl, err := words.get(ctx, p.channel, a.Locale)
			if err != nil {
				return err
			}
			msg, ok := build(e, p, a, tpl, Fill(ev, who.Pass, names), at)
			if !ok {
				skipped++
				continue
			}
			filed, err := s.repo.Queue(ctx, msg)
			if err != nil {
				return err
			}
			if filed {
				queued++
			} else {
				skipped++
			}
		}
	}

	if queued > 0 || skipped > 0 {
		logging.From(ctx).InfoContext(ctx, "notices filed",
			"event_key", e.EventKey, "event_id", e.EventID, "queued", queued, "skipped", skipped)
	}
	return nil
}

// plansFor turns a catalogue row plus whatever the tenant has explicitly routed into the
// (channel, recipient) pairs to notify.
//
// Explicit routes REPLACE the defaults rather than adding to them, because replacing is the
// only way a tenant can say "not by SMS" with the schema we have: there is no mute column on
// a default. A tenant that wants the defaults and one more recipient writes out all of them.
func plansFor(catalogue Event, routes []Route) []plan {
	if len(routes) > 0 {
		out := make([]plan, 0, len(routes))
		for _, route := range routes {
			out = append(out, plan{
				channel: Channel(route.Channel),
				kinds:   []string{route.RecipientType},
				route:   route,
			})
		}
		return out
	}
	if !catalogue.IsOptOut {
		// is_opt_out false means the tenant must ask before it starts sending this, and
		// asking means writing a route - handled above.
		return nil
	}
	out := make([]plan, 0, len(catalogue.Channels))
	for _, channel := range catalogue.Channels {
		if len(catalogue.RecipientTypes) == 0 {
			continue
		}
		out = append(out, plan{
			channel: Channel(channel),
			kinds:   append([]string(nil), catalogue.RecipientTypes...),
		})
	}
	return out
}

// recipientsFor resolves one plan into the humans or addresses it should reach.
func (s *Service) recipientsFor(ctx context.Context, p plan, a Audience) ([]Addressee, error) {
	out := []Addressee{}
	kinds := p.kinds
	if p.route.IncludeActor {
		// include_actor is the tenant's way of saying "and tell the person who did it", which
		// is the one recipient no route row can name - the actor lives on the event.
		kinds = append(append([]string(nil), kinds...), "actor")
	}
	for _, kind := range kinds {
		if kind == "role" && p.route.RecipientRoleKey == nil {
			// A default that says "the supervisors" without naming a role has nothing to
			// resolve; guessing a role here would mail the wrong people.
			continue
		}
		one := a
		one.Route = p.route
		if p.route.SiteID != nil {
			one.SiteID = *p.route.SiteID
		}
		people, err := s.repo.Recipients(ctx, kind, p.channel, one)
		if err != nil {
			return nil, err
		}
		out = append(out, people...)
	}
	return out, nil
}

// templateCache reads wording once per (channel, locale) per event, so a chain with forty
// approvers on forty channels still costs the database one query per distinct pair.
type templateCache struct {
	repo     *Repository
	tenantID int64
	eventKey string
	by       map[string]Template
}

// get reads the wording for one (channel, locale) pair and remembers it. A missing template
// is not an error: it means the notice will say the event's own name in plain words.
func (c *templateCache) get(ctx context.Context, channel Channel, locale string) (Template, error) {
	if c.by == nil {
		c.by = map[string]Template{}
	}
	key := string(channel) + "|" + locale
	if t, ok := c.by[key]; ok {
		return t, nil
	}
	t, err := c.repo.Template(ctx, c.tenantID, c.eventKey, channel, locale)
	if errs.CodeOf(err) == errs.CodeNotFound {
		err = nil
	}
	c.by[key] = t
	return t, err
}

// build files one notice. It reports false when the recipient cannot be reached on this
// channel, which is a skip and not an error: the same person is still getting the in-app
// copy of the same event.
func build(e events.Event, p plan, a Addressee, words Template, v Vars,
	at time.Time) (Message, bool) {
	address, ok := a.Address(p.channel)
	if !ok {
		return Message{}, false
	}
	v.set("recipient_name", a.Name)

	msg := Message{
		TenantID:    e.TenantID,
		EventKey:    e.EventKey,
		Channel:     string(p.channel),
		Locale:      firstNonEmpty(a.Locale, "en"),
		PassID:      nullableInt(e.PassID),
		RelatedType: nullableText(e.AggregateType),
		RelatedID:   nullableInt(e.AggregateID),
		Status:      string(StatusQueued),
		Available:   at,
		MaxAttempts: maxAttempts,
	}
	if words.ID != 0 {
		id := words.ID
		msg.TemplateID = &id
	}
	subject, body := Render(words, p.channel, v)
	if body == "" {
		// A key with no template row at all still has to be said in words somebody wrote:
		// the catalogue's own name for the event, with what the event knew.
		body = Unaddressed(firstNonEmpty(v["event_name"], e.EventKey), v)
	}
	if subject != "" {
		msg.Subject = &subject
	}
	msg.Body = &body
	if a.UserID != nil {
		id := *a.UserID
		msg.ToUserID = &id
	}
	if p.channel != ChannelInApp {
		sent := address
		msg.ToAddress = &sent
	} else {
		// The inbox row IS the delivery: there is no gateway to wait for, so it is finished
		// here and never enters the delivery sweep.
		msg.Status = string(StatusDelivered)
		msg.SentAt = &at
		msg.Delivered = &at
		msg.ToAddress = nil
		address = ""
	}

	key := Dedupe(e.EventID, firstID(a.UserID), address, p.channel)
	msg.DedupeKey = &key
	return msg, true
}

// audience gathers who the event names and who the paper names, in as few queries as it can.
func (s *Service) audienceOf(ctx context.Context, e events.Event) (Audience, nameSet, error) {
	a := Audience{
		TenantID:    e.TenantID,
		SiteID:      e.SiteID,
		ActorUserID: e.ActorUserID,
		Notify:      notifyIDs(e.Payload),
	}
	names := nameSet{}
	if e.PassID == 0 {
		return a, names, nil
	}
	pass, err := s.repo.Pass(ctx, e.TenantID, e.PassID)
	if errs.CodeOf(err) == errs.CodeNotFound {
		// A pass that is gone still has an event worth telling people about; the notice
		// simply has less in it.
		return a, names, nil
	}
	if err != nil {
		return a, names, err
	}
	a.Pass = pass
	if a.SiteID == 0 {
		a.SiteID = pass.SiteID
	}

	ids := []int64{}
	for _, id := range []*int64{pass.Requester, pass.Host, nullableInt(e.ActorUserID)} {
		if id != nil {
			ids = append(ids, *id)
		}
	}
	people, err := s.repo.People(ctx, e.TenantID, ids)
	if err != nil {
		return a, names, err
	}
	byID := map[int64]Person{}
	for _, person := range people {
		byID[person.UserID] = person
	}
	if pass.Requester != nil {
		names.Requester = byID[*pass.Requester].Name
	}
	if pass.Host != nil {
		names.Host = byID[*pass.Host].Name
	}
	if e.ActorUserID != 0 {
		names.Actor = byID[e.ActorUserID].Name
	}
	return a, names, nil
}

// notifyIDs reads the one recipient list any payload carries. The approval engine names the
// humans it just turned to, and that beats any role lookup because it already accounts for
// delegation and escalation.
func notifyIDs(payload map[string]any) []int64 {
	raw, ok := payload["notify_user_ids"].([]any)
	if !ok {
		return nil
	}
	out := make([]int64, 0, len(raw))
	for _, value := range raw {
		if id, ok := asInt64(value); ok {
			out = append(out, id)
		}
	}
	return out
}

func asInt64(value any) (int64, bool) {
	switch n := value.(type) {
	case float64:
		return int64(n), n != 0
	case int64:
		return n, n != 0
	case int:
		return int64(n), n != 0
	case json.Number:
		parsed, err := n.Int64()
		return parsed, err == nil && parsed != 0
	default:
		return 0, false
	}
}

func nullableText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nullableInt(id int64) *int64 {
	if id == 0 {
		return nil
	}
	return &id
}
