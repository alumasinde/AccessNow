package rules

import (
	"context"
	"testing"

	"accessnow/internal/core/movements"
)

// TestSettleAnswers pins the shape of the answer Core reads. The empty Result is not a missing
// value: it is "the rulebook said nothing", and it is what decides whether a counted rule's
// allowance is spent, so a test that assumed ResultGranted here would have passed the walk and
// broken the quota.
func TestSettleAnswers(t *testing.T) {
	limit := func(t *testing.T) candidate {
		return filed(t, 2, ActionLimit, 10, "Two entries a day",
			`{"limit":2,"period":"day","by":"pass"}`, leaf("category", "eq", `"visitor"`))
	}
	warn := func(t *testing.T) candidate {
		return filed(t, 3, ActionWarn, 10, "Escort required in the yard", "",
			leaf("category", "eq", `"visitor"`))
	}
	block := func(t *testing.T) candidate {
		return filed(t, 4, ActionBlock, 10, "Booked through the office", "",
			leaf("purpose", "contains", `"audit"`))
	}

	tests := []struct {
		name       string
		candidates func(*testing.T) candidate
		limits     Limits
		facts      func(*Facts)
		override   bool
		wantResult movements.Result
		wantReason string
		wantSpent  bool
	}{
		{
			name:       "inside the allowance answers nothing and takes one from the bucket",
			candidates: limit, limits: Limits{2: 1},
			wantResult: "", wantSpent: true,
		},
		{
			name:       "a warn is a green light with a sentence",
			candidates: warn,
			wantResult: movements.ResultWarned, wantReason: "Escort required in the yard",
		},
		{
			name:       "a refusal is a refusal",
			candidates: block, facts: func(f *Facts) { f.Purpose = "audit of the ledger" },
			wantResult: movements.ResultDenied, wantReason: "Booked through the office",
		},
		{
			// The service has no access bundle here, which is what a failed permission read
			// answers as: the guard keeps his red light and asks a supervisor.
			name:       "a waiver nobody can be checked against does not lift the refusal",
			candidates: block, facts: func(f *Facts) { f.Purpose = "audit of the ledger" },
			override:   true,
			wantResult: movements.ResultDenied, wantReason: "Booked through the office",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := factsAt(tc.facts)
			v := evaluate([]candidate{tc.candidates(t)}, f, tc.limits)
			q := movements.PolicyQuery{}
			q.TenantID, q.PassNo = 1, f.PassNo
			q.Override.Requested = tc.override

			d := (&Service{}).settle(context.Background(), v, q)
			if d.Result != tc.wantResult {
				t.Errorf("result %q, want %q", d.Result, tc.wantResult)
			}
			if d.Reason != tc.wantReason {
				t.Errorf("reason %q, want %q", d.Reason, tc.wantReason)
			}
			if len(d.Fired) != 1 {
				t.Fatalf("%d firings, want the one rule that was walked", len(d.Fired))
			}
			spent := d.Fired[0].Quota != nil && d.Fired[0].Quota.Spent
			if spent != tc.wantSpent {
				t.Errorf("allowance spent = %v, want %v", spent, tc.wantSpent)
			}
		})
	}
}
