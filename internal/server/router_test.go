package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"accessnow/internal/platform/config"
	"accessnow/internal/platform/router"
)

// passGuard is a router guard that lets every request through. The router test is about the
// two routes that exist before any module: the health probe and the not-found boundary.
func passGuard() router.Guard {
	pass := func(next http.Handler) http.Handler { return next }
	return router.Guard{Session: pass, Module: func(string) func(http.Handler) http.Handler { return pass },
		Permission: func(string) func(http.Handler) http.Handler { return pass },
		GateWork:   pass, Device: pass}
}

func TestHealthAndNotFound(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := New(nil, &Deps{Guard: passGuard()}, config.Config{
		HandlerTimeout: time.Second, AllowedOrigins: []string{"http://localhost"},
		LoginAttempts: 10, ScanRate: 60,
	}, logger)
	server := httptest.NewServer(handler)
	defer server.Close()

	health, err := http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatalf("health request: %v", err)
	}
	defer health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want 200", health.StatusCode)
	}

	missing, err := http.Get(server.URL + "/api/v1/does-not-exist")
	if err != nil {
		t.Fatalf("not-found request: %v", err)
	}
	defer missing.Body.Close()
	if missing.StatusCode != http.StatusNotFound {
		t.Fatalf("missing status = %d, want 404", missing.StatusCode)
	}
}
