package numbering

import (
	"context"
	"time"

	"accessnow/internal/platform/clock"
	"accessnow/internal/platform/db"
)

type Request struct {
	TenantID  int64
	FormatKey string
	Category  string
	Module    string
	Timezone  string
	At        time.Time
}

type Allocator struct{ repo *Repository }

func NewAllocator(repo *Repository) *Allocator { return &Allocator{repo} }

// Formats and SaveFormat expose the admin side of numbering through the allocator Core
// already holds, so passes does not need a second numbering dependency just to show the
// scheme its own numbers come from.
func (a *Allocator) Formats(ctx context.Context, tenantID int64) ([]Format, error) {
	return a.repo.Formats(ctx, tenantID)
}

func (a *Allocator) Format(ctx context.Context, tenantID int64, formatKey string) (Format, error) {
	return a.repo.Format(ctx, tenantID, formatKey)
}

func (a *Allocator) SaveFormat(ctx context.Context, format Format) error {
	return a.repo.SaveFormat(ctx, format)
}

// Next hands out one pass number. Call it on the transaction that inserts the pass: a
// rolled back issue would otherwise burn a number, which is exactly what a gap_required
// format forbids.
func (a *Allocator) Next(ctx context.Context, c db.Conn, req Request) (string, error) {
	repo := a.repo.In(c)
	formatKey := req.FormatKey
	if formatKey == "" {
		formatKey = DefaultFormatKey
	}
	format, err := repo.ActiveFormat(ctx, req.TenantID, formatKey)
	if err != nil {
		return "", err
	}

	at := req.At
	if at.IsZero() {
		at = time.Now().UTC()
	}
	loc := clock.Location(req.Timezone)

	value, err := repo.Claim(ctx, req.TenantID, format.ID, clock.PeriodKey(format.ResetPeriod, at, loc))
	if err != nil {
		return "", err
	}
	return format.Render(value, Values{Category: req.Category, Module: req.Module, At: at}, loc)
}
