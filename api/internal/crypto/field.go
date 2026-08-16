// Package crypto encrypts individual field values before they are stored.
//
// DynamoDB's own server-side encryption protects the disk. It does not protect
// the value from anything holding read access to the table — a support tool, a
// migration script, an export, a role that accumulated `dynamodb:Query` for some
// other reason. For a CPF that difference is the whole of the control: the
// record ARCHITECTURE.md § 9 promises is "encrypted at rest by the repository
// layer", and until this package existed that sentence was not true.
//
// Scope is deliberately narrow: one AEAD, one key, values that are read back by
// id and never searched. Anything that needed to be *queried* by its encrypted
// value would need deterministic encryption, which leaks equality and is a
// different decision — billing has no such field, and this package should not
// grow one quietly.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// prefix marks a value this package produced, and carries the key generation.
//
// Without it, rotation is guesswork: the only way to know whether a stored
// string is v1 or v2 ciphertext is to try one and see whether the tag verifies,
// which is indistinguishable from tampering. With it, a second key is additive —
// new writes stamp v2, old reads still resolve v1.
const prefix = "v1."

// ErrNotSealed reports a stored value that this package did not produce.
//
// It is an error rather than a pass-through. Returning the raw value when the
// prefix is missing would mean a deployment that silently stopped encrypting
// reads exactly like one that never started, and the failure would be
// discovered by whoever exports the table.
var ErrNotSealed = errors.New("value is not sealed")

// ErrNoKey reports a Sealer built without a usable key.
var ErrNoKey = errors.New("field encryption key is missing or invalid")

// Sealer encrypts and decrypts one field at a time.
//
// A construction failure is carried rather than returned, so a repository
// constructor stays a constructor. It is safe because the key is validated at
// startup by config.Load: the only way to hold a broken Sealer is to build a
// Config by hand, which is a test, where failing loudly on first use is the
// behaviour worth having.
type Sealer struct {
	aead cipher.AEAD
	err  error
}

// NewSealer builds a Sealer from a 32-byte key given as base64 or hex.
//
// Both encodings are accepted because ctech-account's SECRET_ENC_KEY accepts
// both, and an operator who has generated one of these before should not have to
// discover that this service is fussier.
func NewSealer(key string) *Sealer {
	raw, err := decodeKey(key)
	if err != nil {
		return &Sealer{err: err}
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return &Sealer{err: fmt.Errorf("%w: %s", ErrNoKey, err)}
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return &Sealer{err: fmt.Errorf("%w: %s", ErrNoKey, err)}
	}
	return &Sealer{aead: aead}
}

// Enabled reports whether this Sealer can be used at all.
func (s *Sealer) Enabled() bool { return s != nil && s.err == nil }

// Seal encrypts a value. Empty in, empty out — an absent tax id is absent, not
// a ciphertext of nothing, which would make every unset field distinguishable
// and identical.
func (s *Sealer) Seal(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	if s == nil || s.err != nil {
		return "", s.keyErr()
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generating nonce: %w", err)
	}
	sealed := s.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return prefix + base64.RawStdEncoding.EncodeToString(sealed), nil
}

// Open reverses Seal.
func (s *Sealer) Open(stored string) (string, error) {
	if stored == "" {
		return "", nil
	}
	if s == nil || s.err != nil {
		return "", s.keyErr()
	}
	body, ok := strings.CutPrefix(stored, prefix)
	if !ok {
		return "", ErrNotSealed
	}
	raw, err := base64.RawStdEncoding.DecodeString(body)
	if err != nil {
		return "", fmt.Errorf("decoding sealed value: %w", err)
	}
	if len(raw) < s.aead.NonceSize() {
		return "", errors.New("sealed value is too short")
	}
	nonce, ct := raw[:s.aead.NonceSize()], raw[s.aead.NonceSize():]
	plaintext, err := s.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		// Deliberately not wrapped with the underlying message. A GCM failure is
		// either the wrong key or a tampered value, and neither is something to
		// describe to a caller in detail.
		return "", errors.New("sealed value did not verify")
	}
	return string(plaintext), nil
}

func (s *Sealer) keyErr() error {
	if s == nil {
		return ErrNoKey
	}
	return s.err
}

func decodeKey(key string) ([]byte, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("%w: empty", ErrNoKey)
	}
	// No dev fallback, and that is a deliberate departure from ctech-account's
	// crypto package, which encrypts with a constant when the variable is unset.
	// A default key is a key in the repository: everything encrypted with it is
	// encrypted with something published, and the deployment that forgets to set
	// the real one looks identical to the one that did not.
	if b, err := base64.StdEncoding.DecodeString(key); err == nil && len(b) == 32 {
		return b, nil
	}
	if b, err := hex.DecodeString(key); err == nil && len(b) == 32 {
		return b, nil
	}
	return nil, fmt.Errorf("%w: must decode to exactly 32 bytes as base64 or hex", ErrNoKey)
}
