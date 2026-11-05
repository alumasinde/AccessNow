package security

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"golang.org/x/crypto/argon2"
)

func TestHashVerifyRoundTrip(t *testing.T) {
	stored, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(stored, "$argon2id$v=19$") {
		t.Errorf("hash format = %q", stored)
	}
	if !VerifyPassword("correct horse battery staple", stored) {
		t.Error("password rejected its own hash")
	}
	if VerifyPassword("Correct horse battery staple", stored) {
		t.Error("case-flipped password accepted")
	}
}

// Two hashes of the same password differ, because each gets its own salt.
func TestHashIsSalted(t *testing.T) {
	first, err := HashPassword("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	second, err := HashPassword("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Error("same password produced the same hash")
	}
}

// Cost lives inside every hash, so raising the defaults must not lock out old rows.
func TestVerifyRespectsStoredCost(t *testing.T) {
	salt := make([]byte, saltBytes)
	if _, err := rand.Read(salt); err != nil {
		t.Fatal(err)
	}
	key := argon2.IDKey([]byte("old-row"), salt, 1, 1024, 1, 16)
	stored := fmt.Sprintf("$argon2id$v=%d$m=1024,t=1,p=1$%s$%s",
		argon2.Version, base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key))

	if !VerifyPassword("old-row", stored) {
		t.Errorf("row written at a cheaper cost no longer verifies: %q", stored)
	}
}

func TestVerifyIgnoresMalformedHashes(t *testing.T) {
	for _, stored := range []string{
		"", "plaintext-not-a-hash",
		"$argon2i$v=19$m=65536,t=1,p=4$AAAA$AAAA",
		"$argon2id$v=16$m=65536,t=1,p=4$AAAA$AAAA",
		"$argon2id$v=19$m=0,t=1,p=4$AAAA$AAAA",
		"$argon2id$v=19$m=65536,t=1,p=4$not base64!$AAAA",
	} {
		if VerifyPassword("anything", stored) {
			t.Errorf("malformed hash accepted: %q", stored)
		}
	}
}

func TestHashTokenMatchesColumnWidth(t *testing.T) {
	token, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if token == other {
		t.Error("two tokens were identical")
	}

	hash := HashToken(token)
	if len(hash) != 64 {
		t.Errorf("HashToken length = %d, want the CHAR(64) column width", len(hash))
	}
	if HashToken(token) != hash {
		t.Error("HashToken is not deterministic, so a lookup could never match")
	}
}
