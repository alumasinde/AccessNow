package httpx

import (
	"context"
	"net/http"
	"strings"
)

// Meta is who stood at the keyboard: the address the request came from, the client that
// sent it, and the id every log line already carries. The audit trail records all three
// with each state change, so "approved" can be traced to a machine and a request, not only
// to a user id that somebody else could have used.
type Meta struct {
	IPAddress string
	UserAgent string
	RequestID string
}

type metaKey struct{}

// WithMeta stamps the request on the context for anything downstream, which is how a
// service that never sees the http.Request still audits an IP address.
func WithMeta(ctx context.Context, r *http.Request, requestID string) context.Context {
	return context.WithValue(ctx, metaKey{}, Meta{
		IPAddress: clientIP(r), UserAgent: r.UserAgent(), RequestID: requestID,
	})
}

func MetaFrom(ctx context.Context) Meta {
	m, _ := ctx.Value(metaKey{}).(Meta)
	return m
}

// clientIP is the peer address, minus the port Go appends. Nothing here trusts
// X-Forwarded-For: that header is whatever the client said it was unless the proxy in
// front of us is known, and this deployment has no such setting to consult.
func clientIP(r *http.Request) string {
	if i := strings.LastIndex(r.RemoteAddr, ":"); i > strings.LastIndex(r.RemoteAddr, "]") {
		return r.RemoteAddr[:i]
	}
	return r.RemoteAddr
}
