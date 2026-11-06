package notifications

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"accessnow/internal/platform/events"
)

func TestSmsUsesTheShortBodyTheTenantWrote(t *testing.T) {
	long := "Your goods pass {{pass_no}} was released at the main gate this morning with {{lines}} lines."
	subject, body := Render(Template{Subject: ptrString("never used"), Body: long,
		ShortBody: ptrString("Released: {{pass_no}} ({{lines}} line(s)).")}, ChannelSMS,
		Vars{"pass_no": "MTL-0007", "lines": "3"})
	if subject != "" {
		t.Errorf("subject = %q, an SMS has no header for it", subject)
	}
	if body != "Released: MTL-0007 (3 line(s))." {
		t.Errorf("body = %q", body)
	}
}

func TestLongSmsIsTrimmedNotSplit(t *testing.T) {
	// A gateway splits a long SMS into parts a person is charged for; one ellipsis is the
	// honest outcome when the tenant has not written a short_body.
	_, body := Render(Template{Body: strings.Repeat("x", smsLimit+120)}, ChannelSMS, Vars{})
	if len([]rune(body)) != smsLimit {
		t.Fatalf("length = %d, want %d", len([]rune(body)), smsLimit)
	}
	if !strings.HasSuffix(body, "…") {
		t.Errorf("tail = %q, want the truncation to be visible", body[len(body)-3:])
	}
}

func TestEmailCarriesItsSubject(t *testing.T) {
	subject, body := Render(Template{Subject: ptrString("{{employee_no}} is still out"),
		Body: "Due back since {{valid_until}}."}, ChannelEmail,
		Vars{"employee_no": "HQ-E002"})
	if subject != "HQ-E002 is still out" {
		t.Errorf("subject = %q", subject)
	}
	if !strings.Contains(body, "Due back since") {
		t.Errorf("body = %q", body)
	}
}

func TestUnknownPlaceholderIsRemovedNotShipped(t *testing.T) {
	// A notice that says "Pass {{pass_no}}" to a reader tells them nothing and looks broken.
	_, body := Render(Template{Body: "Pass {{pass_no}} for {{mystery}}."}, ChannelInApp,
		Vars{"pass_no": "VIS-0004"})
	if strings.Contains(body, "{{") || strings.Contains(body, "mystery") {
		t.Errorf("body = %q, an unknown variable must not reach a person", body)
	}
	if !strings.HasPrefix(body, "Pass VIS-0004") {
		t.Errorf("body = %q", body)
	}
}

// The reader's own wall clock is the only one a notice may use: two people in the same
// workflow otherwise read two different expiry times for one pass.
func TestTimeRendersInTheReadersOwnClock(t *testing.T) {
	at := time.Date(2026, 11, 6, 6, 0, 0, 0, time.UTC)
	zone := "Africa/Nairobi"
	if _, err := time.LoadLocation(zone); err != nil {
		t.Skipf("no zoneinfo for %s on this machine", zone)
	}
	v := Vars{}
	v.setTime("valid_until", &at, zone)
	if got := v["valid_until"]; !strings.HasSuffix(got, "09:00") {
		t.Errorf("%s = %q, want the reader's 09:00 not the server's 06:00", "valid_until", got)
	}

	v2 := Vars{}
	v2.setTime("valid_until", &at, "Mars/Olympus_Mons")
	if got := v2["valid_until"]; !strings.HasSuffix(got, "06:00") {
		t.Errorf("unknown zone = %q, must fall back to the stored UTC hour", got)
	}

	v3 := Vars{}
	v3.setTime("valid_until", nil, zone)
	if _, ok := v3["valid_until"]; ok {
		t.Error("a pass with no expiry must not gain one")
	}
}

func TestMoneyKeepsItsCurrency(t *testing.T) {
	amount, tz := 120000.0, "TZS"
	v := Vars{}
	v.setMoney(&amount, &tz)
	if v["value"] != "120000.00 TZS" {
		t.Errorf("value = %q, three currencies in one country is a band nobody can check without one", v["value"])
	}
	v2 := Vars{}
	v2.setMoney(nil, &tz)
	if _, ok := v2["value"]; ok {
		t.Error("an undeclared value rendered anyway")
	}
}

func TestNumbersLoseTheirDecimalPoint(t *testing.T) {
	v := Vars{}
	v.setNumber("minutes_out", 41)
	v.setNumber("value_band", 12.5)
	v.setNumber("lines", 0)
	if v["minutes_out"] != "41" || v["value_band"] != "12.5" {
		t.Errorf("minutes = %q band = %q", v["minutes_out"], v["value_band"])
	}
	if _, ok := v["lines"]; ok {
		t.Error("a zero count shipped as text")
	}
}

func TestFillPrefersThePassRowAndTheEventAgrees(t *testing.T) {
	payload := events.Event{Payload: map[string]any{
		"pass_no": "MTL-0000", "employee_no": "HQ-E002", "step_no": json.Number("2"),
	}}
	e := eventData{Event: payload, CatalogueName: "Goods declared", ReaderZone: "Africa/Nairobi"}
	p := PassContext{PassNo: "MTL-0009", Status: "pending", CategoryKey: "material",
		Purpose: ptrString("Returnable goods")}
	v := Fill(e, p, nameSet{Requester: "Wanjiru Kamau"})

	// The payload was written when the event fired and the row is what a reader can check:
	// where the two disagree about the same pass, the row wins.
	if v["pass_no"] != "MTL-0009" || v["status"] != "pending" {
		t.Errorf("pass_no = %q status = %q", v["pass_no"], v["status"])
	}
	if v["purpose"] != "Returnable goods" {
		t.Errorf("purpose = %q", v["purpose"])
	}
	if v["step_no"] != "2" {
		t.Errorf("step_no = %q, a JSON number must not print as 2.000000", v["step_no"])
	}
	if v["requester_name"] != "Wanjiru Kamau" {
		t.Errorf("requester_name = %q, \"Filed by 17\" is not a sentence at a gate", v["requester_name"])
	}
	if v["event_name"] != "Goods declared" {
		t.Errorf("event_name = %q", v["event_name"])
	}
}

func TestStaticAddressSortsByWhatItLooksLike(t *testing.T) {
	mail := staticAddressee("duty@example.test")
	if _, ok := mail.Address(ChannelEmail); !ok {
		t.Error("a duty mailbox was refused on the email channel")
	}
	if _, ok := mail.Address(ChannelSMS); ok {
		t.Error("a mailbox addressed as a phone number would mail-merge an @ into a handset")
	}

	phone := staticAddressee("+254700111222")
	if got, ok := phone.Address(ChannelSMS); !ok || got != "+254700111222" {
		t.Errorf("sms = %q ok=%v", got, ok)
	}
	if _, ok := phone.Address(ChannelEmail); ok {
		t.Error("a duty phone is not a mailbox")
	}
}

func TestInAppNeedsALoginAndNothingElse(t *testing.T) {
	user := int64(9)
	if _, ok := (Addressee{UserID: &user, Email: ptrString("a@b.c")}).Address(ChannelInApp); !ok {
		t.Error("a person with a login has an inbox")
	}
	if _, ok := (Addressee{Email: ptrString("a@b.c")}).Address(ChannelInApp); ok {
		t.Error("an in-app notice with no login can never be read")
	}
}
