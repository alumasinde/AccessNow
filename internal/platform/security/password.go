package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	saltBytes   = 16
	argonTime   = 1
	argonMemory = 64 * 1024
	argonThread = 4
	argonKeyLen = 32
)

var ErrMalformedHash = errors.New("malformed argon2id hash")

type cost struct {
	time, memory uint32
	threads      uint8
}

// Changing these later only moves the parameters inside newly written hashes: the cost
// is stored in every hash, so old rows keep verifying on login.
var defaultCost = cost{time: argonTime, memory: argonMemory, threads: argonThread}

// HashPassword produces $argon2id$v=19$m=65536,t=1,p=4$<salt>$<key>.
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, defaultCost.time, defaultCost.memory, defaultCost.threads, argonKeyLen)
	return encode(defaultCost, salt, key), nil
}

// VerifyPassword reports false for a malformed stored hash instead of an error, so a
// login handler cannot tell an attacker which half of the row was wrong.
func VerifyPassword(password, encoded string) bool {
	c, salt, key, err := decode(encoded)
	if err != nil {
		return false
	}
	candidate := argon2.IDKey([]byte(password), salt, c.time, c.memory, c.threads, uint32(len(key)))
	return subtle.ConstantTimeCompare(candidate, key) == 1
}

func encode(c cost, salt, key []byte) string {
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, c.memory, c.time, c.threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key))
}

func decode(encoded string) (cost, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return cost{}, nil, nil, ErrMalformedHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return cost{}, nil, nil, ErrMalformedHash
	}

	c := cost{}
	for _, field := range strings.Split(parts[3], ",") {
		name, raw, ok := strings.Cut(field, "=")
		if !ok {
			return cost{}, nil, nil, ErrMalformedHash
		}
		n, err := strconv.ParseUint(raw, 10, 32)
		if err != nil {
			return cost{}, nil, nil, ErrMalformedHash
		}
		switch name {
		case "m":
			c.memory = uint32(n)
		case "t":
			c.time = uint32(n)
		case "p":
			c.threads = uint8(n)
		default:
			return cost{}, nil, nil, ErrMalformedHash
		}
	}
	if c.memory == 0 || c.time == 0 || c.threads == 0 {
		return cost{}, nil, nil, ErrMalformedHash
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return cost{}, nil, nil, ErrMalformedHash
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return cost{}, nil, nil, ErrMalformedHash
	}
	return c, salt, key, nil
}
