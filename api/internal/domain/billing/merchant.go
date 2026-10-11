package billing

// MerchantStatus is what the console shows about an account organization as a
// merchant (spec § 3): whether it has a tenant, in which modes, and later
// whether its application was rejected or its live tenant suspended.
//
// It is display data, never authority. Access is decided per request by
// ctech-account membership and by the tenant row the request reads (ADR 0028);
// nothing reads this value to allow anything.
type MerchantStatus string

const (
	// MerchantNone: no tenant in either mode. The console shows the invitation.
	MerchantNone MerchantStatus = "none"
	// MerchantTest: a test-mode tenant only (applied, live waiting on review).
	MerchantTest MerchantStatus = "test"
	// MerchantLive: a live tenant exists (approved, or provisioned by CTech).
	MerchantLive MerchantStatus = "live"
	// MerchantRejected: the latest application was rejected; the test tenant
	// stays. Computed from part 2 on.
	MerchantRejected MerchantStatus = "rejected"
	// MerchantSuspended: the live tenant is suspended (reads only). Computed
	// from part 2 on.
	MerchantSuspended MerchantStatus = "suspended"
)

// Valid reports one of the five wire values.
func (s MerchantStatus) Valid() bool {
	switch s {
	case MerchantNone, MerchantTest, MerchantLive, MerchantRejected, MerchantSuspended:
		return true
	}
	return false
}

// MerchantStatusOf derives the status from one account organization's tenant
// rows, nil when absent. Part 2 adds the application and the live row's
// suspension to its inputs; until then rejected and suspended cannot arise.
func MerchantStatusOf(test, live *Organization) MerchantStatus {
	switch {
	case live != nil:
		return MerchantLive
	case test != nil:
		return MerchantTest
	default:
		return MerchantNone
	}
}
