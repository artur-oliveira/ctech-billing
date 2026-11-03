# Card Statement Import Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Import the Inter credit card CSV into a card's open statement, repeatably and without duplicates, pairing lines with purchases the person already entered, creating the rest, and supporting a standalone credit.

**Architecture:** A card import reuses the account-import storage (`IMPORT#` rows, per-line status, per-line lock rows with 90-day TTL) with the lock scope `<cardID>#<statementMonth>`, so `statement.Keys`, `claim` and `decide` are reused unchanged. Parsing and matching are pure functions in `domain/finance/statement`. Pairing is stored as a `CARDPAIR#` lock row written in the same transaction that resolves the line. New purchases go through `CardRepository.addPurchase` (its `extras` hook carries the line decision); a new `CardRepository.AddCredit` makes the standalone credit.

**Tech Stack:** Go (fiber v3, DynamoDB single-table-per-domain via `repositories.Base`), Next.js 16 UI (`@aoctech/ui`, react-query, i18next pt-BR + en).

**Spec:** `docs/specs/2026-11-02-card-statement-import-design.md`

## Global Constraints

- Money is integer cents (`billing.Cents`), never float. Bank text `R$<nbsp>1.234,56` / `-R$<nbsp>5,00` parses to cents.
- File ≤ 1 MiB (`statement.MaxFileBytes`) and ≤ 2000 lines (`statement.MaxLines`); whole file validated before anything is stored; a bad line is rejected (line + reason) and the rest proceed.
- Only the card's **open** statement accepts an import; a closed one is refused with `statement_not_open` (the UI offers `POST /cards/:id/reopen`, shipped in #293). Import never reopens or closes.
- Constants, not settings: pairing date window 2 days, installment cents tolerance 5 (R$ 0,05), group sum tolerance 5 in total, group ≤ 6 lines and ≤ 20 candidate lines.
- Created installment purchase: total = installment amount × (n − k + 1), n − k + 1 installments, purchase date = `min(statement closing date, today)`; original date and `k/n` go in the description note. Refused if the statement period has not begun (today ≤ previous closing date).
- Resolved lines stay resolved forever; a re-import never reopens a decision. Pairs are 1-to-1 (lock row per item). No new AWS resource, alarm or custom metric. No scan, no N+1 (one statement-partition query + one `CARDPAIR#` prefix query per review).
- Errors are RFC 7807 via `problem` with a `code`; the UI maps by `code`; every POST is idempotent (`Idempotency-Key`); copy in pt-BR and en.
- Never commit the real CSV: fixtures are a trimmed copy with anonymised descriptions.
- CI gate: `go vet`, `staticcheck`, `go test -race`, integration tests against DynamoDB local (`api/docker-compose.test.yml`, `DYNAMODB_ENDPOINT=http://localhost:8124`; without it they are skipped, not passed), `scripts/standards-check.sh`, ESLint zero warnings, `tsc`.

## Review Focus

- A file with only the payment line, or only rejected lines: import stores nothing, returns the counts, creates no import row.
- Two identical lines in one file, and a later file with the same two plus a third: the third is new, the first two are duplicates.
- A manual purchase keyed *after* the import: it is found unclaimed and pairs on the next review.
- Two lines match one item (or one line matches two items on adjacent days): the closest date wins, then file order, deterministically across re-imports.
- A line whose purchase was later edited or refunded: the line stays resolved; confirming a pair on an item another line already claimed fails with `pair_taken`, never double-claims.

---

## File Structure

- Create `api/internal/domain/finance/statement/inter_card.go`: `CardLine`, `ParseInterCard`.
- Create `api/internal/domain/finance/statement/card_match.go`: `CardItem`, `CardSuggestion`, `SuggestCard`.
- Create `api/internal/repositories/card_imports.go`: card import upload, suggestions, pair/group/create/credit decisions.
- Create `api/internal/repositories/card_bank_categories.go`: per-card bank-category mapping.
- Modify `api/internal/repositories/imports.go`: extract `store` from `Import`; add fields to `Import`/`importItem`/`lineItem`/`ImportLine`.
- Modify `api/internal/repositories/cards_purchases.go`: add `AddCredit`.
- Modify `api/internal/domain/finance/posting.go`: add `CardCredit`.
- Create `api/internal/api/v1/finance_card_imports.go`; modify `finance_http.go`, `finance_cards.go` (credit), `problem/problem.go`.
- Create `ui/src/components/finance/CardImportView.tsx` (+ test); modify `CardsView.tsx` (entry point, credit form), `lib/api/finance.ts`, `financeTypes.ts`, locales.
- Tests: `domain/finance/statement/*_test.go`, `api/tests/integration/finance_card_imports_test.go`.

