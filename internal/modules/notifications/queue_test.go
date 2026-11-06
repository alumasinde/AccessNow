package notifications

import (
	"context"
	"strings"
	"testing"
	"time"

	"accessnow/internal/platform/events"
)

func testEvent() events.Event {
	return events.Event{
		EventID: 4412, TenantID: 7, EventKey: "outpass.overdue",
		AggregateType: "pass", AggregateID: 99, PassID: 99, SiteID: 3,
		Payload: map[string]any{"employee_no": "HQ-E002", "minutes_out": float64(41),
			"pass_no": "OUT-0031"},
	}
}

func catalogueEvent(channels, kinds []string, optIn bool) Event {
	return Event{EventKey: "outpass.overdue", Name: "Step out overdue",
		Channels: list(channels), RecipientTypes: list(kinds), IsOptOut: optIn, Status: "active"}
}

func TestCatalogueDefaultsRunUntilTheTenantWritesARoute(t *testing.T) {
	plans := plansFor(catalogueEvent([]string{"in_app", "sms"}, []string{"gate_supervisor"}, true), nil)
	if len(plans) != 2 {
		t.Fatalf("got %d plans, want one per default channel", len(plans))
	}
	if plans[0].channel != ChannelInApp || plans[1].channel != ChannelSMS {
		t.Errorf("channels = %v, %v", plans[0].channel, plans[1].channel)
	}
	if strings.Join(plans[1].kinds, ",") != "gate_supervisor" {
		t.Errorf("kinds = %v", plans[1].kinds)
	}
}

// A channel in the defaults with no recipient type would mail the event to nobody, so the
// plan is dropped rather than filed empty.
func TestDefaultChannelWithoutRecipientsIsNotAPlan(t *testing.T) {
	if plans := plansFor(catalogueEvent([]string{"email"}, nil, true), nil); len(plans) != 0 {
		t.Fatalf("got %d plans, want none", len(plans))
	}
}

func TestOptInEventSendsNothingUntilARouteExists(t *testing.T) {
	// is_opt_out false is the catalogue saying the tenant has to ask first.
	if plans := plansFor(catalogueEvent([]string{"in_app"}, []string{"requester"}, false), nil); plans != nil {
		t.Fatalf("got %d plans, want nil before the tenant routes it", len(plans))
	}
	route := Route{EventKey: "material.released", Channel: "in_app", RecipientType: "requester"}
	plans := plansFor(catalogueEvent([]string{"in_app", "email"}, []string{"host"}, false), []Route{route})
	if len(plans) != 1 || plans[0].channel != ChannelInApp {
		t.Fatalf("routes did not replace the defaults: %+v", plans)
	}
}

func TestIncludeActorIsAKindNotARouteRow(t *testing.T) {
	route := Route{Channel: "in_app", RecipientType: "static", RecipientAddress: ptrString("duty@example.test"),
		IncludeActor: true}
	plans := plansFor(catalogueEvent([]string{"in_app"}, []string{"requester"}, true), []Route{route})
	if len(plans) != 1 {
		t.Fatalf("plans = %d", len(plans))
	}
	s := &Service{repo: &Repository{}}
	// Actor 0 means nobody did it, and the resolver must say so without touching a database.
	got, err := s.recipientsFor(context.Background(), plans[0], Audience{TenantID: 7})
	if err != nil {
		t.Fatalf("recipientsFor: %v", err)
	}
	if len(got) != 1 || got[0].Name != "duty@example.test" {
		t.Fatalf("recipients = %+v, want only the static route address", got)
	}
}

