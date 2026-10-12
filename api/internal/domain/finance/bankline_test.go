package finance

import (
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
)

func TestABankLineMovesCashAgainstItsCategory(t *testing.T) {
	d := brcal.MustParse("2026-10-01")
	in, err := BankLine("byok-inter-abc", UnclassifiedInID, true, 1050, d)
	if err != nil {
		t.Fatal(err)
	}
	if in.Kind != KindBankLine || in.Legs[0].AccountID != "byok-inter-abc" || in.Legs[0].Amount != 1050 || in.Legs[0].Flow != UnclassifiedInID {
		t.Fatalf("credit: %+v", in)
	}
	out, _ := BankLine("byok-inter-abc", UnclassifiedOutID, false, 200, d)
	if out.Legs[1].AccountID != "byok-inter-abc" || out.Legs[1].Amount != -200 {
		t.Fatalf("debit: %+v", out)
	}
	if _, err := BankLine("a", "c", true, 0, d); err == nil {
		t.Fatal("a zero line is not a transaction")
	}
}

func TestTheUnclassifiedCategoriesAreValidAndOther(t *testing.T) {
	for _, c := range UnclassifiedCategories() {
		if err := c.Validate(); err != nil || c.Group != GroupOther || c.SystemKey == "" {
			t.Fatalf("%+v: %v", c, err)
		}
	}
}
