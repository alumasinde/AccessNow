package vehicle

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/events"
)

// A plate is the key the whole module hangs from, and it arrives typed by hand at a gate.
// These are the spellings a guard actually produces; if two of them stop matching, one
// vehicle becomes two register rows and the dwell history splits.
func TestNormalizePlate(t *testing.T) {
	for _, raw := range []string{"KCA 123M", "kca-123 m", "KCA/123M", " kca.123_m ", "KCA123M"} {
		if got := NormalizePlate(raw); got != "KCA123M" {
			t.Errorf("NormalizePlate(%q) = %q, want KCA123M", raw, got)
		}
	}
	// Punctuation and spacing go; digits and letters of any script stay, because a plate from
	// a neighbouring country is still one plate and the column is not ASCII-only.
	for _, raw := range []string{"", "   ", "---", "!!"} {
		if got := NormalizePlate(raw); got != "" {
			t.Errorf("NormalizePlate(%q) = %q, want empty: not a plate", raw, got)
		}
	}
	if got := NormalizePlate("KX 0041 QZ"); got != "KX0041QZ" {
		t.Errorf("NormalizePlate = %q, want KX0041QZ", got)
	}
}

// The three kinds are the only choice the desk makes, and each maps onto one seeded
// catalogue row. A wrong key is a journey that silently draws the wrong approval rule.
func TestTypeKeyFor(t *testing.T) {
	cases := []struct{ kind, want string }{
		{KindDelivery, "vehicle_delivery"},
		{KindFleet, "vehicle_fleet"},
		{KindVisitor, "vehicle_visitor"},
	}
	for _, tc := range cases {
		if got := typeKeyFor(tc.kind); got != tc.want {
			t.Errorf("typeKeyFor(%q) = %q, want %q", tc.kind, got, tc.want)
		}
	}
}

func TestLogValidation(t *testing.T) {
	good := Log{Kind: KindDelivery, Plate: "KCA123M", DriverName: "Peter Otieno", Cargo: "Cement"}
	if err := good.validate(NormalizePlate(good.Plate)); err != nil {
		t.Fatalf("a complete delivery was refused: %v", err)
	}

	fleet := Log{Kind: KindFleet, Plate: "KCA123M"}
	if err := fleet.validate("KCA123M"); err != nil {
		t.Errorf("a fleet vehicle names its driver on the register, not on every pass: %v", err)
	}

	cases := []struct {
		name  string
		input Log
	}{
		{"unknown kind", Log{Kind: "private", Plate: "KCA123M", DriverName: "X"}},
		{"no plate", Log{Kind: KindDelivery, Plate: "!!!", DriverName: "X"}},
		{"delivery without a driver", Log{Kind: KindDelivery, Plate: "KCA123M"}},
		{"visitor without a driver", Log{Kind: KindVisitor, Plate: "KCA123M", EscortName: "Ruth"}},
		{"visitor with nobody to see", Log{Kind: KindVisitor, Plate: "KCA123M", DriverName: "X"}},
	}
	for _, tc := range cases {
		if err := tc.input.validate(NormalizePlate(tc.input.Plate)); err == nil {
			t.Errorf("%s was accepted", tc.name)
		}
	}
}

// A blocked plate must never leave the desk with a pass, and an expired insurance cover is
// a warning on the card, not a gate rule.
func TestVehicleStatuses(t *testing.T) {
	expiry := time.Now().UTC().Add(-24 * time.Hour)
	for _, status := range []string{VehicleBlocked, VehicleArchived} {
		if !(Vehicle{Status: status, InsuranceExpiry: &expiry}).Blocked() {
			t.Errorf("%q plate reported as usable", status)
		}
	}
	active := Vehicle{Status: VehicleActive}
	if active.Blocked() {
		t.Error("an active plate reported as blocked")
	}
	if !(Vehicle{Status: VehicleActive, InsuranceExpiry: &expiry}).InsuranceExpired(time.Now().UTC()) {
		t.Error("an insurance date in the past reported as valid")
	}
	if (Vehicle{Status: VehicleActive}).InsuranceExpired(time.Now().UTC()) {
		t.Error("a vehicle with no insurance on file reported as expired")
	}
}

