package space

import "strings"

// Selector is a parsed Billing-Space header: a request, not a permission.
type Selector struct {
	Personal       bool
	OrganizationID string
}

// ParseSelector reads "personal" or "org:{id}". "personal" carries no id and
// there is no syntax for another user's personal space (ADR 0025 rule 1).
//
// A value that is not one of the two shapes is ErrBadSelector (a client bug).
// A well-formed org: selector whose id is not a canonical organization id is
// ErrSpaceNotFound — the same error, hence the same response, as an
// organization that does not exist, so shape probing reveals nothing.
func ParseSelector(header string) (Selector, error) {
	if header == "personal" {
		return Selector{Personal: true}, nil
	}
	id, ok := strings.CutPrefix(header, "org:")
	if !ok || id == "" {
		return Selector{}, ErrBadSelector
	}
	if !validOrganizationID(id) {
		return Selector{}, ErrSpaceNotFound
	}
	return Selector{OrganizationID: id}, nil
}

// validOrganizationID accepts only a canonical lower-case UUID (ctech-account
// issues UUIDv7). The strictness is the defence: the id becomes part of a
// partition key, so it must not be able to contain '#', a prefix such as USER#,
// whitespace or path syntax. Upper-case is refused so one organization has one
// key.
func validOrganizationID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return false
			}
		}
	}
	return true
}
