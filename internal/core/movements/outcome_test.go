package movements

import (
	"testing"
	"time"
)

// The tablet shows one colour, and a warned or overridden entry must not look like a
// refusal to the guard.
func TestOutcomeGreen(t *testing.T) {
	tests := []struct {
		result Result
		green  bool
	}{
		{ResultGranted, true},
		{ResultWarned, true},
		{ResultOverride, true},
		{ResultDenied, false},
		{Result(""), false},
	}
	for _, tc := range tests {
		if got := (Outcome{Result: tc.result}).Green(); got != tc.green {
			t.Errorf("%q green = %v, want %v", tc.result, got, tc.green)
		}
	}
}

// A tablet with no clock must send NULL, never a 0000-00-00 row.
func TestNullable(t *testing.T) {
	if got := nullable(time.Time{}); got != nil {
		t.Errorf("zero time = %v, want nil", got)
	}
	at := time.Date(2026, 11, 5, 9, 0, 0, 0, time.FixedZone("EAT", 3*60*60))
	got, ok := nullable(at).(time.Time)
	if !ok || !got.Equal(at) || got.Location() != time.UTC {
		t.Errorf("nullable = %v, want the same instant in UTC", got)
	}
}

func TestSkewSeconds(t *testing.T) {
	received := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)

	if got := skewSeconds(received, time.Time{}); got != nil {
		t.Errorf("no device clock = %v, want nil", got)
	}
	// Two minutes behind: the sync report needs the sign to tell a stale device from a
	// dishonest one.
	if got := skewSeconds(received, received.Add(-2*time.Minute)); got != 120 {
		t.Errorf("skew = %v, want 120", got)
	}
	if got := skewSeconds(received, received.Add(30*time.Second)); got != -30 {
		t.Errorf("skew = %v, want -30", got)
	}
}

func TestFirstOf(t *testing.T) {
	if got := firstOf("", "", "fallback"); got != "fallback" {
		t.Errorf("firstOf = %q", got)
	}
	if got := firstOf("scan name", "pass name"); got != "scan name" {
		t.Errorf("firstOf = %q", got)
	}
	if got := firstOf("", ""); got != "" {
		t.Errorf("firstOf = %q, want empty", got)
	}
}

func TestContainsIgnoresCase(t *testing.T) {
	if !contains([]string{"Vehicle", "Staff"}, "staff") {
		t.Error("case difference hid a category")
	}
	if contains([]string{"Vehicle"}, "visitor") {
		t.Error("unlisted category matched")
	}
	if contains(nil, "visitor") {
		t.Error("empty gate accepted every category")
	}
}
