package crypto

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func testSealer(t *testing.T) *Sealer {
	t.Helper()
	s := NewSealer(base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	if !s.Enabled() {
		t.Fatalf("test key rejected")
	}
	return s
}

func TestRoundTrip(t *testing.T) {
	s := testSealer(t)
	const cpf = "529.982.247-25"

	sealed, err := s.Seal(cpf)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if strings.Contains(sealed, "529") {
		t.Fatalf("sealed value still contains the plaintext: %q", sealed)
	}

	got, err := s.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if got != cpf {
		t.Errorf("Open = %q, want %q", got, cpf)
	}
}

// The nonce is what makes two customers with the same CPF indistinguishable in
// the table. Without it this is deterministic encryption, which leaks equality —
// and equality on a CPF is "these two records are the same person".
func TestSealIsNotDeterministic(t *testing.T) {
	s := testSealer(t)
	a, _ := s.Seal("11144477735")
	b, _ := s.Seal("11144477735")
	if a == b {
		t.Fatalf("two seals of one value produced the same ciphertext")
	}
}

func TestEmptyRoundTripsToEmpty(t *testing.T) {
	s := testSealer(t)
	if got, err := s.Seal(""); err != nil || got != "" {
		t.Errorf("Seal(\"\") = %q, %v", got, err)
	}
	if got, err := s.Open(""); err != nil || got != "" {
		t.Errorf("Open(\"\") = %q, %v", got, err)
	}
}

func TestOpenRefuses(t *testing.T) {
	s := testSealer(t)

	// An unprefixed value is a value this package did not write. Passing it
	// through would make a deployment that stopped sealing look healthy.
	if _, err := s.Open("529.982.247-25"); !errors.Is(err, ErrNotSealed) {
		t.Errorf("Open(plaintext) error = %v, want ErrNotSealed", err)
	}

	sealed, _ := s.Seal("11144477735")
	tampered := sealed[:len(sealed)-2] + "AA"
	if _, err := s.Open(tampered); err == nil {
		t.Errorf("Open accepted a tampered value")
	}

	other := NewSealer(base64.StdEncoding.EncodeToString([]byte("ffffffffffffffffffffffffffffffff")))
	if _, err := other.Open(sealed); err == nil {
		t.Errorf("Open accepted a value sealed with a different key")
	}
}

func TestKeyIsRequired(t *testing.T) {
	for name, key := range map[string]string{
		"empty":     "",
		"too short": base64.StdEncoding.EncodeToString([]byte("short")),
		"not a key": "not base64 or hex",
	} {
		t.Run(name, func(t *testing.T) {
			s := NewSealer(key)
			if s.Enabled() {
				t.Fatalf("NewSealer accepted %s", name)
			}
			// The failure has to surface on use, not be swallowed into a
			// pass-through that stores the CPF in the clear.
			if _, err := s.Seal("11144477735"); !errors.Is(err, ErrNoKey) {
				t.Errorf("Seal error = %v, want ErrNoKey", err)
			}
		})
	}
}
