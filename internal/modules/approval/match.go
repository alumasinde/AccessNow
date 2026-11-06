package approval

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"accessnow/internal/core/passes"
)

// Routing is the answer to "which of this tenant's rules covers this pass". It is a value
// rather than four return strings because every caller of match needs all of it together:
// the flow for the audit row, the frozen steps for the chain, the site clock for the
// after-hours decision.
type Routing struct {
	Flow     Flow
	Steps    []FlowStep
	Outside  bool
	At       time.Time
	Required bool
}

// None reports that nothing in the tenant's configuration asks anybody about this pass. The
// desk keeps deciding it, exactly as it did before this module existed.
func (r Routing) None() bool { return !r.Required || len(r.Steps) == 0 }

// match picks the flow a pass is worked under. A tenant can write rules that overlap - the
// same pass type, two value bands, one of them after hours - so the answer is not "the first
// row that fits" but "the rule that fits and was written to fit tighter than the others".
// Priority is the tenant's own tie-breaker and the only one this code promises.
func (s *Service) match(ctx context.Context, p passes.Pass, at time.Time) (Routing, error) {
	flows, err := s.repo.ListFlows(ctx, p.TenantID)
	if err != nil {
		return Routing{}, err
	}
	if len(flows) == 0 {
		return Routing{}, nil
	}

	site, err := s.repo.SiteWindowFor(ctx, p.TenantID, p.SiteID)
	if err != nil {
		// A pass whose site is gone cannot be routed. Saying so beats approving it under a
		// rule nobody checked.
		return Routing{}, err
	}
	outside := site.OutsideHours(at)

	// The list is already in priority order, so the first fit wins - unless the fit so far
	// is a default and a later, specific rule also applies. A tenant's "security manager
	// after hours" has to beat their own catch-all, however it was numbered.
	var pick *Flow
	for i, flow := range flows {
		if !flow.Active() || !fits(flow, p, outside) {
			continue
		}
		if pick == nil || (pick.IsDefault && !flow.IsDefault) {
			pick = &flows[i]
		}
	}
	if pick == nil {
		return Routing{}, nil
	}

	steps, err := s.repo.Steps(ctx, p.TenantID, pick.ID)
	if err != nil {
		return Routing{}, err
	}
	return Routing{Flow: *pick, Steps: steps, Outside: outside, At: at, Required: true}, nil
}

// fits is the conditional routing rule in one function: type, category, department, site,
// declared value and the clock, each one only narrowing what it actually says.
func fits(flow Flow, p passes.Pass, outside bool) bool {
	if flow.OutsideHoursOnly && !outside {
		return false
	}
	if flow.PassTypeID != nil && *flow.PassTypeID != p.PassTypeID {
		return false
	}
	if flow.CategoryKey != nil && *flow.CategoryKey != p.CategoryKey {
		return false
	}
	if flow.DepartmentID != nil && (p.DepartmentID == nil || *flow.DepartmentID != *p.DepartmentID) {
		return false
	}
	if flow.SiteID != nil && *flow.SiteID != p.SiteID {
		return false
	}
	return fitsValue(flow, p.ValueAmount)
}

// fitsValue is the "goods above a certain value" rule. A flow with a band cannot claim a
// pass whose value was never declared: an undeclared laptop is not a cheap laptop, and a
// tenant who set a threshold expects the threshold to be about money, not about blanks.
func fitsValue(flow Flow, value *float64) bool {
	if flow.MinValue == nil && flow.MaxValue == nil {
		return true
	}
	if value == nil {
		return false
	}
	if flow.MinValue != nil && *value < *flow.MinValue {
		return false
	}
	return flow.MaxValue == nil || *value <= *flow.MaxValue
}

// snapshot is the step list frozen into the chain. Storing it is what lets a tenant edit a
// flow at noon without rewriting who approved a pass at nine.
func snapshotOf(steps []FlowStep) (string, error) {
	if len(steps) == 0 {
		return "", nil
	}
	raw, err := json.Marshal(steps)
	if err != nil {
		return "", fmt.Errorf("freeze approval flow steps: %w", err)
	}
	return string(raw), nil
}
