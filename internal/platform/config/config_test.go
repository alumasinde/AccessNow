package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseEnvLine(t *testing.T) {
	tests := []struct {
		name       string
		line       string
		wantKey    string
		wantValue  string
		wantAccept bool
	}{
		{"plain", "DB_PORT=3306", "DB_PORT", "3306", true},
		{"export prefix", "export APP_ENV=production", "APP_ENV", "production", true},
		{"spaces around", "  HTTP_ADDR = :9090 ", "HTTP_ADDR", ":9090", true},
		{"double quoted", `NAME="Demo Gate"`, "NAME", "Demo Gate", true},
		{"single quoted keeps backslash", `PATH='C:\tmp'`, "PATH", `C:\tmp`, true},
		{"empty value", "DB_PASSWORD=", "DB_PASSWORD", "", true},
		{"value with an equals sign", "TOKEN=ab=cd", "TOKEN", "ab=cd", true},
		{"mismatched quotes stay literal", `NAME="Demo Gate`, "NAME", `"Demo Gate`, true},
		{"comment", "# comma separated", "", "", false},
		{"blank", "   ", "", "", false},
		{"no equals", "NOT_A_PAIR", "", "", false},
		{"leading digit", "9LIVES=cat", "", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			key, value, ok := parseEnvLine(tc.line)
			if ok != tc.wantAccept || key != tc.wantKey || value != tc.wantValue {
				t.Errorf("parseEnvLine(%q) = %q, %q, %v; want %q, %q, %v",
					tc.line, key, value, ok, tc.wantKey, tc.wantValue, tc.wantAccept)
			}
		})
	}
}

func TestLoadDotenvDoesNotOverrideTheEnvironment(t *testing.T) {
	t.Setenv("APP_ENV", "from-the-real-environment")
	path := writeDotenv(t, "APP_ENV=from-the-file\nDB_NAME=from_the_file\n")
	if err := loadDotenv(path); err != nil {
		t.Fatalf("loadDotenv: %v", err)
	}
	if got := os.Getenv("APP_ENV"); got != "from-the-real-environment" {
		t.Errorf("APP_ENV = %q, a deployment must win over the file", got)
	}
	if got := os.Getenv("DB_NAME"); got != "from_the_file" {
		t.Errorf("DB_NAME = %q, want the file value", got)
	}
}

func TestLoadDotenvIsQuietWhenTheFileIsAbsent(t *testing.T) {
	if err := loadDotenv(filepath.Join(t.TempDir(), "nope.env")); err != nil {
		t.Errorf("missing file = %v, want nil", err)
	}
}

// Load reads a .env from the working directory, and a comma-separated value must survive
// the tag: envconfig rejects an option name it does not know, which is how the original
// seq= tag broke every command before it reached the database.
func TestLoadFromDotenvFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".env"), ""+
		"DB_USER=seed\n"+
		"DB_PASSWORD='s3cr3t, with, commas'\n"+
		"DB_NAME=accessnow_demo\n"+
		"HTTP_WRITE_TIMEOUT=45s\n"+
		"ALLOWED_ORIGINS=http://localhost:5173,https://gate.example\n")

	unsetEnv(t, "DB_USER", "DB_PASSWORD", "DB_NAME", "HTTP_WRITE_TIMEOUT", "ALLOWED_ORIGINS")
	chdir(t, dir)

	cfg, err := Load(context.Background())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.AllowedOrigins) != 2 || cfg.AllowedOrigins[1] != "https://gate.example" {
		t.Errorf("AllowedOrigins = %#v", cfg.AllowedOrigins)
	}
	if cfg.Database.User != "seed" || cfg.Database.Password != "s3cr3t, with, commas" {
		t.Errorf("Database = %+v", cfg.Database)
	}
	if cfg.Database.Name != "accessnow_demo" || cfg.WriteTimeout != 45*time.Second {
		t.Errorf("Database.Name = %q, WriteTimeout = %v", cfg.Database.Name, cfg.WriteTimeout)
	}
	if cfg.ScanRate != 300 || cfg.LoginAttempts != 10 {
		t.Errorf("rate defaults = %d/%d", cfg.ScanRate, cfg.LoginAttempts)
	}
}

func TestLoadRefusesToStartWithoutADatabase(t *testing.T) {
	unsetEnv(t, "DB_USER", "DB_NAME")
	chdir(t, t.TempDir())

	if _, err := Load(context.Background()); err == nil {
		t.Fatal("Load succeeded with no DB_USER or DB_NAME")
	}
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
}

// unsetEnv keeps a developer's exported DB_* from deciding what the test sees.
func unsetEnv(t *testing.T, keys ...string) {
	t.Helper()
	for _, key := range keys {
		previous, existed := os.LookupEnv(key)
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if existed {
				_ = os.Setenv(key, previous)
				return
			}
			_ = os.Unsetenv(key)
		})
	}
}

func writeDotenv(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	writeFile(t, path, body)
	return path
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
