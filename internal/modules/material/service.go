package material

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"

	"accessnow/internal/core/passes"
	"accessnow/internal/platform/audit"
	"accessnow/internal/platform/db"
	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/events"
	"accessnow/internal/platform/tenancy"
)

// Service owns the goods declaration. It writes the detail row and the lines, asks Core
// for the pass, and lets the gate decide when the goods actually moved.
type Service struct {
	pool      *sqlx.DB
	repo      *Repository
	passes    *passes.Service
	passRepo  *passes.Repository
	publisher events.Publisher
	chain     audit.Writer
}

func NewService(pool *sqlx.DB, repo *Repository, passService *passes.Service,
	passRepo *passes.Repository, publisher events.Publisher, chain audit.Writer) *Service {
	return &Service{
		pool: pool, repo: repo, passes: passService, passRepo: passRepo, publisher: publisher,
		chain: chain,
	}
}

// Declare is one goods pass with its lines. kind and return_type pick the pass type, so
// the tenant's own approval rule and validity window come from the catalogue row.
type Declare struct {
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
	VerificationRequired *bool
	ExpectedReturnAt     *time.Time
	Purpose              string
	Origin               string
	Notes                string
	Items                []NewItem
}

// typeKeyFor maps the four seeded goods pass types onto the two flags the desk actually
// asks about. An inward RGP is a personal item: it came in with somebody and must come
// back out again.
func typeKeyFor(kind, returnType string) string {
	switch {
	case kind == KindInward && returnType == ReturnRGP:
		return "material_personal_item"
	case kind == KindInward:
		return "material_inward"
	case returnType == ReturnNRGP:
		return "material_outward_nrgp"
	default:
		return "material_outward_rgp"
	}
}

func (in Declare) validate() error {
	if in.Kind != KindOutward && in.Kind != KindInward {
		return errs.BadField("kind", "must be inward or outward")
	}
	if in.ReturnType != ReturnRGP && in.ReturnType != ReturnNRGP {
		return errs.BadField("return_type", "must be rgp or nrgp")
	}
	if len(in.Items) == 0 {
		return errs.BadField("items", "declare at least one line")
	}
	// An RGP without a date is an item that can never be reported overdue, which is the
	// whole point of the returnable series.
	if in.Kind == KindOutward && in.ReturnType == ReturnRGP && in.ExpectedReturnAt == nil {
		return errs.BadField("expected_return_at", "required on a returnable goods pass")
	}
	for index, item := range in.Items {
		if item.ItemName == "" {
			return errs.BadField("items", "line "+strconv.Itoa(index+1)+" has no item name")
		}
		if item.Quantity <= 0 {
			return errs.BadField("items", "line "+strconv.Itoa(index+1)+" must have a quantity above zero")
		}
	}
	return nil
}

