package notifications

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"testing"
)

// fakeSMTP is a mail server small enough to read: it answers the commands this client sends
// and nothing else, which is what makes it worth having - the hand-written EHLO, AUTH probe
// and DATA framing in smtp.go are exactly the parts a unit test cannot see any other way.
type fakeSMTP struct {
	ln      net.Listener
	replies map[string]string
	got     chan string
}

// serve answers one conversation. data is what the server was asked to carry, reported back
// on the channel so the test can assert on the bytes rather than on a log line.
func serveFakeSMTP(t *testing.T, replies map[string]string) (*fakeSMTP, chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("no loopback for the fake mail server: %v", err)
	}
	f := &fakeSMTP{ln: ln, replies: replies, got: make(chan string, 1)}
	go f.run()
	t.Cleanup(func() { _ = ln.Close() })
	return f, f.got
}

func (f *fakeSMTP) run() {
	conn, err := f.ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()

	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	fmt.Fprint(rw, "220 fake ESMTP\r\n")
	_ = rw.Flush()

	var data strings.Builder
	inData := false
	for {
		line, err := rw.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		up := strings.ToUpper(line)

		if inData {
			if line == "." {
				inData = false
				fmt.Fprint(rw, f.reply("DATA", "250 OK: queued\r\n"))
				_ = rw.Flush()
				f.got <- data.String()
				continue
			}
			data.WriteString(line + "\n")
			continue
		}

		switch {
		case strings.HasPrefix(up, "EHLO"), strings.HasPrefix(up, "HELO"):
			fmt.Fprint(rw, "250-fake greets you\r\n250 SIZE 1048576\r\n")
		case strings.HasPrefix(up, "MAIL FROM"):
			fmt.Fprint(rw, f.reply("MAIL", "250 OK\r\n"))
		case strings.HasPrefix(up, "RCPT TO"):
			fmt.Fprint(rw, f.reply("RCPT", "250 OK\r\n"))
		case up == "DATA":
			inData = true
			fmt.Fprint(rw, "354 send it\r\n")
		case up == "QUIT":
			fmt.Fprint(rw, "221 bye\r\n")
			_ = rw.Flush()
			return
		default:
			fmt.Fprint(rw, "500 not understood\r\n")
		}
		_ = rw.Flush()
	}
}

func (f *fakeSMTP) reply(command, fallback string) string {
	if line, ok := f.replies[command]; ok {
		return line
	}
	return fallback
}

func (f *fakeSMTP) settings(security string) smtpSettings {
	_, port, _ := net.SplitHostPort(f.ln.Addr().String())
	n := 0
	fmt.Sscanf(port, "%d", &n)
	if security == "" {
		security = smtpNone
	}
	return smtpSettings{Host: "127.0.0.1", Port: n, Security: security,
		From: "desk@example.test", FromName: "Demo Gate"}
}

func TestSMTPConversationCarriesTheMessage(t *testing.T) {
	f, got := serveFakeSMTP(t, nil)
	sender, err := newSMTPSender(f.settings(smtpNone))
	if err != nil {
		t.Fatalf("newSMTPSender: %v", err)
	}

	receipt, err := sender.Send(context.Background(), Outbound{
		To: "reader@example.test", Subject: "Still out: HQ-E002",
		Body: "Due back since 6 Nov 2026 09:00."})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	// "sent" is all an SMTP 250 proves; a mailbox that never opened is not this client's
	// claim to make.
	if receipt.ProvedLanding {
		t.Error("a mail server that queued the message was reported as delivered")
	}

	wire := <-got
	if !strings.Contains(wire, "To: reader@example.test") {
		t.Errorf("no To header in:\n%s", wire)
	}
	if !strings.Contains(wire, "From: Demo Gate <desk@example.test>") {
		t.Errorf("sender name lost in:\n%s", wire)
	}
	if !strings.Contains(wire, "Subject: Still out: HQ-E002") ||
		!strings.Contains(wire, "Due back since 6 Nov 2026 09:00.") {
		t.Errorf("message text lost in:\n%s", wire)
	}
	if !strings.Contains(wire, "Date: ") {
		t.Errorf("no Date header, so the mail client shows \"unknown\":\n%s", wire)
	}
}

func TestPermanentSMTPRefusalIsTerminal(t *testing.T) {
	f, _ := serveFakeSMTP(t, map[string]string{"RCPT": "550 no such mailbox\r\n"})
	sender, err := newSMTPSender(f.settings(smtpNone))
	if err != nil {
		t.Fatalf("newSMTPSender: %v", err)
	}

	_, err = sender.Send(context.Background(), Outbound{To: "gone@example.test", Body: "x"})
	failure, ok := refusedHere(err)
	if !ok {
		t.Fatalf("Send error = %v, a 5xx must not be retried", err)
	}
	if failure.Code != "provider_refused" || !strings.Contains(failure.Reason, "no such mailbox") {
		t.Errorf("refusal = %+v", failure)
	}
}

func TestAuthAgainstARelayThatOfferNoneIsRefused(t *testing.T) {
	// The fake advertises no AUTH, so a configured username has to be caught before the
	// client sends a command the server answers with a 500 mid-transaction.
	f, _ := serveFakeSMTP(t, nil)
	set := f.settings(smtpNone)
	set.Username, set.Password = "desk@example.test", "hunter2"
	sender, err := newSMTPSender(set)
	if err != nil {
		t.Fatalf("newSMTPSender: %v", err)
	}

	_, err = sender.Send(context.Background(), Outbound{To: "reader@example.test", Body: "x"})
	failure, ok := refusedHere(err)
	if !ok || failure.Code != "provider_refused" {
		t.Fatalf("Send error = %v, want a refusal naming the missing AUTH", err)
	}
	if !strings.Contains(failure.Reason, "does not offer AUTH") {
		t.Errorf("reason = %q", failure.Reason)
	}
}

func TestSTARTTLSServerThatDoesNotOfferItIsRefused(t *testing.T) {
	f, _ := serveFakeSMTP(t, nil)
	sender, err := newSMTPSender(f.settings(smtpStartTLS))
	if err != nil {
		t.Fatalf("newSMTPSender: %v", err)
	}
	_, err = sender.Send(context.Background(), Outbound{To: "reader@example.test", Body: "x"})
	if failure, ok := refusedHere(err); !ok || failure.Code != "provider_refused" {
		t.Fatalf("Send error = %v, want a refusal rather than a plaintext fallback", err)
	}
}

func TestSecurityAndPortDefaults(t *testing.T) {
	// An omitted security is implicit TLS on 465, which is what a tenant typing only a host
	// means; a starttls row must not silently dial 25 in the clear.
	sender, err := newSMTPSender(smtpSettings{Host: "mail.example.test", From: "desk@example.test"})
	if err != nil {
		t.Fatalf("newSMTPSender: %v", err)
	}
	c := sender.(*smtpSender)
	if c.s.Security != smtpTLS || c.s.Port != 465 {
		t.Errorf("security = %q port = %d", c.s.Security, c.s.Port)
	}

	if _, err := newSMTPSender(smtpSettings{From: "desk@example.test"}); err == nil {
		t.Error("a host-less provider built anyway")
	}
	if _, err := newSMTPSender(smtpSettings{Host: "h", Security: "plaintext"}); err == nil {
		t.Error("an unknown security mode built anyway")
	}
}
