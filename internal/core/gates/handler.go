package gates

import (
	"net/http"

	"accessnow/internal/platform/httpx"
	"accessnow/internal/platform/tenancy"
)

type Handler struct{ svc *Service }

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// List is the picker: a guard with one assigned gate gets it preselected, one with many
// sees this list, a role that does not work a gate never calls it.
func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	gates, err := h.svc.List(r.Context(), scope)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	views := make([]GateView, 0, len(gates))
	for _, gate := range gates {
		views = append(views, view(gate))
	}
	httpx.OK(w, map[string]any{"items": views})
}

func (h *Handler) Select(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req SelectRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	selection, err := h.svc.Select(r.Context(), scope, req.GateID)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, map[string]any{
		"gate":     view(selection.Gate),
		"shift_id": selection.ShiftID,
	})
}

// Current answers "which gate am I on?" after a refresh, from the session, never from
// the client.
func (h *Handler) Current(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	shift, err := h.svc.Current(r.Context(), scope)
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.OK(w, map[string]any{"shift": viewShift(shift), "gate_id": scope.GateID})
}

func (h *Handler) EndShift(w http.ResponseWriter, r *http.Request) {
	scope, err := tenancy.Require(r.Context())
	if err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	var req EndShiftRequest
	if err := httpx.Decode(r, &req); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	if err := h.svc.EndShift(r.Context(), scope, req.Reason); err != nil {
		httpx.Fail(r.Context(), w, r, err)
		return
	}
	httpx.NoContent(w)
}
