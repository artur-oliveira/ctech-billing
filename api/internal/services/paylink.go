package services

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// ErrBadLink reports a payment-link token that is malformed, or signed with a
// key this service does not hold.
var ErrBadLink = errors.New("invalid payment link")

// PayLink signs and verifies public payment links.
//
// A payment link is a URL that opens one invoice's checkout with no sign-in —
// which is the whole point: it is sent in the email that says "your invoice is
// ready", and a person paying a bill will not create an account first.
//
// **The token is derived, not stored.** It is the invoice's address plus an HMAC
// over it, so there is no token row, no lookup index, and no expiry job. Two
// consequences worth stating rather than discovering:
//
//   - The link stays valid as long as the invoice is payable. That is deliberate:
//     an invoice due in 30 days needs a link that lives 30 days, and a checkout
//     *session* — which does expire, in half an hour — is a different object with
//     a different lifetime. Conflating them produces links that die before the
//     bill does.
//   - Revocation is per-key, not per-link: rotating CHECKOUT_LINK_SECRET
//     invalidates every outstanding link at once. Per-invoice revocation would
//     need a stored token, and nothing has asked for it. If it ever does, that is
//     the upgrade — not a bigger token.
//
// Unguessability comes from the MAC, not from the id: 128 bits of tag over an id
// the recipient already knows.
type PayLink struct {
	secret []byte
	// baseURL renders the whole URL for an email. Empty just means the caller
	// only wants tokens.
	baseURL string
}

// NewPayLink builds a signer. An empty secret disables payment links entirely —
// Sign returns "" and Parse rejects everything, so a misconfigured deployment
// serves no public checkout rather than one anybody can forge.
func NewPayLink(secret, baseURL string) *PayLink {
	return &PayLink{secret: []byte(secret), baseURL: strings.TrimSuffix(baseURL, "/")}
}

// Enabled reports whether links can be issued at all.
func (p *PayLink) Enabled() bool { return len(p.secret) > 0 }

// Sign returns the opaque token addressing one invoice.
func (p *PayLink) Sign(organizationID string, livemode bool, invoiceID string) string {
	if !p.Enabled() {
		return ""
	}
	payload := organizationID + "|" + mode(livemode) + "|" + invoiceID
	enc := base64.RawURLEncoding.EncodeToString([]byte(payload))
	return enc + "." + base64.RawURLEncoding.EncodeToString(p.mac(enc))
}

// URL renders the full link, or "" when no base URL is configured.
func (p *PayLink) URL(organizationID string, livemode bool, invoiceID string) string {
	token := p.Sign(organizationID, livemode, invoiceID)
	if token == "" || p.baseURL == "" {
		return ""
	}
	return p.baseURL + "/" + token
}

// Parse verifies a token and returns what it addresses.
//
// The MAC is checked **before** the payload is split into fields, so a forged
// token never reaches a database read. The comparison is constant-time; a
// byte-by-byte one leaks the tag one request at a time.
func (p *PayLink) Parse(token string) (organizationID string, livemode bool, invoiceID string, err error) {
	if !p.Enabled() {
		return "", false, "", ErrBadLink
	}
	enc, sig, ok := strings.Cut(token, ".")
	if !ok {
		return "", false, "", ErrBadLink
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, p.mac(enc)) {
		return "", false, "", ErrBadLink
	}
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return "", false, "", ErrBadLink
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 || parts[0] == "" || parts[2] == "" {
		return "", false, "", ErrBadLink
	}
	switch parts[1] {
	case "live":
		return parts[0], true, parts[2], nil
	case "test":
		return parts[0], false, parts[2], nil
	default:
		return "", false, "", fmt.Errorf("%w: unknown mode", ErrBadLink)
	}
}

// mac is truncated to 128 bits. Full SHA-256 would double the token length for
// no reachable gain: forging needs a collision on the tag, and 128 bits of it is
// already beyond anything an attacker can search.
func (p *PayLink) mac(enc string) []byte {
	h := hmac.New(sha256.New, p.secret)
	h.Write([]byte(enc))
	return h.Sum(nil)[:16]
}

func mode(livemode bool) string {
	if livemode {
		return "live"
	}
	return "test"
}
