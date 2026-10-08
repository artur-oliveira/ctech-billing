package space

// Verbs is a set of finance verbs (spec § 6.1). The role in ctech-account
// decides reach; the product decides what a role may do (ADR 0023).
type Verbs uint8

const (
	Read Verbs = 1 << iota
	Write
	Settle
	Import
	Configure

	All = Read | Write | Settle | Import | Configure
)

var verbNames = []struct {
	v    Verbs
	name string
}{
	{Read, "finance.read"},
	{Write, "finance.write"},
	{Settle, "finance.settle"},
	{Import, "finance.import"},
	{Configure, "finance.configure"},
}

// Has reports whether every verb in want is held.
func (v Verbs) Has(want Verbs) bool { return v&want == want }

// Names lists the held verbs in a stable order, for the console.
func (v Verbs) Names() []string {
	var out []string
	for _, n := range verbNames {
		if v.Has(n.v) {
			out = append(out, n.name)
		}
	}
	return out
}

// VerbsForRole maps a ctech-account organization role to verbs. v1 derives them
// directly from the role, with no billing-side role table until somebody needs
// a finer grant. An unknown role grants nothing: a ladder that grows a fifth
// rung must not silently grant it everything.
func VerbsForRole(role string) Verbs {
	switch role {
	case "owner", "admin":
		return All
	case "member":
		return All &^ Configure
	case "viewer":
		return Read
	default:
		return 0
	}
}
