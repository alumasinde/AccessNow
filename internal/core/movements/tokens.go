// Token scanning is the seam for the token a device reads. It sits beside Screener, Policy and
// Eligibility and is bound the same way: Core holds an interface and never learns what a QR
// code, an NFC card or a plate camera is, and a tenant with the module off gets a refusal and
// the manual scan it always had.
package movements

import (
	"context"
	"time"

	"accessnow/internal/core/passes"
)

// Tokens turns what a scanner read into the pass it names, and files the attempt. Resolve is
// asked before anything is decided because the token *is* the pass: a barrier that cannot name
// one has nothing to write a movement against.
//
// A refusal is not an error. "That card was revoked" is an answer the guard acts on; an error
// means the register could not be read, and it is turned into a red light with a sentence that
// says so rather than a 500 the tablet cannot show.
type Tokens interface {
	Resolve(ctx context.Context, q TokenQuery) (TokenVerdict, error)
	Record(ctx context.Context, a Attempt) error
}

// TokenQuery is one read at one gate. The raw secret travels no further than this call: it is
// hashed by the module, and the hash is the only thing written down.
type TokenQuery struct {
	TenantID      int64
	SiteID        int64
	GateID        int64
	GateSessionID int64
	ActorUserID   int64
	DeviceID      int64

	Token string
	// Input is what kind of reader spoke - qr, nfc, rfid, barcode, plate and so on. Empty is a
	// QR code, because that is the reader a tablet has.
	Input string
	At    time.Time
}

// TokenVerdict is the module's answer: which pass the token names, which row it was, and why it
// cannot be honoured when it cannot.
type TokenVerdict struct {
	PassID  int64
	TokenID int64
	Refusal string
	Input   string
}

// Attempt is one scan as the device made it and the gate answered it. It exists for the rows
// that never become a movement - an unknown card, a revoked badge - because those are the ones
// a supervisor asks about afterwards and the movement log cannot answer.
type Attempt struct {
	TenantID      int64
	SiteID        int64
	GateID        int64
	GateSessionID int64
	ActorUserID   int64
	DeviceID      int64

	Input     string
	Token     string
	Plate     string
	Direction Direction
	Result    Result
	Reason    string

	PassID     int64
	TokenID    int64
	MovementID int64
	PassNo     string

	OccurredAt     time.Time
	IdempotencyKey string
}

// TokenGuard is the handle Core holds while the module that answers is still being built. An
// empty guard refuses a token outright, which is the honest answer: nothing can name the pass,
// so nothing may open the barrier for it.
type TokenGuard struct{ tokens Tokens }

func NewTokenGuard() *TokenGuard { return &TokenGuard{} }

func (g *TokenGuard) Bind(tokens Tokens) { g.tokens = tokens }

func (g *TokenGuard) Resolve(ctx context.Context, q TokenQuery) (TokenVerdict, error) {
	if g.tokens == nil {
		return TokenVerdict{Input: "qr", Refusal: "token scanning is not enabled at this gate"}, nil
	}
	return g.tokens.Resolve(ctx, q)
}

// Record is the log half, asked after the guard already has his colour. A failure here is
// logged by the caller and never turns a green light into an error page.
func (g *TokenGuard) Record(ctx context.Context, a Attempt) error {
	if g.tokens == nil {
		return nil
	}
	return g.tokens.Record(ctx, a)
}

// presented is what the barrier resolved before it decided anything: the pass, and the token it
// came in on when a device read one instead of a pass number.
type presented struct {
	pass    passes.Pass
	tokenID int64
	input   string
	refusal string
}
