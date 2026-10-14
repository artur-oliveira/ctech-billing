# Wallet ledger — P3: Merge `game` into `real` — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every user's `game` balance becomes `real` balance, with a ledger entry on both sides, and from then on holds, cashouts and balance reads work on `real`. The `game` wallet stops existing for new users.

**Architecture:** A flag `GAME_WALLET_MERGED` (default `false`) switches the Wallet service's game-facing operations (`HoldGame`, `ReleaseHold`, `CashoutGame`, balance reads) from the `game` wallet to `real`, and retires the ring-fence routes. A one-off command merges existing balances per user under the ordered wallet lock using the existing atomic `WalletRepository.Transfer`, aborting the whole run if the before and after totals differ. Consent, personal limits and self-exclusion stay in Wallet until P4 moves them.

**Tech Stack:** Go (Fiber v3, fx, DynamoDB conditional transactions, Valkey locks), the `cmd/reconcile` Lambda/CLI pattern.

**Spec:** `docs/specs/2026-10-14-wallet-ledger-billing-rail.md` (§7, §8 P3, §9). **Depends on:** nothing in P1/P2 (independent), but ship **after** P2 so a rollback of the rail never coincides with a rewrite of balances.

Repo: `P_WALLET=/home/artur-revgas/Documents/Projects/Ctech/ctech-wallet` (Go in `P_WALLET/api`); Poker's read of balances is checked in Task 6 (`ctech-poker/api/internal/walletclient`).

## Global Constraints

- No balance is lost or created. For every user: `real_after = real_before + game_before`, and the sum over all users is identical before and after (spec §8 P3).
- The ledger is append-only: the merge writes new entries, never edits or deletes one (invariant 2). The `game` wallet row is **kept** and marked retired, never deleted.
- Every step is idempotent: re-running the command, or merging a user twice, changes nothing the second time (idempotency key `game-merge#<user_id>`).
- Locks are taken with `lock.AcquireOrdered` on both wallet ids, never by hand (invariant 5).
- No `real → sandbox` path appears, and `TestSandboxPurchaseNeverDebitsRealWallet` stays green (invariants 6–7).
- While `GAME_WALLET_MERGED=true` and before P4 ships, personal gambling limits on real-money Poker are **not enforced** (the `real → game` edge they metered is gone). This phase therefore ships only while Poker's `REAL_MONEY_ENABLED` is off, which Task 1 verifies and Task 7 refuses to run without.
- Constants for entry types and flag names; RFC 7807 problems; no attribution trailers.

## Review Focus

- A user with an **open hold** (`held`) is skipped and reported, never merged; the run still finishes for everyone else. Pinned in Task 3.
- A deposit credit landing on `real` **while** the user's merge runs: both succeed in order, final `real` = deposit + game balance. Pinned in Task 3.
- A user with a zero `game` balance, or no `game` wallet at all, is a no-op (no empty ledger entries). Pinned in Task 3.
- The same merge replayed (Lambda retry, operator re-run) credits `real` once. Pinned in Task 3.
- After the flag is on, a cashout for a hold created **before** the merge (wallet id = the retired `game` wallet) credits `real`, not the retired wallet. Pinned in Task 4.
- A client still calling `POST /wallet/game/deposit` or `/sandbox/purchase` after the flag gets a clear `410 gone`, not a silent success or a 500. Pinned in Task 4.

---

## Task 1: Preflight (read-only)

**Files:**
- Create: `api/cmd/merge-game/main.go`, `api/internal/services/game_merge_report.go`, `game_merge_report_test.go`

**Interfaces:**
- Produces:
```go
type MergeReport struct {
	Users, WithGameBalance, ZeroBalance, OpenHolds int
	TotalGameCents, TotalRealCents                 int64 // before
	RealMoneyPokerEnabled                          bool  // from the flag the caller passes in
}
func (s *WalletService) PreflightGameMerge(ctx context.Context) (*MergeReport, error)
```

- [ ] **Step 1: Failing test:** with a fake repo holding three users (one with a `game` balance of 1000 and an open hold, one with 500, one with none), `PreflightGameMerge` returns `WithGameBalance: 2, OpenHolds: 1, TotalGameCents: 1500`.
- [ ] **Step 2: Run** `go test ./internal/services -run PreflightGameMerge -v` — FAIL.
- [ ] **Step 3: Implement.** Page all wallets of `type = game` (an index query if the table has one by type; otherwise a filtered scan, acceptable at the current closed-group size — say which you used in the PR) and list `held` holds with `repo.ListHeldHolds` (add it beside `ListStaleHolds` in `repositories/holds.go`, same paging). `cmd/merge-game --dry-run` prints the report as JSON.
- [ ] **Step 4:** PASS. **Commit** `feat(wallet): game-merge preflight report`.

## Task 2: Entry types and the retired marker

**Files:**
- Modify: `api/internal/domain/wallet/model.go`, `api/internal/repositories/wallet.go`
- Test: `api/internal/domain/wallet/model_test.go`, `api/internal/repositories/wallet_*_test.go`

