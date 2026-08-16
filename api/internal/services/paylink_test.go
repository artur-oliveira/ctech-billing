package services

import (
	"errors"
	"strings"
	"testing"
)

// A payment link is the only credential on the public checkout, so these are
// authentication tests, not serialization tests.

func TestPayLinkRoundTrips(t *testing.T) {
	l := NewPayLink("s3cr3t", "https://pay.test/c")

	token := l.Sign("org_1", true, "in_42")
	org, livemode, invoice, err := l.Parse(token)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if org != "org_1" || !livemode || invoice != "in_42" {
		t.Fatalf("got %q %v %q", org, livemode, invoice)
	}
	if want := "https://pay.test/c/" + token; l.URL("org_1", true, "in_42") != want {
		t.Fatalf("URL = %q, want %q", l.URL("org_1", true, "in_42"), want)
	}
}

// The mode is inside the signature, so a live link cannot be edited into a test
// one or the other way round — which matters because a test invoice and a live
// invoice can carry the same id in two different partitions.
func TestPayLinkSeparatesModes(t *testing.T) {
	l := NewPayLink("s3cr3t", "")
	if l.Sign("org_1", true, "in_42") == l.Sign("org_1", false, "in_42") {
		t.Fatal("live and test links are identical")
	}
	if _, livemode, _, err := l.Parse(l.Sign("org_1", false, "in_42")); err != nil || livemode {
		t.Fatalf("test link parsed as livemode=%v (err %v)", livemode, err)
	}
}

// The whole point of the design: the token is unforgeable without the key, so
// nobody enumerates invoices by editing a URL.
func TestPayLinkRejectsForgeries(t *testing.T) {
	l := NewPayLink("s3cr3t", "")
	valid := l.Sign("org_1", true, "in_42")
	payload, sig, _ := strings.Cut(valid, ".")

	other := NewPayLink("another-key", "")
	forged := payload + "." + strings.Split(other.Sign("org_1", true, "in_99"), ".")[1]

	for name, token := range map[string]string{
		"empty":                            "",
		"no separator":                     payload,
		"tampered payload":                 strings.Split(l.Sign("org_1", true, "in_99"), ".")[0] + "." + sig,
		"signed with a key we do not hold": forged,
		"garbage":                          "abc.def",
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, _, err := l.Parse(token); !errors.Is(err, ErrBadLink) {
				t.Fatalf("accepted %q (err %v)", token, err)
			}
		})
	}
}

// No key means no public checkout at all. A deployment that forgot to set the
// secret must serve nothing rather than serve links anybody can mint.
func TestPayLinkWithoutASecretIsDisabled(t *testing.T) {
	l := NewPayLink("", "https://pay.test/c")
	if l.Enabled() {
		t.Fatal("enabled with no secret")
	}
	if l.Sign("org_1", true, "in_42") != "" {
		t.Fatal("issued a token with no secret")
	}
	if _, _, _, err := l.Parse("anything.at-all"); !errors.Is(err, ErrBadLink) {
		t.Fatalf("accepted a token with no secret: %v", err)
	}
}
