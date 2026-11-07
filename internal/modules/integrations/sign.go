package integrations

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"accessnow/internal/platform/logging"
)

// maxSnippet is how much of a receiver's body is kept. A receiver that answers with a stack
// trace should not be able to fill the delivery table.
const maxSnippet = 512

// signatureHeader carries "t=<unix>,v1=<hex>": the timestamp is signed along with the body, so
// a replay after the fact is detectable by the receiver without a nonce table.
const signatureHeader = "X-AccessNow-Signature"

// signature is what a receiver rebuilds with the shared secret.
func signature(secret string, body []byte, timestamp int64) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(timestamp, 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// secretFor reads the signing key by the name the endpoint stores. The value never lands in the
// database, and a tenant who has not set it gets an unsigned delivery with no signature header
// rather than a delivery signed with a guess.
func secretFor(ref *string) string {
	if ref == nil || strings.TrimSpace(*ref) == "" {
		return ""
	}
	name := "WEBHOOK_SECRET_" + strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(*ref))
	return strings.TrimSpace(os.Getenv(name))
}

// post sends one delivery. The response body is read through a limit so a receiver cannot make
// this process allocate without bound.
func post(ctx context.Context, hook Webhook, body []byte) (int, string, error) {
	timeout := time.Duration(hook.TimeoutMS) * time.Millisecond
	client := &http.Client{Timeout: timeout, Transport: guardedTransport(timeout)}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, hook.URL, strings.NewReader(string(body)))
	if err != nil {
		return 0, "", fmt.Errorf("build webhook request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	request.Header.Set("User-Agent", "AccessNow-Webhooks/1")
	request.Header.Set("X-AccessNow-Event", hook.Name)

	stamp := time.Now().UTC().Unix()
	if secret := secretFor(hook.SecretRef); secret != "" {
		request.Header.Set(signatureHeader,
			fmt.Sprintf("t=%d,v1=%s", stamp, signature(secret, body, stamp)))
	}

	response, err := client.Do(request)
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close()
	snippet, _ := io.ReadAll(io.LimitReader(response.Body, maxSnippet))
	return response.StatusCode, string(snippet), nil
}

// guardedTransport refuses to connect to a private address. The URL check at save time is the
// first defence; this is the one that still holds when the name resolves somewhere else by the
// time the webhook fires.
func guardedTransport(timeout time.Duration) *http.Transport {
	dialer := &net.Dialer{Timeout: timeout}
	return &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			if tcp, ok := conn.RemoteAddr().(*net.TCPAddr); ok && !publicIP(tcp.IP) {
				_ = conn.Close()
				return nil, fmt.Errorf("webhook target resolved to a private address")
			}
			return conn, nil
		},
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
	}
}

// publicIP is the address rule: anything a server should be reachable at is fine, anything that
// is this machine, this network or a link-local neighbour is not.
func publicIP(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
		return false
	}
	// IPv6 unique local addresses are not covered by IsPrivate on every Go release.
	if len(ip) == net.IPv6len && ip[0] == 0xfc || len(ip) == net.IPv6len && ip[0] == 0xfd {
		return false
	}
	return true
}

// Loop is the delivery worker's heartbeat.
type Loop struct {
	svc   *Service
	every time.Duration
}

func NewLoop(svc *Service, every time.Duration) *Loop { return &Loop{svc: svc, every: every} }

func (l *Loop) Run(ctx context.Context) {
	ticker := time.NewTicker(l.every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sent, err := l.svc.DeliverDue(ctx)
			if err != nil {
				logging.From(ctx).ErrorContext(ctx, "webhook delivery sweep failed", "error", err)
				continue
			}
			if sent > 0 {
				logging.From(ctx).InfoContext(ctx, "webhook deliveries attempted", "count", sent)
			}
		}
	}
}
