package builtin

import (
	"net"
	"testing"

	"github.com/NB-Agent/ok/internal/sandbox"
)

func FuzzIsPrivateIP(f *testing.F) {
	seeds := []net.IP{
		net.ParseIP("127.0.0.1"),
		net.ParseIP("::1"),
		net.ParseIP("10.0.0.1"),
		net.ParseIP("192.168.1.1"),
		net.ParseIP("172.16.0.1"),
		net.ParseIP("8.8.8.8"),
		net.ParseIP("fe80::1"),
		net.ParseIP("100.64.0.1"),
		net.ParseIP("0.0.0.1"),
		net.ParseIP("240.0.0.1"),
		net.ParseIP("224.0.1.1"),
		net.ParseIP("198.18.0.1"),
		net.ParseIP("fc00::1"),
		net.ParseIP("169.254.1.1"),
		net.ParseIP("1.1.1.1"),
	}
	for _, ip := range seeds {
		raw, _ := ip.MarshalText()
		f.Add(string(raw))
	}

	f.Fuzz(func(t *testing.T, raw string) {
		ip := net.ParseIP(raw)
		if ip == nil {
			return
		}
		_ = sandbox.BlockedFetchIP(ip)
	})
}
