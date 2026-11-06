package staff

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"accessnow/internal/core/passes"
	"accessnow/internal/platform/events"
)

func minutes(value int) *int { return &value }

func at(value time.Time) *time.Time { return &value }

var base = time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)

func TestValidateReason(t *testing.T) {
	if err := (New{ReasonKey: "medical_appointment"}).validate(); err != nil {
		t.Errorf("a named reason needs no note: %v", err)
	}
	if err := (New{}).validate(); err == nil {
		t.Error("an empty reason was accepted")
	}
	// "Other" is the row whose text is the reason, so it is the one that cannot be blank.
	if err := (New{ReasonKey: reasonOther}).validate(); err == nil {
		t.Error("Other accepted without a note")
	}
	if err := (New{ReasonKey: reasonOther, ReasonNote: "Bank queue"}).validate(); err != nil {
		t.Errorf("Other with a note refused: %v", err)
	}
}

func TestStatusesMatchTheCheckConstraint(t *testing.T) {
	// The eight values chk_outpasses_status allows, each reachable from this package.
	for _, status := range []Status{StatusPending, StatusApproved, StatusRejected,
		StatusCancelled, StatusOut, StatusReturned, StatusOverdue, StatusUnreturned} {
		if !validStatus(string(status)) {
			t.Errorf("%q is an outpass status but the register filter refuses it", status)
		}
	}
	// on_site and completed belong to other modules; active is Core's word for a used pass.
	for _, status := range []string{"", "active", "on_site", "completed", "out ", "OUT"} {
		if validStatus(status) {
			t.Errorf("%q accepted but chk_outpasses_status would reject it", status)
		}
	}
}

func TestOpenCoversEveryoneTheSiteIsWithout(t *testing.T) {
	for _, status := range []Status{StatusOut, StatusOverdue, StatusUnreturned} {
		if !(Outpass{Status: string(status)}).Open() {
			t.Errorf("%q is closed, so a sweep can never find the employee", status)
		}
	}
	for _, status := range []Status{StatusPending, StatusApproved, StatusReturned,
		StatusCancelled, StatusRejected} {
		if (Outpass{Status: string(status)}).Open() {
			t.Errorf("%q counted as somebody still out", status)
		}
	}
}

func TestDeadlines(t *testing.T) {
	left := base.Add(10 * time.Minute)
	out := Outpass{Status: string(StatusOut), ExitedAt: at(left)}

	// The promised return is what the exit scan stamped: an hour after they left.
	out.ExpectedReturnAt = at(left.Add(time.Hour))
	if out.PastExpected(left.Add(59 * time.Minute)) {
		t.Error("overdue a minute early")
	}
	if !out.PastExpected(left.Add(time.Hour)) {
		t.Error("the deadline itself is not overdue")
	}
	if out.PastEndOfShift(left.Add(3 * time.Hour)) {
		t.Error("nothing but the status and its own deadline makes somebody unreturned")
	}

	out.EndOfShiftAt = at(left.Add(4 * time.Hour))
	if out.PastEndOfShift(left.Add(3 * time.Hour)) {
		t.Error("written off three hours early")
	}
	if !out.PastEndOfShift(left.Add(4 * time.Hour)) {
		t.Error("past the pass window and still not written off")
	}

	// An employee who was written off and then walked back is still off the site until the
	// return scan lands, or the sweep would be able to lose them.
	returned := Outpass{Status: string(StatusReturned), ExitedAt: at(left),
		ReturnedAt: at(left.Add(90 * time.Minute))}
	if returned.PastExpected(left.Add(3 * time.Hour)) {
		t.Error("a closed outpass is still escalating")
	}
	if got := derefMinutes(returned.MinutesOut(base.Add(24 * time.Hour))); got != 90 {
		t.Errorf("minutes_out kept counting after the return: %d", got)
	}
}

func TestMinutesOutStopsAtZero(t *testing.T) {
	// A device that synced a clock ahead of the desk's must not report a negative trip.
	row := Outpass{ExitedAt: at(base.Add(time.Hour))}
	if got := derefMinutes(row.MinutesOut(base)); got != 0 {
		t.Errorf("negative time out came back as %d", got)
	}
	// And a request that has not been scanned out has no time out at all: zero would read
	// as "back already", which is the opposite fact.
	if (Outpass{Status: string(StatusApproved)}).MinutesOut(base) != nil {
		t.Error("an outpass with no exit scan reported a duration")
	}
}

func TestDurationPrecedence(t *testing.T) {
	got := firstDuration(minutes(15), minutes(30), minutes(60))
	if *got != 15 {
		t.Errorf("the desk number lost to the catalogue: %d", *got)
	}
	if got := firstDuration(nil, minutes(30), minutes(60)); *got != 30 {
		t.Errorf("the reason's own limit lost: %d", *got)
	}
	if got := firstDuration(minutes(0), nil, minutes(60)); *got != 60 {
		t.Errorf("a zero override should fall through to the type default: %d", *got)
	}
	if got := firstDuration(nil, nil, nil); got != nil {
		t.Errorf("nothing on any of the three should stay nothing: %v", got)
	}
}

func TestEndOfShiftFallsBackToThePassWindow(t *testing.T) {
	window := at(base.Add(4 * time.Hour))
	if got := firstTime(nil, window); got != window {
		t.Error("a desk that knows the real shift end must win")
	}
	desk := at(base.Add(30 * time.Minute))
	if got := firstTime(desk, window); got != desk {
		t.Error("the pass window overrode an explicit shift end")
	}
}

