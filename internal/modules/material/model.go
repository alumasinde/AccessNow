// Package material is the goods slice: inward and outward declarations, returnable and
// non-returnable, hung off the same Core pass every other module uses. Release and return
// are gate events, so a lorry that never touched a desk still has a correct paper trail.
package material

import "time"

// ModuleKey is the catalogue row this module switches on and off with.
const ModuleKey = "material_logistics"

// numberFormat is the tenant's goods series. It is the one gap_required format in the
// starter set, because an auditor has to be able to count the goods passes with no holes.
const numberFormat = "material"

// Status mirrors chk_material_passes_status.
type Status string

const (
	StatusPending           Status = "pending"
	StatusApproved          Status = "approved"
	StatusRejected          Status = "rejected"
	StatusReleased          Status = "released"
	StatusPartiallyReturned Status = "partially_returned"
	StatusReturned          Status = "returned"
	StatusClosed            Status = "closed"
	StatusOverdue           Status = "overdue"
	StatusDisputed          Status = "disputed"
)

// Kind and ReturnType mirror their CHECKs. Outward is goods leaving the site, inward is
// goods or luggage arriving with somebody; RGP means a line is expected back.
const (
	KindOutward = "outward"
	KindInward  = "inward"
	ReturnRGP   = "rgp"
	ReturnNRGP  = "nrgp"
)

// Item status mirrors chk_material_items_status.
const (
	ItemPending  = "pending"
	ItemDeclared = "declared"
	ItemReleased = "released"
	ItemPartial  = "partially_returned"
	ItemOverdue  = "overdue"
	ItemReturned = "returned"
	ItemWaived   = "waived"
	ItemLost     = "lost"
)

// Material is one goods pass plus the Core row it hangs off.
type Material struct {
	ID                   int64      `db:"id"`
	TenantID             int64      `db:"tenant_id"`
	PassID               int64      `db:"pass_id"`
	PassNo               string     `db:"pass_no"`
	PassStatus           string     `db:"pass_status"`
	LineCount            int        `db:"line_count"`
	Kind                 string     `db:"kind"`
	ReturnType           string     `db:"return_type"`
	ItemCategory         *string    `db:"item_category"`
	DeclaredByName       *string    `db:"declared_by_name"`
	DeclaredByStaffID    *int64     `db:"declared_by_staff_id"`
	CarrierName          *string    `db:"carrier_name"`
	CarrierPhone         *string    `db:"carrier_phone"`
	DriverName           *string    `db:"driver_name"`
	VehiclePlate         *string    `db:"vehicle_plate"`
	WaybillRef           *string    `db:"waybill_ref"`
	InvoiceRef           *string    `db:"invoice_ref"`
	PORef                *string    `db:"po_ref"`
	OriginReference      *string    `db:"origin_reference"`
	DestinationReference *string    `db:"destination_reference"`
	DestinationContact   *string    `db:"destination_contact"`
	VerificationRequired bool       `db:"gate_verification_required"`
	SealNo               *string    `db:"seal_no"`
	ExpectedReturnAt     *time.Time `db:"expected_return_at"`
	ReleasedAt           *time.Time `db:"released_at"`
	ReleasedGateID       *int64     `db:"released_gate_id"`
	ClosedAt             *time.Time `db:"closed_at"`
	VerifiedAt           *time.Time `db:"verified_at"`
	Status               string     `db:"status"`
	Notes                *string    `db:"notes"`
	CreatedAt            time.Time  `db:"created_at"`
}

// Outward reports whether this declaration takes goods off the site.
func (m Material) Outward() bool    { return m.Kind == KindOutward }
func (m Material) Returnable() bool { return m.ReturnType == ReturnRGP }

