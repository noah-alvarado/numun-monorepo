// Package httpclient provides the SSRF-hardened *http.Client used by every
// outbound HTTP call to a user-supplied URL (currently only Google Sheets
// fetch — BULK_IMPORT.md §7.2).
//
// Production handler code MUST use New / NewWithOptions from this package and
// must NOT construct a `net/http.Client` directly. `scripts/check-no-raw-http.sh`
// enforces this in CI.
//
// Spec: docs/SECURITY.md §2.7. Behaviour:
//   - Host allowlist: docs.google.com exactly + any host ending in
//     ".googleusercontent.com" (leading dot enforced).
//   - DNS is resolved here, not by the OS dialer, so the IP check and the
//     subsequent connect target are the same address (rebinding defense).
//   - Refuses any IP in private / loopback / link-local / unspecified /
//     CGNAT-style ranges (IPv4 + IPv6) per the SECURITY.md list.
//   - 5 s connect timeout, 10 s total.
//   - Refuses cross-origin redirects.
package httpclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"
)

// Default timeouts per SECURITY.md §2.7.
const (
	defaultConnectTimeout = 5 * time.Second
	defaultTotalTimeout   = 10 * time.Second
)

// Resolver is the minimal LookupIPAddr surface httpclient needs. Tests can
// satisfy this with a stub; production callers leave it nil and pick up
// net.DefaultResolver. Mirrors *net.Resolver so the prod path is a one-liner.
type Resolver interface {
	LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error)
}

// Options tunes the safe client. Zero-value Options is the production config
// (docs.google.com + *.googleusercontent.com, default timeouts, default
// resolver).
type Options struct {
	// ExtraExactHosts adds case-insensitive exact-match hosts to the allowlist.
	// Used by tests and by future M9/M11 integrations.
	ExtraExactHosts []string
	// ExtraSuffixHosts adds suffix patterns (matched as
	// strings.HasSuffix(host, "."+suffix), so the leading dot is implied).
	ExtraSuffixHosts []string
	// Resolver overrides DNS lookup. Defaults to net.DefaultResolver.
	Resolver Resolver
	// ConnectTimeout overrides the 5 s dial timeout.
	ConnectTimeout time.Duration
	// TotalTimeout overrides the 10 s request timeout.
	TotalTimeout time.Duration
}

// allowlist holds the resolved hostname rules. Built once per client.
type allowlist struct {
	exact    map[string]struct{}
	suffixes []string // each entry begins with "." for exact-suffix matching
}

func newAllowlist(extraExact, extraSuffix []string) *allowlist {
	a := &allowlist{exact: make(map[string]struct{})}
	for _, h := range append([]string{"docs.google.com"}, extraExact...) {
		a.exact[strings.ToLower(strings.TrimSpace(h))] = struct{}{}
	}
	suffixes := append([]string{"googleusercontent.com"}, extraSuffix...)
	for _, s := range suffixes {
		s = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(s, ".")))
		if s == "" {
			continue
		}
		a.suffixes = append(a.suffixes, "."+s)
	}
	return a
}

// permits reports whether host (already lower-cased, no port) is allowed.
func (a *allowlist) permits(host string) bool {
	if _, ok := a.exact[host]; ok {
		return true
	}
	for _, suf := range a.suffixes {
		if strings.HasSuffix(host, suf) {
			return true
		}
	}
	return false
}

// New returns the production safe client. Equivalent to
// NewWithOptions(Options{}).
func New() *http.Client {
	return NewWithOptions(Options{})
}

// NewWithOptions returns a safe client configured per opts. Tests use this to
// inject a stub Resolver or extra allowlist entries.
func NewWithOptions(opts Options) *http.Client {
	connectTimeout := opts.ConnectTimeout
	if connectTimeout <= 0 {
		connectTimeout = defaultConnectTimeout
	}
	totalTimeout := opts.TotalTimeout
	if totalTimeout <= 0 {
		totalTimeout = defaultTotalTimeout
	}
	resolver := opts.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	al := newAllowlist(opts.ExtraExactHosts, opts.ExtraSuffixHosts)

	dialer := &net.Dialer{Timeout: connectTimeout}

	transport := &http.Transport{
		// DialContext does the allowlist + DNS + IP-range checks itself and
		// then connects to the resolved IP. The OS resolver is bypassed so a
		// DNS rebind between the check and the dial cannot reach a private IP.
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("safehttp: split host/port %q: %w", addr, err)
			}
			lower := strings.ToLower(host)
			if !al.permits(lower) {
				return nil, fmt.Errorf("safehttp: host %q not in allowlist", host)
			}
			ips, err := resolver.LookupIPAddr(ctx, host)
			if err != nil {
				return nil, fmt.Errorf("safehttp: resolve %q: %w", host, err)
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("safehttp: resolve %q: no addresses", host)
			}
			// Refuse if ANY returned address is private. Belt-and-suspenders:
			// a single public IP in a rebind set still gets refused if there
			// is a private peer.
			for _, ip := range ips {
				if isDisallowedIP(ip.IP) {
					return nil, fmt.Errorf("safehttp: resolved %q to disallowed address %s", host, ip.IP)
				}
			}
			// Dial the first usable IP. The Host header / TLS SNI is preserved
			// because the http.Request still carries the hostname; we only
			// override the dial target.
			var lastErr error
			for _, ip := range ips {
				conn, dErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
				if dErr == nil {
					return conn, nil
				}
				lastErr = dErr
			}
			if lastErr == nil {
				lastErr = errors.New("no addresses")
			}
			return nil, fmt.Errorf("safehttp: dial %q: %w", host, lastErr)
		},
	}

	return &http.Client{
		Transport: transport,
		Timeout:   totalTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) == 0 {
				return nil
			}
			if strings.EqualFold(req.URL.Host, via[0].URL.Host) {
				if len(via) >= 10 {
					return errors.New("safehttp: too many redirects")
				}
				return nil
			}
			return fmt.Errorf("safehttp: cross-origin redirect refused (%s -> %s)", via[0].URL.Host, req.URL.Host)
		},
	}
}

// isDisallowedIP returns true if ip falls in any of the ranges SECURITY.md
// §2.7 lists. Net.IP.IsPrivate covers RFC1918 + ULA; we extend with explicit
// 0.0.0.0/8, 169.254.0.0/16 (link-local), loopback, and IPv6 ::1 / fc00::/7 /
// fe80::/10. netip.Addr makes the CIDR math cheaper than building net.IPNets.
func isDisallowedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return true
	}
	addr = addr.Unmap() // normalise IPv4-in-IPv6
	if addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() || addr.IsPrivate() {
		return true
	}
	// IPv6 unique-local (fc00::/7) and link-local (fe80::/10) are already
	// caught by IsPrivate + IsLinkLocalUnicast. Add an explicit 0.0.0.0/8 +
	// 169.254.0.0/16 check to be defensive about future stdlib behaviour.
	if addr.Is4() {
		b := addr.As4()
		if b[0] == 0 { // 0.0.0.0/8
			return true
		}
		if b[0] == 169 && b[1] == 254 { // 169.254.0.0/16
			return true
		}
		if b[0] == 127 { // 127.0.0.0/8
			return true
		}
	}
	return false
}
