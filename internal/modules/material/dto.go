package material

import (
	"net/http"
	"time"

	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/httpx"
)

const defaultPage = 25

// DeclareRequest is the desk form for one goods pass. `kind` and `return_type` are the
// only two choices the operator makes: they pick the catalogue pass type, and that type
// carries the approval rule and the validity window.
type DeclareRequest struct {
	Kind                 string        `json:"kind" validate:"required,oneof=inward outward"`
	ReturnType           string        `json:"return_type" validate:"required,oneof=rgp nrgp"`
	ItemCategory         string        `json:"item_category" validate:"omitempty,max=64"`
	DeclaredByName       string        `json:"declared_by_name" validate:"required,max=255"`
	DeclaredByStaffID    int64         `json:"declared_by_staff_id" validate:"omitempty,gt=0"`
	CarrierName          string        `json:"carrier_name" validate:"omitempty,max=255"`
	CarrierPhone         string        `json:"carrier_phone" validate:"omitempty,max=32"`
	DriverName           string        `json:"driver_name" validate:"omitempty,max=255"`
	VehiclePlate         string        `json:"vehicle_plate" validate:"omitempty,max=32"`
	WaybillRef           string        `json:"waybill_ref" validate:"omitempty,max=64"`
	InvoiceRef           string        `json:"invoice_ref" validate:"omitempty,max=64"`
	PORef                string        `json:"po_ref" validate:"omitempty,max=64"`
	OriginReference      string        `json:"origin_reference" validate:"omitempty,max=255"`
	DestinationReference string        `json:"destination_reference" validate:"omitempty,max=255"`
	DestinationContact   string        `json:"destination_contact" validate:"omitempty,max=255"`
	SealNo               string        `json:"seal_no" validate:"omitempty,max=64"`
	VerificationRequired *bool         `json:"gate_verification_required"`
	ExpectedReturnAt     *time.Time    `json:"expected_return_at"`
	Purpose              string        `json:"purpose" validate:"omitempty,max=512"`
	Origin               string        `json:"origin" validate:"omitempty,oneof=walk_in pre_registered online api import device renewal"`
	Notes                string        `json:"notes" validate:"omitempty,max=2000"`
	Items                []ItemRequest `json:"items" validate:"required,min=1,max=200,dive"`
}

// ItemRequest is one declared line. The quantity is what the desk says; the gate later
// records what actually moved and the return says what came back.
type ItemRequest struct {
	SKU              string     `json:"sku" validate:"omitempty,max=64"`
	AssetTag         string     `json:"asset_tag" validate:"omitempty,max=64"`
	SerialNo         string     `json:"serial_no" validate:"omitempty,max=128"`
	ItemName         string     `json:"item_name" validate:"required,max=255"`
	Description      string     `json:"description" validate:"omitempty,max=512"`
	Category         string     `json:"category" validate:"omitempty,max=64"`
	Unit             string     `json:"unit" validate:"omitempty,max=16"`
	Quantity         float64    `json:"quantity" validate:"required,gt=0"`
	UnitValue        *float64   `json:"unit_value" validate:"omitempty,gte=0"`
	Currency         string     `json:"currency" validate:"omitempty,len=3"`
	Returnable       bool       `json:"returnable"`
	ExpectedReturnAt *time.Time `json:"expected_return_at"`
	Notes            string     `json:"notes" validate:"omitempty,max=512"`
}

func (req ItemRequest) toItem() NewItem {
	return NewItem{
		SKU: req.SKU, AssetTag: req.AssetTag, SerialNo: req.SerialNo, ItemName: req.ItemName,
		Description: req.Description, Category: req.Category, Unit: req.Unit,
		Quantity: req.Quantity, UnitValue: req.UnitValue, Currency: req.Currency,
		Returnable: req.Returnable, ExpectedReturnAt: req.ExpectedReturnAt, Notes: req.Notes,
	}
}

