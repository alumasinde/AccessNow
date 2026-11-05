package httpx

import (
	"net/http/httptest"
	"testing"
	"time"

	"accessnow/internal/platform/errs"
)

type row struct {
	At time.Time
	ID int64
}

func cursorOf(r row) Cursor { return Cursor{At: r.At, ID: r.ID} }

func TestCursorRoundTrip(t *testing.T) {
	want := Cursor{At: time.Date(2026, 11, 5, 9, 15, 30, 123000, time.UTC), ID: 90210}

	got, err := DecodeCursor(want.Encode())
	if err != nil {
		t.Fatalf("DecodeCursor: %v", err)
	}
	if !got.At.Equal(want.At) || got.ID != want.ID {
		t.Errorf("round trip = %+v, want %+v", got, want)
	}
}

// A cursor crossing a zone boundary must still compare equal: it is stored as UTC micros.
func TestCursorNormalisesToUTC(t *testing.T) {
	zone := time.FixedZone("EAT", 3*60*60)
	at := time.Date(2026, 11, 5, 12, 0, 0, 0, zone)

	got, err := DecodeCursor(Cursor{At: at, ID: 1}.Encode())
	if err != nil {
		t.Fatalf("DecodeCursor: %v", err)
	}
	if !got.At.Equal(at) || got.At.Location() != time.UTC {
		t.Errorf("cursor = %v (%v), want %v in UTC", got.At, got.At.Location(), at)
	}
}

func TestDecodeCursorRejectsGarbage(t *testing.T) {
	for _, raw := range []string{"not base64 !!", "MTIzNA", "bm8tY29sb24"} {
		if _, err := DecodeCursor(raw); errs.CodeOf(err) != errs.CodeInvalidRequest {
			t.Errorf("DecodeCursor(%q) = %v, want invalid_request", raw, err)
		}
	}
}

func TestPaginate(t *testing.T) {
	at := time.Date(2026, 11, 5, 9, 0, 0, 0, time.UTC)
	second := at.Add(time.Second)

	page := Paginate([]row{{at, 1}, {second, 2}, {at.Add(2 * time.Second), 3}}, 2, cursorOf)
	if !page.HasMore || len(page.Items) != 2 {
		t.Fatalf("has_more=%v len=%d, want true/2", page.HasMore, len(page.Items))
	}
	last, err := DecodeCursor(page.NextCursor)
	if err != nil {
		t.Fatalf("next cursor: %v", err)
	}
	if last.ID != 2 {
		t.Errorf("next cursor id = %d, want 2", last.ID)
	}

	full := Paginate([]row{{at, 1}, {second, 2}}, 2, cursorOf)
	if full.HasMore || full.NextCursor != "" {
		t.Errorf("exact page reports more: %+v", full)
	}
}

func TestParsePage(t *testing.T) {
	request := httptest.NewRequest("GET", "/api/v1/passes?limit=500&cursor="+Cursor{At: time.UnixMilli(1), ID: 7}.Encode(), nil)
	page, err := ParsePage(request, 25, 200)
	if err != nil {
		t.Fatalf("ParsePage: %v", err)
	}
	if page.Limit != 200 {
		t.Errorf("limit = %d, want the max of 200", page.Limit)
	}
	if page.Cursor == nil || page.Cursor.ID != 7 {
		t.Errorf("cursor = %+v, want id 7", page.Cursor)
	}

	defaulted, err := ParsePage(httptest.NewRequest("GET", "/api/v1/passes", nil), 25, 200)
	if err != nil || defaulted.Limit != 25 || defaulted.Cursor != nil {
		t.Errorf("bare request = %+v, err %v", defaulted, err)
	}

	for _, target := range []string{"/p?limit=0", "/p?limit=abc", "/p?cursor=!!!"} {
		if _, err := ParsePage(httptest.NewRequest("GET", target, nil), 25, 200); errs.CodeOf(err) != errs.CodeInvalidRequest {
			t.Errorf("%s = %v, want invalid_request", target, err)
		}
	}
}
