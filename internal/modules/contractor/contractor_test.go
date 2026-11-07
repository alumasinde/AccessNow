package contractor

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Thursday 5 November 2026, 09:00 UTC. Every window test is anchored here so a rota that is off
// by one weekday cannot hide behind the clock.
var base = time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)

func TestWindowCoversTheHoursAndTheDayItNames(t *testing.T) {
	dayShift := Window{DayOfWeek: 4, StartsAt: "08:00:00", ClosesAt: "17:00:00"}
	if !dayShift.Covers(base, 0) {
		t.Error("09:00 on a Thursday is inside a Thursday 08:00-17:00 window")
	}
	if dayShift.Covers(base.Add(9*time.Hour), 0) {
		t.Error("18:00 is outside 08:00-17:00")
	}
	if dayShift.Covers(base.Add(-2*time.Hour), 0) {
		t.Error("07:00 is outside 08:00-17:00")
	}
	if (Window{DayOfWeek: 5, StartsAt: "08:00", ClosesAt: "17:00"}).Covers(base, 0) {
		t.Error("a Friday window opened on a Thursday")
	}
	// Zero is the column's own word for every day.
	always := Window{DayOfWeek: 0, StartsAt: "06:00", ClosesAt: "20:00"}
	for offset := 0; offset < 7; offset++ {
		at := base.AddDate(0, 0, offset)
		if !always.Covers(at, 0) {
			t.Errorf("%s is outside an every-day window", at.Weekday())
		}
	}
}

func TestWindowPinsAGateWithoutPinningEveryGate(t *testing.T) {
	delivery := int64(7)
	window := Window{GateID: &delivery, DayOfWeek: 0, StartsAt: "08:00", ClosesAt: "17:00"}
	if !window.Covers(base, 7) {
		t.Error("the window refused its own gate")
	}
	if window.Covers(base, 8) {
		t.Error("a Delivery Gate window opened the Main Gate")
	}
	// A cache or a session with no gate in hand cannot prove the window wrong, so the pass is
	// left to the gate's own category check rather than refused on a comparison nobody made.
	if !window.Covers(base, 0) {
		t.Error("a gate-less check was refused by a gate-scoped window")
	}
}

func TestCrossMidnightWindowIsOneShift(t *testing.T) {
	night := Window{DayOfWeek: 4, StartsAt: "22:00", ClosesAt: "06:00", CrossesMidnight: true}
	thursdayLate := time.Date(2026, 11, 5, 23, 0, 0, 0, time.UTC)
	fridayEarly := time.Date(2026, 11, 6, 5, 0, 0, 0, time.UTC)
	fridayLater := time.Date(2026, 11, 6, 7, 0, 0, 0, time.UTC)

	if !night.Covers(thursdayLate, 0) {
		t.Error("23:00 on the night the window starts is inside it")
	}
	if !night.Covers(fridayEarly, 0) {
		t.Error("05:00 the next morning is the same Thursday night shift")
	}
	if night.Covers(fridayLater, 0) {
		t.Error("07:00 is after the night shift ended")
	}

	// A desk that typed 22:00 to 06:00 and left the flag alone still filed one night shift; the
	// clock is the truth, the flag is a convenience.
	stored := Window{DayOfWeek: 4, StartsAt: "22:00", ClosesAt: "06:00"}
	if !stored.Covers(thursdayLate, 0) || !stored.Covers(fridayEarly, 0) {
		t.Error("a window that closes before it opens was read as a negative window")
	}
}

func TestScheduleWithoutWindowsPermitsAnyHour(t *testing.T) {
	if !Schedule(nil).Permits(base, 3) {
		t.Error("a contractor with no rota was refused every hour")
	}
	rota := Schedule{{DayOfWeek: 1, StartsAt: "08:00", ClosesAt: "12:00"}}
	if rota.Permits(base, 0) {
		t.Error("a Monday rota admitted a Thursday")
	}
	monday := time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)
	if !rota.Permits(monday, 0) {
		t.Error("a Monday 09:00 was refused by a Monday 08:00-12:00 rota")
	}
}

