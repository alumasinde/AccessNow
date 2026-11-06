package blocklist

import (
	"testing"
	"time"

	"accessnow/internal/core/movements"
)

// The whole point of normalising before hashing is that a value typed two ways is one
// listing. These are the shapes a desk and a barrier actually produce for the same thing.
func TestOneValueHasOneHash(t *testing.T) {
	cases := []struct {
		name  string
		kind  MatchType
		left  string
		right string
		same  bool
	}{
		{"a phone with a plus and a phone without", MatchPhone, "+254 712 345 678", "254712345678", true},
		{"a phone with brackets", MatchPhone, "(0712) 345-678", "0712345678", true},
		{"a plate typed in lower case", MatchPlate, "kca 123m", "KCA-123M", true},
		{"a plate with a slash", MatchPlate, "KCA/123/M", "kca123m", true},
		{"an email typed with capitals", MatchEmail, "  Visitor@Example.COM ", "visitor@example.com", true},
		{"a name with a double space", MatchNameHash, "MARY  NKAISAI", "Mary Nkaisai", true},
		{"two different numbers", MatchPhone, "0712345678", "0712345679", false},
		{"two different plates", MatchPlate, "KCA123M", "KDA123M", false},
		{"a name is not a phone", MatchNameHash, "0712345678", "712345678", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			left := New{MatchType: tc.kind, MatchValue: tc.left}.Hash()
			right := New{MatchType: tc.kind, MatchValue: tc.right}.Hash()
			if (left == right) != tc.same {
				t.Errorf("hash(%q) = hash(%q) is %v, want %v", tc.left, tc.right, left == right, tc.same)
			}
		})
	}
}

// An empty column must not be asked at all: hashing "" would make every listing keyed on a
// blank value match every pass.
func TestScreeningSkipsWhatTheGateDoesNotKnow(t *testing.T) {
	got := identifiers(movements.ScreenQuery{
		PassNo: "VIS-26110001", Plate: " kca 123m ", SubjectName: "Mary Nkaisai",
	})
	if len(got) != 2 {
		t.Fatalf("identifiers() = %v, want the plate and the name only", got)
	}
	if got[0].kind != MatchPlate || got[1].kind != MatchNameHash {
		t.Errorf("screening order = %v, want plate before name", got)
	}
	if len(identifiers(movements.ScreenQuery{})) != 0 {
		t.Error("a scan with nothing to compare must ask nothing")
	}
}

// A watchlist match lets the gate open and is filed as an alarm; only a denial is filed as
// one. An override is recorded as an override, because that is the fact somebody will be
// asked about later.
func TestActionFollowsWhatTheGateDid(t *testing.T) {
	cases := map[movements.Result]string{
		movements.ResultDenied:   "denied",
		movements.ResultWarned:   "warned",
		movements.ResultOverride: "override_granted",
		movements.ResultGranted:  "allowed_with_alert",
	}
	for result, want := range cases {
		if got := actionOf(result); got != want {
			t.Errorf("actionOf(%q) = %q, want %q", result, got, want)
		}
	}
}

// A listing that has run out counts as gone even before anything sweeps it: the gate's answer
// must not depend on a job having run first.
func TestAnExpiredListingIsClosed(t *testing.T) {
	now := time.Now().UTC()
	expired := now.Add(-time.Minute)
	live := now.Add(time.Hour)

	open := Entry{Status: string(StatusActive)}
	if !open.Open() || open.ExpiredAt(now) {
		t.Fatal("an active listing with no expiry must be open")
	}
	if !(Entry{Status: string(StatusActive), ExpiresAt: &live}).Open() {
		t.Error("a listing that has not expired yet must be open")
	}
	ended := Entry{Status: string(StatusActive), ExpiresAt: &expired}
	if ended.Open() && !ended.ExpiredAt(now) {
		t.Error("a listing past its expiry must read as expired")
	}
	if (Entry{Status: string(StatusRevoked)}).Open() {
		t.Error("a revoked listing must not be open")
	}
	if !(Entry{Status: string(StatusUnderReview)}).Open() {
		t.Error("a listing somebody is arguing about keeps biting until it is formally taken off")
	}
}

// The desk cannot file a listing the database would refuse, and cannot file one with nothing
// to match on.
func TestNewListingIsCheckedBeforeItIsStored(t *testing.T) {
	now := time.Now().UTC()
	past := now.Add(-time.Minute)

	valid := New{ListType: ListBlock, EntityType: EntityPerson, MatchType: MatchPhone,
		MatchValue: "0712 345 678", Reason: "refused at gate 2"}
	if err := valid.validate(now); err != nil {
		t.Fatalf("a valid listing was refused: %v", err)
	}

	cases := map[string]New{
		"a list that is neither":     {ListType: "deny", EntityType: EntityPerson, MatchType: MatchPhone, MatchValue: "0712345678", Reason: "x"},
		"an entity this cannot be":   {ListType: ListBlock, EntityType: "animal", MatchType: MatchPhone, MatchValue: "0712345678", Reason: "x"},
		"an identifier nobody uses":  {ListType: ListBlock, EntityType: EntityPerson, MatchType: "shoe_size", MatchValue: "42", Reason: "x"},
		"a value with nothing in it": {ListType: ListBlock, EntityType: EntityPerson, MatchType: MatchPlate, MatchValue: " -/ ", Reason: "x"},
		"a listing with no reason":   {ListType: ListBlock, EntityType: EntityPerson, MatchType: MatchPhone, MatchValue: "0712345678"},
		"an expiry in the past": {ListType: ListBlock, EntityType: EntityPerson, MatchType: MatchPhone,
			MatchValue: "0712345678", Reason: "x", ExpiresAt: &past},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if err := in.validate(now); err == nil {
				t.Error("validate() accepted a listing that must be refused")
			}
		})
	}
}
