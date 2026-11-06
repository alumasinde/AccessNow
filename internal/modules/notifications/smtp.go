package notifications

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"net/textproto"
	"os"
	"strconv"
	"time"
)

// smtpSettings is one tenant's mail server as its provider row describes it. Nothing here is
// a secret except Password, which arrives from the environment through credentials_ref and
// never from the database.
type smtpSettings struct {
	Host     string
	Port     int
	Security string
	Username string
	Password string
	From     string
	FromName string
}

// smtpSender is a minimal, honest SMTP client: one connection per message, no pooling, no
// queue of its own. The queue is the notifications table, which is the reason this can afford
// to be simple.
type smtpSender struct {
	s       smtpSettings
	timeout time.Duration
}

// security is declared rather than probed because a tenant that means STARTTLS and gets an
// implicit-TLS handshake has a mail server that refuses the connection and an operator with
// no clue why. "none" exists for the local relay a development machine runs.
const (
	smtpStartTLS = "starttls"
	smtpTLS      = "tls"
	smtpNone     = "none"
)

func newSMTPSender(set smtpSettings) (Sender, error) {
	if set.Host == "" {
		return nil, fmt.Errorf("smtp provider needs options.host")
	}
	switch set.Security {
	case smtpStartTLS, smtpTLS, smtpNone, "ssl", "smtps", "":
	default:
		return nil, fmt.Errorf("smtp security %q is not one of starttls, tls or none", set.Security)
	}
	if set.Security == "" || set.Security == "ssl" || set.Security == "smtps" {
		set.Security = smtpTLS
	}
	if set.Port == 0 {
		set.Port = map[string]int{smtpTLS: 465, smtpStartTLS: 587, smtpNone: 25}[set.Security]
	}
	return &smtpSender{s: set, timeout: 15 * time.Second}, nil
}

func (c *smtpSender) Send(ctx context.Context, msg Outbound) (Receipt, error) {
	client, conn, err := c.dial(ctx)
	if err != nil {
		return Receipt{}, err
	}
	defer conn.Close()

	if err := c.handshake(client, msg); err != nil {
		// The conversation has to be abandoned politely or the server holds the socket open
		// for the next one; a reset here is not the failure the notice records.
		_ = client.Quit()
		return Receipt{}, err
	}
	// 250 after DATA means the server took the message. It says nothing about the mailbox
	// reading it, which is exactly the difference between 'sent' and 'delivered'.
	_ = client.Quit()
	return Receipt{}, nil
}

// dial opens the connection and, for implicit TLS, wraps it before SMTP starts talking.
func (c *smtpSender) dial(ctx context.Context) (*smtp.Client, net.Conn, error) {
	addr := net.JoinHostPort(c.s.Host, strconv.Itoa(c.s.Port))
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(c.timeout)
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, nil, fmt.Errorf("connect smtp %s: %w", addr, err)
	}
	// One deadline over the handshake and the whole exchange: a mail server that stops
	// mid-transaction must not hold a delivery worker, and the row stays queued for the next
	// pass instead.
	if err := conn.SetDeadline(deadline); err != nil {
		conn.Close()
		return nil, nil, err
	}
	if c.s.Security == smtpTLS {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: c.s.Host, MinVersion: tls.VersionTLS12})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			conn.Close()
			return nil, nil, fmt.Errorf("smtp tls handshake %s: %w", addr, err)
		}
		conn = tlsConn
	}
	client, err := smtp.NewClient(conn, c.s.Host)
	if err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("greet smtp %s: %w", addr, err)
	}
	return client, conn, nil
}