func TestValidityRangeIncludesBothEndDays(t *testing.T) {
	filed := Pass{
		ValidFrom: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		ValidTo:   time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC),
	}
	if !filed.ValidOn(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Error("the first day is outside the pass")
	}
	if !filed.ValidOn(time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC)) {
		t.Error("the last day is outside the pass")
	}
	if filed.ValidOn(time.Date(2026, 10, 31, 0, 0, 0, 0, time.UTC)) {
		t.Error("the day before the range was admitted")
	}
	if filed.ValidOn(time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)) {
		t.Error("the day after the range was admitted")
	}
}

func TestOnlyMandatoryLiveDocumentsStopAPass(t *testing.T) {
	today := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	yesterday := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	expired := Document{Status: string(DocValid), Mandatory: true, ExpiresOn: &yesterday}
	if !expired.Expired(today) {
		t.Error("a mandatory document that ran out yesterday does not block")
	}
	optional := Document{Status: string(DocValid), Mandatory: false, ExpiresOn: &yesterday}
	if optional.Expired(today) {
		t.Error("an optional document stopped a pass")
	}
	refused := Document{Status: string(DocRejected), Mandatory: true, ExpiresOn: &yesterday}
	if refused.Expired(today) {
		t.Error("a document the desk already refused was counted as live paperwork")
	}
	open := Document{Status: string(DocValid), Mandatory: true}
	if open.Expired(today) {
		t.Error("a document with no expiry date expired")
	}
}

func TestClockReadsWhatTheDatabaseStores(t *testing.T) {
	if minutes, ok := Clock("22:30:00"); !ok || minutes != 22*60+30 {
		t.Errorf("22:30:00 read as %d (%v)", minutes, ok)
	}
	if minutes, ok := Clock("7:05"); !ok || minutes != 7*60+5 {
		t.Errorf("7:05 read as %d (%v)", minutes, ok)
	}
	for _, bad := range []string{"", "24:00", "12:60", "half past", "12"} {
		if _, ok := Clock(bad); ok {
			t.Errorf("%q was read as a time of day", bad)
		}
	}
	if got, err := NormaliseClock("starts_at", "7:5"); err != nil || got != "07:05:00" {
		t.Errorf("7:5 normalised to %q (%v)", got, err)
	}
	if _, err := NormaliseClock("starts_at", "7am"); err == nil {
		t.Error("a desktop-typed \"7am\" was accepted for a TIME column")
	}
}

func TestSundayIsSevenNotZero(t *testing.T) {
	if ISOWeekday(time.Sunday) != 7 {
		t.Error("Sunday has to be 7, or every Sunday rota runs on Monday")
	}
	if ISOWeekday(time.Monday) != 1 || ISOWeekday(time.Saturday) != 6 {
		t.Error("ISO weekday numbering drifted")
	}
}