- [ ] **Step 1: Failing tests:** `wallet.EntryGameMergeDebit` / `EntryGameMergeCredit` exist and are in whatever list the model test pins for ledger entry types (read `model_test.go` for how the existing types are pinned); `Wallet.RetiredAt` round-trips; `repo.RetireWallet(ctx, walletID, expectedVersion)` sets it only when the balance is 0 (conditional), and `EnsureGamblingWallets` called while `GAME_WALLET_MERGED` is set returns only a sandbox wallet and never creates a `game` one.
- [ ] **Step 2: Run** — FAIL. **Step 3: Implement** (`RetiredAt string \`dynamodbav:"retired_at,omitempty"\``; `RetireWallet` as a conditional update `balance = :zero AND attribute_not_exists(retired_at)`).
- [ ] **Step 4:** PASS. **Commit** `feat(wallet): merge entry types and a retired marker for the game wallet`.

## Task 3: `MergeGameIntoReal`

**Files:**
- Create: `api/internal/services/game_merge.go`, `game_merge_test.go`

**Interfaces:**
- Consumes: `repo.Transfer(ctx, fromWalletID, toWalletID, amount, creditAmount, debitType, creditType, ref, idemKey, reqHash, extra...)` (`repositories/wallet.go`), `lock.AcquireOrdered`, `repo.RetireWallet`, `repo.ListHeldHoldsForWallet`.
- Produces:
```go
type MergeResult string // "merged" | "noop" | "skipped_open_hold"
func (s *WalletService) MergeGameIntoReal(ctx context.Context, userID string) (MergeResult, int64 /*moved cents*/, error)
```

- [ ] **Step 1: Failing tests** (the fake repo/lock helpers used in `wallet_lock_test.go` and `wallet_test.go`):

```go
func TestMergeMovesTheWholeGameBalanceToReal(t *testing.T) {
	s, repo := newMergeFixture(t, mergeUser{ID: "u1", Real: 2500, Game: 1000})
	res, moved, err := s.MergeGameIntoReal(ctx, "u1")
	require.NoError(t, err)
	require.Equal(t, MergeMerged, res)
	require.Equal(t, int64(1000), moved)
	require.Equal(t, int64(3500), repo.Balance(t, "u1", wallet.TypeReal))
	require.Equal(t, int64(0), repo.Balance(t, "u1", wallet.TypeGame))
	require.True(t, repo.Retired(t, "u1", wallet.TypeGame))
	require.Len(t, repo.EntriesOfType(t, "u1", wallet.EntryGameMergeCredit), 1)
}

func TestMergeTwiceIsANoopTheSecondTime(t *testing.T) { /* run twice; second returns MergeNoop, real unchanged, still one credit entry */ }
func TestMergeSkipsAUserWithAnOpenHold(t *testing.T)   { /* Hold in `held` -> MergeSkippedOpenHold, balances untouched */ }
func TestMergeZeroBalanceWritesNoEntries(t *testing.T) { /* Game: 0 -> MergeNoop, no entries, wallet retired */ }
func TestMergeRacesADepositAndKeepsBothAmounts(t *testing.T) { /* goroutine credits real 700 during merge; final real = start+700+game */ }
```

  (`newMergeFixture`, `mergeUser` and the `repo.*` helpers are test helpers you write in `game_merge_test.go` over the in-memory repo the other service tests use; keep them in that file.)
- [ ] **Step 2: Run** `go test ./internal/services -run Merge -v` — FAIL.
- [ ] **Step 3: Implement:** load wallets; if no game wallet or already retired → `noop`; list held holds on the game wallet → `skipped_open_hold`; `AcquireOrdered(real, game)`; re-read the game wallet under the lock; if balance > 0, `repo.Transfer(game→real, bal, bal, EntryGameMergeDebit, EntryGameMergeCredit, "game-merge", "game-merge#"+userID, reqHash(userID, bal))`; then `RetireWallet`. A replay hits `Transfer`'s idempotency and returns the original entries. Description on both entries: `"Saldo de jogos unificado ao saldo real"` (mandatory-description rule).
- [ ] **Step 4:** PASS (`-race`). **Commit** `feat(wallet): merge a user's game balance into real`.

## Task 4: The flag — game operations act on `real`

**Files:**
- Modify: `api/internal/config/config.go` (`GAME_WALLET_MERGED`, default false), `api/internal/services/wallet.go` (`requireActivated`, `HoldGame`, `ReleaseHold`, `CashoutGame`, `BalancesFor`, `FundGame`, `ReturnFromGame`, `PurchaseSandbox`), `api/internal/api/v1/router.go`, `api/internal/api/v1/wallet.go`
- Test: `wallet_test.go`, `router_test.go`

