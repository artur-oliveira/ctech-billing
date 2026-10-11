package finance

import (
	"errors"
	"strings"
	"testing"
)

func TestLedgerAccountValidate(t *testing.T) {
	ok := []LedgerAccount{
		{ID: "bank", Name: "Conta corrente", Class: ClassAsset},
		{ID: "food", Name: "Alimentação", Class: ClassExpense, Group: GroupOperatingExpenses},
		{ID: "sales", Name: "Vendas", Class: ClassIncome, Group: GroupGrossRevenue},
		// The limit is characters, as the API and the UI count it, not bytes:
		// 80 accented letters are 160 bytes and still a valid name.
		{ID: "accents", Name: strings.Repeat("é", 80), Class: ClassAsset},
	}
	for _, a := range ok {
		if err := a.Validate(); err != nil {
			t.Errorf("%+v: %v", a, err)
		}
	}
	bad := map[string]LedgerAccount{
		"no id":                   {Name: "x", Class: ClassAsset},
		"id with a separator":     {ID: "a#b", Name: "x", Class: ClassAsset},
		"no name":                 {ID: "a", Class: ClassAsset},
		"unknown class":           {ID: "a", Name: "x", Class: "banana"},
		"group on an asset":       {ID: "a", Name: "x", Class: ClassAsset, Group: GroupCosts},
		"income in a cost group":  {ID: "a", Name: "x", Class: ClassIncome, Group: GroupCosts},
		"name past 80 characters": {ID: "a", Name: strings.Repeat("a", 81), Class: ClassAsset},
		"81 accented characters":  {ID: "a", Name: strings.Repeat("é", 81), Class: ClassAsset},
	}
	for name, a := range bad {
		if err := a.Validate(); !errors.Is(err, ErrInvalidAccount) {
			t.Errorf("%s: err = %v, want ErrInvalidAccount", name, err)
		}
	}
}

func TestDefaultSystemAccounts(t *testing.T) {
	sys, accts := DefaultSystemAccounts()
	if len(accts) != 3 {
		t.Fatalf("%d system accounts", len(accts))
	}
	byID := map[string]LedgerAccount{}
	for _, a := range accts {
		if !a.System {
			t.Errorf("%s is not marked System", a.ID)
		}
		if err := a.Validate(); err != nil {
			t.Errorf("%s: %v", a.ID, err)
		}
		byID[a.ID] = a
	}
	if byID[sys.Payables].Class != ClassLiability || byID[sys.Receivables].Class != ClassAsset || byID[sys.OpeningBalance].Class != ClassEquity {
		t.Fatalf("classes: %+v", byID)
	}
}

func TestDefaultCategoriesAreValidAndCoverTheSettlementGaps(t *testing.T) {
	for _, personal := range []bool{true, false} {
		seen := map[string]bool{}
		names := map[string]bool{}
		for _, a := range DefaultCategories(personal) {
			if err := a.Validate(); err != nil || a.System || (a.Class != ClassIncome && a.Class != ClassExpense) || a.Group == "" {
				t.Errorf("personal=%v: %+v: %v", personal, a, err)
			}
			if seen[a.ID] {
				t.Errorf("personal=%v: duplicate id %s", personal, a.ID)
			}
			seen[a.ID], names[a.Name] = true, true
		}
		// Spec § 3.2: a settlement for a different amount needs somewhere to go.
		if !names["Juros e multas"] || !names["Descontos obtidos"] {
			t.Errorf("personal=%v: the settlement-gap categories are missing", personal)
		}
	}
	if len(DefaultCategories(true)) == len(DefaultCategories(false)) && DefaultCategories(true)[0].Name == DefaultCategories(false)[0].Name {
		t.Error("personal and organization spaces get different sets (spec § 3.3)")
	}
}
