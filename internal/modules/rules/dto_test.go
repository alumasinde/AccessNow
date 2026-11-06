package rules

import (
	"net/http/httptest"
	"testing"
)

// The parsers are where a desk's typo becomes somebody's 400, so they are tested apart from
// the engine: an absent filter must mean "no filter", and only a value that is present and
// wrong is an error.

func TestParseRulesFilters(t *testing.T) {
	filter, page, err := ParseRules(httptest.NewRequest("GET", "/api/v1/rules", nil))
	if err != nil {
		t.Fatalf("an unfiltered request failed to parse: %v", err)
	}
	if filter.SiteID != 0 || filter.Action != "" || filter.CategoryKey != "" {
		t.Errorf("empty query read as %+v, want no filters", filter)
	}
	if filter.IncludeQuiet {
		t.Error("retired rules are asked for by name, not by default")
	}
	if page.Limit != defaultPage {
		t.Errorf("page limit %d, want the module default %d", page.Limit, defaultPage)
	}

	for _, query := range []string{
		"?action=frozen", "?stage=midnight", "?site_id=0", "?include_retired=maybe",
	} {
		if _, _, err := ParseRules(httptest.NewRequest("GET", "/api/v1/rules"+query, nil)); err == nil {
			t.Errorf("%s parsed cleanly, want a 400 worth showing the desk", query)
		}
	}

	filter, _, err = ParseRules(httptest.NewRequest("GET",
		"/api/v1/rules?action=limit&site_id=3&include_retired=true", nil))
	if err != nil {
		t.Fatalf("a filtered request failed to parse: %v", err)
	}
	if filter.Action != "limit" || filter.SiteID != 3 || !filter.IncludeQuiet {
		t.Errorf("filtered query read as %+v", filter)
	}
}

func TestParseEvaluationsFeed(t *testing.T) {
	filter, _, err := ParseEvaluations(httptest.NewRequest("GET",
		"/api/v1/rules/evaluations?rule_id=7&outcome=override_granted&matched=false", nil))
	if err != nil {
		t.Fatalf("feed filters failed to parse: %v", err)
	}
	if filter.RuleID != 7 || filter.Outcome != OutcomeOverride {
		t.Errorf("feed read as %+v", filter)
	}
	if filter.Matched == nil || *filter.Matched {
		t.Error("matched=false did not survive the parse")
	}

	if _, _, err := ParseEvaluations(httptest.NewRequest("GET",
		"/api/v1/rules/evaluations?outcome=no_match", nil)); err == nil {
		t.Error("no_match is not a row this module writes, so it must not be a filter")
	}
}