---

### Task 1: Inter CSV parser

**Files:**
- Create: `api/internal/domain/finance/statement/inter_card.go`
- Test: `api/internal/domain/finance/statement/inter_card_test.go`

**Interfaces:**
- Produces:
  ```go
  type CardLine struct {
      Line                 // Date, Amount (card's side: purchase > 0, credit/payment < 0), Description; FITID always ""
      BankCategory string  // "SUPERMERCADO"; "" when absent
      K, N         int     // "Parcela k/n"; 0,0 for "Compra à vista"
      Payment      bool    // the previous statement's payment line
  }
  func (l CardLine) Remaining() int // N-K+1 for an installment, 1 otherwise
  func ParseInterCard(data []byte) (lines []CardLine, rejected []Rejected, err error)
  ```

- [ ] **Step 1: Write the failing tests** in `inter_card_test.go`, table-driven over inline CSV bytes (UTF-8 with BOM, header `"Data","Lançamento","Categoria","Tipo","Valor"`):
  - `TestInterCardParsesAmountsAndDates`: `"29/10/2026","99APP  99App  BRA","TRANSPORTE","Compra à vista","R$ 6,50"` → Amount 650, Date 2026-10-29, K=N=0, Remaining()=1; `"R$ 5.052,57"` → 505257; `"-R$ 5,00"` → -500.
  - `TestInterCardInstallment`: `Tipo` `Parcela 3/12`, `R$ 23,83` → K=3, N=12, Remaining()=10, Amount 2383.
  - `TestInterCardPaymentLine`: Lançamento `PAGAMENTO ON LINE`, `-R$ 5.052,57` → `Payment` true, Amount -505257.
  - `TestInterCardRejectsBadRowsAndKeepsTheRest`: a bad date, a bad amount and `R$ 0,00` produce `Rejected{Line, Reason}` with `ReasonDate`/`ReasonAmount`/`ReasonZero` (row = file row, 1-based, header is row 1) and the good rows still parse.
  - `TestInterCardWrongFileIsUnreadable`: a header that is not the five Inter columns → `ErrUnreadable`; empty body → `ErrEmpty`; `> MaxLines` rows → `ErrTooMany`; `> MaxFileBytes` → `ErrTooLarge`.
  - `TestInterCardReadsWindows1252`: non-UTF-8 bytes decode via the existing `decodeText`.

- [ ] **Step 2: Run** `cd api && go test ./internal/domain/finance/statement -run InterCard -v` → FAIL (undefined).

- [ ] **Step 3: Implement** `ParseInterCard` with `decodeText`, `encoding/csv` (delimiter `,`, `LazyQuotes`), header check, `cleanText(…, maxDescription)` for the description; amount parser strips `R$`, NBSP, `.` thousands, `,` decimal, leading `-`, using integer arithmetic only. `Payment` = description starts with `PAGAMENTO`. Reuse existing `Reason*` constants.

- [ ] **Step 4: Run** the same command → PASS. Add `go vet ./...`.

- [ ] **Step 5: Commit** `feat(statement): parse the Inter credit card CSV`.

---

### Task 2: Pairing and grouping rules

**Files:**
- Create: `api/internal/domain/finance/statement/card_match.go`
- Test: `api/internal/domain/finance/statement/card_match_test.go`

**Interfaces:**
- Consumes: `CardLine`, `CardLine.Remaining()` (Task 1).
- Produces:
  ```go
  type CardItem struct { // an installment-kind item of the target statement, unclaimed
      PurchaseID, Tag, Description string
      Date   brcal.Date
      Amount billing.Cents
      Number, Of int
  }
  func (i CardItem) Remaining() int // Of-Number+1

  type SuggestionKind string // "pair" | "group" | "ignore"
  type CardSuggestion struct {
      Kind   SuggestionKind
      Lines  []int // line numbers (1-based file order); 1 for pair/ignore, 2..6 for group
      Item   *CardItem // pair/group target; nil for ignore
  }
  const (
      CardDateWindow = 2   // days
      CardCentsTolerance = 5
      MaxGroupLines = 6
      MaxGroupCandidates = 20
  )
  func SuggestCard(lines map[int]CardLine, items []CardItem) []CardSuggestion
  ```

