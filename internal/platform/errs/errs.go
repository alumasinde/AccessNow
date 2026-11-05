package errs

import (
	"errors"
	"fmt"
)

type Code string

const (
	CodeInvalidRequest  Code = "invalid_request"
	CodeUnauthenticated Code = "unauthenticated"
	CodeForbidden       Code = "forbidden"
	CodeNotFound        Code = "not_found"
	CodeConflict        Code = "conflict"
	CodeModuleDisabled  Code = "module_disabled"
	CodeGateUnassigned  Code = "gate_unassigned"
	CodeTooManyRequests Code = "too_many_requests"
	CodeInternal        Code = "internal"
)

type Field struct {
	Field  string `json:"field"`
	Reason string `json:"reason"`
}

type Error struct {
	Code    Code
	Message string
	Fields  []Field
	Err     error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Err }

func New(code Code, message string) *Error { return &Error{Code: code, Message: message} }

func Wrap(code Code, message string, err error) *Error {
	return &Error{Code: code, Message: message, Err: err}
}

func Invalid(message string, fields ...Field) *Error {
	return &Error{Code: CodeInvalidRequest, Message: message, Fields: fields}
}

func InvalidField(field, reason string) Field { return Field{Field: field, Reason: reason} }

// BadField is the common case: one field, one reason, no bespoke sentence to write.
func BadField(field, reason string) *Error {
	return Invalid("field "+field+" is not acceptable", InvalidField(field, reason))
}

func Unauthenticated(message string) *Error { return New(CodeUnauthenticated, message) }

func Forbidden(message string) *Error { return New(CodeForbidden, message) }

func NotFound(what string) *Error { return New(CodeNotFound, what+" not found") }

func Conflict(message string) *Error { return New(CodeConflict, message) }

func ModuleDisabled(moduleKey string) *Error {
	return New(CodeModuleDisabled, "module "+moduleKey+" is not enabled for this tenant")
}

func GateUnassigned(gateID int64) *Error {
	return New(CodeGateUnassigned, fmt.Sprintf("gate %d is not assigned to this user", gateID))
}

// Internal keeps the cause in the server logs and out of the response body.
func Internal(err error) *Error {
	return &Error{Code: CodeInternal, Message: "internal server error", Err: err}
}

func CodeOf(err error) Code {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return CodeInternal
}
