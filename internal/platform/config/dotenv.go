package config

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strings"
)

// loadDotenv puts KEY=value pairs from a file into the process environment so
// `go run ./cmd/api` works without exporting a dozen variables by hand. A variable that
// is already set wins, so a deployment or CI job always overrides the file.
func loadDotenv(path string) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := parseEnvLine(scanner.Text())
		if !ok {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set %s: %w", key, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}

// parseEnvLine understands blank lines, whole-line # comments, an optional `export `
// prefix, and a value wrapped in one matching pair of quotes. There is deliberately no
// shell expansion or inline comment stripping: this file holds credentials, not scripts,
// and a password with a space in it must survive intact.
func parseEnvLine(line string) (string, string, bool) {
	text := strings.TrimSpace(line)
	if text == "" || strings.HasPrefix(text, "#") {
		return "", "", false
	}
	key, raw, found := strings.Cut(text, "=")
	if !found {
		return "", "", false
	}
	key = strings.TrimSpace(strings.TrimPrefix(key, "export "))
	if !isEnvName(key) {
		return "", "", false
	}
	return key, unquote(strings.TrimSpace(raw)), true
}

func unquote(value string) string {
	if len(value) < 2 {
		return value
	}
	first, last := value[0], value[len(value)-1]
	if first != last || (first != '"' && first != '\'') {
		return value
	}
	return value[1 : len(value)-1]
}

func isEnvName(key string) bool {
	if key == "" {
		return false
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' {
			continue
		}
		if c >= '0' && c <= '9' && i > 0 {
			continue
		}
		return false
	}
	return true
}
