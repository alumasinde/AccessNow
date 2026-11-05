package httpx

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"accessnow/internal/platform/errs"
)

// ID reads a numeric path parameter. A bad id is the caller's mistake, so it answers 400
// rather than falling through to a 404 that hides the typo.
func ID(r *http.Request, name string) (int64, error) {
	return parseID(chi.URLParam(r, name), name)
}

// QueryID reads a numeric query parameter, ?gate_id= for instance.
func QueryID(r *http.Request, name string) (int64, error) {
	return parseID(r.URL.Query().Get(name), name)
}

func parseID(raw, name string) (int64, error) {
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, errs.BadField(name, "expected a positive id")
	}
	return value, nil
}
