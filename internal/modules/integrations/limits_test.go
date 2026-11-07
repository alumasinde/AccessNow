package integrations

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// stubLimiter records what the middleware asked and answers what the test needs.
type stubLimiter struct {
	allowed   bool
	wait      time.Duration
	err       error
	calls     int
	lastKey   int64
	lastLimit int
}

func (s *stubLimiter) allow(_ context.Context, keyID int64, limit int,
	_ time.Time) (bool, time.Duration, error) {
	s.calls++
	s.lastKey = keyID
	s.lastLimit = limit
	return s.allowed, s.wait, s.err
}

func limitOf(value int) *int { return &value }

func TestRateLimitMiddleware(t *testing.T) {
	stub := &stubLimiter{allowed: true}
	h := NewHandler(nil)
	h.limits = stub
	h.now = func() time.Time { return time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC) }
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := h.RateLimit(next)

	call := func(key APIKey) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/machine/me", nil)
		request = request.WithContext(withKey(request.Context(), key))
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder
	}

	key := APIKey{ID: 9, RateLimit: limitOf(2)}
	if code := call(key).Code; code != http.StatusOK {
		t.Fatalf("first request = %d", code)
	}
	if stub.lastKey != 9 || stub.lastLimit != 2 {
		t.Fatalf("limiter asked for key=%d limit=%d", stub.lastKey, stub.lastLimit)
	}

	stub.allowed, stub.wait = false, 30*time.Second
	refused := call(key)
	if refused.Code != http.StatusTooManyRequests {
		t.Fatalf("third request = %d, want 429", refused.Code)
	}
	if refused.Header().Get("Retry-After") == "" {
		t.Fatal("a 429 must say when to try again")
	}
	if refused.Header().Get("X-RateLimit-Limit") != "2" {
		t.Fatalf("limit header = %q", refused.Header().Get("X-RateLimit-Limit"))
	}

	stub.allowed = true
	if code := call(APIKey{ID: 10}).Code; code != http.StatusOK {
		t.Fatalf("key without its own limit = %d, want 200", code)
	}
	if stub.lastLimit != defaultKeyRateLimit {
		t.Fatalf("default limit = %d, want %d", stub.lastLimit, defaultKeyRateLimit)
	}
}

// TestRateLimitFailsOpen keeps a database outage from taking the machine surface with it: the
// limiter's error is logged and the request proceeds.
func TestRateLimitFailsOpen(t *testing.T) {
	stub := &stubLimiter{err: errors.New("database is down")}
	h := NewHandler(nil)
	h.limits = stub
	handler := h.RateLimit(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/integrations/machine/me", nil)
	request = request.WithContext(withKey(request.Context(), APIKey{ID: 1}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want the request to pass", recorder.Code)
	}
}
