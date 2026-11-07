package rollcall

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var started = time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)

func TestVocabularyMatchesTheCheckConstraints(t *testing.T) {
	for _, kind := range []Kind{KindFire, KindMedical, KindSecurity, KindDrill,
		KindNaturalHazard, KindChemical, KindOther} {
		if !kind.valid() {
			t.Errorf("%q is in chk_evacuation_incidents_kind but valid() refuses it", kind)
		}
	}
	if Kind("earthquake").valid() {
		t.Error("a kind the column would reject got through")
	}
	for _, status := range []Status{StatusOpen, StatusInProgress, StatusClosed, StatusCancelled} {
		if !status.valid() {
			t.Errorf("%q is in the status column but valid() refuses it", status)
		}
	}
	// Only the two live states may be marked against, or a closed file changes after the fact.
	if !StatusOpen.Live() || !StatusInProgress.Live() {
		t.Error("an open incident refused marks")
	}
	if StatusClosed.Live() || Status("").Live() {
		t.Error("a settled incident still accepts marks")
	}
}

func TestPersonStatusSeenIsWhatAWardenCanConfirm(t *testing.T) {
	for _, status := range []PersonStatus{PersonAccounted, PersonEvacuated, PersonInjured,
		PersonNotOnSite} {
		if !status.Seen() {
			t.Errorf("%q counts as seen", status)
		}
	}
	for _, status := range []PersonStatus{PersonUnaccounted, PersonMissing, PersonDeceased} {
		if status.Seen() {
			t.Errorf("%q must stay on the board", status)
		}
	}
	if !PersonDeceased.valid() || PersonStatus("home").valid() {
		t.Error("the roster status vocabulary drifted from the CHECK")
	}
}

func TestPersonTypeFollowsTheIssuingModule(t *testing.T) {
	cases := map[string]string{
		"visitor_management": "visitor",
		"staff_outpass":      "staff",
		"contractor_access":  "contractor",
		"vehicle_tracking":   "driver",
		"material_logistics": "other",
		"":                   "other",
	}
	for module, want := range cases {
		if got := personTypeOf(module); got != want {
			t.Errorf("%q mapped to %q, wanted %q", module, got, want)
		}
	}
}

func TestUnaccountedNeverGoesNegative(t *testing.T) {
	if got := (Counts{Expected: 10, Accounted: 7}).Unaccounted(); got != 3 {
		t.Errorf("expected 3, got %d", got)
	}
	// A roster can be reconciled after somebody was added late; a negative number on a board is
	// a bug the warden would have to explain.
	if got := (Counts{Expected: 2, Accounted: 5}).Unaccounted(); got != 0 {
		t.Errorf("a surplus answered %d", got)
	}
	incident := Incident{Expected: 4, Accounted: 4, Status: string(StatusOpen)}
	if incident.Unaccounted() != 0 || !incident.Live() {
		t.Error("an empty board should still be live until it is closed")
	}
}

func TestCloseNeedsASummaryOncePeopleAreMissing(t *testing.T) {
	counts := Counts{Expected: 10, Accounted: 8}
	if err := closeRefusal(counts, ""); err == nil {
		t.Error("a file with two people unaccounted and no summary was signed off")
	}
	if err := closeRefusal(counts, "both were confirmed off site before the alarm"); err != nil {
		t.Errorf("a summary was refused: %v", err)
	}
	if err := closeRefusal(Counts{Expected: 4, Accounted: 4}, ""); err != nil {
		t.Errorf("a clear board still demanded a summary: %v", err)
	}
}

func TestIncidentViewCarriesTheBoard(t *testing.T) {
	muster := "Car park B"
	incident := Incident{
		ID: 9, UUID: "0b6f4c1e-2b8d-4b7a-9a1f-0e7d4d2d5a11", SiteID: 2, SiteName: "Mombasa Rd",
		Kind: string(KindFire), Status: string(StatusOpen), StartedAt: started,
		Expected: 12, Accounted: 5, MusterPoint: &muster,
	}
	body, err := json.Marshal(viewOfIncident(incident))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"unaccounted_count":7`, `"status":"open"`,
		`"site_name":"Mombasa Rd"`, `"muster_point":"Car park B"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("%s missing from %s", want, body)
		}
	}
}

func TestRosterViewShowsTheSnapshotsNotThePass(t *testing.T) {
	phone, plate := "+254700000000", "KDA 123X"
	person := Person{
		ID: 3, PersonKey: "pass:88", PersonType: "visitor", PassID: ptr(88), PassNo: "VIS-1",
		Name: "Amina Wanjiru", Phone: &phone, Plate: &plate, Status: string(PersonUnaccounted),
		Source: string(SourceSystem), LastSeenAt: &started, CreatedAt: started,
	}
	body, err := json.Marshal(viewOfPerson(person))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"name":"Amina Wanjiru"`, `"pass_no":"VIS-1"`,
		`"plate":"KDA 123X"`, `"status":"unaccounted"`, `"source":"system"`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("%s missing from %s", want, body)
		}
	}
	// A name with no phone must not serialise as a present empty string, which a warden's phone
	// would draw as a blank line he thinks is a number he cannot read.
	if strings.Contains(string(body), `"phone":""`) {
		t.Error("an absent phone was serialised as a present one")
	}
}

func TestMarkResultAnswersBothQuestionsAtOnce(t *testing.T) {
	view := MarkResultView{
		Person: viewOfPerson(Person{ID: 3, Name: "Amina", Status: string(PersonAccounted)}),
		Counts: viewOfCounts(Counts{Expected: 12, Accounted: 6}),
	}
	body, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"unaccounted":6`) {
		t.Errorf("the board's next total is missing: %s", body)
	}
	if !strings.Contains(string(body), `"status":"accounted"`) {
		t.Errorf("the person's new row is missing: %s", body)
	}
}

func TestParseRosterRefusesFiltersTheColumnCannotAnswer(t *testing.T) {
	filter, _, err := ParseRoster(httptest.NewRequest("GET",
		"/rollcall/incidents/4/roster?status=missing&search=Amina", nil))
	if err != nil {
		t.Fatal(err)
	}
	if filter.Status != "missing" || filter.Search != "Amina" {
		t.Errorf("filters lost: %+v", filter)
	}
	for _, query := range []string{
		"/rollcall/incidents/4/roster?status=home", // not a roster status
		"/rollcall/incidents/4/roster?search=" + strings.Repeat("x", 200),
	} {
		if _, _, err := ParseRoster(httptest.NewRequest("GET", query, nil)); err == nil {
			t.Errorf("%s accepted", query)
		}
	}
}

func TestParseIncidentsKeepsTheSiteFilter(t *testing.T) {
	filter, _, err := ParseIncidents(httptest.NewRequest("GET",
		"/rollcall/incidents?site_id=5&status=open", nil))
	if err != nil {
		t.Fatal(err)
	}
	if filter.SiteID != 5 || filter.Status != "open" {
		t.Errorf("filters lost: %+v", filter)
	}
	for _, query := range []string{
		"/rollcall/incidents?status=burning",
		"/rollcall/incidents?site_id=0",
		"/rollcall/incidents?site_id=-2",
	} {
		if _, _, err := ParseIncidents(httptest.NewRequest("GET", query, nil)); err == nil {
			t.Errorf("%s accepted", query)
		}
	}
}

func ptr(value int64) *int64 { return &value }
