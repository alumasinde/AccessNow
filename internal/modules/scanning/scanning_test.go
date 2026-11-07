package scanning

import (
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"accessnow/internal/core/movements"
)

var now = time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)

func TestNewSecretIs128BitsOfHex(t *testing.T) {
	first, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Error("two calls returned the same secret, so it is not random")
	}
	if len(first) != 32 {
		t.Errorf("16 bytes of hex is 32 characters, got %d", len(first))
	}
	if _, err := hex.DecodeString(first); err != nil {
		t.Errorf("a secret has to survive a printable scanner: %v", err)
	}
	if PrefixOf(first) != first[:prefixLength] {
		t.Error("the prefix is not the first characters of the secret")
	}
	if len(PrefixOf(first)) != prefixLength {
		t.Error("the prefix is the wrong length for a support agent to read out")
	}
}

func TestTokenRefusalNamesTheRealReason(t *testing.T) {
	revoked := now.Add(-time.Hour)
	expired := now.Add(-time.Minute)
	reason := "badge returned at the desk"
	one, two := 1, 2

	cases := []struct {
		name  string
		token Token
		want  string
	}{
		{"live", Token{}, ""},
		{"revoked", Token{RevokedAt: &revoked, RevokeReason: &reason}, "revoked"},
		{"expired", Token{ExpiresAt: &expired}, "expired"},
		{"spent", Token{MaxUses: &one, UseCount: 1}, "already been used"},
		{"still usable", Token{MaxUses: &two, UseCount: 1}, ""},
		// A revoked badge that has also run out is revoked: the desk is the answer either way,
		// and the first reason is the one the supervisor wrote down.
		{"revoked and expired", Token{RevokedAt: &revoked, ExpiresAt: &expired}, "revoked"},
	}
	for _, c := range cases {
		if got := c.token.Refusal(now); !strings.Contains(got, c.want) {
			t.Errorf("%s: refused with %q, wanted %q", c.name, got, c.want)
		}
	}
	if !(Token{}).Live(now) {
		t.Error("a token with no expiry, no uses and no revocation is live")
	}
}

func TestInputVocabularyStaysInsideTheColumn(t *testing.T) {
	if inputOf("") != InputQR {
		t.Error("an empty input is a QR code, because that is what a tablet has")
	}
	if inputOf("NFC") != InputNFC {
		t.Error("a reader's upper-case kind was not folded")
	}
	// A device bug must not break the insert on a CHECK constraint.
	if inputOf("telepathy") != InputOther {
		t.Error("an unknown reader kind was not filed as other")
	}
	for _, value := range []Input{InputQR, InputBarcode, InputRFID, InputNFC, InputPlate,
		InputManual, InputFace, InputOther} {
		if !value.valid() {
			t.Errorf("%q is in the column but valid() refuses it", value)
		}
	}
	for _, value := range []Format{FormatQRText, FormatQRURL, FormatNumeric, FormatNFC,
		FormatRFID, FormatBarcode, FormatANPR} {
		if !value.valid() {
			t.Errorf("%q is a format chk_pass_tokens_format allows", value)
		}
	}
	if Format("magic").valid() {
		t.Error("an invented format was accepted")
	}
}

func TestClassifyFilesTheRightOutcome(t *testing.T) {
	cases := []struct {
		attempt attempts
		outcome string
		code    string
	}{
		{attempts{movements.ResultGranted, ""}, "granted", "GRANTED"},
		{attempts{movements.ResultWarned, "denied: expired permit"}, "warned", "WARNED"},
		{attempts{movements.ResultOverride, ""}, "override", "OVERRIDE"},
		{attempts{movements.ResultDenied, "denied: that token is not on file"},
			"unknown_token", "UNKNOWN_TOKEN"},
		{attempts{movements.ResultDenied, "denied: the token was revoked"}, "revoked",
			"TOKEN_REVOKED"},
		{attempts{movements.ResultDenied, "denied: the token has expired"}, "expired",
			"TOKEN_EXPIRED"},
		{attempts{movements.ResultDenied, "denied: the token has already been used"},
			"already_used", "TOKEN_USED"},
		{attempts{movements.ResultDenied, "denied: this pass is not valid at this gate"},
			"wrong_gate", "WRONG_GATE"},
		{attempts{movements.ResultDenied, "denied: outside the validity window"},
			"out_of_window", "OUT_OF_WINDOW"},
		{attempts{movements.ResultDenied, "denied: QR scanning is not enabled at this site"},
			"denied", "SCANNING_DISABLED"},
		{attempts{movements.ResultDenied, "denied: something else entirely"}, "denied", "DENIED"},
	}
	for _, c := range cases {
		outcome, code := classify(c.attempt.attempt())
		if outcome != c.outcome || code != c.code {
			t.Errorf("%q classified as %s/%s, wanted %s/%s", c.attempt.reason, outcome, code,
				c.outcome, c.code)
		}
	}
}

