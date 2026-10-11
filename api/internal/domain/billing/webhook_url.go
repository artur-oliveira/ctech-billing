package billing

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
)

// ErrWebhookHostNotAllowed reports a URL whose host is not on the public
// internet. Since merchants register their own endpoints (spec § 5.2), billing
// would otherwise make signed requests, on a schedule, to whatever address a
// merchant names, CTech's own private network included.
var ErrWebhookHostNotAllowed = errors.New("webhook host is not a public address")

// nonPublic are special-purpose ranges Go's IsGlobalUnicast still accepts:
// shared address space (CGNAT), "this network", IETF protocol assignments,
// benchmarking, reserved class E, and the IPv6 translation prefixes (NAT64,
// local-use NAT64, 6to4, Teredo) through which an IPv6 address can carry a
// private IPv4 one.
var nonPublic = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("2001::/32"),
}

// PublicAddr reports an address a merchant's endpoint may resolve to: global
// unicast, and none of private, loopback, link-local, the special-purpose
// ranges in nonPublic, or an IPv4-mapped form of those.
func PublicAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	if !ip.IsValid() || !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, p := range nonPublic {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// CheckWebhookURL is the console's rule for a merchant endpoint, on top of
// WebhookEndpoint.Validate: https, a host, no credentials in the URL, and a
// host that names the public internet. Resolution is checked again at
// delivery (services.Deliverer), where DNS can no longer change the answer.
func CheckWebhookURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return fmt.Errorf("%w: %q is not a URL", ErrInvalidEndpoint, raw)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("%w: %q is not https", ErrInvalidEndpoint, raw)
	}
	if u.User != nil {
		return fmt.Errorf("%w: credentials do not belong in the URL", ErrInvalidEndpoint)
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if ip, err := netip.ParseAddr(host); err == nil {
		if !PublicAddr(ip) {
			return fmt.Errorf("%w: %s", ErrWebhookHostNotAllowed, host)
		}
		return nil
	}
	if !strings.Contains(host, ".") || host == "localhost" {
		return fmt.Errorf("%w: %s", ErrWebhookHostNotAllowed, host)
	}
	for _, suffix := range []string{".localhost", ".local", ".internal"} {
		if strings.HasSuffix(host, suffix) {
			return fmt.Errorf("%w: %s", ErrWebhookHostNotAllowed, host)
		}
	}
	return nil
}

// NewWebhookSecret returns a signing secret: 256 random bits, hex, prefixed so
// a leaked one is recognisable in a log or a repository scan.
func NewWebhookSecret() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b) // crypto/rand.Read never fails (Go 1.24+)
	return "whsec_" + hex.EncodeToString(b)
}