func (req DeclareRequest) toDeclare() Declare {
	items := make([]NewItem, 0, len(req.Items))
	for _, item := range req.Items {
		items = append(items, item.toItem())
	}
	return Declare{
		Kind: req.Kind, ReturnType: req.ReturnType, ItemCategory: req.ItemCategory,
		DeclaredByName: req.DeclaredByName, DeclaredByStaffID: req.DeclaredByStaffID,
		CarrierName: req.CarrierName, CarrierPhone: req.CarrierPhone,
		DriverName: req.DriverName, VehiclePlate: req.VehiclePlate, WaybillRef: req.WaybillRef,
		InvoiceRef: req.InvoiceRef, PORef: req.PORef, OriginReference: req.OriginReference,
		DestinationReference: req.DestinationReference, DestinationContact: req.DestinationContact,
		SealNo: req.SealNo, VerificationRequired: req.VerificationRequired,
		ExpectedReturnAt: req.ExpectedReturnAt, Purpose: req.Purpose, Origin: req.Origin,
		Notes: req.Notes, Items: items,
	}
}

// DecisionRequest is the approve or reject click. An explicit action beats a boolean a
// client can send as false by accident.
type DecisionRequest struct {
	Action string `json:"action" validate:"required,oneof=approve reject"`
	Note   string `json:"note" validate:"omitempty,max=512"`
}

// DisputeRequest is the one sentence that has to accompany a disputed declaration. 512
// because that is the width of the audit reason the words are filed in.
type DisputeRequest struct {
	Reason string `json:"reason" validate:"required,max=512"`
}

// ReturnRequest is the desk recording what came back, line by line.
type ReturnRequest struct {
	Note  string `json:"note" validate:"omitempty,max=512"`
	Lines []struct {
		ItemID   int64   `json:"item_id" validate:"required,gt=0"`
		Quantity float64 `json:"quantity" validate:"required,gt=0"`
		Note     string  `json:"note" validate:"omitempty,max=512"`
	} `json:"lines" validate:"required,min=1,max=200,dive"`
}

func (req ReturnRequest) toLines() []ReturnLine {
	lines := make([]ReturnLine, 0, len(req.Lines))
	for _, line := range req.Lines {
		lines = append(lines, ReturnLine{ItemID: line.ItemID, Quantity: line.Quantity, Note: line.Note})
	}
	return lines
}

