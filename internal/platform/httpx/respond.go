// Package httpx is the thin HTTP layer every handler shares: decode, validate, respond.
package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"

	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/logging"
)

type Body struct {
	Type      string       `json:"type"`
	Title     string       `json:"title"`
	Detail    string       `json:"detail,omitempty"`
	Fields    []errs.Field `json:"fields,omitempty"`
	RequestID string       `json:"request_id,omitempty"`
}

func JSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if value == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(value); err != nil {
		logging.From(context.Background()).Error("encode response", "error", err)
	}
}

func OK(w http.ResponseWriter, value any)      { JSON(w, http.StatusOK, value) }
func Created(w http.ResponseWriter, value any) { JSON(w, http.StatusCreated, value) }
func NoContent(w http.ResponseWriter)          { w.WriteHeader(http.StatusNoContent) }

// Fail turns an error into a response. Only the errs message reaches the client;
// anything else is logged with the request id the client is handed back.
func Fail(ctx context.Context, w http.ResponseWriter, r *http.Request, err error) {
	code, detail, fields, status := translate(err)
	if status == http.StatusInternalServerError {
		logging.From(ctx).ErrorContext(ctx, "request failed", "error", err, "code", string(code))
		detail = "internal server error"
	}
	JSON(w, status, Body{
		Type:      string(code),
		Title:     http.StatusText(status),
		Detail:    detail,
		Fields:    fields,
		RequestID: middleware.GetReqID(r.Context()),
	})
}

func translate(err error) (errs.Code, string, []errs.Field, int) {
	var e *errs.Error
	if !errors.As(err, &e) {
		return errs.CodeInternal, "", nil, http.StatusInternalServerError
	}
	switch e.Code {
	case errs.CodeInvalidRequest:
		return e.Code, e.Message, e.Fields, http.StatusBadRequest
	case errs.CodeUnauthenticated:
		return e.Code, e.Message, nil, http.StatusUnauthorized
	case errs.CodeForbidden, errs.CodeGateUnassigned:
		return e.Code, e.Message, nil, http.StatusForbidden
	case errs.CodeNotFound, errs.CodeModuleDisabled:
		return e.Code, e.Message, nil, http.StatusNotFound
	case errs.CodeConflict:
		return e.Code, e.Message, nil, http.StatusConflict
	case errs.CodeTooManyRequests:
		return e.Code, e.Message, nil, http.StatusTooManyRequests
	default:
		return errs.CodeInternal, e.Message, nil, http.StatusInternalServerError
	}
}
