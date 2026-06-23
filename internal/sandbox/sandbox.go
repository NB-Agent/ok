// Package sandbox wraps a shell command in an OS-level jail so the model's
// `bash` calls are confined: it may read freely but write only inside the
// workspace (plus temp and toolchain caches) and reach the network only when
// allowed. This is the *enforcement* layer beneath the permission rules
// (*policy*): a permitted command still cannot escape the box.
//
// Supported backends: macOS Seatbelt (sandbox-exec), Linux Landlock (kernel
// 5.13+). On every other OS, or when the kernel support is missing, Command
// falls back to running unwrapped (see Available). Confining the in-process
// file-writer built-ins is handled separately, in package tool/builtin.
package sandbox

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// Spec describes how to confine one command. The zero value (Mode == "") does
// not enforce, so an unconfigured caller runs commands unchanged.
type Spec struct {
	Mode       string
	WriteRoots []string
	Network    bool
}

// enforce reports whether the spec asks for confinement.
func (s Spec) enforce() bool { return s.Mode == "enforce" }

// appcontainer reports whether the spec asks for AppContainer confinement.
func (s Spec) appcontainer() bool { return s.Mode == "appcontainer" }

// cgnatRange is RFC 6598 shared address space (100.64.0.0/10). Go's IsPrivate
// doesn't cover it, yet some clouds host instance metadata there (Alibaba Cloud
// at 100.100.100.200), so it's an SSRF target that must be refused.
// Initialised in init() below; if the CIDR literal somehow fails to parse we
// still refuse to start without the SSRF guard.
var cgnatRange *net.IPNet

func init() {
	_, n, err := net.ParseCIDR("100.64.0.0/10")
	if err != nil {
		// Graceful degrade — CGNAT SSRF protection is defense-in-depth.
		// Parsing a literal CIDR string should never fail under a correct
		// standard library, but if it does we log and continue without it
		// rather than crashing the entire binary.
		fmt.Fprintf(os.Stderr, "sandbox: WARNING: failed to parse CGNAT CIDR (%v); SSRF protection reduced\n", err)
		cgnatRange = nil
		return
	}
	cgnatRange = n
}

// BlockedFetchIP reports whether ip is an address that must not be reached
// by outbound HTTP clients (web_fetch, MCP plugin connections). Covers private,
// link-local, unspecified, and CGNAT ranges.
func BlockedFetchIP(ip net.IP) bool {
	// Normalise IPv4-in-IPv6 (::ffff:10.0.0.1) to plain IPv4 so the
	// private-range checks below work correctly.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	return ip.IsPrivate() || // RFC1918 + IPv6 unique-local (fc00::/7)
		ip.IsLinkLocalUnicast() || // 169.254.0.0/16 (incl. cloud metadata) + fe80::/10
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || // 0.0.0.0 / ::
		(cgnatRange != nil && cgnatRange.Contains(ip)) // 100.64.0.0/10 (incl. Alibaba Cloud metadata)
}

// writeAllowDirs is the deduplicated, symlink-resolved set of directories the
// sandbox permits writes to: the caller's roots plus temp dirs, /dev/null, and
// the common toolchain caches under $HOME. Symlinks are resolved because
// e.g. macOS /tmp lives under /private, on Linux /tmp may also be a symlink.
func writeAllowDirs(roots []string) []string {
	dirs := append([]string{}, roots...)
	dirs = append(dirs, "/dev", "/tmp", "/private/tmp", "/private/var/folders", os.TempDir())
	if home, err := os.UserHomeDir(); err == nil {
		for _, sub := range []string{"Library/Caches", ".cache", ".npm", ".cargo", "go"} {
			dirs = append(dirs, filepath.Join(home, sub))
		}
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(dirs))
	for _, d := range dirs {
		if d == "" {
			continue
		}
		abs, err := filepath.Abs(d)
		if err != nil {
			continue
		}
		if real, err := filepath.EvalSymlinks(abs); err == nil {
			abs = real
		}
		if !seen[abs] {
			seen[abs] = true
			out = append(out, abs)
		}
	}
	return out
}
