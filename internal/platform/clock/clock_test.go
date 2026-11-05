package clock

import (
	"testing"
	"time"
)

func TestPeriodKey(t *testing.T) {
	at := time.Date(2026, 11, 5, 23, 30, 0, 0, time.UTC)

	tests := []struct {
		reset  string
		expect string
	}{
		{"daily", "20261105"},
		{"monthly", "202611"},
		{"yearly", "2026"},
		{"never", ""},
		{"", ""},
	}
	for _, tc := range tests {
		if got := PeriodKey(tc.reset, at, time.UTC); got != tc.expect {
			t.Errorf("PeriodKey(%q) = %q, want %q", tc.reset, got, tc.expect)
		}
	}
}

// The bucket must flip when the tenant's clock says so, not when UTC does: 01:00 in
// Nairobi is still 22:00 UTC the day before.
func TestPeriodKeyUsesTenantWallClock(t *testing.T) {
	loc, err := time.LoadLocation("Africa/Nairobi")
	if err != nil {
		t.Skip("IANA zone data not available on this machine")
	}
	at := time.Date(2026, 11, 4, 22, 0, 0, 0, time.UTC)

	if got, want := PeriodKey("daily", at, loc), "20261105"; got != want {
		t.Errorf("daily bucket = %q, want %q", got, want)
	}
}

func TestLocationFallsBackToUTC(t *testing.T) {
	for _, name := range []string{"", "Not/AZone"} {
		if got := Location(name); got != time.UTC {
			t.Errorf("Location(%q) = %v, want UTC", name, got)
		}
	}
}
