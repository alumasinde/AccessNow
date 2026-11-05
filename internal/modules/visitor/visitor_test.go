package visitor

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"accessnow/internal/platform/errs"
	"accessnow/internal/platform/httpx"
)

// The queue is the busiest screen in the module, so a typo in a filter must be a 400
// naming the field rather than an empty list the desk reads as "no visitors today".
func TestParseList(t *testing.T) {
	request := httptest.NewRequest("GET", "/api/v1/visits?status=pending&host_id=4&visitor_id=9&limit=10", nil)
	filter, page, err := ParseList(request)
	if err != nil {
		t.Fatalf("ParseList: %v", err)
	}
	if filter.Status != string(StatusPending) || filter.HostUserID != 4 || filter.VisitorID != 9 {
		t.Errorf("filter = %+v", filter)
	}
	if page.Limit != 10 || page.Cursor != nil {
		t.Errorf("page = %+v", page)
	}

	_, _, err = ParseList(httptest.NewRequest("GET", "/api/v1/visits?status=on_side", nil))
	if errs.CodeOf(err) != errs.CodeInvalidRequest {
		t.Errorf("unknown status = %v, want invalid_request", err)
	}

	_, _, err = ParseList(httptest.NewRequest("GET", "/api/v1/visits?host_id=abc", nil))
	if errs.CodeOf(err) != errs.CodeInvalidRequest {
		t.Errorf("non-numeric host = %v, want invalid_request", err)
	}
}

func TestValidStatusCoversTheCheckConstraint(t *testing.T) {
	for _, status := range []Status{StatusPending, StatusApproved, StatusRejected, StatusCancelled,
		StatusOnSite, StatusCompleted, StatusExpired, StatusNoShow} {
		if !validStatus(string(status)) {
			t.Errorf("%q is a visit status but the filter refuses it", status)
		}
	}
	for _, status := range []string{"", "draft", "PENDING", "void"} {
		if validStatus(status) {
			t.Errorf("%q accepted but chk_visits_status would reject it", status)
		}
	}
}

func TestVisitOpen(t *testing.T) {
	for _, status := range []Status{StatusPending, StatusApproved, StatusOnSite, StatusExpired} {
		if !(Visit{Status: string(status)}).Open() {
			t.Errorf("%q is a visit the desk still has to finish", status)
		}
	}
	for _, status := range []Status{StatusCompleted, StatusCancelled, StatusRejected, StatusNoShow} {
		if (Visit{Status: string(status)}).Open() {
			t.Errorf("%q is closed but reported open", status)
		}
	}
}

func TestTypeKeyFor(t *testing.T) {
	if got := typeKeyFor("walk_in"); got != "visitor_walk_in" {
		t.Errorf("walk_in -> %q", got)
	}
	for _, origin := range []string{"pre_registered", "online", "api", ""} {
		if got := typeKeyFor(origin); got != "visitor_pre_registered" {
			t.Errorf("%q -> %q, want the pre-registered type", origin, got)
		}
	}
}

// The person view carries no document number: the guard reads it from the ID in hand,
// and the retention job blanks the column. Pinning the JSON keeps a later field add from
// quietly leaking it.
func TestVisitorViewLeaksNoDocument(t *testing.T) {
	body, err := json.Marshal(viewOfVisitor(Visitor{
		ID: 1, FirstName: "Alice", LastName: "Zulu", IDType: strPtr("passport"),
		IDNumber: strPtr("A1234567"), Status: "active", VisitCount: 3,
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"id_number", "A1234567", "id_type"} {
		if strings.Contains(string(body), forbidden) {
			t.Errorf("visitor view leaked %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(string(body), `"name":"Alice Zulu"`) {
		t.Errorf("visitor view lost the name: %s", body)
	}
}

func TestDecisionRequestAction(t *testing.T) {
	if err := httpx.Validate(DecisionRequest{Action: "approve"}); err != nil {
		t.Fatalf("approve refused: %v", err)
	}
	err := httpx.Validate(DecisionRequest{Action: "maybe"})
	if errs.CodeOf(err) != errs.CodeInvalidRequest {
		t.Fatalf("invented action accepted: %v", err)
	}
}

func strPtr(value string) *string { return &value }