func TestRoleRouteWithoutARoleNameResolvesNobody(t *testing.T) {
	route := Route{Channel: "in_app", RecipientType: "role"}
	s := &Service{repo: &Repository{}}
	got, err := s.recipientsFor(context.Background(),
		plan{channel: ChannelInApp, kinds: []string{"role"}, route: route}, Audience{TenantID: 7})
	if err != nil {
		t.Fatalf("recipientsFor: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("a role route that names no role must not mail anybody, got %+v", got)
	}
}

func TestInAppNoticeIsDeliveredWhenItIsFiled(t *testing.T) {
	at := time.Date(2026, 11, 6, 9, 12, 0, 0, time.UTC)
	user := int64(42)
	msg, ok := build(testEvent(), plan{channel: ChannelInApp}, Addressee{UserID: &user, Name: "Grace Njeri"},
		Template{ID: 8, Body: "{{employee_no}} is {{minutes_out}} minutes out."},
		Fill(eventData{Event: testEvent(), CatalogueName: "Step out overdue"}, PassContext{}, nameSet{}), at)
	if !ok {
		t.Fatal("an in-app notice with a login was skipped")
	}
	if msg.Status != string(StatusDelivered) {
		t.Errorf("status = %s, an inbox row needs no gateway", msg.Status)
	}
	if msg.ToAddress != nil {
		t.Errorf("to_address = %q, an inbox row has no address to purge later", *msg.ToAddress)
	}
	if msg.TemplateID == nil || *msg.TemplateID != 8 {
		t.Errorf("template id = %v, the wording a notice used has to be nameable", msg.TemplateID)
	}
	if *msg.Body != "HQ-E002 is 41 minutes out." {
		t.Errorf("body = %q", *msg.Body)
	}
	if msg.DedupeKey == nil || len(*msg.DedupeKey) != 64 {
		t.Errorf("dedupe key = %v", msg.DedupeKey)
	}
}

func TestRecipientWithNoEmailAddressIsSkippedNotFailed(t *testing.T) {
	user := int64(42)
	_, ok := build(testEvent(), plan{channel: ChannelEmail}, Addressee{UserID: &user}, Template{},
		Vars{}, time.Now())
	if ok {
		t.Fatal("an email notice for a person with no email address was filed")
	}
}

func TestKeyWithNoTemplateStillSaysSomething(t *testing.T) {
	// A brand new event key has no wording before anybody writes it; losing the notice would
	// be worse than sending a plain one.
	user := int64(42)
	msg, ok := build(testEvent(), plan{channel: ChannelInApp}, Addressee{UserID: &user}, Template{},
		Fill(eventData{Event: testEvent(), CatalogueName: "Step out overdue"}, PassContext{}, nameSet{}),
		time.Now())
	if !ok {
		t.Fatal("notice skipped")
	}
	if *msg.Body != "Step out overdue (OUT-0031)" {
		t.Errorf("body = %q", *msg.Body)
	}
	if msg.TemplateID != nil {
		t.Errorf("template id = %v, nothing was used", msg.TemplateID)
	}
}

func TestDedupeKeepsTwoRecipientsAndTwoEventsApart(t *testing.T) {
	base := Dedupe(100, 7, "reader@example.test", ChannelEmail)
	otherRecipient := Dedupe(100, 8, "reader@example.test", ChannelEmail)
	otherEvent := Dedupe(101, 7, "reader@example.test", ChannelEmail)
	otherChannel := Dedupe(100, 7, "reader@example.test", ChannelSMS)
	folded := Dedupe(100, 7, " Reader@Example.test ", ChannelEmail)

	if base == otherRecipient || base == otherEvent || base == otherChannel {
		t.Fatal("dedupe key collided across recipients, events or channels")
	}
	if base != folded {
		t.Error("the same address written two ways must be one notice")
	}
}

func TestNotifyIDsOnlySeesWhatTheWorkflowNamed(t *testing.T) {
	e := testEvent()
	e.Payload["notify_user_ids"] = []any{float64(12), float64(31)}
	if got := notifyIDs(e.Payload); len(got) != 2 || got[1] != 31 {
		t.Fatalf("notify ids = %v", got)
	}
	if got := notifyIDs(map[string]any{"notify_user_ids": []any{}}); len(got) != 0 {
		t.Fatalf("notify ids = %v, want none", got)
	}
	if got := notifyIDs(map[string]any{"approver_user_id": float64(3)}); got != nil {
		t.Fatalf("notify ids = %v, the payload key is the only source", got)
	}
}

func TestBackoffGrowsThenStops(t *testing.T) {
	if got := backoffFor(1); got != 30*time.Second {
		t.Errorf("first wait = %s", got)
	}
	if got := backoffFor(3); got != 2*time.Minute {
		t.Errorf("third wait = %s, want 30s doubled twice", got)
	}
	// The ladder is capped so a long outage is not hammered every thirty seconds, and a
	// number of attempts that shifts past the end of the int must never mean "retry now".
	if got := backoffFor(40); got != worstBackoff {
		t.Errorf("capped wait = %s", got)
	}
}

func ptrString(s string) *string { return &s }
