package integrations

import (
	"testing"
	"time"
)

// TestKeyScopesAreExact locks the scope rule: a scope grants exactly itself, or everything when
// it is '*'. A prefix match would turn visitor.view into visitor.view_everything.
func TestKeyScopesAreExact(t *testing.T) {
	key := APIKey{Scopes: `["visitor.view","core.pass.view"]`}
	if !key.Allows("visitor.view") || !key.Allows("core.pass.view") {
		t.Fatal("a granted scope must be allowed")
	}
	if key.Allows("visitor.view_all") || key.Allows("core.pass") || key.Allows("") {
		t.Fatal("a scope must not be matched by prefix or as a missing value")
	}
	wildcard := APIKey{Scopes: `["*"]`}
	if !wildcard.Allows("anything.at_all") {
		t.Fatal("the wildcard scope should grant everything")
	}
	if (APIKey{}).Allows("visitor.view") {
		t.Fatal("a key with no scopes grants nothing")
	}
}

// TestDecodeRecordsShapes covers the two bodies a remote system may answer with, and refuses a
// body that is neither rather than reporting a successful sync of zero records.
func TestDecodeRecordsShapes(t *testing.T) {
	list, next, err := decodeRecords([]byte(`[{"id":1},{"id":2}]`))
	if err != nil || len(list) != 2 || next != "" {
		t.Fatalf("bare array: %v %d %q", err, len(list), next)
	}
	envelope, cursor, err := decodeRecords([]byte(`{"items":[{"id":"a"}],"next":"c2"}`))
	if err != nil || len(envelope) != 1 || cursor != "c2" {
		t.Fatalf("envelope: %v %d %q", err, len(envelope), cursor)
	}
	for _, body := range []string{``, `{}`, `{"data":[]}`, `not json`} {
		if _, _, err := decodeRecords([]byte(body)); err == nil {
			t.Fatalf("%q should be refused rather than read as an empty sync", body)
		}
	}
}

func TestRecordFieldsReadStringAndNumber(t *testing.T) {
	record := []byte(`{"id":42,"code":"HR-7","nested":{"a":1}}`)
	if got := recordString(record, "id"); got != "42" {
		t.Fatalf("numeric id = %q, want 42", got)
	}
	if got := recordString(record, "code"); got != "HR-7" {
		t.Fatalf("code = %q", got)
	}
	if got := recordString(record, "nested"); got != "" {
		t.Fatalf("an object is not a scalar id: %q", got)
	}
	if got := recordString(record, "missing"); got != "" {
		t.Fatalf("missing field = %q", got)
	}
}

// TestBuildSystemRefusesUnsafeTargets is the security contract of a sync: https only, no private
// address, a credential reference that cannot name an unrelated environment value, and no
// outbound push for an entity this module cannot read.
func TestBuildSystemRefusesUnsafeTargets(t *testing.T) {
	base := ExternalSystemRequest{
		Kind: "hr", Name: "People", BaseURL: "https://hr.example.com",
		AuthType: "bearer", CredentialsRef: "ACCESSNOW_SYNC_HR", EntityType: "staff",
		SyncDirection: "in",
	}
	if _, err := buildSystem(base, ExternalSystem{}); err != nil {
		t.Fatalf("a valid system was refused: %v", err)
	}

	cases := map[string]func(*ExternalSystemRequest){
		"http":           func(r *ExternalSystemRequest) { r.BaseURL = "http://hr.example.com" },
		"loopback":       func(r *ExternalSystemRequest) { r.BaseURL = "https://127.0.0.1" },
		"localhost":      func(r *ExternalSystemRequest) { r.BaseURL = "https://localhost" },
		"userinfo":       func(r *ExternalSystemRequest) { r.BaseURL = "https://u:p@hr.example.com" },
		"wrong ref":      func(r *ExternalSystemRequest) { r.CredentialsRef = "DB_PASSWORD" },
		"missing ref":    func(r *ExternalSystemRequest) { r.CredentialsRef = "" },
		"unknown entity": func(r *ExternalSystemRequest) { r.EntityType = "payroll" },
		"outbound vehicle": func(r *ExternalSystemRequest) {
			r.SyncDirection = "out"
			r.EntityType = "vehicle"
		},
		"bad path": func(r *ExternalSystemRequest) { r.Path = "/../admin" },
	}
	for name, mutate := range cases {
		req := base
		mutate(&req)
		if _, err := buildSystem(req, ExternalSystem{}); err == nil {
			t.Fatalf("%s should be refused", name)
		}
	}
}

func TestSyncRetryLadder(t *testing.T) {
	cases := []struct {
		attempt int
		wait    time.Duration
		retry   bool
	}{
		{0, 0, false},
		{1, time.Minute, true},
		{2, 5 * time.Minute, true},
		{3, 20 * time.Minute, true},
		{4, 0, false},
		{9, 0, false},
	}
	for _, tc := range cases {
		wait, retry := syncRetryDelay(tc.attempt)
		if wait != tc.wait || retry != tc.retry {
			t.Fatalf("attempt %d: wait=%v retry=%v, want %v/%v", tc.attempt, wait, retry,
				tc.wait, tc.retry)
		}
	}
}

func TestSystemDue(t *testing.T) {
	frequency := 15
	system := ExternalSystem{SyncFrequencyMinutes: &frequency}
	if !systemDue(system, time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)) {
		t.Fatal("a system that never synced is due")
	}
	last := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	system.LastSyncAt = &last
	if systemDue(system, last.Add(10*time.Minute)) {
		t.Fatal("a system inside its interval is not due")
	}
	if !systemDue(system, last.Add(15*time.Minute)) {
		t.Fatal("a system at its interval is due")
	}
	if systemDue(ExternalSystem{}, last.Add(time.Hour)) {
		t.Fatal("a system without a frequency never runs on a schedule")
	}
}
