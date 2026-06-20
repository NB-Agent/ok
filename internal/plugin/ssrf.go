package plugin

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/NB-Agent/ok/internal/sandbox"
)

// ssrfSafeClient returns an http.Client whose dialer refuses connections to
// private, link-local, and unspecified addresses — the same protection web_fetch
// uses. This prevents a configured MCP server URL from reaching internal
// services (cloud metadata, RFC1918 hosts, etc.). Loopback is allowed since
// the agent can already reach localhost via bash.
func ssrfSafeClient(timeout time.Duration) *http.Client {
	return ssrfSafeClientDial(timeout, timeout)
}

// ssrfSafeStreamClient is like ssrfSafeClient but uses 0 HTTP client timeout for
// long-lived SSE connections while keeping the dial timeout.
func ssrfSafeStreamClient(dialTimeout time.Duration) *http.Client {
	return ssrfSafeClientDial(dialTimeout, 0)
}

func ssrfSafeClientDial(dialTimeout, clientTimeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: dialTimeout}
	return &http.Client{
		Timeout: clientTimeout,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				host, port, err := net.SplitHostPort(addr)
				if err != nil {
					return nil, err
				}
				ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
				if err != nil {
					return nil, err
				}
				if len(ips) == 0 {
					return nil, fmt.Errorf("plugin: no addresses resolved for %s", host)
				}
				for _, ip := range ips {
					if sandbox.BlockedFetchIP(ip.IP) {
						return nil, fmt.Errorf("plugin: refusing connection to internal address %s (resolves to %s)", host, ip.IP)
					}
				}
				return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
			},
			// Prevent slow-loris attacks and hanging connections.
			ResponseHeaderTimeout: 30 * time.Second,
			// Limit idle connections to prevent resource exhaustion.
			MaxIdleConns:        10,
			MaxIdleConnsPerHost: 2,
			IdleConnTimeout:     90 * time.Second,
		},
	}
}
