package builtin

import (
	"net"
	"testing"

	"github.com/NB-Agent/ok/internal/sandbox"
)

func TestBlockedFetchIP(t *testing.T) {
	tests := []struct {
		name    string
		ip      string
		blocked bool
	}{
		// Public addresses — NOT blocked.
		{"public google dns", "8.8.8.8", false},
		{"public github", "140.82.121.4", false},
		{"public loopback", "127.0.0.1", false}, // loopback allowed

		// RFC1918 private — blocked.
		{"rfc1918 /8", "10.0.0.1", true},
		{"rfc1918 /12", "172.16.0.1", true},
		{"rfc1918 /16", "192.168.1.1", true},

		// Link-local / cloud metadata — blocked.
		{"link-local aws metadata", "169.254.169.254", true},
		{"link-local zeroconf", "169.254.1.1", true},

		// CGNAT / Alibaba Cloud metadata — blocked.
		{"cgnat 100.64 low", "100.64.0.0", true},
		{"cgnat 100.100 middle", "100.100.100.200", true},
		{"cgnat 100.127 high", "100.127.255.255", true},

		// Edge of CGNAT range — NOT blocked.
		{"just below cgnat", "100.63.255.255", false},
		{"just above cgnat", "100.128.0.0", false},

		// Unspecified — blocked.
		{"unspecified v4", "0.0.0.0", true},

		// IPv6 private / link-local — blocked.
		{"ipv6 unique local", "fd00::1", true},
		{"ipv6 link local", "fe80::1", true},
		{"ipv6 unspecified", "::", true},

		// IPv6 public — NOT blocked.
		{"ipv6 public", "2001:4860:4860::8888", false},
	}

	for _, tt := range tests {
		ip := net.ParseIP(tt.ip)
		if ip == nil {
			t.Fatalf("%s: parse %q failed", tt.name, tt.ip)
		}
		got := sandbox.BlockedFetchIP(ip)
		if got != tt.blocked {
			t.Errorf("%s (%s): blocked=%v, want %v", tt.name, tt.ip, got, tt.blocked)
		}
	}
}

func TestBlockedFetchIPIPv4Mapped(t *testing.T) {
	// IPv4-mapped IPv6 addresses should still be blocked.
	ip := net.ParseIP("::ffff:10.0.0.1")
	if ip == nil {
		t.Fatal("parse failed")
	}
	// Go's IsPrivate handles IPv4-mapped addresses.
	if !sandbox.BlockedFetchIP(ip) {
		t.Error("::ffff:10.0.0.1 should be blocked (private)")
	}
}

func TestCGNATRangeBounds(t *testing.T) {
	// 100.64.0.0/10 = 100.64.0.0 to 100.127.255.255
	// Test via BlockedFetchIP since cgnatRange is now internal to sandbox.
	if !sandbox.BlockedFetchIP(net.ParseIP("100.64.0.0")) {
		t.Error("100.64.0.0 should be blocked (CGNAT)")
	}
	if !sandbox.BlockedFetchIP(net.ParseIP("100.127.255.255")) {
		t.Error("100.127.255.255 should be blocked (CGNAT)")
	}
	if sandbox.BlockedFetchIP(net.ParseIP("100.63.255.255")) {
		t.Error("100.63.255.255 should not be blocked (below CGNAT)")
	}
	if sandbox.BlockedFetchIP(net.ParseIP("100.128.0.0")) {
		t.Error("100.128.0.0 should not be blocked (above CGNAT)")
	}
}
