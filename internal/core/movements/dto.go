package movements

import "time"

// ScanRequest is the body a tablet posts. It carries no gate, no tenant, no guard: those
// come from the device token and the session, so a stolen tablet cannot log at a
// gate it was never bolted to.
type ScanRequest struct {
	PassNo         string     `json:"pass_no" validate:"omitempty,max=64"`
	PassID         int64      `json:"pass_id" validate:"omitempty,gt=0"`
	Direction      string     `json:"direction" validate:"required,oneof=in out"`
	Source         string     `json:"source" validate:"omitempty,oneof=scan manual api camera offline_sync"`
	Reason         string     `json:"reason" validate:"omitempty,max=512"`
	IdempotencyKey string     `json:"idempotency_key" validate:"omitempty,max=64"`
	SubjectName    string     `json:"subject_name" validate:"omitempty,max=255"`
	Plate          string     `json:"plate" validate:"omitempty,max=32"`
	OccurredAt     *time.Time `json:"occurred_at"`
	// Override asks for a rulebook refusal to be waived. The reason field is the sentence it
	// has to carry, and the permission check happens in the rules module, which is the only
	// place that knows both the rule and who is allowed to overrule it.
	Override bool `json:"override"`
}

func (req ScanRequest) toScan() Scan {
	var at time.Time
	if req.OccurredAt != nil {
		at = *req.OccurredAt
	}
	return Scan{
		PassID: req.PassID, PassNo: req.PassNo, Direction: Direction(req.Direction),
		Source: Source(req.Source), Reason: req.Reason, IdempotencyKey: req.IdempotencyKey,
		SubjectName: req.SubjectName, Plate: req.Plate, OccurredAt: at, Override: req.Override,
	}
}

// SyncRequest is one offline batch. The device keeps its own queue and sends what it
// cannot deliver; 200 rows at a time is a tablet's worth of a shift, not a data load.
type SyncRequest struct {
	Items []ScanRequest `json:"items" validate:"required,min=1,max=200,dive"`
}

// OutcomeView is the green-or-red answer. Colour is in the payload because the tablet
// must not re-derive it from a result string it has to keep in step with.
type OutcomeView struct {
	MovementID   int64     `json:"movement_id"`
	UUID         string    `json:"uuid"`
	Colour       string    `json:"colour"`
	Result       string    `json:"result"`
	Reason       string    `json:"reason,omitempty"`
	Direction    string    `json:"direction"`
	PassNo       string    `json:"pass_no"`
	PassStatus   string    `json:"pass_status"`
	SubjectName  *string   `json:"subject_name,omitempty"`
	Plate        *string   `json:"plate,omitempty"`
	DwellMinutes *int      `json:"dwell_minutes,omitempty"`
	OccurredAt   time.Time `json:"occurred_at"`
}

func (o Outcome) View() OutcomeView {
	colour := "red"
	if o.Green() {
		colour = "green"
	}
	return OutcomeView{
		MovementID: o.Movement.ID, UUID: o.Movement.UUID, Colour: colour,
		Result: string(o.Result), Reason: o.Reason, Direction: o.Movement.Direction,
		PassNo: o.PassNo, PassStatus: o.Status, SubjectName: o.Movement.SubjectName,
		Plate: o.Movement.Plate, DwellMinutes: o.Movement.DwellMinutes,
		OccurredAt: o.Movement.OccurredAt,
	}
}

// SyncedView answers for one queued scan, by the index the device sent it at.
type SyncedView struct {
	Index      int    `json:"index"`
	Result     string `json:"result,omitempty"`
	Reason     string `json:"reason,omitempty"`
	MovementID int64  `json:"movement_id,omitempty"`
	Error      string `json:"error,omitempty"`
}