- [ ] **Step 1: Write the failing tests** (`card_match_test.go`), using the real-data shapes:
  - `TestPairsAVistaBySameAmountWithinTwoDays`: line 26/10 R$ 72,03 and item 25/10 72,03 → pair; item 23/10 → no pair (3 days); amount 72,04 → no pair.
  - `TestPairsInstallmentByRemainingAndCents`: line `Parcela 8/12` 30946 vs item 1/5 30948 → pair (diff 2, remaining 5 = 5); vs item 1/6 → no pair; diff 6 → no pair; **dates are ignored** for installments (line 2026-03-20, item 2026-10-11).
  - `TestGroupsInstallmentLinesThatSumToOneItem`: four `Parcela 3/12` lines 2383, 2698, 3098, 2655 and one item `Compras Shein (Agrupadas)` 10834, 1/10 → one `group` suggestion with the four line numbers; a fifth unrelated 3/12 line is not pulled in; sum off by 6 → no group; lines of mixed remaining counts → no group.
  - `TestPaymentLineIsSuggestedAsIgnore`: `Payment` line → `ignore`, never a pair.
  - `TestAmbiguityIsDeterministic`: two items with the same amount on days 24 and 25 for a line on 25 → picks 25; equal distance → earlier file order; each item used by at most one line; shuffling the item slice does not change the result.
  - `TestRefundLineHasNoPair`: a negative non-payment line is never paired with an item (it becomes a credit).

- [ ] **Step 2: Run** `go test ./internal/domain/finance/statement -run 'Pairs|Group|Payment|Ambiguity|Refund' -v` → FAIL.

- [ ] **Step 3: Implement** `SuggestCard`: process lines in file order; candidates are unclaimed `installment`-kind items; à vista needs equal amount and `|days| ≤ CardDateWindow`; installment needs `|Δ| ≤ CardCentsTolerance` and equal `Remaining()`; rank by date distance (installment: amount distance) then item key; groups: among lines still unpaired with the same `Remaining()` > 1, per item try subsets of size 2..`MaxGroupLines` over at most `MaxGroupCandidates` lines (sorted by file order), accept the first subset whose sum is within `CardCentsTolerance` of the item amount and whose `Remaining()` equals the item's.

- [ ] **Step 4: Run** → PASS.

- [ ] **Step 5: Commit** `feat(statement): suggest pairs and groups for card statement lines`.

---

### Task 3: Standalone credit

**Files:**
- Modify: `api/internal/domain/finance/posting.go` (add `CardCredit`), `api/internal/repositories/cards_purchases.go` (add `AddCredit`), `api/internal/api/v1/finance_http.go`, `finance_cards.go`
- Test: `api/internal/domain/finance/posting_test.go`, `api/tests/integration/finance_card_credits_test.go`

**Interfaces:**
- Produces:
  ```go
  // domain/finance
  func CardCredit(cardAccountID, categoryID string, amount billing.Cents, date brcal.Date) (Transaction, error) // legs: category -amount, card +amount; amount > 0 else ErrInvalidTransaction
  // repositories
  type Credit struct{ ID, CardID, Description, CategoryID string; Date brcal.Date; Amount billing.Cents } // Amount > 0
  func (r *CardRepository) AddCredit(ctx context.Context, sp space.ResolvedSpace, cardID string, c Credit, meta PostMeta, now time.Time) (Credit, error)
  func (r *CardRepository) addCredit(ctx context.Context, sp space.ResolvedSpace, cardID string, c Credit, meta PostMeta, now time.Time, extras purchaseExtras) (Credit, error)
  ```
  `POST /cards/:id/credits` `{description, category_id, date, amount}` → 201 credit DTO; `space.Write`, idempotent; `meta.IdempotencyKey` makes the id `idempotentID(sp, "card-credit", key)`.

