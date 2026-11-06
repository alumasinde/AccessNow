package movements

import (
	"net/http"

	"accessnow/internal/platform/httpx"
	"accessnow/internal/platform/tenancy"
)

const (
	rosterPage = 50
	rosterMax  = 200
)

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Scan answers the one question a gate asks, in one round trip.
func (h *Handler) Scan(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req ScanRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	outcome, err := h.svc.Scan(r.Context(), scope, req.toScan())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, outcome.View())
}

// OnSite is the roster at this gate, or the whole site when the device carries no gate.
func (h *Handler) OnSite(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	page, err := httpx.ParsePage(r, rosterPage, rosterMax)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	gateID := scope.GateID
	if r.URL.Query().Get("gate_id") != "" {
		gateID, err = httpx.QueryID(r, "gate_id")
		if err != nil {
			httpx.Fail(r.Context(), w, r, err)
			return
		}
	}
	roster, err := h.svc.OnSite(r.Context(), scope, gateID, page.Limit)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, map[string]any{"items": roster, "count": len(roster)})
}

// Sync takes a tablet's queue after signal comes back. The response is per item, and the
// request already succeeded once the rows are on the server.
func (h *Handler) Sync(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req SyncRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}

	summary := h.svc.Sync(r.Context(), scope, req.toQueue())
	views := make([]SyncedView, 0, len(summary.Items))
	for _, result := range summary.Items {
		view := SyncedView{Index: result.Index, MovementID: result.MovementID,
			Reason: result.Reason, Duplicate: result.Duplicate}
		if result.Result != "" {
			view.Result = string(result.Result)
		}
		if result.Err != nil {
			view.Error = result.Err.Error()
		}
		views = append(views, view)
	}
	httpx.OK(w, map[string]any{
		"batch_id": summary.BatchID, "status": summary.Status, "items": views,
	})
}
