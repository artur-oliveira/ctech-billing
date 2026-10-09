// Package space is the authority on which finance partition a request may touch.
//
// The console sends X-Billing-Space: personal | org:{id}. That header is a
// request. The only way to obtain a ResolvedSpace for it is Resolver.Resolve,
// which checks the token's own subject and ctech-account membership first; and
// the type has no exported field, so no handler can build one from the header
// (ADR 0025). Repositories accept nothing else.
package space

import (
	"errors"
	"strings"
)

var (
	// ErrNoSpace is a repository handed the zero ResolvedSpace.
	ErrNoSpace = errors.New("space: no resolved space")
	// ErrDenied is a verb the space's role does not carry.
	ErrDenied = errors.New("space: verb not granted")
	// ErrSpaceNotFound is every reason a space is refused: no membership, an
	// unknown organization, a malformed id. One error, so one response.
	ErrSpaceNotFound = errors.New("space: not found")
	// ErrSpaceUnavailable is ctech-account being unreachable. Access fails
	// closed; this is never read as permission and never as "not found".
	ErrSpaceUnavailable = errors.New("space: membership unavailable")
	// ErrBadSelector is a X-Billing-Space value that is not personal or org:{id}.
	ErrBadSelector = errors.New("space: malformed selector")
	// ErrInvalidSubject is a token whose subject cannot be a key component.
	ErrInvalidSubject = errors.New("space: invalid subject")
)

// ResolvedSpace names a finance partition and what the caller may do in it.
// Fields are unexported on purpose; TestResolvedSpaceHasNoExportedFields pins it.
type ResolvedSpace struct {
	owner    string // organization id, or "USER#{sub}"
	personal bool
	kind     Kind
	livemode bool
	verbs    Verbs
}

const userPrefix = "USER#"

// PK is the space key S: the leading part of every finance partition key.
func (s ResolvedSpace) PK() string {
	if s.owner == "" {
		return ""
	}
	return s.owner + "#" + s.Mode()
}

// Owner is the organization id, or USER#{sub} for a personal space.
func (s ResolvedSpace) Owner() string { return s.owner }

// Personal is the default personal space, USER#{sub} — not a personal
// workspace, which is Kind() == KindPersonal.
func (s ResolvedSpace) Personal() bool { return s.personal }

// Kind is personal_default for USER#{sub}, else the workspace's kind. The
// console uses it to decide what to show; it authorizes nothing by itself.
func (s ResolvedSpace) Kind() Kind { return s.kind }

// OrganizationID is the workspace id (an organization's or a personal
// workspace's), and empty for the default personal space.
func (s ResolvedSpace) OrganizationID() string {
	if s.personal {
		return ""
	}
	return s.owner
}

func (s ResolvedSpace) Livemode() bool { return s.livemode }

// Mode is "live" or "test", the suffix of the space key.
func (s ResolvedSpace) Mode() string {
	if s.livemode {
		return "live"
	}
	return "test"
}

func (s ResolvedSpace) Verbs() Verbs { return s.verbs }

// Can reports whether every verb in v is held.
func (s ResolvedSpace) Can(v Verbs) bool { return !s.IsZero() && s.verbs.Has(v) }

// IsZero is the value no resolver returned.
func (s ResolvedSpace) IsZero() bool { return s.owner == "" }

// Require is the check repositories make before a write: the space must be a
// real one and must hold the verb.
func (s ResolvedSpace) Require(v Verbs) error {
	if s.IsZero() {
		return ErrNoSpace
	}
	if !s.verbs.Has(v) {
		return ErrDenied
	}
	return nil
}

func personalSpace(sub string, livemode bool) (ResolvedSpace, error) {
	if sub == "" || strings.Contains(sub, "#") {
		return ResolvedSpace{}, ErrInvalidSubject
	}
	return ResolvedSpace{owner: userPrefix + sub, personal: true, kind: KindPersonalDefault, livemode: livemode, verbs: All}, nil
}

func orgSpace(orgID string, kind Kind, livemode bool, verbs Verbs) ResolvedSpace {
	return ResolvedSpace{owner: orgID, kind: kind, livemode: livemode, verbs: verbs}
}

// ForJob builds a space for a binary (cmd/finance, cmd/finance-rebuild) that
// chooses its space by operator decision, not from a request. It holds every
// verb. It is forbidden on the request path by TestForJobIsNotUsedOnTheRequestPath.
func ForJob(owner string, livemode bool) (ResolvedSpace, error) {
	if sub, ok := strings.CutPrefix(owner, userPrefix); ok {
		return personalSpace(sub, livemode)
	}
	if !validOrganizationID(owner) {
		return ResolvedSpace{}, ErrSpaceNotFound
	}
	// A job cannot know a workspace's kind and reports organization; jobs never
	// seed a space (only a request does), so the kind changes nothing they write.
	return orgSpace(owner, KindOrganization, livemode, All), nil
}

// Narrow returns s holding only the verbs in v that s already held. It can
// remove access, never add it, which is why it is safe to export.
func Narrow(s ResolvedSpace, v Verbs) ResolvedSpace {
	s.verbs &= v
	return s
}
