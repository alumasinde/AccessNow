package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// linkStore stands in for the repository's link table, so pull can be exercised without a
// database. It applies the same rule: a matching digest is not a change.
type linkStore struct {
	links map[string]string
}

func (l *linkStore) UpsertLink(_ context.Context, _, _ int64, _, external string,
	_ *int64, digest string, _ time.Time) (bool, bool, error) {
	if l.links == nil {
		l.links = map[string]string{}
	}
	if previous, ok := l.links[external]; ok {
		if previous == digest {
			return false, false, nil
		}
		l.links[external] = digest
		return false, true, nil
	}
	l.links[external] = digest
	return true, false, nil
}

type recordList struct {
	rows []json.RawMessage
}

func (r recordList) LocalRecords(_ context.Context, _ int64, _ string,
	_ int) ([]json.RawMessage, error) {
	return r.rows, nil
}

// receiver makes the sync client trust one httptest server. The production transport, which
// refuses private addresses, is still what TestSyncClientRefusesLoopback exercises.
func receiver(t *testing.T, srv *httptest.Server) {
	t.Helper()
	previous := syncTransport
	syncTransport = func(time.Duration) http.RoundTripper { return srv.Client().Transport }
	t.Cleanup(func() { syncTransport = previous })
}

func syncTestService() *Service {
	return &Service{now: func() time.Time {
		return time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	}}
}

func refName(value string) *string { return &value }

func testSystem(url string) ExternalSystem {
	return ExternalSystem{ID: 7, TenantID: 1, Name: "People", BaseURL: refName(url),
		AuthType: "bearer", CredentialsRef: refName("ACCESSNOW_SYNC_TEST")}
}

