package billing

// Ported from ctech-account (api/internal/domain/company/taxid.go), which is in
// an internal package and cannot be imported. ponytail: two copies of one rule;
// move it to ctech-go-common (api-commons) and have both import it.

import "strings"

const (
	TaxIDCNPJ = "cnpj"
	TaxIDCPF  = "cpf"
)

const (
	cnpjLength = 14
	cpfLength  = 11
	// cnpjBodyLength is how much of a CNPJ may be alphanumeric. The last two
	// positions are check digits and are numeric by rule.
	cnpjBodyLength = 12
)

// NormalizeTaxID canonicalizes a tax id and says which kind of document it is.
//
// Canonical means: mask removed, letters uppercased, nothing else touched. A
// CNPJ has been alphanumeric in its first twelve positions since the Receita
// Federal's 2026 change, so "digits only" is no longer the shape — but the two
// check digits stayed numeric, and a CPF stayed numeric throughout.
//
// The check digits are verified here rather than at the registry lookup because
// they are arithmetic, not a fact about the world: a CNPJ issued this morning is
// unknown to every public register and must still be accepted.
func NormalizeTaxID(raw string) (string, string, bool) {
	canonical, ok := canonicalize(raw)
	if !ok || allSameChar(canonical) {
		return "", "", false
	}
	switch len(canonical) {
	case cpfLength:
		// A CPF is numeric everywhere. Eleven characters holding a letter is
		// not a short CNPJ; it is nothing.
		if !isAllDigits(canonical) || !validCheckDigits(canonical, cpfLength-2, cpfWeight) {
			return "", "", false
		}
		return canonical, TaxIDCPF, true
	case cnpjLength:
		// The check digits must be numeric, or a letter would be fed to the
		// modulus arithmetic and could verify by accident.
		if !isAllDigits(canonical[cnpjBodyLength:]) || !validCheckDigits(canonical, cnpjBodyLength, cnpjWeight) {
			return "", "", false
		}
		return canonical, TaxIDCNPJ, true
	}
	return "", "", false
}

// canonicalize keeps alphanumerics, uppercases letters, and drops only the
// punctuation a mask is made of. Anything else is a character somebody typed by
// mistake, and dropping it silently could turn a typo into a different, valid
// document.
func canonicalize(raw string) (string, bool) {
	var b strings.Builder
	b.Grow(len(raw))
	for _, r := range raw {
		switch {
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 'a' + 'A')
		case r == '.' || r == '/' || r == '-' || r == ' ':
			// Mask punctuation, dropped.
		default:
			return "", false
		}
	}
	return b.String(), true
}

func isAllDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func allSameChar(s string) bool {
	if s == "" {
		return true
	}
	for i := 1; i < len(s); i++ {
		if s[i] != s[0] {
			return false
		}
	}
	return true
}

// charValue is the Receita Federal's rule for the alphanumeric CNPJ: a
// character's weight-input is its ASCII code minus 48, which leaves '0'–'9' as
// 0–9 and makes 'A' 17 through 'Z' 42. A numeric CNPJ and a CPF are digits, so
// the same rule reads them unchanged.
func charValue(c byte) int { return int(c) - 48 }

// The two documents share the modulus-11 skeleton and nothing else: their
// weight sequences genuinely differ, and treating them as one is a bug that
// still validates most inputs by luck.

// cnpjWeight cycles 2..9 from the right, restarting at 2 past the ninth
// position — the sequence published as 5,4,3,2,9,8,7,6,5,4,3,2 read right to
// left.
func cnpjWeight(posFromRight int) int { return posFromRight%8 + 2 }

// cpfWeight descends from bodyLen+1 down to 2 — the sequence published as
// 10,9,8,7,6,5,4,3,2 for the first digit and 11,…,2 for the second. It never
// wraps, because a CPF body is short enough not to need it.
func cpfWeight(posFromRight int) int { return posFromRight + 2 }

// validCheckDigits verifies the two trailing modulus-11 digits over a body of
// bodyLen characters, weighted by the given document's sequence. The second
// digit is computed over the body plus the first, which is why they cannot be
// checked independently.
func validCheckDigits(s string, bodyLen int, weight func(int) int) bool {
	if s[bodyLen] != checkDigit(s[:bodyLen], weight) {
		return false
	}
	return s[bodyLen+1] == checkDigit(s[:bodyLen+1], weight)
}

func checkDigit(body string, weight func(int) int) byte {
	sum := 0
	for i := len(body) - 1; i >= 0; i-- {
		sum += charValue(body[i]) * weight(len(body)-1-i)
	}
	if rem := sum % 11; rem >= 2 {
		return byte('0' + 11 - rem)
	}
	return '0'
}