- [ ] **Step 1: Write the failing tests.**
  - Domain `TestCardCreditReversesAPurchase`: `CardCredit("visa","food",500,d)` legs are the negation of `CardPurchase("visa","food",500,d)`; amount 0 → `ErrInvalidTransaction`.
  - Integration (`finance_card_credits_test.go`, reuse `newCardsFixture`): `TestACreditLowersTheOpenStatementAndTheCard` — purchase 10000, `AddCredit` 500 → open statement total 9500, a `credit` item of -500 on the open month, card balance owed 9500, expense category 9500; `TestACreditIsIdempotent` — same idempotency key twice → one item; `TestACreditRefusesAnArchivedCardAndAnUnusableCategory`; `TestACreditLargerThanTheStatementCarriesToTheNextOne` — closing a statement with total < 0 creates the existing carry.

- [ ] **Step 2: Run** `go test ./internal/domain/finance -run CardCredit` and the integration tests with `DYNAMODB_ENDPOINT` set → FAIL.

- [ ] **Step 3: Implement** `CardCredit` next to `CardPurchase`. `addCredit` mirrors `addPurchase` for one installment: `activeCard`, `expenseCategory`, ledger plan (`meta.Origin = "card_credit"`), one `itemRow{Kind: "credit", Amount: -amount}` at `ItemSK(card.OpenMonth, c.ID, "credit")` put-if-absent, `cardBump`, `extras`; same cancellation handling and idempotent replay. A credit dated in the future is allowed (like a purchase); it always lands on the open statement. No edit/delete for a credit in this build.

- [ ] **Step 4: Run** → PASS (`go test -race ./internal/... ` and the integration suite).

- [ ] **Step 5: Commit** `feat(cards): standalone credit on the open statement`.

---

### Task 4: Card import upload and storage

**Files:**
- Modify: `api/internal/repositories/imports.go`
- Create: `api/internal/repositories/card_imports.go`
- Test: `api/tests/integration/finance_card_imports_test.go`

**Interfaces:**
- Consumes: `statement.CardLine`, `statement.Keys`, existing `claim`, `Import`, `ImportLine`, `LineStatus`.
- Produces:
  - `Import` gains `Kind string` (`""` account, `"card"`), `StatementMonth string`; `ImportLine` gains `K, N int`, `BankCategory string`. Row attributes `kind`, `statement_month`, `k`, `n`, `bank_category` (omitempty). Existing account imports are unchanged.
  - `func (r *ImportRepository) store(ctx context.Context, sp space.ResolvedSpace, imp Import, keyed []statement.Keyed, extra map[int]cardLineExtra, now time.Time) (Import, error)` — the body of today's `Import` after the account check, so both uploads share it.
  - ```go
    var ErrStatementNotOpen = errors.New("imports: the card's statement is not open")
    var ErrStatementNotBegun = errors.New("imports: the statement period has not begun")
    func (r *ImportRepository) ImportCard(ctx context.Context, sp space.ResolvedSpace, cardID string, month finance.Month, lines []statement.CardLine, rejected []statement.Rejected, idempotencyKey string, now time.Time) (Import, error)
    ```
  `ImportRepository` gets a `cards *CardRepository` field set in `NewImportRepository`.