func (s *Service) Declare(ctx context.Context, scope tenancy.Scope, in Declare) (Material, error) {
	if scope.SiteID == 0 {
		return Material{}, errs.Forbidden("no site on this session")
	}
	if err := in.validate(); err != nil {
		return Material{}, err
	}
	passType, err := s.passRepo.TypeByKey(ctx, typeKeyFor(in.Kind, in.ReturnType))
	if err != nil {
		return Material{}, err
	}

	status := StatusApproved
	if passType.RequiresApproval {
		status = StatusPending
	}
	origin := in.Origin
	if origin == "" {
		origin = "walk_in"
	}
	now := time.Now().UTC()
	until := passes.Window(now, passType.DefaultValidityHours)
	verification := true
	if in.VerificationRequired != nil {
		verification = *in.VerificationRequired
	}
	value, currency := declaredValue(in.Items)

	var material Material
	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		pass, err := s.passes.Issue(ctx, c, passes.Issue{
			New: passes.New{
				TenantID: scope.TenantID, SiteID: scope.SiteID, PassTypeID: passType.ID,
				CategoryKey: passType.CategoryKey, SourceModuleKey: ModuleKey,
				Status: passStatus(status), Origin: origin, SubjectName: in.DeclaredByName,
				SubjectPhone: blankToNil(in.CarrierPhone), Purpose: blankToNil(purpose(in)),
				RequesterUserID: num(scope.UserID), ValidFrom: now, ValidUntil: until,
				ValueAmount: value, ValueCurrency: currency,
			},
			FormatKey: numberFormat, Timezone: scope.Timezone, ActorUserID: scope.UserID,
		})
		if err != nil {
			return err
		}

		detail := in.NewMaterial(scope.TenantID, pass.ID, status, verification)
		materialID, err := repo.CreateMaterial(ctx, detail)
		if err != nil {
			return err
		}
		if err := repo.InsertItems(ctx, scope.TenantID, materialID, scope.UserID, in.Items); err != nil {
			return err
		}

		payload := map[string]any{
			"pass_no": pass.PassNo, "kind": in.Kind, "return_type": in.ReturnType,
			"status": string(status), "lines": len(in.Items),
			"declared_by": in.DeclaredByName,
		}
		if err := s.publisher.Publish(ctx, c, event(scope, "material.declared", materialID, pass.ID, payload)); err != nil {
			return err
		}
		if status == StatusPending {
			if err := s.publisher.Publish(ctx, c, event(scope, "material.approval_needed", materialID, pass.ID, payload)); err != nil {
				return err
			}
		}
		material, err = repo.MaterialByID(ctx, scope.TenantID, materialID)
		return err
	})
	if err != nil {
		return Material{}, err
	}
	return material, nil
}

// NewMaterial turns the declaration plus the pass Core just issued into the detail row.
func (in Declare) NewMaterial(tenantID, passID int64, status Status, verification bool) NewMaterial {
	return NewMaterial{
		TenantID: tenantID, PassID: passID, Kind: in.Kind, ReturnType: in.ReturnType,
		ItemCategory: in.ItemCategory, DeclaredByName: in.DeclaredByName,
		DeclaredByStaffID: in.DeclaredByStaffID, CarrierName: in.CarrierName,
		CarrierPhone: in.CarrierPhone, DriverName: in.DriverName, VehiclePlate: in.VehiclePlate,
		WaybillRef: in.WaybillRef, InvoiceRef: in.InvoiceRef, PORef: in.PORef,
		OriginReference: in.OriginReference, DestinationReference: in.DestinationReference,
		DestinationContact: in.DestinationContact, SealNo: in.SealNo,
		VerificationRequired: verification, ExpectedReturnAt: in.ExpectedReturnAt,
		Status: status, Notes: in.Notes,
	}
}

// Decide answers a pending outward declaration. Core owns the pass and the goods row
// mirrors it, so a crash between the two leaves a decided pass the desk can settle again.
func (s *Service) Decide(ctx context.Context, scope tenancy.Scope, materialID int64, approve bool, note string) (Material, error) {
	material, err := s.repo.MaterialByID(ctx, scope.TenantID, materialID)
	if err != nil {
		return Material{}, err
	}
	if material.Status != string(StatusPending) {
		return Material{}, errs.Conflict("goods pass " + material.PassNo + " is already " + material.Status)
	}

	to, mirror := passes.StatusApproved, StatusApproved
	if !approve {
		to, mirror = passes.StatusRejected, StatusRejected
	}
	if _, err := s.passes.Decide(ctx, scope, material.PassID, to, note); err != nil {
		return Material{}, err
	}
	if err := s.repo.MarkStatus(ctx, scope.TenantID, materialID, StatusPending, mirror); err != nil {
		return Material{}, err
	}
	return s.repo.MaterialByID(ctx, scope.TenantID, materialID)
}

// ReturnLine is one line coming back: the gate counted it, the desk is writing it down.
type ReturnLine struct {
	ItemID   int64
	Quantity float64
	Note     string
}

