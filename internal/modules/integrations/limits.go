package integrations

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/httpx"
	"accessnow/internal/platform/logging"
)

// defaultKeyRateLimit applies when a key did not set its own. Machines poll a register; they
// should not be able to make it look like a scrape while doing so.
const defaultKeyRateLimit = 600

// rateLimiter spends one request from a credential's budget. The production implementation keeps
// the window in the database, because the budget belongs to the key and not to whichever API
// process answered this time.
type rateLimiter interface {
	allow(ctx context.Context, keyID int64, limit int, now time.Time) (bool, time.Duration, error)
}

// RateLimit spends one request from the credential's own budget. It runs after RequireKey, so
// there is always a key to charge. Over budget is a 429 with Retry-After, not a 403: the
// credential is valid, it is asking too fast.
func (h *Handler) RateLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key, ok := keyFrom(r.Context())
		if !ok || h.limits == nil {
			next.ServeHTTP(w, r)
			return
		}
		limit := defaultKeyRateLimit
		if key.RateLimit != nil && *key.RateLimit > 0 {
			limit = *key.RateLimit
		}
		allowed, wait, err := h.limits.allow(r.Context(), key.ID, limit, h.now())
		if err != nil {
			// The limiter is abuse control, not authorisation. A database that cannot answer
			// must not take the machine surface down with it, so this request is let through
			// and the failure is left where an operator will see it.
			logging.From(r.Context()).WarnContext(r.Context(),
				"api key rate limit check failed", "key_id", key.ID, "error", err)
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
		if !allowed {
			seconds := int(wait.Seconds()) + 1
			w.Header().Set("Retry-After", strconv.Itoa(seconds))
			httpx.Fail(r.Context(), w, r, errs.New(errs.CodeTooManyRequests,
				"api key rate limit exceeded; retry after "+strconv.Itoa(seconds)+"s"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
