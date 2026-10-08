package finance

import (
	"errors"
	"testing"
)

func TestLedgerAccountValidate(t *testing.T) {
	ok := []LedgerAccount{
		{ID: "bank", Name: "Conta corrente", Class: ClassAsset},
		{ID: "food", Name: "Alimentação", Class: ClassExpense, Group: GroupOperatingExpenses},
		{ID: "sales", Name: "Vendas", Class: ClassIncome, Group: GroupGrossRevenue},
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
		"name past 80 characters": {ID: "a", Name: string(make([]byte, 81)), Class: ClassAsset},
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
