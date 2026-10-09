package space

// postingVerbs are what a posting rule needs in a space: read the bill it may
// already have written, write the fact, settle it. Never Import or Configure.
const postingVerbs = Read | Write | Settle

// ForInvoiceIssuer is the space of the organization that issued a paid invoice
// (spec § 3.8): its ctech-account organization id, which an operator linked to
// billing's tenant in the tenant plan. ForInvoicePayer is the personal space of
// the person who paid it, from the customer's ctech-account subject.
//
// Neither takes anything from a request, and only the posting rule may call them
// (TestPostingSpacesAreBuiltOnlyByTheInvoicePostingRule). livemode is the
// invoice's: a test invoice never reaches a live ledger.
func ForInvoiceIssuer(organizationID string, livemode bool) (ResolvedSpace, error) {
	if !validOrganizationID(organizationID) {
		return ResolvedSpace{}, ErrSpaceNotFound
	}
	return orgSpace(organizationID, KindOrganization, livemode, postingVerbs), nil
}

// ForInvoicePayer: see ForInvoiceIssuer.
func ForInvoicePayer(sub string, livemode bool) (ResolvedSpace, error) {
	sp, err := personalSpace(sub, livemode)
	if err != nil {
		return ResolvedSpace{}, err
	}
	return Narrow(sp, postingVerbs), nil
}

// IsOrganizationID reports whether s is a canonical ctech-account organization
// id, the only shape a space key accepts. The tenant plan checks its link with
// it before anything is written.
func IsOrganizationID(s string) bool { return validOrganizationID(s) }
