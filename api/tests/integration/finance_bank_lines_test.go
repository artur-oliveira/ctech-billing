//go:build integration

package integration

import (
	"errors"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

func byokSpace(t *testing.T) (*repositories.LedgerRepository, space.ResolvedSpace) {
	t.Helper()
	ctx := ctxT(t)
	ledger := repositories.NewLedgerRepository(testDB, testCfg)
	sp, _ := space.ForJob(newSpaceOrgID(), true)
	if err := ledger.EnsureReady(ctx, sp, now()); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.EnsureByokAccount(ctx, sp, "byok-inter-abc", "Inter ···4567", "inter", now()); err != nil {
		t.Fatal(err)
	}
	return ledger, sp
}

func byokBalance(t *testing.T, ledger *repositories.LedgerRepository, sp space.ResolvedSpace, id string) int64 {
	a, err := ledger.GetAccount(ctxT(t), sp, id)
	if err != nil {
		t.Fatal(err)
	}
	return int64(a.Balance)
}

func TestSyncingTheSameRangeTwiceAddsNothing(t *testing.T) {
	ctx := ctxT(t)
	ledger, sp := byokSpace(t)
	lines := []repositories.BankLine{
		{Provider: "inter", LineID: "T1", Date: brcal.MustParse("2026-10-01"), Credit: true, Amount: 10000, Memo: "Pix recebido"},
		{Provider: "inter", LineID: "T2", Date: brcal.MustParse("2026-10-02"), Credit: false, Amount: 2500, Memo: "Tarifa"},
	}
	for pass := range 2 {
		for _, l := range lines {
			_, posted, err := ledger.PostBankLine(ctx, sp, "byok-inter-abc", l, repositories.PostMeta{Actor: "job"}, now())
			if err != nil || posted != (pass == 0) {
				t.Fatalf("pass %d line %s: posted=%v err=%v", pass, l.LineID, posted, err)
			}
		}
	}
	if got := byokBalance(t, ledger, sp, "byok-inter-abc"); got != 7500 {
		t.Fatalf("balance %d, want 7500", got)
	}
}

func TestALineReusedWithAnotherAmountIsPostedOnce(t *testing.T) {
	ctx := ctxT(t)
	ledger, sp := byokSpace(t)
	a := repositories.BankLine{Provider: "inter", LineID: "T9", Date: brcal.MustParse("2026-10-01"), Credit: true, Amount: 100}
	b := a
	b.Amount = 150 // the bank corrected the line under the same id
	for _, l := range []repositories.BankLine{a, b, b} {
		if _, _, err := ledger.PostBankLine(ctx, sp, "byok-inter-abc", l, repositories.PostMeta{}, now()); err != nil {
			t.Fatal(err)
		}
	}
	if got := byokBalance(t, ledger, sp, "byok-inter-abc"); got != 250 {
		t.Fatalf("balance %d, want 250: each distinct (id, date, amount) once", got)
	}
}

func TestClassificationNeverMovesTheBalance(t *testing.T) {
	ctx := ctxT(t)
	ledger, sp := byokSpace(t)
	bills := repositories.NewBillRepository(testDB, testCfg)
	l := repositories.BankLine{Provider: "inter", LineID: "T1", Date: brcal.MustParse("2026-10-01"), Credit: true, Amount: 10000}
	txID, _, _ := ledger.PostBankLine(ctx, sp, "byok-inter-abc", l, repositories.PostMeta{}, now())
	un, _ := ledger.Unclassified(ctx, sp, "byok-inter-abc")
	if len(un) != 1 || un[0].TxID != txID {
		t.Fatalf("unclassified %+v", un)
	}
	if _, err := ledger.Classify(ctx, bills, sp, txID, repositories.Classification{CategoryID: "cat-vendas"}, repositories.PostMeta{Actor: "u"}, now()); err != nil {
		t.Fatal(err)
	}
	if got := byokBalance(t, ledger, sp, "byok-inter-abc"); got != 10000 {
		t.Fatalf("balance moved to %d", got)
	}
	if un, _ := ledger.Unclassified(ctx, sp, "byok-inter-abc"); len(un) != 0 {
		t.Fatalf("still unclassified: %+v", un)
	}
	if _, err := ledger.Classify(ctx, bills, sp, txID, repositories.Classification{CategoryID: "cat-vendas"}, repositories.PostMeta{}, now()); !errors.Is(err, repositories.ErrNotUnclassified) {
		t.Fatalf("classifying twice: %v", err)
	}
}

func TestTheProviderOpeningPlusLinesEqualsTheProviderBalance(t *testing.T) {
	ctx := ctxT(t)
	ledger, sp := byokSpace(t)
	if _, err := ledger.PostProviderOpening(ctx, sp, "byok-inter-abc", 50000, brcal.MustParse("2026-06-30"), repositories.PostMeta{}, now()); err != nil {
		t.Fatal(err)
	}
	_, _, _ = ledger.PostBankLine(ctx, sp, "byok-inter-abc", repositories.BankLine{Provider: "inter", LineID: "T1", Date: brcal.MustParse("2026-07-01"), Credit: false, Amount: 1234}, repositories.PostMeta{}, now())
	_ = ledger.SetProviderBalance(ctx, sp, "byok-inter-abc", repositories.ProviderBalance{Available: 48766, At: brcal.MustParse("2026-07-01"), SyncedAt: now()})
	a, _ := ledger.GetAccount(ctx, sp, "byok-inter-abc")
	if int64(a.Balance) != int64(a.Provider.Available) {
		t.Fatalf("ledger %d, provider %d", a.Balance, a.Provider.Available)
	}
}

func TestALineSettlesOnlyABillOfItsOwnDirection(t *testing.T) {
	ctx := ctxT(t)
	ledger, sp := byokSpace(t)
	bills := repositories.NewBillRepository(testDB, testCfg)
	d := brcal.MustParse("2026-10-05")
	payable, err := bills.Create(ctx, sp, finance.Bill{Direction: finance.Payable, Amount: 2500, AccountID: "byok-inter-abc",
		CategoryID: "cat-aluguel", Description: "Aluguel", Competence: d, Due: d, Origin: finance.OriginManual},
		repositories.PostMeta{Actor: "u"}, now())
	if err != nil {
		t.Fatal(err)
	}
	in, _, _ := ledger.PostBankLine(ctx, sp, "byok-inter-abc", repositories.BankLine{Provider: "inter", LineID: "IN", Date: d, Credit: true, Amount: 2500}, repositories.PostMeta{}, now())
	if _, err := ledger.Classify(ctx, bills, sp, in, repositories.Classification{BillID: payable.ID}, repositories.PostMeta{Actor: "u"}, now()); err == nil {
		t.Fatal("money in settled a payable: the bank balance would move")
	}
	out, _, _ := ledger.PostBankLine(ctx, sp, "byok-inter-abc", repositories.BankLine{Provider: "inter", LineID: "OUT", Date: d, Credit: false, Amount: 2500}, repositories.PostMeta{}, now())
	if _, err := ledger.Classify(ctx, bills, sp, out, repositories.Classification{BillID: payable.ID}, repositories.PostMeta{Actor: "u"}, now()); err != nil {
		t.Fatalf("money out settles the payable: %v", err)
	}
	if got := byokBalance(t, ledger, sp, "byok-inter-abc"); got != 0 {
		t.Fatalf("balance %d, want 0 (+2500 -2500, classification moves nothing)", got)
	}
}
