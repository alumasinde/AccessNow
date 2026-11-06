package offline

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"accessnow/internal/core/passes"
)

func cacheable(id int64, from, until *time.Time, entries int) passes.Cacheable {
	return passes.Cacheable{
		PassID: id, PassNo: "VIS-000000" + strconv.FormatInt(id, 10),
		CategoryKey: "visitor", ModuleKey: "visitor_management", Status: "active",
		ValidFrom: *from, ValidUntil: until, EntryCount: entries,
	}
}

// A refresh that changes nothing must send nothing, because the register is the biggest thing
// in the bundle and a tablet pulls it every quarter-hour. What the ledger writes is the whole
// set either way: "what does this device hold" has to be one question with one answer.
func TestAssembleSendsOnlyWhatChanged(t *testing.T) {
	from, to := tenFifteen, handedIn
	untilEndOfDay := handedIn.Add(6 * time.Hour)

	current := []passes.Cacheable{
		cacheable(1, &from, &untilEndOfDay, 0),
		cacheable(2, &from, &untilEndOfDay, 0),
	}
	// Pass 1 is exactly what the server would compute now, down to the reason and the clamped
	// start - which is the whole test: anything else about it is a change worth a re-send.
	kept := manifestOf(current[0], handedIn.Add(-time.Hour), to)
	kept.PushedAt = tenThirty
	held := []Manifest{
		kept,
		{PassID: 3, Reason: string(ReasonToday), ValidFrom: from, ValidUntil: untilEndOfDay},
	}

	send, stock, removed := assemble(current, held, handedIn.Add(-time.Hour), to)
	if len(send) != 1 || send[0].PassID != 2 {
		t.Fatalf("sent %v, want the one pass the device had never been given", send)
	}
	if len(removed) != 1 || removed[0] != 3 {
		t.Errorf("removed %v, want pass 3, which is no longer scannable", removed)
	}
	if len(stock) != 2 {
		t.Fatalf("stocked %d rows, want the whole set the device now holds", len(stock))
	}
	if !stock[0].PushedAt.Equal(tenThirty) {
		t.Errorf("the unchanged pass was restocked with pushed_at %v, want the time it first went out",
			stock[0].PushedAt)
	}
}

// A pull that changes nothing writes nothing: the epoch is the device's answer to "may I trust
// this", so moving it for an empty bundle would retire a cache that is still correct.
func TestAssembleOfAnUnchangedRegister(t *testing.T) {
	from, to := tenFifteen, handedIn
	until := from.Add(time.Hour)
	current := []passes.Cacheable{cacheable(1, &from, &until, 0)}
	held := []Manifest{{PassID: 1, Reason: string(ReasonToday), ValidFrom: from,
		ValidUntil: until, PushedAt: tenThirty}}

	send, stock, removed := assemble(current, held, from, to)
	if len(send) != 0 || len(removed) != 0 {
		t.Fatalf("send %v removed %v, want nothing to send and nothing to drop", send, removed)
	}
	if len(stock) != 1 {
		t.Errorf("stocked %v, want the pass kept on the record", stock)
	}
}

// The reason is why a pass is in the cache and why it will leave it, and a multi-entry pass is
// the row a stale cache hangs on longest, so it has to say so.
func TestReasonSaysWhyAPassIsInTheCache(t *testing.T) {
	horizon := handedIn.Add(cacheHorizon)
	beyond := handedIn.Add(cacheHorizon * 3)
	inside := handedIn.Add(time.Hour)
	until := &inside
	open := (*time.Time)(nil)

	cases := []struct {
		name string
		row  passes.Cacheable
		want CacheReason
	}{
		{"already scanned once", passes.Cacheable{EntryCount: 1, ValidUntil: until}, ReasonMultiEntry},
		{"no end printed", passes.Cacheable{ValidUntil: open}, ReasonRange},
		{"outlives the window", passes.Cacheable{ValidUntil: &beyond}, ReasonRange},
		{"lives and dies inside it", passes.Cacheable{ValidUntil: until}, ReasonToday},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := reasonOf(tc.row, horizon); got != tc.want {
				t.Errorf("reasonOf(%+v) = %s, want %s", tc.row, got, tc.want)
			}
		})
	}
}

