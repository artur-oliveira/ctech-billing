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

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// PublicAddr reports an address a merchant's endpoint may resolve to: global
// unicast, and none of private, loopback, link-local, shared (CGNAT) or an
// IPv4-mapped form of those.
func PublicAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsValid() && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() &&
		!ip.IsLinkLocalUnicast() && !cgnat.Contains(ip)
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
