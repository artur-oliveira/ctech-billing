//go:build integration

package integration

import (
	"errors"
	"testing"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/domain/finance/statement"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

func TestManualMovementsIntoASyncedAccountAreRefused(t *testing.T) {
	ctx := ctxT(t)
	ledger := repositories.NewLedgerRepository(testDB, testCfg)
	sp, _ := space.ForJob(newSpaceOrgID(), true)
	if err := ledger.EnsureReady(ctx, sp, now()); err != nil {
		t.Fatal(err)
	}
	if _, err := ledger.EnsureByokAccount(ctx, sp, "byok-inter-abc", "Inter ···4567", "inter", now()); err != nil {
		t.Fatal(err)
	}
	if err := ledger.CreateAccount(ctx, sp, finance.LedgerAccount{ID: "caixa", Name: "Caixa", Class: finance.ClassAsset}, now()); err != nil {
		t.Fatal(err)
	}
	d := brcal.MustParse("2026-10-01")
	meta := repositories.PostMeta{Actor: "u", RequestID: "r"}
	if _, err := ledger.PostTransfer(ctx, sp, "caixa", "byok-inter-abc", 100, d, meta, now()); !errors.Is(err, repositories.ErrAccountIsSynced) {
		t.Fatalf("transfer in: %v", err)
	}
	if _, err := ledger.PostTransfer(ctx, sp, "byok-inter-abc", "caixa", 100, d, meta, now()); !errors.Is(err, repositories.ErrAccountIsSynced) {
		t.Fatalf("transfer out: %v", err)
	}
	if _, err := ledger.PostOpeningBalance(ctx, sp, "byok-inter-abc", 100, d, meta, now()); !errors.Is(err, repositories.ErrAccountIsSynced) {
		t.Fatalf("opening: %v", err)
	}
	imports := repositories.NewImportRepository(testDB, testCfg)
	if _, err := imports.Import(ctx, sp, "byok-inter-abc", statement.FormatOFX, oneLineOFX(t), "", now()); !errors.Is(err, repositories.ErrAccountIsSynced) {
		t.Fatalf("import: %v", err)
	}
}

func oneLineOFX(t *testing.T) statement.Parsed {
	t.Helper()
	p, err := statement.ParseOFX(syntheticOFX(ofxLine("F1", "20261001", "10.00", "uma linha")))
	if err != nil {
		t.Fatal(err)
	}
	return p
}
