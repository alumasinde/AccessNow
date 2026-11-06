package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"accessnow/internal/platform/tenancy"
)

func TestOnlyADueQueuedRowIsWork(t *testing.T) {
	now := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	if !(Message{Status: string(StatusQueued), Available: now.Add(-time.Second)}).Due(now) {
		t.Error("a queued row past its time was not due")
	}
	// available_at in the future is a retry cooling down; taking it early is how a tenant
	// hammers a gateway that is already down.
	if (Message{Status: string(StatusQueued), Available: now.Add(2 * time.Minute)}).Due(now) {
		t.Error("a cooling retry was swept")
	}
	if (Message{Status: string(StatusSent), Available: now.Add(-time.Hour)}).Due(now) {
		t.Error("a delivered row went out twice")
	}
}

func TestSpentBudgetEndsTheSweep(t *testing.T) {
	if !(Message{Status: string(StatusQueued), Attempts: 5, MaxAttempts: 5}).Spent() {
		t.Error("a row that used its attempts is still open")
	}
	if (Message{Status: string(StatusQueued), Attempts: 1, MaxAttempts: 5}).Spent() {
		t.Error("a first retry was written off")
	}
	// An in-app notice is delivered at queue time, so it is never swept again.
	if !(Message{Status: string(StatusDelivered)}).Spent() {
		t.Error("a delivered row is spent")
	}
}

func TestFromAddressComesFromTheRowThenTheOptions(t *testing.T) {
	address := "desk@example.test"
	row := Message{Channel: "email", ToAddress: ptrString("reader@example.test"),
		Body: ptrString("Due back since 09:00."), Subject: ptrString("Still out")}

	sent := outbound(row, Provider{SenderID: &address, Provider: ProviderSMTP})
	if sent.To != "reader@example.test" || sent.From != address || sent.Subject != "Still out" {
		t.Errorf("outbound = %+v", sent)
	}
	// sender_id wins; a tenant that typed its from address into options instead still gets a
	// mail notice rather than an envelope sender of "".
	if got := outbound(row, Provider{SenderID: ptrString("mailer"), Options: json.RawMessage(
		`{"from_address":"fallback@example.test"}`)}).From; got != "mailer" {
		t.Errorf("from = %q, sender_id must win over options", got)
	}
	if got := outbound(row, Provider{Options: json.RawMessage(
		`{"from_address":"fallback@example.test"}`)}).From; got != "fallback@example.test" {
		t.Errorf("from = %q", got)
	}
}

