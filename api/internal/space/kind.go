package space

// Kind is what a space is (ADR 0027). A ResolvedSpace always has one of the
// three below; a kind ctech-account sends that billing does not know is carried
// as itself only until VerbsFor refuses it.
type Kind string

const (
	// KindPersonalDefault is USER#{sub}: one per person, keyed on their
	// identity, never shareable.
	KindPersonalDefault Kind = "personal_default"
	// KindPersonal is a ctech-account workspace of kind personal: an additional
	// or shared personal space.
	KindPersonal Kind = "personal"
	// KindOrganization is a ctech-account workspace of kind organization.
	KindOrganization Kind = "organization"
)

// WorkspaceKind reads the kind ctech-account sent for a workspace. An absent
// kind is organization: the routes answered without one before ctech-account
// shipped personal workspaces, and a cache entry written before this deploy has
// none. For every role, organization grants no more than personal does, so the
// default can only narrow access (spec § 4). Any other value is kept as sent,
// so that VerbsFor refuses it rather than a new kind upstream inheriting a grant.
func WorkspaceKind(raw string) Kind {
	if raw == "" {
		return KindOrganization
	}
	return Kind(raw)
}