// MaterialView is the register row and the detail card.
type MaterialView struct {
	ID                   int64      `json:"id"`
	PassID               int64      `json:"pass_id"`
	PassNo               string     `json:"pass_no"`
	PassStatus           string     `json:"pass_status"`
	Kind                 string     `json:"kind"`
	ReturnType           string     `json:"return_type"`
	Status               string     `json:"status"`
	ItemCategory         *string    `json:"item_category,omitempty"`
	DeclaredByName       *string    `json:"declared_by_name,omitempty"`
	CarrierName          *string    `json:"carrier_name,omitempty"`
	CarrierPhone         *string    `json:"carrier_phone,omitempty"`
	DriverName           *string    `json:"driver_name,omitempty"`
	VehiclePlate         *string    `json:"vehicle_plate,omitempty"`
	WaybillRef           *string    `json:"waybill_ref,omitempty"`
	InvoiceRef           *string    `json:"invoice_ref,omitempty"`
	PORef                *string    `json:"po_ref,omitempty"`
	DestinationReference *string    `json:"destination_reference,omitempty"`
	SealNo               *string    `json:"seal_no,omitempty"`
	VerificationRequired bool       `json:"gate_verification_required"`
	ExpectedReturnAt     *time.Time `json:"expected_return_at,omitempty"`
	ReleasedAt           *time.Time `json:"released_at,omitempty"`
	ClosedAt             *time.Time `json:"closed_at,omitempty"`
	VerifiedAt           *time.Time `json:"verified_at,omitempty"`
	Notes                *string    `json:"notes,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	Items                []ItemView `json:"items,omitempty"`
}

func viewOf(m Material) MaterialView {
	return MaterialView{
		ID: m.ID, PassID: m.PassID, PassNo: m.PassNo, PassStatus: m.PassStatus,
		Kind: m.Kind, ReturnType: m.ReturnType, Status: m.Status,
		ItemCategory: m.ItemCategory, DeclaredByName: m.DeclaredByName,
		CarrierName: m.CarrierName, CarrierPhone: m.CarrierPhone, DriverName: m.DriverName,
		VehiclePlate: m.VehiclePlate, WaybillRef: m.WaybillRef, InvoiceRef: m.InvoiceRef,
		PORef: m.PORef, DestinationReference: m.DestinationReference, SealNo: m.SealNo,
		VerificationRequired: m.VerificationRequired, ExpectedReturnAt: m.ExpectedReturnAt,
		ReleasedAt: m.ReleasedAt, ClosedAt: m.ClosedAt, VerifiedAt: m.VerifiedAt,
		Notes: m.Notes, CreatedAt: m.CreatedAt,
	}
}

// ItemView carries the three quantities, because the difference between them is the
// entire outstanding report.
type ItemView struct {
	ID               int64      `json:"id"`
	LineNo           int        `json:"line_no"`
	ItemName         string     `json:"item_name"`
	SKU              *string    `json:"sku,omitempty"`
	AssetTag         *string    `json:"asset_tag,omitempty"`
	SerialNo         *string    `json:"serial_no,omitempty"`
	Category         *string    `json:"category,omitempty"`
	Unit             string     `json:"unit"`
	QuantityDeclared float64    `json:"quantity_declared"`
	QuantityIn       float64    `json:"quantity_in"`
	QuantityOut      float64    `json:"quantity_out"`
	QuantityReturned float64    `json:"quantity_returned"`
	Outstanding      float64    `json:"quantity_outstanding"`
	UnitValue        *float64   `json:"unit_value,omitempty"`
	Currency         *string    `json:"currency,omitempty"`
	Returnable       bool       `json:"returnable"`
	ExpectedReturnAt *time.Time `json:"expected_return_at,omitempty"`
	ReturnedAt       *time.Time `json:"returned_at,omitempty"`
	Status           string     `json:"status"`
}

func viewOfItem(item Item) ItemView {
	return ItemView{
		ID: item.ID, LineNo: item.LineNo, ItemName: item.ItemName, SKU: item.SKU,
		AssetTag: item.AssetTag, SerialNo: item.SerialNo, Category: item.Category,
		Unit: item.Unit, QuantityDeclared: item.QuantityDeclared, QuantityIn: item.QuantityIn,
		QuantityOut: item.QuantityOut, QuantityReturned: item.QuantityReturned,
		Outstanding: item.Outstanding(), UnitValue: item.UnitValue, Currency: item.Currency,
		Returnable: item.Returnable, ExpectedReturnAt: item.ExpectedReturnAt,
		ReturnedAt: item.ReturnedAt, Status: item.Status,
	}
}

func viewsOf(items []Item) []ItemView {
	views := make([]ItemView, 0, len(items))
	for _, item := range items {
		views = append(views, viewOfItem(item))
	}
	return views
}

// ParseList reads the register filters: ?kind=&return_type=&status=&search=&limit=&cursor=.
func ParseList(r *http.Request) (ListFilter, httpx.Page, error) {
	query := r.URL.Query()
	filter := ListFilter{
		Kind: query.Get("kind"), ReturnType: query.Get("return_type"),
		Status: query.Get("status"), Search: query.Get("search"),
	}
	if filter.Kind != "" && filter.Kind != KindInward && filter.Kind != KindOutward {
		return filter, httpx.Page{}, errs.BadField("kind", "must be inward or outward")
	}
	if filter.ReturnType != "" && filter.ReturnType != ReturnRGP && filter.ReturnType != ReturnNRGP {
		return filter, httpx.Page{}, errs.BadField("return_type", "must be rgp or nrgp")
	}
	if filter.Status != "" && !validStatus(filter.Status) {
		return filter, httpx.Page{}, errs.BadField("status", filter.Status+" is not a goods pass status")
	}
	if filter.Search != "" && len(filter.Search) > 128 {
		return filter, httpx.Page{}, errs.BadField("search", "too long")
	}
	page, err := httpx.ParsePage(r, defaultPage, 100)
	return filter, page, err
}

func validStatus(status string) bool {
	for _, candidate := range []Status{
		StatusPending, StatusApproved, StatusRejected, StatusReleased,
		StatusPartiallyReturned, StatusReturned, StatusClosed, StatusOverdue, StatusDisputed,
	} {
		if string(candidate) == status {
			return true
		}
	}
	return false
}
