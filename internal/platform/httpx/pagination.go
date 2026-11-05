package httpx

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"accessnow/internal/platform/errs"
)

// Cursor is a keyset position, not an offset: a gate list pulled twice a minute must
// not skip or repeat rows because someone else inserted in between.
type Cursor struct {
	At time.Time
	ID int64
}

type Page struct {
	Limit  int
	Cursor *Cursor
}

type Envelope[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
	HasMore    bool   `json:"has_more"`
}

// ParsePage reads ?limit= and ?cursor=. Repositories fetch Limit+1 rows and hand the
// slice to Paginate, which trims and decides has_more.
func ParsePage(r *http.Request, fallback, max int) (Page, error) {
	query := r.URL.Query()
	limit := fallback
	if raw := query.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			return Page{}, errs.Invalid("limit must be a positive number", errs.InvalidField("limit", "not a number"))
		}
		if n > max {
			n = max
		}
		limit = n
	}

	page := Page{Limit: limit}
	if raw := query.Get("cursor"); raw != "" {
		cursor, err := DecodeCursor(raw)
		if err != nil {
			return Page{}, err
		}
		page.Cursor = &cursor
	}
	return page, nil
}

func Paginate[T any](items []T, limit int, cursorOf func(T) Cursor) Envelope[T] {
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	page := Envelope[T]{Items: items, HasMore: hasMore}
	if hasMore && len(items) > 0 {
		page.NextCursor = cursorOf(items[len(items)-1]).Encode()
	}
	return page
}

func (c Cursor) Encode() string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte(fmt.Sprintf("%d:%d", c.At.UTC().UnixMicro(), c.ID)))
}

func DecodeCursor(raw string) (Cursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return Cursor{}, errs.Invalid("cursor is not recognised", errs.InvalidField("cursor", "malformed"))
	}
	micros, id, found := strings.Cut(string(decoded), ":")
	if !found {
		return Cursor{}, errs.Invalid("cursor is not recognised", errs.InvalidField("cursor", "malformed"))
	}
	micro, err := strconv.ParseInt(micros, 10, 64)
	if err != nil {
		return Cursor{}, errs.Invalid("cursor is not recognised", errs.InvalidField("cursor", "malformed"))
	}
	number, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return Cursor{}, errs.Invalid("cursor is not recognised", errs.InvalidField("cursor", "malformed"))
	}
	return Cursor{At: time.UnixMicro(micro).UTC(), ID: number}, nil
}