func TestOriginSaysWhoAsked(t *testing.T) {
	// Both values have to be in chk_passes_origin, which is why there is no "desk" here.
	if got := defaultOrigin(0); got != "online" {
		t.Errorf("self-service filed as %q", got)
	}
	if got := defaultOrigin(7); got != "walk_in" {
		t.Errorf("a guard filing for somebody got %q", got)
	}
}

func TestCoreStatusOnlyCarriesTheTwoDeskStates(t *testing.T) {
	if coreStatus(StatusPending) != passes.StatusPending {
		t.Error("a pending request reached Core as something else")
	}
	for _, status := range []Status{StatusApproved, StatusOut, StatusOverdue, StatusReturned} {
		if coreStatus(status) != passes.StatusApproved {
			t.Errorf("%q mapped to a Core status the spine would not accept", status)
		}
	}
}

func TestPurposeReadsAsOneLine(t *testing.T) {
	medical := Reason{Name: "Medical appointment"}
	if got := purpose(New{}, medical); got != "Medical appointment" {
		t.Errorf("plain reason printed as %q", got)
	}
	note := New{ReasonNote: "clinic, follow-up"}
	if got := purpose(note, medical); got != "Medical appointment: clinic, follow-up" {
		t.Errorf("note lost: %q", got)
	}
}

func TestOutpassViewShowsTheClockTheDeskReads(t *testing.T) {
	// viewOf reads the real clock, because that is the only clock a desk can show a person.
	now := time.Now().UTC()
	left := now.Add(-50 * time.Minute)
	row := Outpass{
		ID: 4, PassID: 9, PassNo: "OP-2611004", PassStatus: "active", StaffID: 2,
		StaffName: "Wanjiru Kamau", EmployeeNo: "HQ-E001", ReasonKey: "personal_errand",
		ExpectedDuration: minutes(30), ExitedAt: at(left),
		ExpectedReturnAt: at(left.Add(30 * time.Minute)), Status: string(StatusOut),
		CreatedAt: left,
	}
	view := viewOf(row)
	if !view.Overdue {
		t.Error("twenty minutes past a thirty minute errand is not overdue")
	}
	if got := derefMinutes(view.MinutesOut); got != 50 {
		t.Errorf("minutes_out = %d, want 50", got)
	}

	body, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	// The register is a staff document: it must not carry the identity paperwork Core keeps
	// behind the directory row, nor the gate rows that point at it.
	for _, forbidden := range []string{"id_number", "document", "photo", "exit_movement_id",
		"return_movement_id", "exit_gate_id"} {
		if strings.Contains(string(body), forbidden) {
			t.Errorf("outpass view contains %q: %s", forbidden, body)
		}
	}
	for _, want := range []string{`"pass_no":"OP-2611004"`, `"is_overdue":true`,
		`"minutes_out":50`, `"employee_no":"HQ-E001"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("view lost %s in %s", want, body)
		}
	}
}

func TestReasonViewCarriesWhatTheFormNeeds(t *testing.T) {
	view := viewOfReason(Reason{ReasonKey: "official_business", Name: "Official business off site",
		DefaultDurationMinutes: minutes(180), RequiresReturnScan: true, SortOrder: 40})
	if view.DefaultDurationMinutes == nil || *view.DefaultDurationMinutes != 180 {
		t.Error("the form cannot warn about a limit it never received")
	}
	if !view.RequiresReturnScan {
		t.Error("a reason that needs a scan back showed as one that does not")
	}
}

func TestParseListFilters(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/outpasses?status=out&staff_id=7&reason=medical_appointment"+
		"&open=true&search=wanjiru&limit=10", nil)
	filter, page, err := ParseList(r)
	if err != nil {
		t.Fatal(err)
	}
	if filter.Status != "out" || filter.StaffID != 7 || filter.ReasonKey != "medical_appointment" {
		t.Errorf("filters lost: %+v", filter)
	}
	if !filter.OpenOnly {
		t.Error("open=1 ignored")
	}
	if page.Limit != 10 {
		t.Errorf("limit = %d", page.Limit)
	}
}

func TestParseListRefusesWhatTheDatabaseWould(t *testing.T) {
	for _, query := range []string{
		"/outpasses?status=active", // Core's word, not this register's
		"/outpasses?staff_id=-3",   // an id cannot be negative
		"/outpasses?open=maybe",    // a dropped filter would list everybody
		"/outpasses?search=" + strings.Repeat("x", 200),
	} {
		if _, _, err := ParseList(httptest.NewRequest("GET", query, nil)); err == nil {
			t.Errorf("%s accepted", query)
		}
	}
}

func TestOutpassMapsToTheRequest(t *testing.T) {
	window := base.Add(90 * time.Minute)
	req := OutpassRequest{
		StaffID: 3, ReasonKey: "transport_delay", Destination: "NBC offices",
		VehiclePlate: "KDA001M", CompanyVehicle: true, Duration: minutes(45),
		ExpectedReturn: &window, Origin: "online", Notes: "taxi strike",
	}
	in := req.toRequest()
	if in.StaffID != 3 || in.ReasonKey != "transport_delay" || in.Destination != "NBC offices" {
		t.Errorf("dropped a field: %+v", in)
	}
	if !in.CompanyVehicle || in.Duration == nil || *in.Duration != 45 {
		t.Errorf("the two numbers that change the deadline were lost: %+v", in)
	}
	if in.ExpectedReturn == nil || !in.ExpectedReturn.Equal(window) {
		t.Error("an explicit promised return was dropped")
	}
	if in.Origin != "online" {
		t.Errorf("origin = %q", in.Origin)
	}
}

func TestOnMovementIgnoresEventsThatAreNotAnOutpassScan(t *testing.T) {
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