func TestPassDwellStates(t *testing.T) {
	entered := time.Now().UTC().Add(-90 * time.Minute)
	threeHours := 180
	limit := &threeHours

	onSite := Pass{Status: string(StatusOnSite), EnteredAt: &entered, DwellLimitMinutes: limit}
	if !onSite.OnSite() || !onSite.Open() {
		t.Error("an on-site journey must be both live and open")
	}
	if onSite.PastDwell(time.Now().UTC()) {
		t.Error("90 minutes on a 180 minute limit is not an overstay")
	}
	if !onSite.PastDwell(entered.Add(3 * time.Hour)) {
		t.Error("3 hours on a 180 minute limit is an overstay")
	}
	if got := derefMinutes(onSite.MinutesOnSite(time.Now().UTC())); got < 85 || got > 95 {
		t.Errorf("MinutesOnSite = %d, want about 90", got)
	}

	// overdue is still on site, just not politely: dropping it from the roster would hide
	// exactly the vehicles worth finding.
	overdue := Pass{Status: string(StatusOverdue), EnteredAt: &entered, DwellLimitMinutes: limit}
	if !overdue.OnSite() || !overdue.Open() {
		t.Error("an overdue journey dropped off the roster")
	}

	closed := Pass{Status: string(StatusCompleted), EnteredAt: &entered, DwellLimitMinutes: limit}
	if closed.OnSite() || closed.Open() {
		t.Error("a completed journey is still counted on site")
	}
	if closed.PastDwell(time.Now().UTC()) {
		t.Error("a journey that has left cannot be overstaying")
	}
	if (Pass{Status: string(StatusApproved)}).MinutesOnSite(time.Now().UTC()) != nil {
		t.Error("a vehicle that never entered has a dwell")
	}
	// No limit on file is no rule, so nothing is ever late.
	if (Pass{Status: string(StatusOnSite), EnteredAt: &entered}).PastDwell(time.Now().UTC()) {
		t.Error("an unlimited dwell was flagged as an overstay")
	}
}

func TestParseList(t *testing.T) {
	for _, raw := range []string{
		"/vehicle-passes", "/vehicle-passes?status=on_site", "/vehicle-passes?plate=kca-123-m",
		"/vehicle-passes?vehicle_id=7&search=KCA", "/vehicle-passes?limit=10",
	} {
		if _, _, err := ParseList(httptest.NewRequest("GET", raw, nil)); err != nil {
			t.Errorf("%s refused: %v", raw, err)
		}
	}
	// A plate filter is normalised the same way the register is, or it finds nothing.
	filter, _, err := ParseList(httptest.NewRequest("GET", "/vehicle-passes?plate=kca%20123m", nil))
	if err != nil {
		t.Fatal(err)
	}
	if filter.Plate != "KCA123M" {
		t.Errorf("plate filter = %q, want KCA123M", filter.Plate)
	}

	for _, raw := range []string{
		"/vehicle-passes?status=driven", "/vehicle-passes?vehicle_id=0",
		"/vehicle-passes?vehicle_id=abc", "/vehicle-passes?plate=!!!", "/vehicle-passes?limit=0",
	} {
		if _, _, err := ParseList(httptest.NewRequest("GET", raw, nil)); errs.CodeOf(err) != errs.CodeInvalidRequest {
			t.Errorf("%s = %v, want invalid_request", raw, err)
		}
	}
}

func TestParseVehicles(t *testing.T) {
	filter, _, err := ParseVehicles(httptest.NewRequest("GET", "/vehicles?fleet=true", nil))
	if err != nil {
		t.Fatal(err)
	}
	if filter.Fleet == nil || !*filter.Fleet {
		t.Error("?fleet=true did not parse")
	}
	if _, page, err := ParseVehicles(httptest.NewRequest("GET", "/vehicles?limit=5", nil)); err != nil || page.Limit != 5 {
		t.Errorf("limit page = %+v, %v", page, err)
	}
	for _, raw := range []string{"/vehicles?status=scrapped", "/vehicles?fleet=maybe", "/vehicles?class=" +
		strings.Repeat("x", 25)} {
		if _, _, err := ParseVehicles(httptest.NewRequest("GET", raw, nil)); errs.CodeOf(err) != errs.CodeInvalidRequest {
			t.Errorf("%s = %v, want invalid_request", raw, err)
		}
	}
}

func TestValidStatusCoversTheCheckConstraint(t *testing.T) {
	for _, status := range []Status{StatusPending, StatusApproved, StatusRejected, StatusCancelled,
		StatusOnSite, StatusCompleted, StatusOverdue, StatusExpired} {
		if !validStatus(string(status)) {
			t.Errorf("%q is a vehicle pass status but the filter refuses it", status)
		}
	}
	// released and returned are goods statuses; active is Core's word, not this module's.
	for _, status := range []string{"", "released", "returned", "active", "ON_SITE"} {
		if validStatus(status) {
			t.Errorf("%q accepted but chk_vehicle_passes_status would reject it", status)
		}
	}
}