// Return records what came back against an outward declaration. It is the desk half of the
// story; a gate return scan closes every outstanding line at once through OnMovement.
func (s *Service) Return(ctx context.Context, scope tenancy.Scope, materialID int64, lines []ReturnLine, note string) (Material, error) {
	material, err := s.repo.MaterialByID(ctx, scope.TenantID, materialID)
	if err != nil {
		return Material{}, err
	}
	if !material.Outward() {
		return Material{}, errs.Conflict("an inward declaration does not return anything")
	}
	if material.Status != string(StatusReleased) && material.Status != string(StatusPartiallyReturned) {
		return Material{}, errs.Conflict("goods pass " + material.PassNo + " is " + material.Status + ", not released")
	}
	if len(lines) == 0 {
		return Material{}, errs.BadField("lines", "record at least one returned line")
	}

	items, err := s.repo.Items(ctx, scope.TenantID, materialID)
	if err != nil {
		return Material{}, err
	}
	byID := make(map[int64]Item, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	for _, line := range lines {
		item, ok := byID[line.ItemID]
		if !ok {
			return Material{}, errs.BadField("item_id", "line does not belong to this goods pass")
		}
		if line.Quantity <= 0 {
			return Material{}, errs.BadField("quantity", "must be above zero")
		}
		if line.Quantity > item.Outstanding() {
			return Material{}, errs.BadField("quantity", "only "+
				strconv.FormatFloat(item.Outstanding(), 'f', -1, 64)+" of "+item.ItemName+" is outstanding")
		}
	}

	err = db.Transact(ctx, s.pool, func(c db.Conn) error {
		repo := s.repo.In(c)
		for _, line := range lines {
			if err := repo.ReturnLine(ctx, scope.TenantID, line.ItemID, line.Quantity); err != nil {
				return err
			}
			item := byID[line.ItemID]
			kind := "partial_return"
			if line.Quantity >= item.Outstanding() {
				kind = "returned"
			}
			if err := repo.RecordLineEvent(ctx, scope.TenantID, materialID, line.ItemID, kind,
				line.Quantity, scope.GateID, 0, scope.UserID); err != nil {
				return err
			}
		}
		return repo.RefreshReturnStatus(ctx, scope.TenantID, materialID)
	})
	if err != nil {
		return Material{}, err
	}
	updated, err := s.repo.MaterialByID(ctx, scope.TenantID, materialID)
	if err != nil {
		return Material{}, err
	}
	// The desk is the only one who knows a partial return finished the job, so this is
	// where a goods pass stops being a live document.
	return updated, s.finish(ctx, scope, materialID)
}

// declaredValue is what the whole load is worth: every priced line counted at the quantity
// the desk gave it. Nothing priced is a nil, not a zero - a flow that adds a security
// manager above a threshold must not fire for goods nobody put a price on. The currency is
// the first one declared, because one lorry invoiced in two currencies is two lorries.
func declaredValue(items []NewItem) (*float64, string) {
	total := 0.0
	currency := ""
	priced := false
	for _, item := range items {
		if item.UnitValue == nil {
			continue
		}
		total += *item.UnitValue * item.Quantity
		priced = true
		if currency == "" {
			currency = strings.ToUpper(strings.TrimSpace(item.Currency))
		}
	}
	if !priced {
		return nil, ""
	}
	return &total, currency
}

func num(value int64) *int64 {
	if value == 0 {
		return nil
	}
	return &value
}

func purpose(in Declare) string {
	if in.Purpose != "" {
		return in.Purpose
	}
	for _, candidate := range []string{in.DestinationReference, in.WaybillRef, in.ItemCategory} {
		if candidate != "" {
			return candidate
		}
	}
	return "goods declaration"
}

func passStatus(status Status) passes.Status {
	if status == StatusPending {
		return passes.StatusPending
	}
	return passes.StatusApproved
}

func event(scope tenancy.Scope, key string, materialID, passID int64, payload map[string]any) events.Event {
	return events.Event{
		TenantID: scope.TenantID, EventKey: key, AggregateType: "material_pass",
		AggregateID: materialID, PassID: passID, SiteID: scope.SiteID, GateID: scope.GateID,
		ActorUserID: scope.UserID, Payload: payload,
	}
}

func blankToNil(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
