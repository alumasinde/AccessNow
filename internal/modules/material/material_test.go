package material

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/events"
	"accessnow/internal/platform/httpx"
)

// The four seeded goods pass types are the whole catalogue for this module, so the
// mapping from the two flags the desk picks has to stay pinned: a wrong key is a
// declaration that silently draws the wrong approval rule.
func TestTypeKeyFor(t *testing.T) {
	cases := []struct {
		kind, returnType, want string
	}{
		{KindOutward, ReturnRGP, "material_outward_rgp"},
		{KindOutward, ReturnNRGP, "material_outward_nrgp"},
		{KindInward, ReturnNRGP, "material_inward"},
		// A thing that comes in and must go back out again is a personal item.
		{KindInward, ReturnRGP, "material_personal_item"},
	}
	for _, tc := range cases {
		if got := typeKeyFor(tc.kind, tc.returnType); got != tc.want {
			t.Errorf("typeKeyFor(%q, %q) = %q, want %q", tc.kind, tc.returnType, got, tc.want)
		}
	}
}

func TestDeclareValidation(t *testing.T) {
	date := time.Now().UTC().Add(24 * time.Hour)
	good := Declare{
		Kind: KindOutward, ReturnType: ReturnRGP, DeclaredByName: "Amina Njoroge",
		ExpectedReturnAt: &date, Items: []NewItem{{ItemName: "Drill", Quantity: 2}},
	}
	if err := good.validate(); err != nil {
		t.Fatalf("a complete declaration was refused: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*Declare)
	}{
		{"no lines", func(d *Declare) { d.Items = nil }},
		{"unknown kind", func(d *Declare) { d.Kind = "sideways" }},
		{"unknown return type", func(d *Declare) { d.ReturnType = "maybe" }},
		{"returnable without a date", func(d *Declare) { d.ExpectedReturnAt = nil }},
		{"line without a name", func(d *Declare) { d.Items[0].ItemName = "" }},
		{"line with no quantity", func(d *Declare) { d.Items[0].Quantity = 0 }},
	}
	for _, tc := range cases {
		declaration := good
		declaration.Items = append([]NewItem(nil), good.Items...)
		tc.mutate(&declaration)
		if err := declaration.validate(); errs.CodeOf(err) != errs.CodeInvalidRequest {
			t.Errorf("%s: got %v, want invalid_request", tc.name, err)
		}
	}

	// An inward declaration never has a return date: nothing is owed anybody.
	inward := good
	inward.Kind = KindInward
	inward.ExpectedReturnAt = nil
	if err := inward.validate(); err != nil {
		t.Errorf("inward declaration without a date refused: %v", err)
	}
}

func TestItemOutstanding(t *testing.T) {
	cases := []struct {
		name          string
		out, returned float64
		want          float64
	}{
		{"never released", 0, 0, 0},
		{"whole load out", 5, 0, 5},
		{"part back", 5, 2, 3},
		{"all back", 5, 5, 0},
		{"over-returned", 5, 7, 0},
	}
	for _, tc := range cases {
		item := Item{QuantityOut: tc.out, QuantityReturned: tc.returned}
		if got := item.Outstanding(); got != tc.want {
			t.Errorf("%s: Outstanding() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestParseList(t *testing.T) {
	request := httptest.NewRequest("GET", "/api/v1/materials?kind=outward&return_type=rgp&status=released&limit=10", nil)
	filter, page, err := ParseList(request)
	if err != nil {
		t.Fatalf("ParseList: %v", err)
	}
	if filter.Kind != KindOutward || filter.ReturnType != ReturnRGP || filter.Status != string(StatusReleased) {
		t.Errorf("filter = %+v", filter)
	}
	if page.Limit != 10 || page.Cursor != nil {
		t.Errorf("page = %+v", page)
	}

	for _, raw := range []string{
		"/api/v1/materials?kind=goods",
		"/api/v1/materials?return_type=rp",
		"/api/v1/materials?status=shipped",
	} {
		if _, _, err := ParseList(httptest.NewRequest("GET", raw, nil)); errs.CodeOf(err) != errs.CodeInvalidRequest {
			t.Errorf("%s = %v, want invalid_request", raw, err)
		}
	}
}

func TestValidStatusCoversTheCheckConstraint(t *testing.T) {
	for _, status := range []Status{StatusPending, StatusApproved, StatusRejected, StatusReleased,
		StatusPartiallyReturned, StatusReturned, StatusClosed, StatusOverdue, StatusDisputed} {
		if !validStatus(string(status)) {
			t.Errorf("%q is a goods status but the filter refuses it", status)
		}
	}
	for _, status := range []string{"", "on_site", "RELEASED", "void"} {
		if validStatus(status) {
			t.Errorf("%q accepted but chk_material_passes_status would reject it", status)
		}
	}
}

// quantity_outstanding is the column the outstanding report is built from. Pinning the
// JSON keeps a rename on the view from quietly breaking the dashboard.
func TestItemViewCarriesTheOutstandingColumn(t *testing.T) {
	body, err := json.Marshal(viewOfItem(Item{
		ID: 3, LineNo: 1, ItemName: "Generator", Unit: "pcs", QuantityDeclared: 4,
		QuantityOut: 4, QuantityReturned: 1, Returnable: true, Status: ItemReleased,
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"quantity_outstanding":3`, `"returnable":true`, `"line_no":1`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("item view is missing %s: %s", want, body)
		}
	}
}

func TestReturnRequestValidation(t *testing.T) {
	if err := httpx.Validate(ReturnRequest{
		Lines: []struct {
			ItemID   int64   `json:"item_id" validate:"required,gt=0"`
			Quantity float64 `json:"quantity" validate:"required,gt=0"`
			Note     string  `json:"note" validate:"omitempty,max=512"`
		}{{ItemID: 1, Quantity: 2}},
	}); err != nil {
		t.Fatalf("a plain return was refused: %v", err)
	}

	var empty ReturnRequest
	if err := httpx.Validate(empty); errs.CodeOf(err) != errs.CodeInvalidRequest {
		t.Fatalf("an empty return accepted: %v", err)
	}
}

func TestDeclareRequestMapsToServiceInput(t *testing.T) {
	date := time.Now().UTC()
	verification := false
	req := DeclareRequest{
		Kind: KindInward, ReturnType: ReturnRGP, DeclaredByName: "Sam Otieno",
		SealNo: "SEAL-9", VerificationRequired: &verification, ExpectedReturnAt: &date,
		Items: []ItemRequest{{ItemName: "Laptop", Quantity: 1, Unit: "pcs", SerialNo: "SN1"}},
	}
	in := req.toDeclare()
	if in.Kind != KindInward || in.SealNo != "SEAL-9" || len(in.Items) != 1 {
		t.Fatalf("toDeclare lost the form: %+v", in)
	}
	if in.Items[0].SerialNo != "SN1" {
		t.Errorf("line not mapped: %+v", in.Items[0])
	}
	if in.VerificationRequired == nil || *in.VerificationRequired {
		t.Error("gate_verification_required false was dropped")
	}
	// An inward declaration has no return date, whatever the client sent.
	if got := typeKeyFor(in.Kind, in.ReturnType); got != "material_personal_item" {
		t.Errorf("type key = %q", got)
	}
}

// The pass has to stop being honoured at a barrier exactly when the goods are done with
// it. A personal item is the case that bites: receiving it is not the end, and closing its
// pass at the door would turn the owner's exit red.
func TestClosedShapeTable(t *testing.T) {
	tests := []struct {
		kind, returnType, status string
		want                     bool
	}{
		{KindOutward, ReturnRGP, string(StatusReleased), false},
		{KindOutward, ReturnRGP, string(StatusPartiallyReturned), false},
		{KindOutward, ReturnRGP, string(StatusReturned), true},
		{KindOutward, ReturnNRGP, string(StatusReleased), true},
		{KindOutward, ReturnNRGP, string(StatusApproved), false},
		{KindInward, ReturnRGP, string(StatusReleased), false},
		{KindInward, ReturnRGP, string(StatusClosed), true},
		{KindInward, ReturnNRGP, string(StatusReleased), true},
	}
	for _, tc := range tests {
		m := Material{Kind: tc.kind, ReturnType: tc.returnType, Status: tc.status}
		if got := m.Closed(); got != tc.want {
			t.Errorf("%s/%s/%s: Closed() = %v, want %v", tc.kind, tc.returnType, tc.status, got, tc.want)
		}
	}
}

func TestPurposeFallsBackToThePaperwork(t *testing.T) {
	cases := []struct {
		Declare Declare
		want    string
	}{
		{Declare{Purpose: "machine tool service"}, "machine tool service"},
		{Declare{DestinationReference: "Nakuru depot"}, "Nakuru depot"},
		{Declare{WaybillRef: "WB-44"}, "WB-44"},
		{Declare{ItemCategory: "spare"}, "spare"},
		{Declare{}, "goods declaration"},
	}
	for _, tc := range cases {
		if got := purpose(tc.Declare); got != tc.want {
			t.Errorf("purpose(%+v) = %q, want %q", tc.Declare, got, tc.want)
		}
	}
}

// The relay hands a handler a payload decoded from JSON, so a missing key is an empty
// string rather than a panic. A refused scan and an event about something with no pass
// must both leave the goods row alone, which is what "the module is off" sounds like from
// the inside.
func TestOnMovementIgnoresEventsThatAreNotAGoodsRelease(t *testing.T) {
	// A repository with no connection proves the handler never reaches the database.
	svc := &Service{repo: NewRepository(nil)}
	for _, payload := range []string{
		`{"direction":"out","result":"denied"}`,
		`{"direction":"sideways","result":"granted"}`,
		`{"direction":"out"}`,
		`{}`,
	} {
		var body map[string]any
		if err := json.Unmarshal([]byte(payload), &body); err != nil {
			t.Fatal(err)
		}
		event := events.Event{TenantID: 1, EventKey: "movement.recorded", Payload: body}
		if err := svc.OnMovement(context.Background(), event); err != nil {
			t.Errorf("%s: %v", payload, err)
		}
	}
}

// declaredValue is the only thing that turns a basket of line items into the number a
// tenant's "above this value, ask the security manager" rule can read, so the blank cases
// matter as much as the sums: a pass with no priced line must stay unpriced rather than
// arrive at the desk as worth zero.
func TestDeclaredValue(t *testing.T) {
	money := func(v float64) *float64 { return &v }

	cases := []struct {
		name      string
		items     []NewItem
		wantTotal *float64
		wantCur   string
	}{
		{"no items at all", nil, nil, ""},
		{"nothing priced", []NewItem{{ItemName: "Crate", Quantity: 3}}, nil, ""},
		{"one priced line", []NewItem{{ItemName: "Rack", Quantity: 2, UnitValue: money(60000),
			Currency: "kes"}}, money(120000), "KES"},
		{"unpriced lines are left out of the sum",
			[]NewItem{{ItemName: "Rack", Quantity: 1, UnitValue: money(5000), Currency: "KES"},
				{ItemName: "Unknown spare", Quantity: 4}}, money(5000), "KES"},
		{"the first currency wins when lines disagree",
			[]NewItem{{ItemName: "Rack", Quantity: 1, UnitValue: money(5000), Currency: "KES"},
				{ItemName: "Import", Quantity: 1, UnitValue: money(90), Currency: "USD"}},
			money(5090), "KES"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			total, currency := declaredValue(tc.items)
			switch {
			case tc.wantTotal == nil && total != nil:
				t.Errorf("total = %v, want no declared value", *total)
			case tc.wantTotal != nil && total == nil:
				t.Error("total = nil, want a declared value")
			case tc.wantTotal != nil && *total != *tc.wantTotal:
				t.Errorf("total = %v, want %v", *total, *tc.wantTotal)
			}
			if currency != tc.wantCur {
				t.Errorf("currency = %q, want %q", currency, tc.wantCur)
			}
		})
	}
}