// The licence number is read from the document at the gate and the column behind it is
// retention-bound. Nothing that leaves this module may carry it.
func TestPassViewLeaksNoDocument(t *testing.T) {
	body, err := json.Marshal(viewOf(Pass{
		ID: 1, PassID: 2, PassNo: "VH-202600001", PlateNormalized: "KCA123M",
		DriverName: str("Peter Otieno"), Status: string(StatusApproved),
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"driver_id_number", "id_number", "document"} {
		if strings.Contains(string(body), forbidden) {
			t.Errorf("pass view contains %q: %s", forbidden, body)
		}
	}
	for _, want := range []string{`"plate_normalized":"KCA123M"`, `"pass_no":"VH-202600001"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("pass view is missing %s: %s", want, body)
		}
	}
}

func TestLogRequestMapsToServiceInput(t *testing.T) {
	expected := time.Now().UTC().Add(time.Hour)
	req := LogRequest{
		Kind: KindVisitor, Plate: "KCA123M", DriverName: "Amina", EscortName: "Ruth",
		Cargo: "Sample", ExpectedExit: &expected, DwellLimitMinutes: minutes(45),
	}
	in := req.toLog()
	if in.Kind != KindVisitor || in.EscortName != "Ruth" || in.Cargo != "Sample" {
		t.Errorf("request dropped fields: %+v", in)
	}
	if in.ExpectedExit == nil || !in.ExpectedExit.Equal(expected) {
		t.Error("expected_exit_at did not reach the service input")
	}
	if in.DwellLimitMinutes == nil || *in.DwellLimitMinutes != 45 {
		t.Error("dwell_limit_minutes did not reach the service input")
	}
}

// The journey row is a snapshot, not a join: it keeps the plate as typed, the fleet flag and
// the driver from this log, so a later edit of the register cannot rewrite a past visit.
func TestNewPassSnapshotsTheVehicle(t *testing.T) {
	in := Log{Kind: KindDelivery, Plate: "kca-123-m", DriverName: "Peter", Purpose: "Cement", Cargo: "bags"}
	v := Vehicle{ID: 9, Fleet: true, PlateDisplay: "KCA 123M"}
	detail := in.NewPass(1, 2, v, "KCA123M", StatusPending)

	if detail.VehicleID != 9 || !detail.FleetSnapshot || detail.PlateNorm != "KCA123M" {
		t.Errorf("snapshot wrong: %+v", detail)
	}
	if detail.PlateDisplay != "kca-123-m" {
		t.Errorf("plate display = %q, want the plate as the caller typed it", detail.PlateDisplay)
	}
	if detail.Status != StatusPending {
		t.Error("the detail row did not take the status the catalogue asked for")
	}
	if purpose(in) != "Cement" {
		t.Errorf("purpose = %q, want the stated purpose ahead of the cargo", purpose(in))
	}
	if got := purpose(Log{Cargo: "Steel"}); got != "Steel" {
		t.Errorf("purpose = %q, want the cargo when no purpose was given", got)
	}
}

// The relay hands a handler a payload decoded from JSON, so a missing key is an empty string
// rather than a panic. A refused scan, a scan with no pass behind it and an event about
// somebody else's pass must all leave the register alone - which is also what a tenant with
// vehicle_tracking switched off looks like from the inside.
func TestOnMovementIgnoresEventsThatAreNotAVehicleScan(t *testing.T) {
	// A repository with no connection proves the handler never reaches the database.
	svc := &Service{repo: NewRepository(nil)}
	for _, payload := range []string{
		`{"direction":"in","result":"denied"}`,
		`{"direction":"out","result":"override"}`,
		`{"direction":"in"}`,
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

// holds is the guard in front of every compare-and-set: it decides which journeys a scan
// may start, and getting it wrong would move a pending pass straight onto the roster.
func TestHolds(t *testing.T) {
	from := []Status{StatusApproved, StatusCompleted}
	for _, status := range []string{"approved", "completed"} {
		if !holds(from, status) {
			t.Errorf("%q should be enterable", status)
		}
	}
	for _, status := range []string{"pending", "on_site", "cancelled", ""} {
		if holds(from, status) {
			t.Errorf("%q should not be enterable", status)
		}
	}
}

func str(value string) *string { return &value }
func minutes(value int) *int   { return &value }
