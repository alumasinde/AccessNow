package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"accessnow/internal/platform/errs"
)

// Fail logs a 500 the way a request would; the tests are about the body, not the noise.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

func TestFailStatusPerCode(t *testing.T) {
	tests := []struct {
		err    error
		expect int
	}{
		{errs.Invalid("nope"), 400},
		{errs.BadField("plate", "is required"), 400},
		{errs.Unauthenticated("sign in"), 401},
		{errs.Forbidden("not your tenant"), 403},
		{errs.GateUnassigned(7), 403},
		{errs.NotFound("pass"), 404},
		{errs.ModuleDisabled("visitor_management"), 404},
		{errs.Conflict("already closed"), 409},
		{errs.New(errs.CodeTooManyRequests, "slow down"), 429},
		{errors.New("connection refused to 10.0.0.5:3306"), 500},
	}

	for _, tc := range tests {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest("GET", "/api/v1/passes", nil)
		Fail(context.Background(), recorder, request, tc.err)

		if recorder.Code != tc.expect {
			t.Errorf("%v -> %d, want %d", tc.err, recorder.Code, tc.expect)
		}
		var body Body
		if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body %q: %v", recorder.Body.String(), err)
		}
		if body.Type == "" {
			t.Errorf("%v -> empty error type", tc.err)
		}
	}
}

// The client gets a request id to quote, never the driver's words.
func TestFailHidesInternalDetail(t *testing.T) {
	recorder := httptest.NewRecorder()
	Fail(context.Background(), recorder, httptest.NewRequest("GET", "/", nil),
		errs.Internal(errors.New("duplicate key 'uk_passes_pass_no' on tenant 42")))

	if strings.Contains(recorder.Body.String(), "uk_passes_pass_no") {
		t.Fatalf("internal detail leaked: %s", recorder.Body.String())
	}
	var body Body
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Detail != "internal server error" {
		t.Errorf("detail = %q", body.Detail)
	}
}

func TestBadFieldNamesTheField(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/api/v1/visits", nil)
	Fail(context.Background(), recorder, request, errs.BadField("host_mobile", "is not a phone number"))

	if recorder.Code != 400 {
		t.Fatalf("status = %d", recorder.Code)
	}
	var body Body
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Fields) != 1 || body.Fields[0].Field != "host_mobile" {
		t.Errorf("fields = %+v", body.Fields)
	}
}
