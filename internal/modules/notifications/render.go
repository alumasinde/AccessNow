package notifications

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"accessnow/internal/platform/events"
)

// smsLimit is the length a gateway will accept before it starts splitting a notice into
// parts a person is charged for. 480 is generous; the operators who quote per segment start
// at 160 and the tenant who cares will have written a short_body.
const smsLimit = 480

// placeholder is {{name}} as templates in the database write it. Anything that does not
// match a known variable is removed rather than shipped: a notice that says "Pass {{pass_no}"
// tells the reader nothing and looks broken.
var placeholder = regexp.MustCompile(`\{\{[^{}]*\}\}`)

// Vars is the message's fill: the payload's own keys, the pass row's facts, and the few
// names that only a resolved recipient can supply.
type Vars map[string]string

func (v Vars) set(key, value string) {
	if value == "" {
		return
	}
	v[key] = value
}

// setTime renders a moment in the reader's own wall clock. A notice that says a pass expired
// at 06:00 when the reader is at 09:00 is a notice about a different pass.
func (v Vars) setTime(key string, at *time.Time, zone string) {
	if at == nil {
		return
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		loc = time.UTC
	}
	v[key] = at.In(loc).Format("2 Jan 2006 15:04")
}

// setMoney keeps the currency visible. A value band that reads "120000" in a country with
// three currencies in use is a band nobody can check.
func (v Vars) setMoney(amount *float64, currency *string) {
	if amount == nil {
		return
	}
	out := strconv.FormatFloat(*amount, 'f', 2, 64)
	if currency != nil && *currency != "" {
		out += " " + *currency
	}
	v["value"] = out
}

// eventData is what a template can read about one outbox row: the event itself, the plain
// words the catalogue gives that key, and the wall clock of whoever will read the notice.
// ReaderZone is per recipient, so the caller copies this value once per person.
type eventData struct {
	events.Event
	CatalogueName string
	ReaderZone    string
}

// Num reads a number the producer put in the payload. Everything from JSON is a float64;
// sqlx-free handlers must not assume that, because a handler written in Go publishes ints.
func (e eventData) Num(key string) float64 {
	switch value := e.Payload[key].(type) {
	case float64:
		return value
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case json.Number:
		parsed, err := value.Float64()
		if err != nil {
			return 0
		}
		return parsed
	default:
		return 0
	}
}

// nameSet carries the three names a notice reads better for having looked up. An id in a
// message to a human is a bug the reader has to work around.
type nameSet struct {
	Requester string
	Host      string
	Actor     string
}

// Fill turns the event and the pass into the variables a template can name. Everything here
// comes from data the system already had; nothing invents a fact to make a message read
// better.
func Fill(e eventData, p PassContext, names nameSet) Vars {
	v := Vars{}
	v.set("event_name", e.CatalogueName)
	v.set("pass_no", p.PassNo)
	if p.PassNo == "" {
		v.set("pass_no", e.Str("pass_no"))
	}
	v.set("status", e.Str("status"))
	if p.Status != "" {
		v.set("status", p.Status)
	}
	v.set("chain_status", e.Str("chain_status"))
	v.set("decision", e.Str("decision"))
	v.set("comment", e.Str("comment"))
	v.set("reason", e.Str("reason"))
	v.set("note", e.Str("note"))
	v.set("purpose", e.Str("purpose"))
	if p.Purpose != nil && e.Str("purpose") == "" {
		v.set("purpose", *p.Purpose)
	}
	v.set("kind", e.Str("kind"))
	v.set("category", p.CategoryKey)
	v.set("site_name", p.SiteName)
	v.set("department", p.Department)
	v.set("subject_name", firstNonEmpty(p.SubjectName, e.Str("subject_name"), e.Str("visitor"),
		e.Str("staff_name")))
	v.set("employee_no", e.Str("employee_no"))
	v.set("staff_name", e.Str("staff_name"))
	v.set("destination", e.Str("destination"))
	v.set("declared_by", e.Str("declared_by"))
	v.set("plate", e.Str("plate"))
	v.set("driver", e.Str("driver"))
	v.set("step_name", e.Str("step_name"))
	v.set("assignee", e.Str("assignee"))
	v.setNumber("step_no", e.Num("step_no"))
	v.setNumber("lines", e.Num("lines"))
	v.setNumber("minutes_out", e.Num("minutes_out"))
	v.setNumber("dwell_minutes", e.Num("dwell_minutes"))
	v.setNumber("dwell_limit_minutes", e.Num("dwell_limit_minutes"))
	v.setNumber("expected_minutes", e.Num("expected_duration_minutes"))
	v.setMoney(p.ValueAmount, p.ValueCurrency)
	v.setTime("valid_until", p.ValidUntil, e.ReaderZone)

	// The people names are worth a lookup because "Filed by 17" is not a sentence anybody at
	// a gate can act on.
	v.set("requester_name", firstNonEmpty(names.Requester, p.SubjectName))
	v.set("host_name", names.Host)
	v.set("actor_name", names.Actor)
	return v
}

func (v Vars) setNumber(key string, value float64) {
	if value == 0 {
		return
	}
	// Step numbers and minute counts are integers in the database and arrive as float64
	// through JSON; printing "2.000000" in a message is a bug the reader would see.
	if value == float64(int64(value)) {
		v[key] = strconv.FormatInt(int64(value), 10)
		return
	}
	v[key] = strconv.FormatFloat(value, 'f', 1, 64)
}

// Render fills one template for one channel. An SMS or WhatsApp notice uses short_body when
// the tenant wrote one, because the point of that column is a text that fits.
func Render(t Template, channel Channel, v Vars) (subject, body string) {
	switch channel {
	case ChannelSMS, ChannelWhatsApp:
		body = trim(apply(derefString(t.ShortBody), t.Body, v), smsLimit)
		return "", body
	case ChannelInApp:
		return "", apply("", t.Body, v)
	default:
		body = apply("", t.Body, v)
		if t.Subject != nil {
			subject = apply("", *t.Subject, v)
		}
		return subject, body
	}
}

// Unaddressed is what a notice says when the tenant has no template row at all - which
// happens the moment a new event key is published and before anybody has written its copy.
// Losing the notice would be the worse error.
func Unaddressed(eventName string, v Vars) string {
	out := eventName
	if passNo := v["pass_no"]; passNo != "" {
		out += " (" + passNo + ")"
	}
	if name := v["subject_name"]; name != "" {
		out += ": " + name
	}
	return out
}

func apply(short, long string, v Vars) string {
	if short != "" {
		return substitute(short, v)
	}
	return substitute(long, v)
}

func substitute(text string, v Vars) string {
	if text == "" {
		return ""
	}
	pairs := make([]string, 0, len(v)*2)
	for key, value := range v {
		pairs = append(pairs, "{{"+key+"}}", value)
	}
	out := strings.NewReplacer(pairs...).Replace(text)
	return strings.TrimSpace(placeholder.ReplaceAllString(out, ""))
}

func trim(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	if limit <= 1 {
		return string(runes[:limit])
	}
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}

func firstNonEmpty(candidates ...string) string {
	for _, c := range candidates {
		if strings.TrimSpace(c) != "" {
			return c
		}
	}
	return ""
}