- [ ] **Step 1: Failing tests (flag on):** `HoldGame` debits `real` and creates the hold with `WalletID = real`; `CashoutGame` credits `real` even for a hold whose `WalletID` is the retired game wallet; `ReleaseHold` likewise; `GET /wallet` returns `game: null` (field kept so old clients parse) and `real` = the old sum; `POST /wallet/game/deposit`, `/game/withdraw`, `/sandbox/purchase`, `/sandbox/purchases` (the game-funded ones) return `410` with problem code `route-retired`; with the flag **off** every one of these behaves exactly as before (run the existing tests unchanged).
- [ ] **Step 2: Run** `go test ./internal/services ./internal/api/v1 -v` — FAIL.
- [ ] **Step 3: Implement** with one helper, `s.gameWalletFor(ctx, userID) (*wallet.Wallet, error)`, returning the `real` wallet when merged and the `game` wallet otherwise, used by the four operations. Everything else (activation consent gate, `requireNotExcluded`, limits) is untouched: those move in P4.
- [ ] **Step 4:** PASS; run the whole suite and `TestSandboxPurchaseNeverDebitsRealWallet`. **Commit** `feat(wallet): GAME_WALLET_MERGED switches game operations to the real wallet`.

## Task 5: The batch command

**Files:**
- Modify: `api/cmd/merge-game/main.go`
- Create: `api/internal/services/game_merge_run.go`, `game_merge_run_test.go`

**Interfaces:**
- Produces:
```go
type RunSummary struct {
	Merged, Noop, SkippedOpenHold, Failed int
	TotalGameBefore, TotalRealBefore, TotalRealAfter int64
}
func (s *WalletService) RunGameMerge(ctx context.Context, limit int) (*RunSummary, error)
```

- [ ] **Step 1: Failing tests:** a run over five users (mixed cases from Task 3) returns counts that sum to five; `TotalRealAfter == TotalRealBefore + movedSum` where `movedSum` is the sum of `moved` over merged users; a run whose fake repo is rigged to lose 1 cent on one transfer returns an error `ErrMergeTotalsDiffer` and the command exits non-zero **before** the next user is touched; re-running the command after a partial run finishes the rest and double-credits nobody.
- [ ] **Step 2: Implement** the loop (ordered by user id, resumable by the idempotency key, no state needed) and the final check. `--dry-run` calls `PreflightGameMerge`. `main.go` follows `cmd/reconcile/main.go` (Lambda handler or CLI by env); refuse to run unless `REAL_MONEY_ENABLED` is reported off by the caller (`--poker-real-money-off` flag the operator must pass explicitly after checking Poker's config).
- [ ] **Step 3:** PASS. **Commit** `feat(wallet): game-merge command with totals check`.

## Task 6: Readers of `game` (Wallet UI and Poker)

**Files:**
- Modify: `ui/src` wallet screens (grep `game` in `ui/src` — the balance card, the ring-fence deposit/return dialogs, the limits page copy), locale files `pt-BR`/`en`; `ctech-poker/api/internal/walletclient/client.go` (`Balances`) and its tests

- [ ] **Step 1: Failing UI tests:** with `game: null` the dashboard renders one balance (Real) plus Sandbox, and no "Dinheiro de Jogos" card or ring-fence dialog; the statement tab no longer offers a `game` ledger. Update snapshots/tests accordingly.
- [ ] **Step 2: Failing Poker test** (`walletclient/gamewallet_test.go`): `Balances` against a response with `game: null` returns the real balance for real-money buy-in affordability and does not error on the null.
- [ ] **Step 3: Implement** both; `npx eslint src --ext .ts,.tsx` clean (the wallet UI gate: zero errors **and** zero warnings).
- [ ] **Step 4: Commit** in each repo: `feat(wallet-ui): one Real balance; no game wallet card` / `fix(poker): tolerate a null game balance`.

## Task 7: Run it

**Files:**
- Create: `docs/runbooks/game-merge.md` (in `P_WALLET`)

- [ ] **Step 1: Runbook:** (1) confirm Poker `REAL_MONEY_ENABLED=false` in the target environment; (2) `merge-game --dry-run`, paste the report; (3) deploy Wallet with `GAME_WALLET_MERGED=false` (code only); (4) run the command in non-prod, compare `TotalRealAfter`; (5) in prod, take a DynamoDB on-demand backup first; run the command; verify the summary and spot-check the owner's own ledger; (6) set `GAME_WALLET_MERGED=true`, deploy; (7) rollback = `merge-game --reverse` (below) and flag off.
- [ ] **Step 2: Reverse command, because the ledger is append-only:** `--reverse` finds each `game-merge#<user>` pair and, for users whose `real` still covers the moved amount, transfers it back with idempotency key `game-unmerge#<user>`; users whose `real` no longer covers it (they spent it) are reported, never forced. Failing test first for both cases.
- [ ] **Step 3:** Do steps 1–4 and report. **Ask the owner before step 5** (live balances).
- [ ] **Step 4: Commit.** Report: no new infra; the DynamoDB backup is a one-time on-demand cost.

## Self-review (against the spec)

| Spec requirement | Task |
|---|---|
| §8 P3 merge `game` → `real`, idempotent, ledger-preserving, before/after check | 3, 5 |
| §9 invariants kept; ring-fence rules retired | 4 (routes `410`), 2 (no new `game` wallets) |
| §7 `game` removed from readers | 6 |
| Risk: limits gap until P4 | Global Constraints, 5, 7 (refuses without real-money off) |
| Rollback | 7 |