func TestNoticeViewLeaksNoContactAddress(t *testing.T) {
	raw, err := json.Marshal(viewOf(Message{ID: 1, Channel: "email",
		ToAddress: ptrString("someone@example.test"), Body: ptrString("hi"),
		Status: string(StatusSent), QueuedAt: time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// An inbox list that echoes contacts back is a list endpoint that leaks them, and the
	// person reading it already knows their own number.
	if strings.Contains(string(raw), "someone@example.test") || strings.Contains(string(raw), "to_address") {
		t.Errorf("view = %s", raw)
	}
}

func TestRouteValidationMatchesTheTargetToTheKind(t *testing.T) {
	cases := []struct {
		name, field string
		body        RouteBody
	}{
		{"user without a person", "recipient_user_id", RouteBody{Channel: "in_app", RecipientType: "user"}},
		{"role without a role", "recipient_role_key", RouteBody{Channel: "in_app", RecipientType: "role"}},
		{"role that is only spaces", "recipient_role_key",
			RouteBody{Channel: "in_app", RecipientType: "role", RecipientRoleKey: "   "}},
		{"static without an address", "recipient_address",
			RouteBody{Channel: "sms", RecipientType: "static"}},
		{"unknown channel", "channel",
			RouteBody{Channel: "carrier_pigeon", RecipientType: "user", RecipientUserID: 4}},
		{"unknown kind", "recipient_type", RouteBody{Channel: "email", RecipientType: "visitor"}},
		// The pass-derived kinds resolve from the event and the pass, so an empty target is
		// the normal case, not a missing one.
		{"requester needs no target", "", RouteBody{Channel: "in_app", RecipientType: "requester"}},
		{"gate supervisor needs no target", "", RouteBody{Channel: "email", RecipientType: "gate_supervisor"}},
	}
	for _, tc := range cases {
		err := checkRoute(tc.body)
		switch {
		case tc.field == "" && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case tc.field != "" && err == nil:
			t.Errorf("%s was accepted", tc.name)
		case tc.field != "" && !strings.Contains(err.Error(), tc.field):
			t.Errorf("%s: error = %v, want it to name %q", tc.name, err, tc.field)
		}
	}
}

func TestRouteBodyKeepsOnlyTheTargetItNames(t *testing.T) {
	route := RouteBody{EventKey: "outpass.overdue", Channel: "email", RecipientType: "static",
		RecipientAddress: " duty@example.test ", IncludeActor: true}.toRoute(7)
	if route.RecipientAddress == nil || *route.RecipientAddress != "duty@example.test" {
		t.Fatalf("address = %v, a trailing space in a route is a notice that never arrives", route.RecipientAddress)
	}
	if route.RecipientUserID != nil || route.RecipientRoleKey != nil {
		t.Errorf("route = %+v, unset targets must stay NULL", route)
	}
	if !route.IsActive || !route.IncludeActor || route.TenantID != 7 {
		t.Errorf("route = %+v", route)
	}
}

func TestTemplateValidation(t *testing.T) {
	if err := checkTemplate(TemplateBody{Channel: "sms", Body: "   "}); err == nil {
		t.Error("a blank body was accepted")
	}
	if err := checkTemplate(TemplateBody{Channel: "push", Body: "{{pass_no}} is ready"}); err != nil {
		t.Errorf("valid template rejected: %v", err)
	}

	tenant := int64(7)
	row := TemplateBody{EventKey: "visit.created", Channel: "email", Body: "A visitor arrived"}.toTemplate(tenant)
	// The locale is stated even when the writer left it out, because the precedence query
	// compares locales rather than NULLs.
	if row.Locale != "en" {
		t.Errorf("locale = %q", row.Locale)
	}
	if row.Status != "active" || row.TenantID == nil || *row.TenantID != 7 {
		t.Errorf("row = %+v, a tenant must never be able to write a system default", row)
	}
	if row.Subject != nil {
		t.Error("an absent subject became an empty one instead of NULL")
	}
}

func TestSystemRowIsMarkedAsNotTheTenants(t *testing.T) {
	tenant := int64(3)
	if !viewOfTemplate(Template{ID: 1, Body: "system"}).IsSystem {
		t.Error("a system default was shown as the tenant's own, so the screen would offer to delete it")
	}
	if viewOfTemplate(Template{ID: 2, TenantID: &tenant, Body: "mine"}).IsSystem {
		t.Error("the tenant's own override was labelled a default")
	}
}

func TestInboxFiltersTakeTheReaderFromTheSession(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/notifications?unread=true&event_key=visit.created&limit=5", nil)
	filter, page, err := inboxQuery(r, tenancy.Scope{TenantID: 7, UserID: 42})
	if err != nil {
		t.Fatalf("inboxQuery: %v", err)
	}
	// An inbox you can point at somebody else is not an inbox, it is a surveillance endpoint.
	if filter.UserID != 42 || filter.TenantID != 7 {
		t.Errorf("filter = %+v", filter)
	}
	if !filter.UnreadOnly || filter.EventKey != "visit.created" || page.Limit != 5 {
		t.Errorf("filter = %+v page = %+v", filter, page)
	}
}

func TestStatusQueryUsesTheColumnsVocabulary(t *testing.T) {
	status, err := statusQuery(httptest.NewRequest("GET", "/x?status=failed", nil))
	if err != nil || status != StatusFailed {
		t.Errorf("status = %q err = %v", status, err)
	}
	if status, err := statusQuery(httptest.NewRequest("GET", "/x", nil)); err != nil || status != "" {
		t.Errorf("no filter = %q err = %v", status, err)
	}
	if _, err := statusQuery(httptest.NewRequest("GET", "/x?status=marmalade", nil)); err == nil {
		t.Error("an invented status was accepted")
	}
}

// countingSweeper stands in for the service so the loop's own promise can be tested: a pass
// that fails is logged and tried again on the next tick, never a goroutine that stops. The
// channel is the record, because the sweep runs on the loop's goroutine.
type countingSweeper struct{ passes chan struct{} }

func (c *countingSweeper) DeliverDue(context.Context) error {
	c.passes <- struct{}{}
	return errors.New("db is down")
}

func TestDeliveryLoopKeepsSweepingAfterAFailure(t *testing.T) {
	sweeper := &countingSweeper{passes: make(chan struct{}, 8)}
	loop := NewDeliveryLoop(sweeper, 5*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { loop.Run(ctx); close(done) }()

	for range 3 {
		select {
		case <-sweeper.passes:
		case <-time.After(2 * time.Second):
			t.Fatal("the sweep stopped after a failure")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the loop did not stop with its context")
	}
}
