package config

import "testing"

func TestProductionRefusesUnsafeCookieAndOrigins(t *testing.T) {
	base := Config{Environment: "production", Database: Database{User: "u", Name: "n"}}
	if err := base.check(); err == nil {
		t.Fatal("production without COOKIE_SECURE must not start")
	}
	base.CookieSecure = true
	if err := base.check(); err == nil {
		t.Fatal("production without ALLOWED_ORIGINS must not start")
	}
	base.AllowedOrigins = []string{"*"}
	if err := base.check(); err == nil {
		t.Fatal("a wildcard origin with credentials must not start")
	}
	base.AllowedOrigins = []string{"https://app.example.com"}
	if err := base.check(); err != nil {
		t.Fatalf("a safe production config must start: %v", err)
	}
	dev := Config{Environment: "development", Database: Database{User: "u", Name: "n"}}
	if err := dev.check(); err != nil {
		t.Fatalf("development stays permissive: %v", err)
	}
}