func TestPassRequestRefusesWhatTheDatabaseWould(t *testing.T) {
	today := time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC)
	good := PassRequest{
		PassTypeKey: PassTypeRecurring, ValidFrom: today,
		ValidTo: today.AddDate(0, 1, 0),
	}
	if err := good.validate(today); err != nil {
		t.Errorf("a valid request was refused: %v", err)
	}
	cases := map[string]PassRequest{
		"unknown type": {PassTypeKey: "visitor_walk_in", ValidFrom: today, ValidTo: today},
		"ends before it starts": {PassTypeKey: PassTypeDay,
			ValidFrom: today.AddDate(0, 0, 5), ValidTo: today},
		"already ended": {PassTypeKey: PassTypeDay,
			ValidFrom: today.AddDate(0, 0, -10), ValidTo: today.AddDate(0, 0, -1)},
		"no dates": {PassTypeKey: PassTypeDay},
	}
	for name, request := range cases {
		if err := request.validate(today); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestStatusesMatchTheCheckConstraint(t *testing.T) {
	for _, status := range []Status{StatusPending, StatusActive, StatusExpired, StatusBlocked,
		StatusRevoked} {
		if !status.valid() {
			t.Errorf("%q is a contractor pass status but the filter refuses it", status)
		}
	}
	// Core's words are not this module's: a register filter that accepts them would page an
	// empty list and look like a bug.
	for _, status := range []string{"", "approved", "on_site", "completed", "void"} {
		if Status(status).valid() {
			t.Errorf("%q accepted but chk_contractor_passes_status rejects it", status)
		}
	}
	// A block a person filed must never be told apart by its wording.
	if BlockByDesk == BlockByDocument || string(BlockByDocument) != "document" {
		t.Error("the block source vocabulary drifted from the column")
	}
}

func TestDefaultMultiEntryAndSeries(t *testing.T) {
	if !defaultMultiEntry(PassTypeRecurring) {
		t.Error("a month-long pass has to let the same person back in")
	}
	if defaultMultiEntry(PassTypeDay) || defaultMultiEntry(PassTypeService) {
		t.Error("a day pass or a service attendance was issued as multi-entry by default")
	}
	if formatFor(PassTypeService) != "service" {
		t.Error("service engineers must not share the contractor series")
	}
	if formatFor(PassTypeRecurring) != numberFormat {
		t.Error("a recurring pass left the contractor series")
	}
}

func TestPassViewAlwaysCarriesAList(t *testing.T) {
	view := viewOfPass(Pass{
		ID: 4, PassID: 9, PassNo: "CTR-26110004", ContractorID: 2,
		ContractorName: "John Otieno", Status: string(StatusActive),
		ValidFrom: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		ValidTo:   time.Date(2026, 11, 30, 0, 0, 0, 0, time.UTC),
	}, time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC))

	body, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"windows":[]`) {
		t.Errorf("a pass with no rota must serialise an empty list: %s", body)
	}
	if !strings.Contains(string(body), `"expires_in_days":25`) {
		t.Errorf("expiry countdown lost: %s", body)
	}
	if view.Windows == nil {
		t.Error("windows was nil, so a client has to special-case two shapes")
	}
}

func TestParsePassesRefusesWhatTheDatabaseWould(t *testing.T) {
	r := httptest.NewRequest("GET", "/contractor-passes?status=active&contractor_id=3&open=true", nil)
	filter, _, err := ParsePasses(r)
	if err != nil {
		t.Fatal(err)
	}
	if filter.Status != "active" || filter.ContractorID != 3 || !filter.OpenOnly {
		t.Errorf("filters lost: %+v", filter)
	}

	for _, query := range []string{
		"/contractor-passes?status=on_site",   // Core's word, not this register's
		"/contractor-passes?contractor_id=-1", // an id cannot be negative
		"/contractor-passes?open=maybe",       // a dropped filter would list everybody
		"/contractor-passes?search=" + strings.Repeat("x", 200),
	} {
		if _, _, err := ParsePasses(httptest.NewRequest("GET", query, nil)); err == nil {
			t.Errorf("%s accepted", query)
		}
	}
}

func TestFirmStatusesAreTheOnesTheColumnAllows(t *testing.T) {
	for _, status := range []string{FirmActive, FirmPending, FirmSuspended, FirmArchived} {
		if !validFirmStatus(status) {
			t.Errorf("%q refused but chk_contractor_org_status allows it", status)
		}
	}
	if validFirmStatus("approved") || validFirmStatus("") {
		t.Error("a firm status the column would reject got through")
	}
	for _, status := range []string{PersonActive, PersonPending, PersonSuspended, PersonExpired,
		PersonArchived} {
		if !validPersonStatus(status) {
			t.Errorf("%q refused but chk_contractors_status allows it", status)
		}
	}
}
