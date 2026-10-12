package byokseal

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func key(t *testing.T) string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

func TestABundleSealedForOneConnectionDoesNotOpenOnAnother(t *testing.T) {
	s, err := New(key(t))
	if err != nil {
		t.Fatal(err)
	}
	mine := AAD("org-a", "live", "inter", "conn-1", 1)
	sealed, err := s.Seal([]byte(`{"client_secret":"s3cr3t"}`), mine)
	if err != nil || !strings.HasPrefix(sealed, "b1.") || strings.Contains(sealed, "s3cr3t") {
		t.Fatalf("%q %v", sealed, err)
	}
	if plain, err := s.Open(sealed, mine); err != nil || string(plain) != `{"client_secret":"s3cr3t"}` {
		t.Fatalf("own AAD: %s %v", plain, err)
	}
	for _, other := range [][]byte{
		AAD("org-b", "live", "inter", "conn-1", 1), AAD("org-a", "test", "inter", "conn-1", 1),
		AAD("org-a", "live", "other", "conn-1", 1), AAD("org-a", "live", "inter", "conn-2", 1),
		AAD("org-a", "live", "inter", "conn-1", 2),
	} {
		if _, err := s.Open(sealed, other); !errors.Is(err, ErrOpen) {
			t.Fatalf("opened under %s: %v", other, err)
		}
	}
}

func TestOpenRefusesUnprefixedAndForeignFormats(t *testing.T) {
	s, _ := New(key(t))
	for _, v := range []string{"", "plaintext", "v1.AAAA", "b2.AAAA"} {
		if _, err := s.Open(v, AAD("o", "live", "inter", "c", 1)); !errors.Is(err, ErrNotSealed) {
			t.Fatalf("%q: %v", v, err)
		}
	}
}

func TestNewRefusesAnythingButThirtyTwoBytes(t *testing.T) {
	for _, k := range []string{"", "short", base64.StdEncoding.EncodeToString(make([]byte, 16))} {
		if _, err := New(k); err == nil {
			t.Fatalf("%q accepted", k)
		}
	}
}
