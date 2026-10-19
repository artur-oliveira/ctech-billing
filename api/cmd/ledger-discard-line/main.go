// Command ledger-discard-line undoes bank lines that were posted when they
// should not have been, typically a payment the ledger already counted through its
// settlement (the BYOK statement sync read the line before the bank completed it).
// Each line is reversed on its own date and leaves "A classificar", in one
// transaction; nothing is deleted from the history.
//
//	ledger-discard-line -space 0190... -mode live -tx 01M4ZSXV...,01M500CR...           # report only
//	ledger-discard-line -space 0190... -mode live -tx 01M4ZSXV...,01M500CR... -apply    # discard
//
// -space is an operator's decision, not a request: the binary builds its space with
// space.ForJob, which the request path may not call. Only a bank line still on
// "A classificar" is discarded. Afterwards run finance-rebuild for the same space
// (it should report no drift).
package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"strings"
	"time"

	"gopkg.aoctech.app/billing/api/internal/app"
	"gopkg.aoctech.app/billing/api/internal/config"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	spaceFlag := flag.String("space", "", "organization id or USER#{sub} (required)")
	mode := flag.String("mode", "", "test or live (required)")
	txs := flag.String("tx", "", "comma-separated transaction ids of the bank lines to discard (required)")
	apply := flag.Bool("apply", false, "discard the lines (default: report only)")
	flag.Parse()

	if *spaceFlag == "" || *txs == "" || (*mode != "test" && *mode != "live") {
		// No default for -mode, on purpose: a mistyped flag must not act on the other world.
		flag.Usage()
		os.Exit(2)
	}
	sp, err := space.ForJob(*spaceFlag, *mode == "live")
	if err != nil {
		slog.Error("invalid space", "space", *spaceFlag, "error", err)
		os.Exit(2)
	}
	cfg, err := config.Load()
	if err != nil {
		slog.Error("configuration", "error", err)
		os.Exit(2)
	}
	ctx := context.Background()
	ledger, err := app.BuildLedger(ctx, cfg)
	if err != nil {
		slog.Error("startup", "error", err)
		os.Exit(2)
	}

	failed := 0
	for _, id := range strings.Split(*txs, ",") {
		id = strings.TrimSpace(id)
		tx, err := ledger.GetTransaction(ctx, sp, id)
		if err != nil || tx.Tx.Kind != finance.KindBankLine {
			slog.Error("not a bank line of this space", "tx", id, "error", err)
			failed++
			continue
		}
		slog.Info("bank line", "tx", id, "date", tx.Tx.Date.String(), "memo", tx.Memo, "legs", len(tx.Tx.Legs), "apply", *apply)
		if !*apply {
			continue
		}
		rev, err := ledger.DiscardBankLine(ctx, sp, id, repositories.PostMeta{Actor: "operator:ledger-discard-line", Memo: "duplicate of a settled payment"}, time.Now())
		if err != nil {
			slog.Error("not discarded", "tx", id, "error", err)
			failed++
			continue
		}
		slog.Info("discarded", "tx", id, "reversal", rev)
	}
	slog.Info("done", "space", sp.PK(), "table_prefix", cfg.TablePrefix, "applied", *apply, "failed", failed)
	if failed > 0 {
		os.Exit(1)
	}
}
