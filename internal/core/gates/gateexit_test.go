package gates

import "testing"

func TestGateExitMatchesWholeSegments(t *testing.T) {
	for path, want := range map[string]bool{
		"/api/v1/auth/logout":       true,
		"/api/v1/gates":             true,
		"/api/v1/gates/active":      true,
		"/api/v1/gates/shift/end":   true,
		"/api/v1/reports/gates-sum": false,
		"/api/v1/passes/gates":      false,
		"/api/v1/visitors/auth/x":   false,
		"/api/v1/gate/scan":         false,
		"/other/api/v1/gates":       false,
	} {
		if got := gateExit(path); got != want {
			t.Errorf("gateExit(%q) = %v, want %v", path, got, want)
		}
	}
}
