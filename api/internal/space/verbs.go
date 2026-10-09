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

// VerbsFor maps a ctech-account workspace kind and role to verbs. The role in
// ctech-account decides reach; the product decides what it may do (ADR 0023),
// and the same role means different things on the two kinds (ADR 0027):
//
//   - organization: owner/admin all, member all but configure, viewer read.
//   - personal: owner and member (Acesso total) all, viewer (Leitura) read.
//     admin does not exist there — ctech-account refuses it — so it grants
//     nothing and the answer is the ordinary 404.
//
// Any other kind or role grants nothing: a ladder or a kind that grows upstream
// must never inherit a grant by default.
func VerbsFor(kind Kind, role string) Verbs {
	switch kind {
	case KindOrganization:
		switch role {
		case "owner", "admin":
			return All
		case "member":
			return All &^ Configure
		case "viewer":
			return Read
		}
	case KindPersonal:
		switch role {
		case "owner", "member":
			return All
		case "viewer":
			return Read
		}
	}
	return 0
}
