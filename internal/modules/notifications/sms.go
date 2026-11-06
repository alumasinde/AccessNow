package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// httpSettings is one SMS or WhatsApp gateway described as plainly as an API lets: a URL, the
// header the key goes in, and the two field names that carry the number and the text. Every
// gateway this tenant base has been pointed at - the aggregators and the operator HTTP APIs
// alike - fits those five knobs, and the ones that want more want them in "extra".
//
// The body is JSON. A gateway that insists on form-encoding is a real thing, and the answer
// when a tenant meets one is one more option here, not a second sender.
type httpSettings struct {
	URL     string
	APIKey  string
	Header  string
	Scheme  string
	From    string
	ToField string
	BodyKey string
	Extra   map[string]string
}

type httpSender struct {
	set    httpSettings
	client *http.Client
}

func newHTTPSender(set httpSettings) (Sender, error) {
	if !strings.HasPrefix(set.URL, "http://") && !strings.HasPrefix(set.URL, "https://") {
		return nil, fmt.Errorf("sms provider needs options.base_url starting with https://")
	}
	if set.Header == "" {
		set.Header = "apikey"
	}
	if set.ToField == "" {
		set.ToField = "to"
	}
	if set.BodyKey == "" {
		set.BodyKey = "message"
	}
	return &httpSender{set: set, client: &http.Client{Timeout: 15 * time.Second}}, nil
}

func (s *httpSender) Send(ctx context.Context, msg Outbound) (Receipt, error) {
	payload := map[string]any{
		s.set.ToField: msg.To,
		s.set.BodyKey: msg.Body,
		"from":        s.set.From,
	}
	for key, value := range s.set.Extra {
		payload[key] = value
	}
	// Some gateways want the subject as the first line of the text; an SMS has no other place
	// for it, and an operator who set one expects to see it.
	if msg.Subject != "" && !strings.Contains(msg.Body, msg.Subject) {
		payload[s.set.BodyKey] = msg.Subject + ": " + msg.Body
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return Receipt{}, refused("payload_unencodable", err.Error())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.set.URL, bytes.NewReader(raw))
	if err != nil {
		return Receipt{}, refused("provider_url_invalid", err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	if s.set.APIKey != "" {
		value := s.set.APIKey
		if s.set.Scheme != "" {
			value = s.set.Scheme + " " + value
		}
		req.Header.Set(s.set.Header, value)
	}

	res, err := s.client.Do(req)
	if err != nil {
		// A gateway that is unreachable is not a reason to give up on a notice: this is the
		// temporary case the retry budget exists for.
		return Receipt{}, fmt.Errorf("call notification gateway: %w", err)
	}
	defer res.Body.Close()
	// The answer is read because the status line alone never says which number was refused,
	// and capped because a gateway that returns an HTML error page is not going to tell us
	// anything in the first two hundred bytes.
	body, _ := io.ReadAll(io.LimitReader(res.Body, 2048))

	switch {
	case res.StatusCode == http.StatusTooManyRequests:
		return Receipt{}, fmt.Errorf("gateway rate limit (429), retrying later")
	case res.StatusCode >= 500:
		return Receipt{}, fmt.Errorf("gateway fault (%d), retrying later", res.StatusCode)
	case res.StatusCode >= 400:
		return Receipt{}, refused(gatewayCode(res.StatusCode), summarize(body))
	default:
		return Receipt{MessageID: messageID(body)}, nil
	}
}

// gatewayCode keeps a 4xx out of the retry budget under a name an operator can act on: 401
// and 403 are credentials, 404 is the URL, anything else is the message or the number.
func gatewayCode(status int) string {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "credentials_refused"
	case http.StatusNotFound:
		return "gateway_url_not_found"
	default:
		return "message_refused"
	}
}

// messageID finds the provider's own id for the message, if it named one. The key differs by
// gateway, and a queue that can quote "the number said yes to message 8f31" answers a
// complaint without a support ticket.
func messageID(body []byte) string {
	var res map[string]any
	if json.Unmarshal(body, &res) != nil {
		return ""
	}
	for _, key := range []string{"id", "message_id", "messageId", "sms_id"} {
		switch value := res[key].(type) {
		case string:
			if value != "" {
				return value
			}
		case float64:
			return strconv.FormatFloat(value, 'f', -1, 64)
		}
	}
	if data, ok := res["data"].(map[string]any); ok {
		for _, key := range []string{"id", "message_id", "messageId"} {
			if text, ok := data[key].(string); ok {
				return text
			}
		}
	}
	return ""
}

// summarize is the reason column's whole source of truth about a gateway's answer, so a
// quoted request body cannot put a token in the database.
func summarize(body []byte) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return "gateway refused the message"
	}
	if len(text) > 200 {
		text = text[:200]
	}
	return strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(text)
}