- [ ] **Step 1: Write the failing integration tests** (`TestCardImport…`, fixture from `newCardsFixture` with the card's open month):
  - `…StoresPendingLinesAndSkipsDuplicates`: import 3 lines → `Lines==3`, all `pending`; the same lines again → `ID==""`, `Duplicates==3`, no new import row.
  - `…OverlappingFileAddsOnlyTheNew`: file A lines {x,y}; file B {x,y,y,z} (y twice) → B adds one `y` and `z`, duplicates 2.
  - `…IdenticalLinesAreTwoLines`: two identical lines in one file → `Lines==2`.
  - `…KeyIncludesTipo`: same date/amount/description with `Compra à vista` vs `Parcela 2/6` are two lines.
  - `…RefusesAClosedOrFutureStatement` → `ErrStatementNotOpen`; a card whose period has not begun → `ErrStatementNotBegun`; an archived card → `ErrCardArchived`.
  - `…RetryWithTheSameKeyResumesTheSameImport`.

- [ ] **Step 2: Run** (`DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -race ./tests/integration -run CardImport -v`) → FAIL.

- [ ] **Step 3: Extract `store`** from `Import` (behaviour-preserving; run the existing import tests first and after). **Implement `ImportCard`**: `sp.Require(space.Import)`, `activeCard`, month must equal `card.OpenMonth`, `ErrStatementNotBegun` when `today ≤ ClosingDate(month−1)`; scope = `cardID+"#"+month.String()`; build keys with `statement.Keys(scope, lines)` over lines whose `Description` temporarily carries `" ["+Tipo+"]"` so the hash covers the installment, then restore the clean description before storing; `Import.AccountID = cardID`, `Kind="card"`, `StatementMonth`; the idempotent import id uses `fileFingerprint(scope, "inter_csv", …)`.

- [ ] **Step 4: Run** → PASS, plus the full existing import tests.

- [ ] **Step 5: Commit** `feat(imports): store a card statement import without duplicates`.

---

### Task 5: Suggestions and decisions

**Files:**
- Modify: `api/internal/repositories/card_imports.go`
- Test: `api/tests/integration/finance_card_imports_test.go`

**Interfaces:**
- Consumes: `SuggestCard` (Task 2), `AddCredit`/`addCredit` (Task 3), `addPurchase`'s `extras` hook, `decide`, `ImportLockSK`.
- Produces:
  ```go
  func CardPairSK(cardID string, month finance.Month, purchaseID, tag string) string // "CARDPAIR#<card>#<month>#<purchase>#<tag>"
  var ErrPairTaken = errors.New("imports: that purchase is already paired with another line")
  func (r *ImportRepository) CardSuggestions(ctx context.Context, sp space.ResolvedSpace, importID string, now time.Time) (Import, []ImportLine, []statement.CardSuggestion, error)
  func (r *ImportRepository) ConfirmPair(ctx context.Context, sp space.ResolvedSpace, importID string, lines []int, purchaseID, tag string, now time.Time) ([]ImportLine, error) // 1 line = pair, 2..6 = group
  func (r *ImportRepository) ConfirmAll(ctx context.Context, sp space.ResolvedSpace, importID string, now time.Time) (confirmed int, err error)
  func (r *ImportRepository) CreateFromCardLine(ctx context.Context, sp space.ResolvedSpace, importID string, n int, categoryID, description string, meta PostMeta, now time.Time) (ImportLine, error)
  ```
  `ConfirmPair` records the link only (line `linked`, `bill_id` holds the purchase id, nothing posted). `CreateFromCardLine` creates a purchase (à vista or remaining-installments as in Global Constraints) for a positive line and a standalone credit for a negative non-payment line, in one transaction with `decide`; the purchase/credit id is `idempotentID(sp, "card-import-line", importID+"#"+n)`.

- [ ] **Step 1: Write the failing integration tests:**
  - `…SuggestsOnlyUnclaimedInstallmentItems`: items = purchase installments of the target statement, never credits/carries/advances; an item paired by line 1 is absent from the suggestions for line 2.
  - `…ConfirmPairLinksAndPostsNothing`: ledger balances unchanged, line `linked`, `Resolved` +1.
  - `…ConfirmPairTwiceOnOneItemIsPairTaken`: second line on the same item → `ErrPairTaken`, the first stays linked.
  - `…ConfirmGroupResolvesAllLinesWithOneClaim` (the four Shein lines → one purchase).
  - `…ConfirmAllConfirmsEverySuggestedPairAndGroup`, and is a no-op the second time.
  - `…ManualPurchaseKeyedAfterImportIsFoundOnReview`.
  - `…CreateInstallmentLineKeysRemainingInstallments`: `Parcela 8/12` 30946 → purchase total 154730, 5 installments, first on the open statement, description note keeps the original date and `8/12`; retry → same purchase.
  - `…CreateAVistaLineUsesFileDate`; `…CreateRefundLineMakesACredit` (-500 → credit item -500 in the given category); `…CreatePaymentLineIsRefused`.
  - `…ResolvedStaysResolvedWhenThePurchaseIsRefunded`; `…IgnoreAndReopenReuseTheExistingFlow`.
  - `…TotalAfterCreatingEverythingMatchesTheFileExcludingIgnored` (statement total = file sum minus ignored lines).

- [ ] **Step 2: Run** → FAIL.

- [ ] **Step 3: Implement.** `CardSuggestions`: one `GetStatement` (items), one query on `CARDPAIR#<card>#<month>#` to drop claimed items, `Get` lines, `SuggestCard`. `ConfirmPair`: one transaction = conditional put of the `CARDPAIR#` lock (`attribute_not_exists(pk)`, → `ErrPairTaken`) + `decide(...LineLinked, purchaseID)` per line. `ConfirmAll`: recompute suggestions, confirm each (skip `ErrPairTaken`/already resolved, count the rest). `CreateFromCardLine`: choose category (explicit or the card's mapping — Task 6), `addPurchase`/`addCredit` with an `extras` that appends `decide(...LineCreated, id)`; map `errExtraFailed` to `ErrLineResolved`.

- [ ] **Step 4: Run** → PASS (`-race`).

- [ ] **Step 5: Commit** `feat(imports): confirm card pairs and create purchases and credits from lines`.

---

### Task 6: Bank-category mapping

**Files:**
- Create: `api/internal/repositories/card_bank_categories.go`
- Test: `api/tests/integration/finance_card_imports_test.go`

**Interfaces:**
- Produces:
  ```go
  func CardBankCategoriesSK(cardID string) string // "CARDBANKCAT#<card>"
  func (r *ImportRepository) GetBankCategories(ctx context.Context, sp space.ResolvedSpace, cardID string) (map[string]string, error) // bank category -> category id; empty map when none
  func (r *ImportRepository) PutBankCategories(ctx context.Context, sp space.ResolvedSpace, cardID string, m map[string]string, now time.Time) error // validates ≤ 50 entries, key ≤ 60 bytes, each id an active expense category
  ```
  `CreateFromCardLine` (Task 5) falls back to this mapping when `categoryID == ""` and returns `ErrCategoryUnmapped` when neither exists.

- [ ] **Step 1: Write the failing tests:** `…PutThenGetRoundTrips`; `…RefusesAnArchivedOrIncomeCategory`; `…TooManyEntriesIsRefused`; `…CreateWithoutCategoryUsesTheMapping`; `…CreateWithoutCategoryAndWithoutMappingIsErrCategoryUnmapped`.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement** like `PutMapping` (permanent row in the imports table, `space.Import`).
- [ ] **Step 4: Run** → PASS.
- [ ] **Step 5: Commit** `feat(imports): remember the bank category mapping per card`.

---

### Task 7: API and service

**Files:**
- Create: `api/internal/api/v1/finance_card_imports.go`; modify `finance_http.go` (routes), `api/internal/problem/problem.go`, `problem_test.go`
- Test: `api/tests/integration/finance_card_imports_http_test.go`

**Interfaces:**
- Consumes: Task 3–6 repository functions.
- Produces (all under the finance console prefix, `Idempotency-Key` on every POST/PUT):
  - `POST /cards/:id/imports` `{statement_month, bank:"inter", content(base64)}` → import DTO (`space.Import`); `bank != "inter"` → 422.
  - `GET /imports/:id` for `kind=="card"` adds `statement_month`, per-line `k`, `n`, `bank_category`, `item_ref`, and `suggestions[]` (`kind`, `lines`, `item{purchase_id, tag, description, amount, date}`); account imports are byte-identical to today.
  - `POST /imports/:id/confirm` `{lines:[n…], purchase_id, tag}`; `POST /imports/:id/confirm-all` → `{confirmed}`; `POST /imports/:id/lines/:n/create-card` `{category_id?, description?}`; existing `…/ignore` and `…/reopen` work for card lines.
  - `GET|PUT /cards/:id/bank-categories` `{categories: {"SUPERMERCADO":"<id>"}}`.
  - Problem codes: `statement_not_open`, `statement_not_begun`, `pair_taken` (409), `category_unmapped` (422), plus existing `line_resolved`, `invalid_import`.

- [ ] **Step 1: Write the failing tests** (`…_http_test.go`, same helpers as `finance_cards_http_test.go`): upload → 201 with `lines`; same file again → `duplicates` and no id; closed statement → 409 `statement_not_open`; `GET` shows suggestions for a seeded manual purchase; `confirm-all` then `GET` shows no pending pairs; `create-card` on an unmapped line → 422 `category_unmapped`; credit endpoint 201; without the right verb → 403; `problem_test.go` has the four new codes.
- [ ] **Step 2: Run** → FAIL.
- [ ] **Step 3: Implement** handlers on `financeHandlers` (reuse `decodeStrict`, `fail`, the base64/size checks of `uploadImport`); route table entries with the same `space` verb masks as the account import (`Import`, plus `Write|Settle` where a purchase or credit is created); map errors in `problem.go`.
- [ ] **Step 4: Run** → PASS; `go vet ./... && staticcheck ./...`; `bash scripts/standards-check.sh` → `standards: ok`.
- [ ] **Step 5: Commit** `feat(api): card statement import endpoints`.

---

### Task 8: UI

**Files:**
- Create: `ui/src/components/finance/CardImportView.tsx`, `CardImportView.test.tsx`
- Modify: `ui/src/components/finance/CardsView.tsx` ("Importar fatura" and "Lançar crédito" actions), `ui/src/lib/api/finance.ts`, `financeTypes.ts`, `ui/src/locales/{pt-BR,en}/finance.json` and `errors.json`, `lib/errors.test.ts`

**Interfaces:**
- Consumes: Task 7 endpoints. Produces typed clients `uploadCardImport`, `getImport` (extended type), `confirmCardLines`, `confirmAllCardLines`, `createCardLine`, `getBankCategories`, `putBankCategories`, `addCardCredit`.

- [ ] **Step 1: Write the failing tests** (`CardImportView.test.tsx`, spy on `lib/api/finance` like `CardsView.test.tsx`): upload step suggests the month from `fatura-inter-2026-11.csv` and from the card's open month; a closed target shows "Reabrir fatura" (calls `reopenStatement`) and no upload button; review lists pairs, groups and "ignorar" suggestions in words (not colour alone); each pair has "Confirmar"; "Confirmar todos" calls `confirmAllCardLines` once; an unpaired line shows "Criar compra" disabled until its bank category is mapped, then enabled; a refund line says "Criar crédito"; the result shows added/paired/created/ignored/duplicates/rejected and a **warning** (not a block) when system total ≠ file total; `pair_taken` and `category_unmapped` render their translated messages; the manual credit form validates amount and category.
- [ ] **Step 2: Run** `cd ui && npx vitest run src/components/finance/CardImportView.test.tsx` → FAIL.
- [ ] **Step 3: Implement** with `@aoctech/ui` components, react-query invalidation `wholeSpace` as `CardsView` does, loading/error/empty states, pt-BR + en copy, error codes in `errors.json` and `errors.test.ts`.
- [ ] **Step 4: Run** the new tests, then `npx vitest run src/components/finance`, `npx eslint src --max-warnings 0`, `npx tsc --noEmit` (ignore the pre-existing `components/console` errors) → PASS.
- [ ] **Step 5: Commit** `feat(ui): import a card statement and enter a standalone credit`.

---

### Task 9: Docs, real-file check, PR

**Files:**
- Modify: `docs/specs/2026-11-02-card-statement-import-design.md` (status: implemented, decisions as built)
- Create: fixture `api/internal/domain/finance/statement/testdata/inter_card_sample.csv` (anonymised, ≤ 20 lines covering à vista, installment, group, refund, payment)

- [ ] **Step 1:** Add an end-to-end integration test `TestCardImportRealisticFile` that parses the fixture, imports it twice, confirms all, creates the rest with a mapping, and asserts the statement total equals the file sum minus the payment line.
- [ ] **Step 2:** Run the full gate: `go test -race ./internal/...`, integration suite against DynamoDB local, `bash scripts/standards-check.sh`, UI vitest + eslint.
- [ ] **Step 3:** Manually run Task-8 UI against the dev mock data if a mock exists (`ui/src/dev/financeMockData.ts`); extend it for the new endpoints only if the dev server needs them.
- [ ] **Step 4: Commit and open the PR** from `feat/card-statement-import` with the Definition-of-done checklist (dependencies, bounded reads, no new resource/cost, RFC 7807, idempotent POSTs, i18n, docs). No Claude attribution lines.