// attempts keeps the two fields classify reads side by side, so the table above reads as the
// sentence a guard would see.
type attempts struct {
	result movements.Result
	reason string
}

func (a attempts) attempt() movements.Attempt {
	return movements.Attempt{Result: a.result, Reason: a.reason}
}

func TestIssueRequestRefusesWhatTheColumnsWould(t *testing.T) {
	if err := (IssueRequest{}).validate(); err != nil {
		t.Errorf("an empty request means a QR code with no expiry: %v", err)
	}
	if err := (IssueRequest{Format: FormatNFC, Delivery: "badge"}).validate(); err != nil {
		t.Errorf("a badge format with a badge delivery was refused: %v", err)
	}
	zero := 0
	for name, req := range map[string]IssueRequest{
		"format":   {Format: "magic"},
		"delivery": {Delivery: "carrier pidgeon"},
		"max_uses": {MaxUses: &zero},
	} {
		if err := req.validate(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestIssuedViewCarriesTheSecretOnceAndNoHash(t *testing.T) {
	token := Token{
		ID: 7, PassID: 3, PassNo: "VIS-26110001", PassStatus: "approved",
		TokenPrefix: "aabbccdd", Format: "qr_text", IssuedAt: now,
	}
	view := viewOfIssued(Issued{Token: token, Secret: "aabbccdd11223344556677889900aabb"}, now)

	body, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"token":"aabbccdd11223344556677889900aabb"`) {
		t.Errorf("the issue response has to hand back the secret: %s", body)
	}
	if strings.Contains(string(body), "token_hash") {
		t.Errorf("the hash is not an API field: %s", body)
	}
	if view.Status != "live" {
		t.Errorf("a fresh token answered %q", view.Status)
	}

	// And a listed token carries the prefix only.
	listed, err := json.Marshal(viewOfToken(token, now))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(listed), "112233445566") {
		t.Errorf("the list leaked the secret: %s", listed)
	}
	if !strings.Contains(string(listed), `"prefix":"aabbccdd"`) {
		t.Errorf("the list lost the prefix a supervisor reads to a caller: %s", listed)
	}
}

func TestStatusOfIsTheOneWordADeskColoursBy(t *testing.T) {
	revoked := now.Add(-time.Minute)
	expired := now.Add(-time.Minute)
	one := 1
	cases := map[string]struct {
		token Token
		want  string
	}{
		"live":    {Token{}, "live"},
		"revoked": {Token{RevokedAt: &revoked}, "revoked"},
		"expired": {Token{ExpiresAt: &expired}, "expired"},
		"used":    {Token{MaxUses: &one, UseCount: 1}, "used"},
	}
	for name, c := range cases {
		if got := statusOf(c.token, now); got != c.want {
			t.Errorf("%s answered %q, wanted %q", name, got, c.want)
		}
	}
}

func TestParseAttemptsRefusesFiltersTheColumnCannotAnswer(t *testing.T) {
	filter, _, err := ParseAttempts(httptest.NewRequest("GET",
		"/scan-attempts?gate_id=3&pass_id=9&outcome=revoked&input=nfc", nil))
	if err != nil {
		t.Fatal(err)
	}
	if filter.GateID != 3 || filter.PassID != 9 || filter.Outcome != "revoked" ||
		filter.Input != "nfc" {
		t.Errorf("filters lost: %+v", filter)
	}
	for _, query := range []string{
		"/scan-attempts?outcome=maybe",   // not an outcome the CHECK allows
		"/scan-attempts?input=telepathy", // not a reader kind
		"/scan-attempts?gate_id=-4",      // an id cannot be negative
	} {
		if _, _, err := ParseAttempts(httptest.NewRequest("GET", query, nil)); err == nil {
			t.Errorf("%s accepted", query)
		}
	}
}
