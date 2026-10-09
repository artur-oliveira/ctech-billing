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

func TestBillingCategoriesAreSeeded(t *testing.T) {
	find := func(personal bool, id string) (LedgerAccount, bool) {
		for _, a := range DefaultCategories(personal) {
			if a.ID == id {
				return a, true
			}
		}
		return LedgerAccount{}, false
	}
	rev, ok := find(false, CategorySubscriptionRevenue)
	if !ok || rev.Class != ClassIncome || rev.Group != GroupGrossRevenue || rev.Name != "Assinaturas" {
		t.Fatalf("organization revenue category = %+v, %v", rev, ok)
	}
	if _, ok := find(true, CategorySubscriptionRevenue); ok {
		t.Error("a personal space does not sell subscriptions")
	}
	for _, personal := range []bool{true, false} {
		exp, ok := find(personal, CategoryCTechSubscriptions)
		if !ok || exp.Class != ClassExpense || exp.Group != GroupOperatingExpenses || exp.Name != "Assinaturas CTech" {
			t.Fatalf("personal=%v: expense category = %+v, %v", personal, exp, ok)
		}
	}
	if BillingCategory(Receivable) != rev {
		t.Errorf("BillingCategory(receivable) = %+v", BillingCategory(Receivable))
	}
	if exp, _ := find(true, CategoryCTechSubscriptions); BillingCategory(Payable) != exp {
		t.Errorf("BillingCategory(payable) = %+v", BillingCategory(Payable))
	}
}
