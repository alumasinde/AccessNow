package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"

	"accessnow/internal/platform/errs"
)

const maxBodyBytes = 1 << 20

var validate = newValidator()

func newValidator() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())
	v.RegisterTagNameFunc(func(field reflect.StructField) string {
		name := strings.SplitN(field.Tag.Get("json"), ",", 2)[0]
		if name == "-" {
			return ""
		}
		return name
	})
	return v
}

// Decode reads the body, refuses unknown keys so a typo in a client cannot silently
// drop a field, then validates the DTO.
func Decode(r *http.Request, dst any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return errs.Invalid("request body is required")
		}
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			return errs.Invalid("request body has the wrong type",
				errs.InvalidField(typeErr.Field, "expected "+typeErr.Type.String()))
		}
		if key, unknown := unknownKey(err); unknown {
			return errs.Invalid("request body has a key this endpoint does not take",
				errs.InvalidField(key, "unknown field"))
		}
		return errs.Invalid("request body is not valid JSON")
	}
	return Validate(dst)
}

// unknownKey pulls the offending name out of the decoder's message, which reads
// json: unknown field "username". The name came from the client, so echoing it back is
// safe and turns a guess at the payload shape into a one-line fix.
func unknownKey(err error) (string, bool) {
	text, found := strings.CutPrefix(err.Error(), "json: unknown field ")
	if !found {
		return "", false
	}
	return strings.Trim(text, `"`), true
}

func Validate(dst any) error {
	if err := validate.Struct(dst); err != nil {
		var invalid validator.ValidationErrors
		if errors.As(err, &invalid) {
			fields := make([]errs.Field, 0, len(invalid))
			for _, f := range invalid {
				fields = append(fields, errs.InvalidField(f.Field(), describe(f)))
			}
			return errs.Invalid("request failed validation", fields...)
		}
		return errs.Internal(err)
	}
	return nil
}

func describe(f validator.FieldError) string {
	if f.Tag() == "required" {
		return "is required"
	}
	if f.Param() != "" {
		return f.Tag() + " " + f.Param()
	}
	return f.Tag()
}
