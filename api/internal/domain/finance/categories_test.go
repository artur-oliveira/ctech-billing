package finance

import "testing"

func TestDefaultAccountsHaveUniqueSystemKeys(t *testing.T) {
	_, system := DefaultSystemAccounts()
	for _, personal := range []bool{true, false} {
		seen := map[string]string{}
		for _, a := range append(append([]LedgerAccount{}, system...), DefaultCategories(personal)...) {
			if a.SystemKey == "" {
				t.Errorf("personal=%v: %s has no system key", personal, a.ID)
			}
			if prev, dup := seen[a.SystemKey]; dup {
				t.Errorf("personal=%v: key %q used by %s and %s", personal, a.SystemKey, prev, a.ID)
			}
			seen[a.SystemKey] = a.ID
		}
	}
}
