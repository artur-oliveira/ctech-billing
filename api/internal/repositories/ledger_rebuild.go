package repositories

import (
	"context"
	"sort"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// StoredLedger is what the cache rows hold, in the shape Derive produces.
type StoredLedger struct {
	Balances map[string]billing.Cents
	Summary  map[finance.SummaryKey]finance.Totals
}

// Drift is one cached value that differs from the derivation.
type Drift struct {
	Kind    string // "balance" | "summary"
	Account string
	Month   string // summary only, "yyyy-mm"

	Stored, Derived                                            billing.Cents // balance
	StoredDebits, StoredCredits, DerivedDebits, DerivedCredits billing.Cents // summary
}

// Compare lists every cached balance or summary that is not what the entries
// say. Missing on either side counts as zero.
func Compare(stored StoredLedger, derived finance.Derived) []Drift {
	var out []Drift
	accounts := map[string]struct{}{}
	for a := range stored.Balances {
		accounts[a] = struct{}{}
	}
	for a := range derived.Balances {
		accounts[a] = struct{}{}
	}
	for a := range accounts {
		if s, d := stored.Balances[a], derived.Balances[a]; s != d {
			out = append(out, Drift{Kind: "balance", Account: a, Stored: s, Derived: d})
		}
	}
	keys := map[finance.SummaryKey]struct{}{}
	for k := range stored.Summary {
		keys[k] = struct{}{}
	}
	for k := range derived.Summary {
		keys[k] = struct{}{}
	}
	for k := range keys {
		if s, d := stored.Summary[k], derived.Summary[k]; s != d {
			out = append(out, Drift{Kind: "summary", Account: k.AccountID, Month: k.Month.String(),
				StoredDebits: s.Debits, StoredCredits: s.Credits, DerivedDebits: d.Debits, DerivedCredits: d.Credits})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		if out[i].Month != out[j].Month {
			return out[i].Month < out[j].Month
		}
		return out[i].Account < out[j].Account
	})
	return out
}

// Rebuild derives balances and summaries from the space's transactions and
// compares them with the cache rows. With apply it overwrites each drifted row
// with the derived value. See the contract in the plan: run when quiet, repeat
// until the report is empty.
func (r *LedgerRepository) Rebuild(ctx context.Context, sp space.ResolvedSpace, apply bool, now time.Time) ([]Drift, error) {
	if err := sp.Require(space.Configure); err != nil {
		return nil, err
	}
	txs, err := r.AllTransactions(ctx, sp)
	if err != nil {
		return nil, err
	}
	derived := finance.Derive(txs)

	accounts, err := r.ListAccounts(ctx, sp)
	if err != nil {
		return nil, err
	}
	stored := StoredLedger{Balances: map[string]billing.Cents{}, Summary: map[finance.SummaryKey]finance.Totals{}}
	for _, a := range accounts {
		stored.Balances[a.ID] = a.Balance
	}
	rows, err := r.allSummaries(ctx, sp)
	if err != nil {
		return nil, err
	}
	for _, s := range rows {
		stored.Summary[finance.SummaryKey{Month: s.Month, AccountID: s.AccountID}] = s.Totals
	}

	drift := Compare(stored, derived)
	if !apply {
		return drift, nil
	}
	known := make(map[string]bool, len(accounts))
	for _, a := range accounts {
		known[a.ID] = true
	}
	return drift, r.applyDrift(ctx, sp, drift, known, now)
}

// applyDrift overwrites each drifted cache row with the derived value. A balance
// drift for an account that is not in the chart (a "ghost") is reported but not
// written: updating a missing key would create a stray row, and a balance with
// no account is something an operator must investigate, not paper over.
func (r *LedgerRepository) applyDrift(ctx context.Context, sp space.ResolvedSpace, drift []Drift, known map[string]bool, now time.Time) error {
	stamp := now.UTC().Format(time.RFC3339Nano)
	for _, d := range drift {
		switch d.Kind {
		case "balance":
			if !known[d.Account] {
				continue
			}
			sk := LedgerAccountSK(d.Account)
			if _, err := r.accounts.UpdateItem(ctx, sp.PK(), &sk, map[string]any{
				"balance": int64(d.Derived), "updated_at": stamp,
			}); err != nil {
				return err
			}
		case "summary":
			month, err := brcal.Parse(d.Month + "-01")
			if err != nil {
				return err
			}
			sk := LedgerSummarySK(finance.MonthOf(month), d.Account)
			// Upsert: a derived summary that was never stored has no row to update.
			if err := r.accounts.UpsertAttrs(ctx, sp.PK(), &sk, map[string]any{
				"debits": int64(d.DerivedDebits), "credits": int64(d.DerivedCredits), "updated_at": stamp,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}
