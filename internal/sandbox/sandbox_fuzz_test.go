package sandbox

import (
	"net"
	"testing"
)

// FuzzBlockedFetchIP tests that the SSRF guard never panics on arbitrary IPs.
func FuzzBlockedFetchIP(f *testing.F) {
	f.Add("127.0.0.1")
	f.Add("192.168.1.1")
	f.Add("10.0.0.1")
	f.Add("100.64.0.1")
	f.Add("8.8.8.8")
	f.Add("")
	f.Add("not-an-ip")
	f.Add("::1")
	f.Add("fe80::1")
	f.Add("0.0.0.0")

	f.Fuzz(func(t *testing.T, ip string) {
		parsed := net.ParseIP(ip)
		if parsed == nil {
			return // skip invalid IPs
		}
		_ = BlockedFetchIP(parsed)
	})
}

// FuzzSpecMode tests Spec.Mode handling invariants.
func FuzzSpecMode(f *testing.F) {
	f.Add("enforce", true)
	f.Add("warn", false)
	f.Add("", false)
	f.Add("appcontainer", false)
	f.Add("badmode", false)

	f.Fuzz(func(t *testing.T, mode string, net bool) {
		s := Spec{
			Mode:    mode,
			Network: net,
		}
		enforces := s.enforce()
		isAC := s.appcontainer()

		// enforce=true only for "enforce" mode
		if mode == "enforce" && !enforces {
			t.Errorf("mode=%q enforce()=false, want true", mode)
		}
		if mode != "enforce" && enforces {
			t.Errorf("mode=%q enforce()=true, want false", mode)
		}
		// appcontainer=true only for "appcontainer" mode
		if mode == "appcontainer" && !isAC {
			t.Errorf("mode=%q appcontainer()=false, want true", mode)
		}
		if mode != "appcontainer" && isAC {
			t.Errorf("mode=%q appcontainer()=true, want false", mode)
		}
	})
}
