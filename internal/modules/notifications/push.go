package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// pushSettings is a tenant's push gateway. Push is addressed by a device token in the message
// address, so unlike SMTP there is no from address to require; the gateway's URL and key are
// the whole configuration.
type pushSettings struct {
	URL    string
	APIKey string
	Header string
	Scheme string
}

// pushSender speaks the smallest useful push contract: one JSON object with a target, a title
// and a body. A gateway with a different envelope is configured through the introspectable
// fields, which is why the keys are settings rather than constants.
type pushSender struct {
	settings pushSettings
	client   *http.Client
}

func newPushSender(settings pushSettings) (Sender, error) {
	if strings.TrimSpace(settings.URL) == "" {
		return nil, fmt.Errorf("push provider needs options.base_url")
	}
	parsed, err := url.Parse(settings.URL)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return nil, fmt.Errorf("push provider base_url must be an http or https URL")
	}
	if settings.Header == "" {
		settings.Header = "Authorization"
	}
	if settings.Scheme == "" {
		settings.Scheme = "Bearer"
	}
	return &pushSender{settings: settings, client: &http.Client{Timeout: 15 * time.Second}}, nil
}

func (s *pushSender) Send(ctx context.Context, msg Outbound) (Receipt, error) {
	payload := map[string]string{
		"to": msg.To, "title": msg.Subject, "body": msg.Body,
	}
	if msg.From != "" {
		payload["from"] = msg.From
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Receipt{}, fmt.Errorf("encode push payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.settings.URL, bytes.NewReader(body))
	if err != nil {
		return Receipt{}, fmt.Errorf("build push request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.settings.APIKey != "" {
		value := s.settings.APIKey
		if s.settings.Scheme != "" {
			value = s.settings.Scheme + " " + value
		}
		req.Header.Set(s.settings.Header, value)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return Receipt{}, fmt.Errorf("push gateway: %w", err)
	}
	defer resp.Body.Close()
	snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		// A push gateway accepting the message is not proof the handset displayed it.
		return Receipt{MessageID: resp.Header.Get("X-Message-Id"), ProvedLanding: false}, nil
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return Receipt{}, refused("push_rejected", strings.TrimSpace(string(snippet)))
	default:
		return Receipt{}, fmt.Errorf("push gateway answered %d: %s", resp.StatusCode,
			strings.TrimSpace(string(snippet)))
	}
}
