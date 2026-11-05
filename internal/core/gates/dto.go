package gates

import "time"

type Selection struct {
	Gate    Gate
	ShiftID int64
}

type GateView struct {
	ID         int64    `json:"id"`
	SiteID     int64    `json:"site_id"`
	Code       string   `json:"code"`
	Name       string   `json:"name"`
	GateType   string   `json:"gate_type"`
	Direction  string   `json:"direction"`
	Status     string   `json:"status"`
	IsDefault  bool     `json:"is_default"`
	Categories []string `json:"categories,omitempty"`
}

func view(g Gate) GateView {
	return GateView{
		ID: g.ID, SiteID: g.SiteID, Code: g.Code, Name: g.Name, GateType: g.GateType,
		Direction: g.Direction, Status: g.Status, IsDefault: g.IsDefault, Categories: g.Categories,
	}
}

type ShiftView struct {
	ShiftID   int64     `json:"shift_id"`
	GateID    int64     `json:"gate_id"`
	OpenedAt  time.Time `json:"opened_at"`
	ActiveFor string    `json:"active_for"`
}

func viewShift(shift *Shift) *ShiftView {
	if shift == nil {
		return nil
	}
	return &ShiftView{
		ShiftID: shift.ID, GateID: shift.GateID, OpenedAt: shift.OpenedAt,
		ActiveFor: shift.OpenedAt.UTC().Format(time.RFC3339),
	}
}

type SelectRequest struct {
	GateID int64 `json:"gate_id" validate:"required,gt=0"`
}

type EndShiftRequest struct {
	Reason string `json:"reason" validate:"required,oneof=logout switched_gate timeout admin end_of_shift"`
}
