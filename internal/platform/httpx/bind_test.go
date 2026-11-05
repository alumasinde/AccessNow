package httpx

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"accessnow/internal/platform/errs"
)

type createThing struct {
	Name   string `json:"name" validate:"required,min=3"`
	Count  int    `json:"count" validate:"omitempty,min=1"`
	Active bool   `json:"active"`
}

func decodeThing(body string) error {
	request := httptest.NewRequest("POST", "/api/v1/things", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	var dst createThing
	return Decode(request, &dst)
}

func fieldErrors(t *testing.T, err error) map[string]string {
	t.Helper()
	var e *errs.Error
	if !errors.As(err, &e) {
		t.Fatalf("err %v is not an *errs.Error", err)
	}
	got := make(map[string]string, len(e.Fields))
	for _, f := range e.Fields {
		got[f.Field] = f.Reason
	}
	return got
}

func TestDecodeAcceptsGoodBody(t *testing.T) {
	if err := decodeThing(`{"name":"Kodi POS","count":2,"active":true}`); err != nil {
		t.Fatalf("Decode: %v", err)
	}
}

// A misspelled key must not be dropped silently — that is how a client loses a field.
// The name is worth returning: the client sent it, so it can only confuse its own author.
func TestDecodeRejectsUnknownField(t *testing.T) {
	err := decodeThing(`{"name":"Kodi","activ":true}`)
	if errs.CodeOf(err) != errs.CodeInvalidRequest {
		t.Fatalf("unknown field = %v, want invalid_request", err)
	}
	if got := fieldErrors(t, err); got["activ"] != "unknown field" {
		t.Errorf("field errors = %v, want activ to be named", got)
	}
}

func TestDecodeRejectsMalformedBody(t *testing.T) {
	err := decodeThing(`{"name":`)
	if errs.CodeOf(err) != errs.CodeInvalidRequest {
		t.Fatalf("malformed body = %v, want invalid_request", err)
	}
	if !strings.Contains(err.Error(), "not valid JSON") {
		t.Errorf("message = %q", err)
	}
}

func TestDecodeReportsValidationPerField(t *testing.T) {
	got := fieldErrors(t, decodeThing(`{"name":"ki","count":-1}`))
	if got["name"] != "min 3" {
		t.Errorf("name reason = %q, want %q", got["name"], "min 3")
	}
	if got["count"] != "min 1" {
		t.Errorf("count reason = %q, want %q", got["count"], "min 1")
	}
}

// omitempty lets a client leave an optional field out instead of having to send 0.
func TestDecodeSkipsEmptyOptionalFields(t *testing.T) {
	if err := decodeThing(`{"name":"Kodi POS"}`); err != nil {
		t.Fatalf("omitted optional field: %v", err)
	}
}

func TestDecodeRejectsWrongType(t *testing.T) {
	err := decodeThing(`{"name":12}`)
	if !strings.Contains(err.Error(), "wrong type") {
		t.Errorf("wrong type = %v", err)
	}
	if fieldErrors(t, err)["name"] == "" {
		t.Error("type error named no field")
	}
}

func TestDecodeRejectsEmptyBody(t *testing.T) {
	if errs.CodeOf(decodeThing("")) != errs.CodeInvalidRequest {
		t.Error("empty body accepted")
	}
}
