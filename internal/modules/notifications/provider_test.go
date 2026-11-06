package notifications

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLogProviderNeedsNoCredentials(t *testing.T) {
	s := newSenders(func(string) string { panic("the log provider must not ask for a secret") })
	sender, err := s.build(Provider{ID: 1, Provider: ProviderLog})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	receipt, err := sender.Send(context.Background(), Outbound{To: "reader@example.test", Body: "hi"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if !receipt.ProvedLanding {
		t.Error("the dev provider reports landing, which is what makes an end-to-end demo honest")
	}
}

func TestMisconfiguredProviderIsRefusedWithTheReason(t *testing.T) {
	// Every one of these is a row a tenant can save; the queue's answer has to be a sentence
	// an operator can act on, not a stack of wrapped dial errors.
	cases := []struct {
		name   string
		row    Provider
		wanted string
	}{
		{"unknown gateway", Provider{ID: 1, Provider: "twilio"}, "is not one this build can speak to"},
		{"smtp with no from", Provider{ID: 2, Provider: ProviderSMTP,
			Options: json.RawMessage(`{"host":"mail.example.test"}`)}, "from address"},
		{"secret not in the environment", Provider{ID: 3, Provider: ProviderSMTP,
			SenderID: ptrString("desk@example.test"), CredentialsRef: ptrString("mail_default"),
			Options: json.RawMessage(`{"host":"mail.example.test","security":"starttls"}`)}, "mail_default"},
		{"sms with no account", Provider{ID: 4, Provider: ProviderHTTP,
			Options: json.RawMessage(`{"base_url":"https://gw.example.test"}`)}, "sender_id"},
	}
	s := newSenders(func(string) string { return "" })
	for _, tc := range cases {
		_, err := s.build(tc.row)
		failure, ok := refusedHere(err)
		if !ok {
			t.Errorf("%s: error = %v, want a refusal", tc.name, err)
			continue
		}
		if failure.Code != "provider_misconfigured" || !strings.Contains(failure.Reason, tc.wanted) {
			t.Errorf("%s: refusal = %+v, want it to mention %q", tc.name, failure, tc.wanted)
		}
	}
}

// A tenant that fixes a host or swaps a credentials_ref edits the row, and updated_at moves.
// The cache must follow, or the fix does not take until the process restarts.
func TestEditedProviderRowBuildsANewSender(t *testing.T) {
	edited := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	row := Provider{ID: 5, Provider: ProviderHTTP, SenderID: ptrString("DEMGW"),
		Options:   json.RawMessage(`{"base_url":"https://gw.example.test"}`),
		UpdatedAt: edited}
	s := newSenders(nil)

	first, err := s.build(row)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	again, err := s.build(row)
	if err != nil {
		t.Fatalf("second build: %v", err)
	}
	if first != again {
		t.Error("the same row built twice, so every notice paid for a new client")
	}

	row.UpdatedAt = edited.Add(time.Minute)
	refreshed, err := s.build(row)
	if err != nil {
		t.Fatalf("build after the edit: %v", err)
	}
	if refreshed == first {
		t.Error("a provider row the tenant edited kept the client built from the old settings")
	}
}

func TestFailedBuildIsReportedTheSameWayEveryTime(t *testing.T) {
	row := Provider{ID: 6, Provider: ProviderSMTP, UpdatedAt: time.Unix(0, 0)}
	s := newSenders(nil)
	_, first := s.build(row)
	_, second := s.build(row)
	if first.Error() != second.Error() {
		t.Errorf("errors differ: %v / %v", first, second)
	}
}

// gateway is an httptest server that answers like the SMS APIs this tenant base has met:
// JSON out, a status code that means something specific, and an id when it took the message.
func gateway(t *testing.T, status int, body string, seen *http.Request, gotBody *map[string]any) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = *r.Clone(r.Context())
		}
		if gotBody != nil {
			var payload map[string]any
			_ = json.NewDecoder(r.Body).Decode(&payload)
			*gotBody = payload
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func TestGatewayPostIsWhatTheTenantConfigured(t *testing.T) {
	var seen http.Request
	var payload map[string]any
	srv := gateway(t, http.StatusOK, `{"message_id":"8f31"}`, &seen, &payload)
	defer srv.Close()

	settings := `{"base_url":"` + srv.URL +
		`","to_field":"msisdn","text_field":"txt","auth_header":"x-key","extra":{"account":"demo"}}`
	sender, err := newSenders(func(string) string { return "sms-secret" }).build(Provider{
		ID: 7, Provider: ProviderHTTP,
		SenderID:       ptrString("DEMGW"),
		CredentialsRef: ptrString("sms_key"),
		Options:        json.RawMessage(settings),
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	receipt, err := sender.Send(context.Background(), Outbound{
		To: "+254700111222", Body: "Gate supervisor approval needed for VIS-0004."})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	// "accepted" is not "read on the handset", so the row must land as sent, not delivered.
	if receipt.ProvedLanding {
		t.Error("a gateway that only took the message claimed it landed")
	}
	if receipt.MessageID != "8f31" {
		t.Errorf("message id = %q", receipt.MessageID)
	}

	if got := seen.Header.Get("x-key"); got != "sms-secret" {
		t.Errorf("auth header = %q, the configured header name was ignored", got)
	}
	if payload["msisdn"] != "+254700111222" || payload["txt"] == nil || payload["account"] != "demo" {
		t.Errorf("payload = %v", payload)
	}
}

func TestGatewayStatusCodesPickTheOutcome(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		body     string
		refused  bool
		wantCode string
	}{
		{"rate limited", http.StatusTooManyRequests, "slow down", false, ""},
		{"gateway down", http.StatusBadGateway, "bad gateway", false, ""},
		{"bad key", http.StatusUnauthorized, `{"error":"invalid api key"}`, true, "credentials_refused"},
		{"wrong url", http.StatusNotFound, "no such endpoint", true, "gateway_url_not_found"},
		{"number refused", http.StatusBadRequest, "sender not allowed to this handset", true, "message_refused"},
	}
	for i, tc := range cases {
		srv := gateway(t, tc.status, tc.body, nil, nil)
		sender, err := newSenders(func(string) string { return "sms-secret" }).build(Provider{
			// A distinct id per case, because the sender cache is keyed by the row and an
			// outage test must not be handed the client the previous case built.
			ID: int64(90 + i), Provider: ProviderHTTP, SenderID: ptrString("DEMGW"),
			CredentialsRef: ptrString("sms_key"),
			Options:        json.RawMessage(`{"base_url":"` + srv.URL + `"}`)})
		if err != nil {
			srv.Close()
			t.Fatalf("%s: build: %v", tc.name, err)
		}
		_, err = sender.Send(context.Background(), Outbound{To: "+254700111222", Body: "x"})
		srv.Close()

		failure, ok := refusedHere(err)
		if ok != tc.refused {
			t.Errorf("%s: refused = %v, error = %v", tc.name, ok, err)
			continue
		}
		if ok && failure.Code != tc.wantCode {
			t.Errorf("%s: code = %q, want %q", tc.name, failure.Code, tc.wantCode)
		}
	}
}

func TestGatewayBodyNeverBecomesAQuotedToken(t *testing.T) {
	// summarize is the only thing that copies a gateway's answer into error_message, so it is
	// where a long HTML page or a newline-hidden token has to stop.
	long := "refused:\nAuthorization: Bearer abc" + strings.Repeat("x", 400)
	text := summarize([]byte(long))
	if len(text) > 200 {
		t.Errorf("length = %d, a gateway page does not belong in a column", len(text))
	}
	if strings.ContainsAny(text, "\n\r") {
		t.Errorf("text = %q, one line per row", text)
	}
	if got := summarize(nil); got != "gateway refused the message" {
		t.Errorf("empty body = %q", got)
	}
}

func TestMessageIDSurvivesTheKeyEachGatewayPicks(t *testing.T) {
	for _, body := range []string{`{"id":9012}`, `{"message_id":"m-1"}`, `{"messageId":"m-2"}`,
		`{"sms_id":"m-3"}`, `{"data":{"id":"nested"}}`} {
		if got := messageID([]byte(body)); got == "" {
			t.Errorf("no id read out of %s", body)
		}
	}
	if got := messageID([]byte(`{"status":"accepted"}`)); got != "" {
		t.Errorf("id = %q from a body that named none", got)
	}
}

func TestSecretIsOnlyEverANameInTheDatabase(t *testing.T) {
	t.Setenv("NOTIFY_SECRET_MAIL_DEFAULT", "hunter2")
	if got := EnvSecret("mail_default"); got != "hunter2" {
		t.Errorf("secret = %q", got)
	}
	if got := EnvSecret("mail-default"); got != "hunter2" {
		t.Errorf("dashed ref = %q, the same name must read the same variable", got)
	}
	if got := EnvSecret("absent"); got != "" {
		t.Errorf("absent secret = %q", got)
	}
}