// Closed reports the one status per declaration shape where the goods have done their job
// and Core may stop honouring the pass at a barrier. A personal item is the trap in this
// table: it arrives with somebody, so receiving it is not the end, and closing the pass
// then would turn its exit red.
func (m Material) Closed() bool {
	switch {
	case m.Kind == KindOutward && m.ReturnType == ReturnRGP:
		return m.Status == string(StatusReturned) || m.Status == string(StatusClosed)
	case m.Kind == KindOutward:
		return m.Status == string(StatusReleased) || m.Status == string(StatusClosed)
	case m.Kind == KindInward && m.ReturnType == ReturnRGP:
		return m.Status == string(StatusClosed)
	default:
		return m.Status == string(StatusReleased) || m.Status == string(StatusClosed)
	}
}

// Item is one declared line. The three quantities are the audit trail: what was said at
// the desk, what actually crossed the barrier, and what came back.
type Item struct {
	ID               int64      `db:"id"`
	MaterialID       int64      `db:"material_pass_id"`
	LineNo           int        `db:"line_no"`
	SKU              *string    `db:"sku"`
	AssetTag         *string    `db:"asset_tag"`
	SerialNo         *string    `db:"serial_no"`
	ItemName         string     `db:"item_name"`
	Description      *string    `db:"description"`
	Category         *string    `db:"category"`
	Unit             string     `db:"unit"`
	QuantityDeclared float64    `db:"quantity_declared"`
	QuantityIn       float64    `db:"quantity_in"`
	QuantityOut      float64    `db:"quantity_out"`
	QuantityReturned float64    `db:"quantity_returned"`
	UnitValue        *float64   `db:"unit_value"`
	LineValue        *float64   `db:"line_value"`
	Currency         *string    `db:"currency"`
	Returnable       bool       `db:"returnable"`
	ExpectedReturnAt *time.Time `db:"expected_return_at"`
	ReturnedAt       *time.Time `db:"returned_at"`
	OverdueSince     *time.Time `db:"overdue_since"`
	Status           string     `db:"status"`
	Notes            *string    `db:"notes"`
}

// Outstanding reports the part of a released line that has not come back.
func (i Item) Outstanding() float64 {
	if i.QuantityOut > i.QuantityReturned {
		return i.QuantityOut - i.QuantityReturned
	}
	return 0
}

// NewMaterial is the detail row, already tied to the pass Core just issued.
type NewMaterial struct {
	TenantID             int64
	PassID               int64
	Kind                 string
	ReturnType           string
	ItemCategory         string
	DeclaredByName       string
	DeclaredByStaffID    int64
	CarrierName          string
	CarrierPhone         string
	DriverName           string
	VehiclePlate         string
	WaybillRef           string
	InvoiceRef           string
	PORef                string
	OriginReference      string
	DestinationReference string
	DestinationContact   string
	SealNo               string
	VerificationRequired bool
	ExpectedReturnAt     *time.Time
	Status               Status
	Notes                string
}

// NewItem is one declared line as the desk typed it.
type NewItem struct {
	SKU              string
	AssetTag         string
	SerialNo         string
	ItemName         string
	Description      string
	Category         string
	Unit             string
	Quantity         float64
	UnitValue        *float64
	Currency         string
	Returnable       bool
	ExpectedReturnAt *time.Time
	Notes            string
}

// OutstandingRow is one line still owed to the site, straight off the view.
type OutstandingRow struct {
	MaterialID       int64      `db:"material_pass_id" json:"material_pass_id"`
	PassID           int64      `db:"pass_id" json:"pass_id"`
	PassNo           string     `db:"pass_no" json:"pass_no"`
	ItemID           int64      `db:"material_item_id" json:"material_item_id"`
	ItemName         string     `db:"item_name" json:"item_name"`
	SerialNo         *string    `db:"serial_no" json:"serial_no,omitempty"`
	QuantityOut      float64    `db:"quantity_outstanding" json:"quantity_outstanding"`
	ExpectedReturnAt *time.Time `db:"expected_return_at" json:"expected_return_at,omitempty"`
	OverdueSince     *time.Time `db:"overdue_since" json:"overdue_since,omitempty"`
	Status           string     `db:"status" json:"status"`
	DeclaredByName   *string    `db:"declared_by_name" json:"declared_by_name,omitempty"`
}
