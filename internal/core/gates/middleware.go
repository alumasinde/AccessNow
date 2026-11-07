package gates

import (
	"log/slog"
	"net/http"
	"strings"

	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/httpx"
	"accessnow/internal/platform/logging"
	"accessnow/internal/platform/tenancy"
)

const deviceHeader = "X-Device-Token"

// DeviceMiddleware is the gate surface's answer to "which gate is this?". A registered
// tablet decides it, so a guard cannot log a movement at the wrong gate by picking badly.
type DeviceMiddleware struct{ svc *Service }

func NewDeviceMiddleware(svc *Service) *DeviceMiddleware { return &DeviceMiddleware{svc: svc} }

// ActiveGateMiddleware revalidates the gate the session already carries. It sits immediately
// inside session resolution, so every authenticated request - not just a scan - notices that
// the gate was disabled or the guard was taken off it.
type ActiveGateMiddleware struct{ svc *Service }

func NewActiveGateMiddleware(svc *Service) *ActiveGateMiddleware {
	return &ActiveGateMiddleware{svc: svc}
}

func (m *ActiveGateMiddleware) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A guard whose gate was disabled must still be able to reach the picker, end the
		// shift, or sign out. Those are the exits from a bad session; blocking them would turn
		// a disabled gate into a locked console.
		path := r.URL.Path
		if strings.Contains(path, "/auth/") || strings.Contains(path, "/gates") {
			next.ServeHTTP(w, r)
			return
		}
		scope, err := tenancy.Require(r.Context())
		if err != nil {
			httpx.Fail(r.Context(), w, r, err)
			return
		}
		if err := m.svc.RevalidateActive(r.Context(), scope); err != nil {
			httpx.Fail(r.Context(), w, r, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (m *DeviceMiddleware) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get(deviceHeader)
		if token == "" {
			httpx.Fail(r.Context(), w, r, errs.Unauthenticated("device token is required on the gate surface"))
			return
		}
		scope, err := tenancy.Require(r.Context())
		if err != nil {
			httpx.Fail(r.Context(), w, r, err)
			return
		}
		device, err := m.svc.Device(r.Context(), token)
		if err != nil {
			httpx.Fail(r.Context(), w, r, err)
			return
		}
		scope, err = m.svc.AttachDevice(r.Context(), scope, device)
		if err != nil {
			httpx.Fail(r.Context(), w, r, err)
			return
		}

		ctx := tenancy.Put(r.Context(), scope)
		ctx = logging.With(ctx, logging.From(ctx).With(logging.Gate(scope.GateID), slog.Int64("device_id", device.ID)))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
