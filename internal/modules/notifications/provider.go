package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"accessnow/internal/platform/logging"
)

// Outbound is the whole of what a gateway is asked to carry. Passing this rather than the
// queue row keeps every sender honest about one thing only - it gets an address and the
// words - and makes a provider swap a matter of configuration, not of the schema.
type Outbound struct {
	To      string
	Subject string
	Body    string
	From    string
}

// Receipt is a provider's answer about one message. ProvedLanding is the difference between
// 'sent' and 'delivered' in the queue: a gateway that only says "accepted" cannot prove the
// handset saw it, and pretending otherwise would let an operator believe a delivery that
// never happened.
type Receipt struct {
	MessageID     string
	ProvedLanding bool
}

// Sender is the seam every gateway sits behind.
type Sender interface {
	Send(ctx context.Context, msg Outbound) (Receipt, error)
}

// Refused is the permanent kind of failure: this message will never be accepted, so the
// notice stops being retried and keeps the reason on its row. Anything else a sender returns
// is treated as worth trying again - a timeout, a 503, a gateway mid-maintenance.
type Refused struct {
	Code   string
	Reason string
}

func (r *Refused) Error() string { return r.Code + ": " + r.Reason }

func refused(code, reason string) error { return &Refused{Code: code, Reason: reason} }

// refusedHere reports whether an error chain ends in a refusal, which is the only question
// the delivery sweep asks of an error.
func refusedHere(err error) (*Refused, bool) {
	var r *Refused
	if errors.As(err, &r) {
		return r, true
	}
	return nil, false
}

// Secret resolves a provider row's credentials_ref into the value it names. The reference is
// a name, never a secret, and it is looked up in the environment under NOTIFY_SECRET_ so the
// same row works in staging and production and no credential is ever stored in the database.
type Secret func(ref string) string

// EnvSecret is the production lookup: a ref of "mail_default" reads NOTIFY_SECRET_MAIL_DEFAULT.
func EnvSecret(ref string) string {
	return os.Getenv("NOTIFY_SECRET_" + strings.ToUpper(strings.ReplaceAll(ref, "-", "_")))
}

// options is a provider row's JSON column read lazily. Every gateway needs a slightly
// different set of knobs, and a column beats a migration every time a tenant picks a new one.
type options map[string]any

func optionsOf(raw json.RawMessage) options {
	var out options
	if len(raw) == 0 || json.Unmarshal(raw, &out) != nil {
		return options{}
	}
	return out
}

func (o options) str(key, fallback string) string {
	if value, ok := o[key].(string); ok && value != "" {
		return value
	}
	return fallback
}

func (o options) number(key string, fallback float64) float64 {
	if value, ok := o[key].(float64); ok {
		return value
	}
	return fallback
}

// senderKey caches a built sender against the row it came from. The cache is keyed by the
// provider id and the secret's own value length so a rotated credential cannot keep using a
// client built with the old one.
type senderKey struct {
	providerID int64
	// editedAt is the row's own timestamp, so a tenant that fixes a host or swaps a
	// credentials_ref gets a new client on the next notice rather than the cached old one.
	editedAt time.Time
}

// senders builds and remembers one sender per provider row. Notices for the same tenant and
// channel arrive in a batch, and neither an SMTP handshake nor an HTTP client per message is
// a thing a busy relay should repeat.
type senders struct {
	secret Secret
	byKey  map[senderKey]Sender
	failed map[senderKey]string
}

// logNotice is the dev provider's whole delivery: the message, in the log, with the address
// it would have gone to.
func logNotice(ctx context.Context, msg Outbound) {
	logging.From(ctx).InfoContext(ctx, "notification sent by log provider",
		"to", msg.To, "from", msg.From, "subject", msg.Subject, "body", msg.Body)
}

func newSenders(secret Secret) *senders {
	if secret == nil {
		secret = EnvSecret
	}
	return &senders{secret: secret, byKey: map[senderKey]Sender{}, failed: map[senderKey]string{}}
}

// build returns the primary active sender for one tenant and channel, or says why there is
// none. The "why" matters more here than usual: an operator staring at a queue of failures
// should read "no sms provider configured" and not have to guess.
func (s *senders) build(p Provider) (Sender, error) {
	if p.Provider == ProviderLog {
		// The dev provider needs no row-level settings at all, so it is never cached or
		// refused: it cannot be misconfigured.
		return logSender{}, nil
	}
	key := senderKey{providerID: p.ID, editedAt: p.UpdatedAt}
	if sender, ok := s.byKey[key]; ok {
		return sender, nil
	}
	if reason, ok := s.failed[key]; ok {
		return nil, refused("provider_misconfigured", reason)
	}
	sender, err := s.construct(p)
	if err != nil {
		s.failed[key] = err.Error()
		return nil, refused("provider_misconfigured", err.Error())
	}
	s.byKey[key] = sender
	return sender, nil
}

func (s *senders) construct(p Provider) (Sender, error) {
	o := optionsOf(p.Options)
	from := derefString(p.SenderID)
	switch p.Provider {
	case ProviderSMTP:
		if from == "" {
			from = o.str("from_address", "")
		}
		if from == "" {
			return nil, fmt.Errorf("email provider needs a from address in sender_id")
		}
		password := ""
		if ref := derefString(p.CredentialsRef); ref != "" {
			password = s.secret(ref)
			if password == "" {
				return nil, fmt.Errorf("secret %q is not set in the environment", ref)
			}
		}
		return newSMTPSender(smtpSettings{
			Host:     o.str("host", ""),
			Port:     int(o.number("port", 587)),
			Security: o.str("security", "starttls"),
			Username: o.str("username", ""),
			Password: password,
			From:     from,
		})
	case ProviderHTTP:
		if from == "" {
			return nil, fmt.Errorf("%s provider needs a sender_id (the short code or account)", p.Provider)
		}
		apiKey := ""
		if ref := derefString(p.CredentialsRef); ref != "" {
			apiKey = s.secret(ref)
			if apiKey == "" {
				return nil, fmt.Errorf("secret %q is not set in the environment", ref)
			}
		}
		return newHTTPSender(httpSettings{
			URL:     o.str("base_url", ""),
			APIKey:  apiKey,
			Header:  o.str("auth_header", "apikey"),
			Scheme:  o.str("auth_scheme", ""),
			From:    from,
			ToField: o.str("to_field", "to"),
			BodyKey: o.str("text_field", "message"),
			Extra:   extraFields(o),
		})
	default:
		return nil, fmt.Errorf("provider %q is not one this build can speak to", p.Provider)
	}
}

// extraFields reads options.extra as flat strings - an account name or a sender label that
// the gateway wants alongside the message.
func extraFields(o options) map[string]string {
	raw, ok := o["extra"].(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]string, len(raw))
	for key, value := range raw {
		if text, ok := value.(string); ok {
			out[key] = text
		}
	}
	return out
}

// Provider names this build knows how to reach. Anything else in the table is a row nobody
// wired, and saying so on the notice is better than dialing a guess.
const (
	ProviderLog  = "log"
	ProviderSMTP = "smtp"
	ProviderHTTP = "http"
)

// logSender is the development provider: it writes the message to the application log and
// reports it landed. It is what makes the whole path testable - and a fresh tenant demoable -
// before anybody has an SMTP password.
type logSender struct{}

func (logSender) Send(ctx context.Context, msg Outbound) (Receipt, error) {
	// The notice text goes in the log at info level because "was this person told" is the
	// first question an operator asks of a development box, and the answer should be in the
	// output they are already watching.
	logNotice(ctx, msg)
	return Receipt{MessageID: "", ProvedLanding: true}, nil
}
