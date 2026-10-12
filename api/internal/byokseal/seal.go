// Package byokseal seals BYOK credential bundles (spec § 3.3): AES-256-GCM,
// random nonce, prefix "b1.", additional data binding the ciphertext to its
// connection. Only cmd/byok-outbound imports it (imports_test.go): the key
// lives in that Lambda alone.
package byokseal

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

const prefix = "b1."

var (
	ErrNotSealed = errors.New("byokseal: value is not a b1 bundle")
	ErrOpen      = errors.New("byokseal: bundle does not open under this key and binding")
)

type Sealer struct{ aead cipher.AEAD }

func New(keyB64 string) (*Sealer, error) {
	k, err := base64.StdEncoding.DecodeString(strings.TrimSpace(keyB64))
	if err != nil || len(k) != 32 {
		return nil, fmt.Errorf("byokseal: the key must be 32 bytes, base64")
	}
	block, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

// AAD binds a bundle to {account_organization_id, mode, provider, connection_id, version}.
func AAD(accountOrgID, mode, provider, connectionID string, version int) []byte {
	return fmt.Appendf(nil, "%s|%s|%s|%s|%d", accountOrgID, mode, provider, connectionID, version)
}

func (s *Sealer) Seal(plain, aad []byte) (string, error) {
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(s.aead.Seal(nonce, nonce, plain, aad)), nil
}

func (s *Sealer) Open(sealed string, aad []byte) ([]byte, error) {
	body, ok := strings.CutPrefix(sealed, prefix)
	if !ok {
		return nil, ErrNotSealed
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil || len(raw) < s.aead.NonceSize() {
		return nil, ErrOpen
	}
	n := s.aead.NonceSize()
	plain, err := s.aead.Open(nil, raw[:n], raw[n:], aad)
	if err != nil {
		return nil, ErrOpen
	}
	return plain, nil
}
