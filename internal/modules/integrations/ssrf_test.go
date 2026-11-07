package integrations

import (
	"net"
	"testing"
)

func TestPublicIPRefusesRangesNetIPMisses(t *testing.T) {
	for _, raw := range []string{
		"100.100.100.200", // carrier-grade NAT: some clouds serve metadata here
		"100.64.0.1",
		"198.18.0.1",
		"192.0.2.10",
		"240.0.0.1",
		"::ffff:10.0.0.1", // IPv4 hidden inside IPv6
		"::ffff:127.0.0.1",
		"fd12:3456::1",
		"169.254.169.254",
		"0.1.2.3",
	} {
		if publicIP(net.ParseIP(raw)) {
			t.Errorf("publicIP(%s) = true, want false", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !publicIP(net.ParseIP(raw)) {
			t.Errorf("publicIP(%s) = false, want true", raw)
		}
	}
}

func TestRefusePrivateDialRunsBeforeConnect(t *testing.T) {
	if err := refusePrivateDial("tcp", "127.0.0.1:22", nil); err == nil {
		t.Fatal("loopback must be refused before the socket connects")
	}
	if err := refusePrivateDial("tcp", "8.8.8.8:443", nil); err != nil {
		t.Fatalf("a public address must pass: %v", err)
	}
}
