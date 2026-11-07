package movements

import "time"

// ScanRequest is the body a tablet posts. It carries no gate, no tenant, no guard: those
// come from the device token and the session, so a stolen tablet cannot log at a
// gate it was never bolted to.
type ScanRequest struct {
	PassNo string `json:"pass_no" validate:"omitempty,max=64"`
	PassID int64  `json:"pass_id" validate:"omitempty,gt=0"`
	// Token is what a reader produced. It is the pass, for a device that can read one, and it
	// is hashed and matched by the scanning module rather than here.
	Token string `json:"token" validate:"omitempty,max=256"`
	// Input is which reader spoke. Empty means a QR code, because that is what a tablet has.
	Input          string     `json:"input" validate:"omitempty,oneof=qr barcode rfid nfc plate manual face other"`
	Direction      string     `json:"direction" validate:"required,oneof=in out"`
	Source         string     `json:"source" validate:"omitempty,oneof=scan manual api camera offline_sync"`
	Reason         string     `json:"reason" validate:"omitempty,max=512"`
	IdempotencyKey string     `json:"idempotency_key" validate:"omitempty,max=64"`
	SubjectName    string     `json:"subject_name" validate:"omitempty,max=255"`
	Plate          string     `json:"plate" validate:"omitempty,max=32"`
	OccurredAt     *time.Time `json:"occurred_at"`
	// DeviceUUID is the tablet's own name for the movement it made. A queue that is replayed
	// has to be argued about row by row, and the argument needs a name both sides have. It
	// defaults to the idempotency key, because a sensible device uses one uuid per movement
	// and there is no reason to make it mint two.
	DeviceUUID string `json:"device_uuid" validate:"omitempty,max=36"`
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
		PassID: req.PassID, PassNo: req.PassNo, Token: req.Token, Input: req.Input,
		Direction: Direction(req.Direction),
		Source:    Source(req.Source), Reason: req.Reason, IdempotencyKey: req.IdempotencyKey,
		SubjectName: req.SubjectName, Plate: req.Plate, OccurredAt: at,
		DeviceUUID: firstOf(req.DeviceUUID, req.IdempotencyKey), Override: req.Override,
	}
}

// SyncRequest is one offline batch. The device keeps its own queue and sends what it
// cannot deliver; 200 rows at a time is a tablet's worth of a shift, not a data load.
//
// batch_id is required because it is the device's name for the batch, and the only thing
// that makes "the upload cut out halfway, so send it again" safe at the ledger as well as at
// the movement rows: a queue that arrives twice under one id is one batch, not two.
type SyncRequest struct {
	BatchID string        `json:"batch_id" validate:"required,max=64"`
	Items   []ScanRequest `json:"items" validate:"required,min=1,max=200,dive"`
}

func (req SyncRequest) toQueue() Queue {
	scans := make([]Scan, 0, len(req.Items))
	for _, item := range req.Items {
		scans = append(scans, item.toScan())
	}
	return Queue{BatchID: req.BatchID, Scans: scans}
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
	// Duplicate tells the tablet its own row was already on file, so it can drop it from the
	// queue instead of sending it again on the next boot.
	Duplicate bool   `json:"duplicate,omitempty"`
	Error     string `json:"error,omitempty"`
}
