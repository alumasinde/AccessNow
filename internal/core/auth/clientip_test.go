package auth

import (
	"net/http/httptest"
	"testing"
)

func TestClientIPIgnoresForwardedHeader(t *testing.T) {
	r := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
	r.RemoteAddr = "203.0.113.9:4444"
	r.Header.Set("X-Forwarded-For", "10.0.0.1")
	if got := clientIP(r); got != "203.0.113.9" {
		t.Fatalf("clientIP = %q, want the socket address", got)
	}
}