// The window on the manifest is the pass's own, in both directions. Re-stamping the start with
// the pull's instant would make every row disagree with itself on the next pull, which sends
// the whole register out again every fifteen minutes and never lets an epoch settle. A pass with
// no end is the one row the server has to finish, and the date it invents must be a shift edge
// the next pull can repeat, and still after the pass starts.
func TestManifestWindowIsThePassesOwn(t *testing.T) {
	horizon := handedIn.Add(cacheHorizon)
	from := handedIn.Add(-4 * time.Hour)
	until := handedIn.Add(2 * time.Hour)

	row := cacheable(1, &from, &until, 0)
	got := manifestOf(row, handedIn, horizon)
	if !got.ValidFrom.Equal(from) {
		t.Errorf("valid_from = %v, want the pass's own start, not the pull", got.ValidFrom)
	}
	if !got.ValidUntil.Equal(until) {
		t.Errorf("valid_until = %v, want the pass's own expiry", got.ValidUntil)
	}

	open := cacheable(2, &from, nil, 0)
	if got := manifestOf(open, handedIn, horizon); !got.ValidUntil.Equal(horizon.Add(refreshAfter)) {
		t.Errorf("an open-ended pass was stocked to %v, want the refresh edge after the horizon",
			got.ValidUntil)
	}
	// Two pulls a quarter of the horizon apart must invent the same date, or the row is never
	// unchanged and the device is re-sent it every time.
	later := manifestOf(open, handedIn.Add(4*time.Minute), horizon.Add(4*time.Minute))
	if !later.ValidUntil.Equal(manifestOf(open, handedIn, horizon).ValidUntil) {
		t.Error("the stand-in moved between pulls, so no register can ever be reported unchanged")
	}
	// The table refuses a window that ends before it opens: a pass that starts late inside this
	// horizon still has to be given a drop-by date after its own start.
	late := cacheable(3, &horizon, nil, 0)
	if got := manifestOf(late, handedIn, horizon); !got.ValidUntil.After(got.ValidFrom) {
		t.Errorf("a pass starting at the horizon was stocked %v to %v, which the window check rejects",
			got.ValidFrom, got.ValidUntil)
	}
}

// The checksum is the one value a device can be handed to answer "did my cache change", so it
// covers every part that decides what a gate would do offline - and none of the times, which
// move on every pull and would make the answer always yes.
func TestChecksumCoversTheDecisionNotTheClock(t *testing.T) {
	rows := []CachedPass{{PassID: 1, PassNo: "VIS-01", ValidUntil: handedIn}}
	base := Bundle{Passes: rows, RefreshAfter: refreshAfter, HeldSince: handedIn, Version: 4}
	again := Bundle{Passes: rows, RefreshAfter: refreshAfter,
		HeldSince: handedIn.Add(15 * time.Minute), Version: 9}

	if checksum(base) != checksum(again) {
		t.Error("the same cache checksummed differently at a later time")
	}
	screening := Bundle{Passes: rows, Screening: []CachedListing{
		{ListType: "blocklist", MatchType: "plate", MatchValue: "KCA123M"}}}
	if checksum(base) == checksum(screening) {
		t.Error("a cache that gained a listing checksummed the same as one that did not")
	}
	categories := Bundle{Passes: rows, AllowedCategories: []string{"visitor"}}
	if checksum(base) == checksum(categories) {
		t.Error("a narrower gate checksummed the same as the whole gate")
	}
	if checksum(base) == "" {
		t.Error("checksum returned empty, which is the value a bundle with nothing in it has")
	}
}

// A bundle is what a tablet parses, so the seconds are seconds and every list is a list. A
// device that decodes null as "the server said nothing" and [] as "hold nothing" is a device
// that empties its cache on a quiet pull.
func TestBundleViewSaysWhatADeviceNeedsToHear(t *testing.T) {
	view := Bundle{DeviceID: 7, GateID: 3, Version: 12, RefreshAfter: refreshAfter,
		Fresh: true}.View()
	if view.RefreshAfter != 900 {
		t.Errorf("refresh_after_seconds = %d, want 900 for a quarter hour", view.RefreshAfter)
	}

	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatalf("a bundle does not encode: %v", err)
	}
	for _, want := range []string{`"passes":[]`, `"removed_pass_ids":[]`, `"screening":[]`,
		`"allowed_categories":[]`} {
		if !strings.Contains(string(encoded), want) {
			t.Errorf("bundle has no %s, so a device has to guess what null means", want)
		}
	}
}
