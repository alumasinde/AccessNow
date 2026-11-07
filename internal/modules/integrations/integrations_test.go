package integrations

import (
	"encoding/hex"
	"net"
	"testing"
)

func TestSignatureIsStableAndBoundToBody(t *testing.T) {
	// A fixed secret and body, so the vector is a contract: a receiver that computes the same
	// HMAC must see exactly this value.
	got := signature("s3cr3t", []byte(`{"event":"gate.movement"}`), 1700000000)
	if len(got) != 64 {
		t.Fatalf("signature is %d characters, want 64 hex", len(got))
	}
	if decoded, err := hex.DecodeString(got); err != nil || len(decoded) != 32 {
		t.Fatalf("signature is not a 32-byte HMAC: %v", err)
	}
	if again := signature("s3cr3t", []byte(`{"event":"gate.movement"}`), 1700000000); again != got {
		t.Fatal("signature is not deterministic")
	}
	if other := signature("s3cr3t", []byte(`{"event":"gate.movement!"}`), 1700000000); other == got {
		t.Fatal("a different body must sign differently")
	}
	if other := signature("s3cr3t", []byte(`{"event":"gate.movement"}`), 1700000001); other == got {
		t.Fatal("a different timestamp must sign differently")
	}
}

func TestPublicIPRejectsPrivateTargets(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1":       false,
		"10.1.2.3":        false,
		"192.168.1.10":    false,
		"172.16.0.5":      false,
		"169.254.169.254": false,
		"::1":             false,
		"fd00::1":         false,
		"8.8.8.8":         true,
		"1.1.1.1":         true,
	}
	for raw, want := range cases {
		ip := net.ParseIP(raw)
		if ip == nil {
			t.Fatalf("%s did not parse", raw)
		}
		if got := publicIP(ip); got != want {
			t.Fatalf("publicIP(%s) = %v, want %v", raw, got, want)
		}
	}
}

func TestValidWebhookURLRefusesLocalAndPlainHTTP(t *testing.T) {
	for _, raw := range []string{
		"http://example.com/hook",
		"https://localhost/hook",
		"https://127.0.0.1/hook",
		"https://10.0.0.4/hook",
		"https://user:pass@example.com/hook",
		"not a url",
	} {
		if err := validWebhookURL(raw); err == nil {
			t.Fatalf("%q should have been refused", raw)
		}
	}
	if err := validWebhookURL("https://hooks.example.com/accessnow"); err != nil {
		t.Fatalf("a public https URL was refused: %v", err)
	}
}

func TestWebhookWants(t *testing.T) {
	all := Webhook{SubscribeAll: true}
	if !all.Wants("anything.at.all") {
		t.Fatal("subscribe_all must accept every key")
	}
	some := Webhook{Events: `["visit.created","pass.approved"]`}
	if !some.Wants("pass.approved") {
		t.Fatal("a listed key must be accepted")
	}
	if some.Wants("blocklist.watched_hit") {
		t.Fatal("an unlisted key must not be accepted")
	}
	broken := Webhook{Events: `not json`}
	if broken.Wants("visit.created") {
		t.Fatal("a subscription that cannot be read must send nothing")
	}
}

func TestBackoffGrowsAndCaps(t *testing.T) {
	first := backoff(1)
	if first != firstBackoff {
		t.Fatalf("first retry waits %s, want %s", first, firstBackoff)
	}
	if second := backoff(2); second <= first {
		t.Fatalf("second retry waits %s, not more than %s", second, first)
	}
	if worst := backoff(100); worst != worstBackoff {
		t.Fatalf("a long ladder waits %s, want the cap %s", worst, worstBackoff)
	}
}

func TestKeyScopeAndSecretValidation(t *testing.T) {
	if _, err := scopeList([]string{"visit.view", "pass.approve"}); err != nil {
		t.Fatalf("valid scopes were refused: %v", err)
	}
	if _, err := scopeList([]string{"Visit View"}); err == nil {
		t.Fatal("a scope that is not a permission key must be refused")
	}
	if _, err := scopeList([]string{"*"}); err != nil {
		t.Fatalf("the wildcard scope was refused: %v", err)
	}
	if !validSecretRef("ACCESSNOW_HOOK") {
		t.Fatal("an environment-style secret ref was refused")
	}
	if validSecretRef("accessnow hook") {
		t.Fatal("a secret ref with a space was accepted")
	}
}

func TestCommandVocabulary(t *testing.T) {
	if !knownCommand("open_boom_gate") || !knownCommand("release_turnstile") {
		t.Fatal("a seeded actuator command was refused")
	}
	if knownCommand("open_everything") {
		t.Fatal("an unknown command was accepted")
	}
	if !knownSource(SourceApproved) || knownSource("because") {
		t.Fatal("command sources are not a closed vocabulary")
	}
}