func (c *smtpSender) handshake(client *smtp.Client, msg Outbound) error {
	// EHLO before anything else: Extension() only answers once that exchange has run, and
	// every check below - STARTTLS, AUTH - reads the list it fills.
	if err := client.Hello(c.localName()); err != nil {
		return fmt.Errorf("smtp ehlo: %w", err)
	}
	if c.s.Security == smtpStartTLS {
		ok, _ := client.Extension("STARTTLS")
		if !ok {
			return refused("provider_refused", "server at "+c.s.Host+" does not offer STARTTLS")
		}
		if err := client.StartTLS(&tls.Config{ServerName: c.s.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if c.s.Username != "" {
		// Checked rather than attempted because a plain relay that offers no AUTH answers an
		// AUTH command with a 500, and a 500 in the middle of a transaction is a much worse
		// thing to explain to an operator than this sentence.
		if ok, _ := client.Extension("AUTH"); !ok {
			return refused("provider_refused", c.s.Host+" does not offer AUTH; clear the username or use a relay that does")
		}
		if err := client.Auth(c.auth()); err != nil {
			return c.authFailure(err)
		}
	}
	if err := client.Mail(c.s.From); err != nil {
		return permanent(err, "sender address refused")
	}
	if err := client.Rcpt(msg.To); err != nil {
		return permanent(err, "recipient refused")
	}
	w, err := client.Data()
	if err != nil {
		return permanent(err, "server refused to start the message")
	}
	if _, err := w.Write(wire(c.s, msg)); err != nil {
		w.Close()
		return fmt.Errorf("write smtp message: %w", err)
	}
	if err := w.Close(); err != nil {
		return permanent(err, "server refused the message")
	}
	return nil
}

func (c *smtpSender) auth() smtp.Auth {
	// PlainAuth only sends the password once the TLS record is up, which is why a starttls
	// server that refuses the upgrade is refused above rather than negotiated with.
	return smtp.PlainAuth("", c.s.Username, c.s.Password, c.s.Host)
}

// authFailure tells a bad password from a server that went away. The first is permanent for
// this notice and the second is not: an operator fixing credentials should see the queue move
// on the next pass, not a row that already gave up for the wrong reason.
func (c *smtpSender) authFailure(err error) error {
	if err == nil {
		return nil
	}
	var serverErr *textproto.Error
	if errors.As(err, &serverErr) && serverErr.Code >= 500 {
		return refused("auth_failed", serverErr.Msg)
	}
	return fmt.Errorf("smtp auth: %w", err)
}

// permanent turns an SMTP 5xx - the class the RFC says means "do not try this again" - into a
// refusal. Everything else stays an ordinary error so the sweep retries it.
func permanent(err error, what string) error {
	if err == nil {
		return nil
	}
	var serverErr *textproto.Error
	if errors.As(err, &serverErr) && serverErr.Code >= 500 {
		return refused("provider_refused", what+": "+serverErr.Msg)
	}
	return fmt.Errorf("%s: %w", what, err)
}

// localName is the name this side introduces itself with. "localhost" is what net/smtp uses
// when the hostname is unavailable, and a mail server that dislikes it says so in the 5xx it
// returns to EHLO rather than in a delivery report nobody reads.
func (c *smtpSender) localName() string {
	if name, err := os.Hostname(); err == nil && name != "" {
		return name
	}
	return "localhost"
}

// wire builds the RFC-822 bytes the server gets. Date is included because a mail client shows
// it whether or not the message carries it, and a notice without one lands as "unknown".
func wire(set smtpSettings, msg Outbound) []byte {
	from := set.From
	if set.FromName != "" {
		from = fmt.Sprintf("%s <%s>", set.FromName, set.From)
	}
	var b []byte
	b = append(b, "From: "+from+"\r\n"...)
	b = append(b, "To: "+msg.To+"\r\n"...)
	b = append(b, "Subject: "+msg.Subject+"\r\n"...)
	b = append(b, "Date: "+time.Now().UTC().Format(time.RFC1123Z)+"\r\n"...)
	b = append(b, "MIME-Version: 1.0\r\n"...)
	b = append(b, "Content-Type: text/plain; charset=utf-8\r\n"...)
	b = append(b, "\r\n"...)
	return append(b, msg.Body...)
}