// TestPullAgainstReceiver drives the worker's inbound half through a receiver that answers with
// an envelope, a cursor and one record that has no external id.
func TestPullAgainstReceiver(t *testing.T) {
	var seenAuth, seenPath, seenCursor string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		seenPath = r.URL.Path
		seenCursor = r.URL.Query().Get("cursor")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"items":[{"id":"e1","updated_at":"2026-03-01"},`+
			`{"id":2,"updated_at":"2026-03-02"},{"name":"no id"}],"next":"cursor-9"}`)
	}))
	defer srv.Close()
	receiver(t, srv)
	t.Setenv("ACCESSNOW_SYNC_TEST", "s3cret")

	store := &linkStore{}
	opts := systemOptions{Entity: "staff", Path: "/people", ExternalIDField: "id",
		CursorField: "updated_at"}
	job := SyncJob{ID: 1, TenantID: 1, SystemID: 7, EntityType: "staff",
		Direction: SystemIn, CursorValue: refName("cursor-1")}

	seen, created, updated, failed, cursor, err := syncTestService().pull(
		context.Background(), testSystem(srv.URL), opts, job, store)
	if err != nil {
		t.Fatalf("pull: %v", err)
	}
	if seen != 2 || created != 2 || updated != 0 || failed != 1 {
		t.Fatalf("seen/created/updated/failed = %d/%d/%d/%d", seen, created, updated, failed)
	}
	if cursor != "2026-03-02" {
		t.Fatalf("cursor = %q, want the last record's cursor field", cursor)
	}
	if seenAuth != "Bearer s3cret" {
		t.Fatalf("authorization = %q", seenAuth)
	}
	if seenPath != "/people" {
		t.Fatalf("path = %q", seenPath)
	}
	if seenCursor != "cursor-1" {
		t.Fatalf("cursor query = %q, want the job's cursor", seenCursor)
	}
	if len(store.links) != 2 {
		t.Fatalf("links written = %d, want 2", len(store.links))
	}

	// A second pull over unchanged records must not report them as updates.
	_, _, updated, _, _, err = syncTestService().pull(context.Background(),
		testSystem(srv.URL), opts, job, store)
	if err != nil || updated != 0 {
		t.Fatalf("unchanged pull: updated=%d err=%v", updated, err)
	}
}

// TestPullRetriedAfterFailure is what the scheduler does with a failed job: the next pass calls
// the same receiver again, and the records that arrive then are linked.
func TestPullRetriedAfterFailure(t *testing.T) {
	var attempts int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			http.Error(w, "receiver is restarting", http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `[{"id":"e1"},{"id":"e2"}]`)
	}))
	defer srv.Close()
	receiver(t, srv)
	t.Setenv("ACCESSNOW_SYNC_TEST", "s3cret")

	store := &linkStore{}
	opts := systemOptions{Entity: "staff", Path: "/people", ExternalIDField: "id"}
	job := SyncJob{ID: 2, TenantID: 1, SystemID: 7, EntityType: "staff", Direction: SystemIn}
	service := syncTestService()

	if _, _, _, _, _, err := service.pull(context.Background(), testSystem(srv.URL),
		opts, job, store); err == nil {
		t.Fatal("a 503 must be reported as a failed sync")
	}
	if len(store.links) != 0 {
		t.Fatal("a failed call must not link anything")
	}

	seen, created, _, failed, _, err := service.pull(context.Background(),
		testSystem(srv.URL), opts, job, store)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if seen != 2 || created != 2 || failed != 0 || len(store.links) != 2 {
		t.Fatalf("retry: seen=%d created=%d failed=%d links=%d", seen, created, failed,
			len(store.links))
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

// TestPushAgainstReceiver drives the outbound half: the local register arrives under an items
// envelope with the credential the system names.
func TestPushAgainstReceiver(t *testing.T) {
	var method, auth, entity string
	var items int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		auth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			Entity string            `json:"entity"`
			Items  []json.RawMessage `json:"items"`
		}
		_ = json.Unmarshal(body, &payload)
		entity = payload.Entity
		items = len(payload.Items)
		w.WriteHeader(http.StatusAccepted)
		fmt.Fprint(w, `{"accepted":true}`)
	}))
	defer srv.Close()
	receiver(t, srv)
	t.Setenv("ACCESSNOW_SYNC_TEST", "s3cret")

	rows := []json.RawMessage{json.RawMessage(`{"id":3,"first_name":"A"}`),
		json.RawMessage(`{"id":4,"first_name":"B"}`)}
	opts := systemOptions{Entity: "staff", Path: "/people"}
	seen, updated, err := syncTestService().push(context.Background(),
		testSystem(srv.URL), opts, recordList{rows: rows})
	if err != nil {
		t.Fatalf("push: %v", err)
	}
	if seen != 2 || updated != 2 {
		t.Fatalf("seen/updated = %d/%d", seen, updated)
	}
	if method != http.MethodPost || entity != "staff" || items != 2 {
		t.Fatalf("method=%s entity=%q items=%d", method, entity, items)
	}
	if auth != "Bearer s3cret" {
		t.Fatalf("authorization = %q", auth)
	}
}

func TestPushRetriedAfterFailure(t *testing.T) {
	var attempts int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&attempts, 1) == 1 {
			http.Error(w, "try again", http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	receiver(t, srv)
	t.Setenv("ACCESSNOW_SYNC_TEST", "s3cret")

	opts := systemOptions{Entity: "department", Path: "/departments"}
	source := recordList{rows: []json.RawMessage{json.RawMessage(`{"id":1,"name":"Ops"}`)}}
	service := syncTestService()

	if _, _, err := service.push(context.Background(), testSystem(srv.URL), opts,
		source); err == nil {
		t.Fatal("a 502 must be reported as a failed push")
	}
	if seen, updated, err := service.push(context.Background(), testSystem(srv.URL),
		opts, source); err != nil || seen != 1 || updated != 1 {
		t.Fatalf("retry: seen=%d updated=%d err=%v", seen, updated, err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

// TestSyncClientRefusesLoopback keeps the SSRF guard honest: even with a valid https URL, the
// dialer refuses a private address. This uses the real transport, not the test seam.
func TestSyncClientRefusesLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("request reached a loopback receiver")
	}))
	defer srv.Close()

	_, err := syncHTTPClient(time.Second).Get(srv.URL)
	if err == nil {
		t.Fatal("a loopback address must be refused")
	}
	if !strings.Contains(err.Error(), "private address") {
		t.Fatalf("error = %v, want a private-address refusal", err)
	}
}
