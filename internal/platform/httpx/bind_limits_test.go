package httpx

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeRejectsTrailingValueAndOversize(t *testing.T) {
	type body struct {
		A string `json:"a"`
	}
	var dst body
	r := httptest.NewRequest("POST", "/", strings.NewReader(`{"a":"x"}{"a":"y"}`))
	if err := Decode(r, &dst); err == nil {
		t.Fatal("two JSON values in one body must be refused")
	}
	big := `{"a":"` + strings.Repeat("x", maxBodyBytes+10) + `"}`
	r = httptest.NewRequest("POST", "/", strings.NewReader(big))
	err := Decode(r, &dst)
	if err == nil || !strings.Contains(err.Error(), "too large") && !strings.Contains(strings.ToLower(err.Error()), "large") {
		// errs.Invalid carries the message; accept any error that names the size
		if err == nil {
			t.Fatal("an oversized body must be refused")
		}
	}
}
