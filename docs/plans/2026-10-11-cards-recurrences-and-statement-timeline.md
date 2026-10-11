# Cards: Recurrences Paid by Card and the Statement Timeline Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A payable recurrence can be charged to a card (each occurrence becomes a card purchase on its own date, landing on the right statement, virtual in the projection until then), and the Cards tab becomes a statement timeline (3 previous + open + 3 next, paged both ways, items of the selected statement below).

**Architecture:** The recurrence gains a second target (`card_id` + `installments`, exactly one of account or card). The daily job branches per recurrence: account recurrences keep making forecast bills; card recurrences post `CardRepository.PostOccurrence` (a wrapper over the existing purchase write) for every occurrence dated on or before today, guarded by the same `OCCURRENCE#{rec}#{nominal}` lock row the bill path writes and by a ConditionCheck on the recurrence, so an occurrence is posted once however often or concurrently the job runs, on whichever card. A broken target (archived card or category) sets `needs_attention` on the recurrence and stops it without moving the cursor. The projection places each not-yet-posted card occurrence's installments in the due month of the statement it would land on. A new range endpoint reads N statement summaries with one `BETWEEN` Query on the card partition; the UI pages it with a bidirectional `useInfiniteQuery`.

**Tech Stack:** Go 1.27, Fiber v3, DynamoDB (aws-sdk-go-v2, `gopkg.aoctech.app/api-commons/dynamo` Base), Next.js 16 / React 19, TanStack Query v5, `@aoctech/ui`, i18next, Vitest 4 + Testing Library.

**Spec:** `docs/specs/2026-10-11-cards-recurrences-and-statement-timeline.md` (read it with this plan; § numbers below are the spec's).

## Global Constraints

- **Dependency:** build on `origin/main` **after** the card/account rename work (branch `feat/finance-rename`: `PATCH /cards/:id` name, `PATCH /accounts/:id`, `CardForm`, `AccountsView`, `cards.go UpdateCard`, `finance.ts`, `financeTypes.ts`, mock data, locales) has merged. Before Task 1, run `git fetch origin && git log --oneline origin/main | grep -i rename`; if it has not landed, stop and tell the owner. Do not copy its changes in. Files both touch: `api/internal/repositories/cards.go`, `api/internal/api/v1/finance_cards.go`, `api/internal/api/v1/finance_http.go`, `api/internal/api/v1/finance_routes_test.go`, `ui/src/components/finance/{CardsView,AccountsView}.tsx`, `ui/src/lib/api/{finance,financeTypes}.ts`, `ui/src/dev/financeMockData.ts`, `ui/src/locales/*/finance.json`; rebase, never overwrite.
- D1: "Pay with" is **an account or a card, exactly one**; a card only for `direction == payable`.
- D2: a card occurrence is posted **by the daily job, on its own date, never earlier** (no future-dated purchase).
- D3: installments default **1**, at most `finance.MaxInstallments` (48); each installment ≥ R$ 0,01 (`finance.SplitInstallments`).
- `auto_settle` is refused on a card recurrence.
- D4: until posted, an occurrence is a **virtual payable in its statement's due month**; posted iff `nominal <= cursor` (same test as account recurrences). Never counted twice.
- D5: timeline opens on **3 previous + open + 3 next** on desktop, **one at a time with swipe** on a phone; paging loads **3 more** beyond the loaded edge; loaded pages stay cached.
- D6: `GET /cards/:id/statements?from=YYYY-MM&count=N&direction=forward|backward` returns summaries (`items_count`, no items); `count` 1..12, default 3. `GET /cards/:id/statements/:month` is unchanged.
- D7 / memory rule: routes, endpoints, query params, file names and identifiers in **English**; Portuguese only in `ui/src/locales/pt-BR/*.json`.
- UI copy: **no em dash** (`—`) in interface text (DESIGN.md "Copy and controls"); separators are `•`, `;` or parentheses. Styled `Select` with `selectCopy()`, never native. Every frontend task is built with the `impeccable` skill (`/impeccable`), follows `ui/DESIGN.md`, and gets **its own phone layout**.
- Before writing UI code read `ui/AGENTS.md`: this Next.js has breaking changes; check `ui/node_modules/next/dist/docs/` for any Next API you touch.
- Commands. Go unit: `cd api && go test ./internal/...`. Go integration (DynamoDB Local on 8124 must be up): `cd api && DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -race -count=1 ./tests/integration/`. UI: `cd ui && npx vitest run --maxWorkers=2 <files>`; judge **by exit code** (`echo $?` → `0`), not by scanning output. **Never run the Go and the UI suites at the same time.**
- Commits: conventional style (`feat(cards): …`, `test(recurrences): …`). **No attribution lines of any kind** (no `Co-Authored-By`, no "Generated with", no Claude/Anthropic mention) in commits or PR bodies.
- Spec § 6 tests are required (mapped in Self-Review at the end).

## Review Focus

1. **The `BETWEEN` upper bound must include the last month's items.** Item rows sort as `STATEMENT#2026-12#ITEM#…`, after `STATEMENT#2026-12`; an upper bound of `StatementSK(hi)` silently drops the last month's items (wrong total, `items_count` 0). Expected: the last month of a range has the same total as the single-month endpoint. Pinned in Task 7 (`TestRangeTotalsEqualTheSingleMonthEndpoint`, last month carries an item).
2. **A recurrence switched from account to card (or card to account) never posts an occurrence twice or skips one.** Expected: occurrences already made as bills stay bills; the card branch starts after the cursor; the reverse switch makes bills only after today. Pinned in Task 6 (`TestSwitchingTargetNeverPostsAnOccurrenceTwice`).
3. **A card recurrence whose Start predates the card being added to Finanças.** Without a floor, the first run would dump years of back-dated purchases onto the open statement. Expected: occurrences before the card's first month (its creation month) are skipped, not posted. Pinned in Task 1 (`TestCardDueSkipsOccurrencesBeforeTheCardExisted`) and Task 6.
4. **A recurrence with no description.** A statement item with an empty title is unreadable. Expected: the posted purchase is titled with the category's name. Pinned in Task 3 (`TestAnOccurrenceWithoutDescriptionIsTitledByItsCategory`).
5. **Previous pages arriving on a phone move the card under the finger.** Prepending a page to a scroll-snap strip shifts everything right unless `scrollLeft` is corrected. Expected: the card on screen stays on screen when older statements load. Pinned in Task 12 (`keeps the card on screen when older statements load on a phone`).

---

## File Structure

Backend (`api/`):
- `internal/domain/finance/recurrence_rule.go` (modify): `CardID`, `Installments`, `NeedsAttention` on `Recurrence`; exactly-one-target `Validate`; card branch of `NextMaterialiseDate`; `CardDue` and `CardCharge`; `Materialise` refuses a card recurrence.
- `internal/domain/finance/installments.go` (modify): `PlaceInstallments` (allocation floored at the open month), used by the purchase write, the projection and the occurrence history.
- `internal/repositories/recurrences.go` (modify): stores and edits the card target, `needs_attention`, `FlagAttention`; `MarkMaterialised` clears attention.
- `internal/repositories/recurrence_targets.go` (create): `checkCategory`, `checkAssetAccount`, `checkCardTarget` (split out of `checkDirectionAccounts`, which stays as a wrapper in `bills.go`).
- `internal/repositories/cards.go` (modify): `CardRow.FirstMonth`.
- `internal/repositories/cards_purchases.go` (modify): `addPurchase` with caller extras, `PostOccurrence`, `ErrCardArchived`, `ErrCategoryUnusable`, `RecurrenceID`/`OriginRef` on purchases and items, `buildStatement`.
- `internal/repositories/cards_range.go` (create): `StatementRange`, `lastItemMonth`, `statementMonthOf`.
- `internal/repositories/bills.go` (modify): named `occurrenceLockRow`, `cardRecurrenceUnchanged`, `ForRecurrence`/`MadeAfter` aware of purchase locks, `PaidAmong`.
- `internal/services/finance_recurrences.go` (modify): card branch in `Materialise`, card virtuals in `Project`.
- `internal/services/finance_cards.go` (modify): wider card interface, `projectCards` takes the loaded cards.
- `internal/services/finance_bills.go` (modify): `PaidAmong` passthrough.
- `internal/api/v1/finance_recurrences.go`, `finance_cards.go`, `finance_http.go` (modify): DTOs, validation, range route.

Frontend (`ui/src/`):
- `lib/api/financeTypes.ts`, `lib/api/finance.ts` (modify): types, `listCardStatements`, key.
- `dev/financeMockData.ts` (+ test) (modify): range route, card recurrences, seeded card recurrence.
- `lib/finance/statementTimeline.ts` (+ test) (create): pure paging and window logic.
- `components/finance/StatementTimeline.tsx` (create): the summary row (desktop window, phone strip).
- `components/finance/CardsView.tsx` (+ test) (modify): timeline above, items below, "recorrente" marker.
- `components/finance/RecurrencesView.tsx` (+ test) (modify): pay with a card, installments, needs-attention, card history, `?recurrence=` focus.
- `app/(finance)/finance/recurrences/page.tsx` (modify): reads `?recurrence=`.
- `components/finance/AccountsView.tsx` (+ test) (modify): archive warning with the count of recurrences using the card.
- `locales/{pt-BR,en}/{finance,bills}.json` (modify).

---
### Task 1: Domain: the card target on a recurrence

**Files:**
- Modify: `api/internal/domain/finance/recurrence_rule.go` (struct :12-26, `Validate` :40, `Materialise` :91, `NextMaterialiseDate` :140)
- Modify: `api/internal/domain/finance/installments.go` (after `AllocateInstallments` :63)
- Test: `api/internal/domain/finance/recurrence_rule_test.go`, `api/internal/domain/finance/installments_test.go`

**Interfaces:**
- Consumes: `Schedule.Occurrences(from, toExclusive)`, `AllocateInstallments`, `SplitInstallments`, `Month.MonthsSince`.
- Produces:
  - `Recurrence.CardID string`, `Recurrence.Installments int` (0 for an account recurrence, 1..48 for a card one), `Recurrence.NeedsAttention string` (read-only, set by the repository: `""`, `AttentionCardArchived`, `AttentionCategoryArchived`).
  - `const AttentionCardArchived = "card_archived"`, `const AttentionCategoryArchived = "category_archived"`.
  - `type CardCharge struct { RecurrenceID string; Nominal brcal.Date; CardID, CategoryID, Description string; Amount billing.Cents; Installments int }`
  - `func (r Recurrence) CardDue(cursor, today, floor brcal.Date) ([]CardCharge, error)`: occurrences with nominal in `(cursor, today]`, none before `floor`.
  - `func (r Recurrence) IsCard() bool`
  - `func PlaceInstallments(total billing.Cents, n int, purchase brcal.Date, closingDay int, open Month) ([]Installment, error)`

- [ ] **Step 1: Write the failing tests**

Append to `api/internal/domain/finance/recurrence_rule_test.go`:

```go
func streaming() Recurrence {
	return Recurrence{
		ID: "r9", Direction: Payable, Amount: 3990, CategoryID: "subs", CardID: "visa", Installments: 1, Description: "Streaming",
		Schedule: Schedule{Expression: DayOfMonth{Day: 28}, Start: d(2026, time.January, 1), Adjust: AdjustNone},
	}
}

func TestACardRecurrenceHasExactlyOneTargetAndIsPayableOnly(t *testing.T) {
	if err := streaming().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Recurrence){
		"both targets":        func(r *Recurrence) { r.AccountID = "bank" },
		"no target":           func(r *Recurrence) { r.CardID = "" },
		"receivable on card":  func(r *Recurrence) { r.Direction = Receivable },
		"auto settle on card": func(r *Recurrence) { r.AutoSettle = true },
		"zero installments":   func(r *Recurrence) { r.Installments = 0 },
		"49 installments":     func(r *Recurrence) { r.Installments = 49 },
		"installment < 0,01":  func(r *Recurrence) { r.Amount = 2; r.Installments = 3 },
	} {
		r := streaming()
		mutate(&r)
		if err := r.Validate(); !errors.Is(err, ErrInvalidRecurrence) {
			t.Errorf("%s: err = %v, want ErrInvalidRecurrence", name, err)
		}
	}
	acct := rent()
	acct.Installments = 2
	if err := acct.Validate(); !errors.Is(err, ErrInvalidRecurrence) {
		t.Errorf("installments on an account recurrence: %v", err)
	}
}

func TestACardRecurrenceIsDueOnItsOccurrenceDayNotAMonthAhead(t *testing.T) {
	r := streaming()
	next, ok := r.NextMaterialiseDate(d(2026, time.February, 28))
	if !ok || next != d(2026, time.March, 28) {
		t.Fatalf("next = %s %v, want the occurrence day itself (2026-03-28)", next, ok)
	}
	if _, err := r.Materialise(brcal.Date{}, d(2026, time.March, 20)); !errors.Is(err, ErrInvalidRecurrence) {
		t.Fatalf("Materialise on a card recurrence = %v, want a refusal: it posts purchases, not bills", err)
	}
}

func TestCardDuePostsOnlyWhatHasHappened(t *testing.T) {
	r := streaming()
	got, err := r.CardDue(d(2026, time.January, 28), d(2026, time.March, 28), d(2026, time.January, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Nominal != d(2026, time.February, 28) || got[1].Nominal != d(2026, time.March, 28) {
		t.Fatalf("charges = %+v, want Feb 28 and Mar 28 (today included, nothing after)", got)
	}
	c := got[0]
	if c.RecurrenceID != "r9" || c.CardID != "visa" || c.CategoryID != "subs" || c.Amount != 3990 || c.Installments != 1 || c.Description != "Streaming" {
		t.Fatalf("charge = %+v", c)
	}
	if none, _ := r.CardDue(d(2026, time.March, 28), d(2026, time.April, 27), d(2026, time.January, 1)); len(none) != 0 {
		t.Fatalf("before the 28th nothing is due, got %+v", none)
	}
}

// Review Focus 3.
func TestCardDueSkipsOccurrencesBeforeTheCardExisted(t *testing.T) {
	r := streaming() // since January
	got, err := r.CardDue(brcal.Date{}, d(2026, time.March, 28), d(2026, time.March, 1)) // card added in March
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Nominal != d(2026, time.March, 28) {
		t.Fatalf("charges = %+v, want only March: January and February were never on this card here", got)
	}
}

func TestCardDueOfAnArchivedRecurrenceIsNothing(t *testing.T) {
	r := streaming()
	r.Archived = true
	if got, err := r.CardDue(brcal.Date{}, d(2026, time.March, 28), d(2026, time.January, 1)); err != nil || len(got) != 0 {
		t.Fatalf("got %+v %v", got, err)
	}
}
```

Append to `api/internal/domain/finance/installments_test.go`:

```go
func TestPlaceInstallmentsFloorsAtTheOpenMonthAndKeepsTheSpacing(t *testing.T) {
	open := Month{Year: 2026, Month: time.April}
	plan, err := PlaceInstallments(30000, 3, brcal.New(2026, time.February, 20), 25, open) // would be Feb, Mar, Apr
	if err != nil {
		t.Fatal(err)
	}
	want := []Month{open, open.Add(1), open.Add(2)}
	for i, inst := range plan {
		if inst.Statement != want[i] {
			t.Fatalf("installment %d on %s, want %s", inst.Number, inst.Statement, want[i])
		}
	}
	late, _ := PlaceInstallments(1000, 1, brcal.New(2026, time.April, 28), 25, open)
	if late[0].Statement != open.Add(1) {
		t.Fatalf("after the closing day: %s, want May", late[0].Statement)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `cd api && go test ./internal/domain/finance/ -run 'CardRecurrence|CardDue|PlaceInstallments' -count=1`
Expected: FAIL to compile (`unknown field CardID in struct literal`, `undefined: PlaceInstallments`).

- [ ] **Step 3: Implement**

In `recurrence_rule.go`, extend the struct and add the constants and type:

```go
type Recurrence struct {
	ID          string
	Direction   Direction
	Amount      billing.Cents
	CategoryID  string
	// AccountID or CardID, exactly one (spec D1). A card recurrence posts each
	// occurrence as a purchase on that card, on its own date (D2).
	AccountID   string
	CardID      string
	// Installments splits each card occurrence (D3); 0 on an account recurrence.
	Installments int
	Description string
	Schedule    Schedule
	AutoSettle  bool
	Archived    bool
	// NeedsAttention is why the job stopped on this recurrence (card_archived,
	// category_archived), or empty. Written by the job, cleared by an edit or a
	// successful run; never part of a request.
	NeedsAttention string
}

const (
	AttentionCardArchived     = "card_archived"
	AttentionCategoryArchived = "category_archived"
)

// IsCard reports whether the recurrence is charged to a card.
func (r Recurrence) IsCard() bool { return r.CardID != "" }

// CardCharge is one card occurrence ready to post as a purchase.
type CardCharge struct {
	RecurrenceID string
	Nominal      brcal.Date
	CardID       string
	CategoryID   string
	Description  string
	Amount       billing.Cents
	Installments int
}
```

Replace `Validate`'s switch:

```go
	switch {
	case r.Direction != Payable && r.Direction != Receivable:
		return fail("direction must be payable or receivable")
	case r.Amount <= 0 || r.Amount > MaxLegAmount:
		return fail("amount must be between 1 and %d centavos", MaxLegAmount)
	case r.CategoryID == "":
		return fail("category is required")
	case r.AccountID == "" && r.CardID == "":
		return fail("an account or a card is required")
	case r.AccountID != "" && r.CardID != "":
		return fail("pay with an account or a card, not both")
	case len(r.Description) > maxBillDescription:
		return fail("description is at most %d characters", maxBillDescription)
	}
	if r.IsCard() {
		switch {
		case r.Direction != Payable:
			return fail("a card pays expenses only")
		case r.AutoSettle:
			return fail("auto_settle does not apply to a card: the statement is what gets paid")
		}
		if _, err := SplitInstallments(r.Amount, r.Installments); err != nil {
			return fail("%v", err)
		}
	} else if r.Installments != 0 {
		return fail("installments are for a card recurrence only")
	}
	return r.Schedule.Validate()
```

At the top of `Materialise`, before the cursor checks:

```go
	if r.IsCard() {
		return nil, fmt.Errorf("%w: a card recurrence posts purchases (CardDue), not bills", ErrInvalidRecurrence)
	}
```

In `NextMaterialiseDate`, replace the final `return`:

```go
	if r.IsCard() {
		// A card occurrence is posted on its own day, never ahead (D2).
		return occ[0].Nominal, true
	}
	return MonthOf(occ[0].Nominal).Add(-1).First(), true
```

Add `CardDue` after `Materialise`:

```go
// CardDue returns the card charges owed for occurrences whose nominal day is in
// (cursor, today] and on or after floor (the card's first month: what came
// before was never on this card in Finanças). A zero cursor catches up from
// Start under the same bound as Materialise.
func (r Recurrence) CardDue(cursor, today, floor brcal.Date) ([]CardCharge, error) {
	if !r.IsCard() {
		return nil, fmt.Errorf("%w: not a card recurrence", ErrInvalidRecurrence)
	}
	if cursor.IsZero() {
		if err := r.ValidateAt(today.AddDays(-catchUpSlackDays)); err != nil {
			return nil, err
		}
	} else if err := r.Validate(); err != nil {
		return nil, err
	}
	if r.Archived {
		return nil, nil
	}
	from := r.Schedule.Start
	if !cursor.IsZero() {
		from = cursor.AddDays(1)
	}
	if from.Before(floor) {
		from = floor
	}
	var out []CardCharge
	for lo := from; !lo.After(today); {
		hi := lo.AddDays(MaxSpanDays)
		if hi.After(today.AddDays(1)) {
			hi = today.AddDays(1)
		}
		occ, err := r.Schedule.Occurrences(lo, hi)
		if err != nil {
			return nil, err
		}
		for _, o := range occ {
			out = append(out, CardCharge{RecurrenceID: r.ID, Nominal: o.Nominal, CardID: r.CardID, CategoryID: r.CategoryID,
				Description: r.Description, Amount: r.Amount, Installments: r.Installments})
		}
		lo = hi
	}
	return out, nil
}
```

The purchase is dated on the **nominal** day: a card is charged on any calendar day, so the business-day adjustment (a bill's due date) does not apply.

In `installments.go`, after `AllocateInstallments`:

```go
// PlaceInstallments is AllocateInstallments floored at the card's open month:
// a purchase dated in a month already closed lands on the open statement and
// its later installments follow it, one month apart (spec § 3.6). The purchase
// write, the projection and the occurrence history all place with it.
func PlaceInstallments(total billing.Cents, n int, purchase brcal.Date, closingDay int, open Month) ([]Installment, error) {
	plan, err := AllocateInstallments(total, n, purchase, closingDay)
	if err != nil {
		return nil, err
	}
	if plan[0].Statement.Compare(open) < 0 {
		shift := open.MonthsSince(plan[0].Statement)
		for i := range plan {
			plan[i].Statement = plan[i].Statement.Add(shift)
		}
	}
	return plan, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `cd api && go test ./internal/domain/finance/ -count=1`
Expected: `ok` (the whole package, so `rent()`-based tests still pass).

- [ ] **Step 5: Commit**

```bash
git add api/internal/domain/finance/
git commit -m "feat(recurrences): a recurrence can target a card (exactly one target, payable only, installments)"
```

---

### Task 2: Repository: store, validate and edit the card target; needs_attention

**Files:**
- Create: `api/internal/repositories/recurrence_targets.go`
- Modify: `api/internal/repositories/bills.go:131-163` (`checkDirectionAccounts` becomes a wrapper)
- Modify: `api/internal/repositories/recurrences.go` (item :67-112, `NewRecurrenceRepository` :63, `newRecurrenceItem` :131, `Create` :154, `RecurrencePatch` :293, `Update` :312, `MarkMaterialised` :505)
- Test: `api/tests/integration/finance_card_recurrences_test.go` (create)

**Interfaces:**
- Consumes: Task 1 (`CardID`, `Installments`, `NeedsAttention`, `Validate`, `IsCard`).
- Produces:
  - `RecurrencePatch.CardID *string`, `RecurrencePatch.Installments *int`. Setting a non-empty `CardID` switches to the card (clears `AccountID` and `AutoSettle`, installments default 1); a non-empty `AccountID` switches to the account (clears `CardID`, installments 0). Both non-empty in one patch is `ErrInvalidRecurrence`.
  - `func (r *RecurrenceRepository) FlagAttention(ctx context.Context, sp space.ResolvedSpace, recID, reason string, now time.Time) error`
  - `MarkMaterialised` and `Update` remove `needs_attention`.
  - `func checkCategory(ctx, ledger, sp, dir finance.Direction, categoryID string, invalid error) error`, `func checkAssetAccount(ctx, ledger, sp, accountID string, invalid error) error`, `func checkCardTarget(ctx context.Context, ledger *LedgerRepository, cards Base, sp space.ResolvedSpace, cardID string, invalid error) error`, `func (r *RecurrenceRepository) checkTarget(ctx context.Context, sp space.ResolvedSpace, rec finance.Recurrence) error`.

- [ ] **Step 1: Write the failing integration test**

Create `api/tests/integration/finance_card_recurrences_test.go`:

```go
//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/billing"
	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/repositories"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// cardRecFixture: a space with bank (asset), subs and food (expense) and a card
// "Visa" closing on the 25th, due on the 5th, added on 2026-03-01 (first and
// open month: March 2026).
type cardRecFixture struct {
	sp     space.ResolvedSpace
	ledger *repositories.LedgerRepository
	bills  *repositories.BillRepository
	cards  *repositories.CardRepository
	recs   *repositories.RecurrenceRepository
	card   repositories.CardRow
}

var cardRecNow = time.Date(2026, time.March, 1, 12, 0, 0, 0, time.UTC)

func newCardRecFixture(t *testing.T) cardRecFixture {
	t.Helper()
	f := cardRecFixture{sp: jobSpace(t, newSpaceOrgID(), true), ledger: repositories.NewLedgerRepository(testDB, testCfg),
		bills: repositories.NewBillRepository(testDB, testCfg), cards: repositories.NewCardRepository(testDB, testCfg),
		recs: repositories.NewRecurrenceRepository(testDB, testCfg)}
	ctx := context.Background()
	if err := f.ledger.EnsureSpace(ctx, f.sp, cardRecNow); err != nil {
		t.Fatal(err)
	}
	for _, a := range []finance.LedgerAccount{
		{ID: "bank", Name: "Banco", Class: finance.ClassAsset},
		{ID: "subs", Name: "Assinaturas", Class: finance.ClassExpense, Group: finance.GroupOperatingExpenses},
		{ID: "food", Name: "Mercado", Class: finance.ClassExpense, Group: finance.GroupOperatingExpenses},
	} {
		if err := f.ledger.CreateAccount(ctx, f.sp, a, cardRecNow); err != nil {
			t.Fatal(err)
		}
	}
	card, err := f.cards.CreateCard(ctx, f.sp, "Visa", finance.Card{ClosingDay: 25, DueDay: 5, PayingAccountID: "bank"}, cardRecNow)
	if err != nil {
		t.Fatal(err)
	}
	f.card = card
	return f
}

// cardRecurrence: monthly on `day`, since 2026-03-01, `n` installments.
func (f cardRecFixture) cardRecurrence(t *testing.T, day int, amount billing.Cents, n int) finance.Recurrence {
	t.Helper()
	rec, err := f.recs.Create(context.Background(), f.sp, finance.Recurrence{
		Direction: finance.Payable, Amount: amount, CategoryID: "subs", CardID: f.card.ID, Installments: n, Description: "Streaming",
		Schedule: finance.Schedule{Expression: finance.DayOfMonth{Day: day}, Start: brcal.New(2026, time.March, 1), Adjust: finance.AdjustNone},
	}, repositories.PostMeta{}, cardRecNow)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestACardRecurrenceIsStoredAndReadBack(t *testing.T) {
	f := newCardRecFixture(t)
	rec := f.cardRecurrence(t, 28, 3990, 2)
	got, cursor, err := f.recs.GetWithCursor(context.Background(), f.sp, rec.ID)
	if err != nil || got.CardID != f.card.ID || got.AccountID != "" || got.Installments != 2 || !cursor.IsZero() {
		t.Fatalf("read back %+v cursor %s, %v", got, cursor, err)
	}
}

func TestACardRecurrenceRefusesAnArchivedCardAndAnUnusableCategory(t *testing.T) {
	f := newCardRecFixture(t)
	ctx := context.Background()
	base := finance.Recurrence{Direction: finance.Payable, Amount: 3990, CategoryID: "subs", CardID: f.card.ID, Installments: 1,
		Schedule: finance.Schedule{Expression: finance.DayOfMonth{Day: 28}, Start: brcal.New(2026, time.March, 1), Adjust: finance.AdjustNone}}

	other := base
	other.CardID = "bank" // an asset account is not a card
	if _, err := f.recs.Create(ctx, f.sp, other, repositories.PostMeta{}, cardRecNow); !errors.Is(err, repositories.ErrUnknownAccount) {
		t.Fatalf("an account id as the card: %v", err)
	}
	wrongCat := base
	wrongCat.CategoryID = "bank"
	if _, err := f.recs.Create(ctx, f.sp, wrongCat, repositories.PostMeta{}, cardRecNow); !errors.Is(err, finance.ErrInvalidRecurrence) {
		t.Fatalf("an asset as the category: %v", err)
	}
	foreign := jobSpace(t, newSpaceOrgID(), true)
	if err := f.ledger.EnsureSpace(ctx, foreign, cardRecNow); err != nil {
		t.Fatal(err)
	}
	if _, err := f.recs.Create(ctx, foreign, base, repositories.PostMeta{}, cardRecNow); !errors.Is(err, repositories.ErrUnknownAccount) {
		t.Fatalf("another space's card: %v", err)
	}
	if err := f.ledger.ArchiveAccount(ctx, f.sp, f.card.ID, cardRecNow); err != nil {
		t.Fatal(err)
	}
	if _, err := f.recs.Create(ctx, f.sp, base, repositories.PostMeta{}, cardRecNow); !errors.Is(err, finance.ErrInvalidRecurrence) {
		t.Fatalf("an archived card: %v", err)
	}
}

func TestSwitchingARecurrenceBetweenAccountAndCardIsAPatch(t *testing.T) {
	f := newCardRecFixture(t)
	ctx := context.Background()
	rec, err := f.recs.Create(ctx, f.sp, finance.Recurrence{
		Direction: finance.Payable, Amount: 3990, CategoryID: "subs", AccountID: "bank", AutoSettle: true,
		Schedule: finance.Schedule{Expression: finance.DayOfMonth{Day: 28}, Start: brcal.New(2026, time.March, 1), Adjust: finance.AdjustNone},
	}, repositories.PostMeta{}, cardRecNow)
	if err != nil {
		t.Fatal(err)
	}
	card := f.card.ID
	if err := f.recs.Update(ctx, f.sp, rec.ID, repositories.RecurrencePatch{CardID: &card}, cardRecNow); err != nil {
		t.Fatal(err)
	}
	got, _ := f.recs.Get(ctx, f.sp, rec.ID)
	if got.CardID != card || got.AccountID != "" || got.AutoSettle || got.Installments != 1 {
		t.Fatalf("after switching to the card: %+v (auto_settle cleared, installments 1)", got)
	}
	bank, three := "bank", 3
	if err := f.recs.Update(ctx, f.sp, rec.ID, repositories.RecurrencePatch{CardID: &card, AccountID: &bank}, cardRecNow); !errors.Is(err, finance.ErrInvalidRecurrence) {
		t.Fatalf("both targets in one patch: %v", err)
	}
	if err := f.recs.Update(ctx, f.sp, rec.ID, repositories.RecurrencePatch{Installments: &three}, cardRecNow); err != nil {
		t.Fatal(err)
	}
	if err := f.recs.Update(ctx, f.sp, rec.ID, repositories.RecurrencePatch{AccountID: &bank}, cardRecNow); err != nil {
		t.Fatal(err)
	}
	got, _ = f.recs.Get(ctx, f.sp, rec.ID)
	if got.AccountID != "bank" || got.CardID != "" || got.Installments != 0 {
		t.Fatalf("after switching back: %+v", got)
	}
	yes := true
	if err := f.recs.Update(ctx, f.sp, rec.ID, repositories.RecurrencePatch{CardID: &card, AutoSettle: &yes}, cardRecNow); !errors.Is(err, finance.ErrInvalidRecurrence) {
		t.Fatalf("auto_settle sent with a card: %v", err)
	}
}

func TestNeedsAttentionIsSetByTheJobAndClearedByAnEditOrARun(t *testing.T) {
	f := newCardRecFixture(t)
	ctx := context.Background()
	rec := f.cardRecurrence(t, 28, 3990, 1)
	if err := f.recs.FlagAttention(ctx, f.sp, rec.ID, finance.AttentionCardArchived, cardRecNow); err != nil {
		t.Fatal(err)
	}
	got, _ := f.recs.Get(ctx, f.sp, rec.ID)
	if got.NeedsAttention != finance.AttentionCardArchived {
		t.Fatalf("needs_attention = %q", got.NeedsAttention)
	}
	desc := "Streaming HD"
	if err := f.recs.Update(ctx, f.sp, rec.ID, repositories.RecurrencePatch{Description: &desc}, cardRecNow); err != nil {
		t.Fatal(err)
	}
	if got, _ = f.recs.Get(ctx, f.sp, rec.ID); got.NeedsAttention != "" {
		t.Fatalf("an edit must clear it, got %q", got.NeedsAttention)
	}
	_ = f.recs.FlagAttention(ctx, f.sp, rec.ID, finance.AttentionCategoryArchived, cardRecNow)
	if err := f.recs.MarkMaterialised(ctx, f.sp, rec.ID, brcal.New(2026, time.March, 28), cardRecNow); err != nil {
		t.Fatal(err)
	}
	if got, _ = f.recs.Get(ctx, f.sp, rec.ID); got.NeedsAttention != "" {
		t.Fatalf("a run that moved the cursor must clear it, got %q", got.NeedsAttention)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd api && DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -race -count=1 -run 'CardRecurrence|SwitchingARecurrence|NeedsAttention' ./tests/integration/`
Expected: FAIL to compile (`unknown field CardID in struct literal of type repositories.RecurrencePatch`, `f.recs.FlagAttention undefined`).

- [ ] **Step 3: Implement**

Create `api/internal/repositories/recurrence_targets.go`:

```go
package repositories

import (
	"context"
	"errors"
	"fmt"

	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// checkCategory refuses a category the reports could not read: a payable's must
// be an active, non-system expense; a receivable's an income.
func checkCategory(ctx context.Context, ledger *LedgerRepository, sp space.ResolvedSpace, dir finance.Direction, categoryID string, invalid error) error {
	cat, err := ledger.getAccount(ctx, sp, categoryID)
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%w: category %s", ErrUnknownAccount, categoryID)
	}
	if err != nil {
		return err
	}
	want := finance.ClassExpense
	if dir == finance.Receivable {
		want = finance.ClassIncome
	}
	if cat.Class != want || cat.System || cat.Archived {
		return fmt.Errorf("%w: a %s needs an active %s category", invalid, dir, want)
	}
	return nil
}

// checkAssetAccount refuses anything but an active asset account to pay from or into.
func checkAssetAccount(ctx context.Context, ledger *LedgerRepository, sp space.ResolvedSpace, accountID string, invalid error) error {
	acct, err := ledger.getAccount(ctx, sp, accountID)
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%w: account %s", ErrUnknownAccount, accountID)
	}
	if err != nil {
		return err
	}
	if acct.Class != finance.ClassAsset || acct.System || acct.Archived {
		return fmt.Errorf("%w: the account must be an active asset account", invalid)
	}
	return nil
}

// checkCardTarget refuses a card that is not this space's or is archived. A
// card is a CARD# row in the cards table plus a liability account with the same
// id; the account carries the archived flag.
func checkCardTarget(ctx context.Context, ledger *LedgerRepository, cards Base, sp space.ResolvedSpace, cardID string, invalid error) error {
	raw, err := cards.GetItem(ctx, sp.PK(), CardSK(cardID))
	if err != nil {
		return err
	}
	if raw == nil {
		return fmt.Errorf("%w: card %s", ErrUnknownAccount, cardID)
	}
	acct, err := ledger.getAccount(ctx, sp, cardID)
	if errors.Is(err, ErrNotFound) {
		return fmt.Errorf("%w: card %s", ErrUnknownAccount, cardID)
	}
	if err != nil {
		return err
	}
	if acct.Archived {
		return fmt.Errorf("%w: the card is archived; choose another card or an account", invalid)
	}
	return nil
}

// checkTarget is the repository half of a recurrence's validation:
// its category, and the account or the card it is paid with (spec § 3.1).
func (r *RecurrenceRepository) checkTarget(ctx context.Context, sp space.ResolvedSpace, rec finance.Recurrence) error {
	if err := checkCategory(ctx, r.ledger, sp, rec.Direction, rec.CategoryID, finance.ErrInvalidRecurrence); err != nil {
		return err
	}
	if rec.IsCard() {
		return checkCardTarget(ctx, r.ledger, r.cards, sp, rec.CardID, finance.ErrInvalidRecurrence)
	}
	return checkAssetAccount(ctx, r.ledger, sp, rec.AccountID, finance.ErrInvalidRecurrence)
}
```

In `bills.go`, replace the body of `checkDirectionAccounts` (keep its doc comment and signature, its callers in bills stay unchanged):

```go
func checkDirectionAccounts(ctx context.Context, ledger *LedgerRepository, sp space.ResolvedSpace, dir finance.Direction, categoryID, accountID string, invalid error) error {
	if err := checkCategory(ctx, ledger, sp, dir, categoryID, invalid); err != nil {
		return err
	}
	return checkAssetAccount(ctx, ledger, sp, accountID, invalid)
}
```

In `recurrences.go`:

```go
type RecurrenceRepository struct {
	recs   Base
	ledger *LedgerRepository
	// cards is read only to check a card target exists in the space.
	cards Base
}

func NewRecurrenceRepository(db *dynamodb.Client, cfg *config.Config) *RecurrenceRepository {
	return &RecurrenceRepository{recs: NewBase(db, cfg, TableRecurrences), ledger: NewLedgerRepository(db, cfg), cards: NewBase(db, cfg, TableCards)}
}
```

`recurrenceItem` gains (beside `AccountID`, which becomes `omitempty`):

```go
	AccountID   string `dynamodbav:"account_id,omitempty"`
	CardID      string `dynamodbav:"card_id,omitempty"`
	// Installments is written on every card recurrence (>= 1), never omitted there,
	// so the job's ConditionCheck can compare it.
	Installments   int    `dynamodbav:"installments,omitempty"`
	NeedsAttention string `dynamodbav:"needs_attention,omitempty"`
```

In `recurrence()` add `CardID: i.CardID, Installments: i.Installments, NeedsAttention: i.NeedsAttention` to the literal; in `newRecurrenceItem` add `CardID: rec.CardID, Installments: rec.Installments`.

In `Create`, replace the `checkDirectionAccounts` call with `if err := r.checkTarget(ctx, sp, rec); err != nil { return finance.Recurrence{}, err }` and clear `rec.NeedsAttention = ""` next to `rec.Archived = false`.

`RecurrencePatch` gains:

```go
	// CardID / AccountID switch the target (spec § 3.1): a non-empty CardID moves
	// the recurrence to that card (account and auto_settle cleared, installments 1
	// unless sent); a non-empty AccountID moves it back. The switch applies to
	// occurrences after the cursor; what was made or posted stays.
	CardID       *string
	Installments *int
```

In `Update`, replace the `p.AccountID` block and the `p.AutoSettle` block with (order matters: the explicit `auto_settle` is applied after the switch, so `auto_settle: true` with a card is refused by `Validate`):

```go
	if p.CardID != nil && p.AccountID != nil && *p.CardID != "" && *p.AccountID != "" {
		return fmt.Errorf("%w: pay with an account or a card, not both", finance.ErrInvalidRecurrence)
	}
	if p.CardID != nil && *p.CardID != "" {
		rec.CardID, rec.AccountID, rec.AutoSettle = *p.CardID, "", false
		if rec.Installments == 0 {
			rec.Installments = 1
		}
	}
	if p.AccountID != nil && *p.AccountID != "" {
		rec.AccountID, rec.CardID, rec.Installments = *p.AccountID, "", 0
	}
	if p.Installments != nil {
		rec.Installments = *p.Installments
	}
	if p.Description != nil {
		rec.Description = *p.Description
	}
	if p.AutoSettle != nil {
		rec.AutoSettle = *p.AutoSettle
	}
```

(the `Amount` and `CategoryID` blocks above it stay). Replace the `checkDirectionAccounts` call with `r.checkTarget(ctx, sp, rec)`. In the SET/REMOVE building, replace `set("account_id", …)` with:

```go
	// The target: one of the two is set, the other removed, never stored as "".
	if rec.IsCard() {
		set("card_id", str(rec.CardID))
		set("installments", numberValue(int64(rec.Installments)))
		names["#account_id"] = "account_id"
		remove = append(remove, "#account_id")
	} else {
		set("account_id", str(rec.AccountID))
		names["#card_id"], names["#installments"] = "card_id", "installments"
		remove = append(remove, "#card_id", "#installments")
	}
	// An edit is the person's answer to needs_attention: the next run re-checks.
	names["#needs_attention"] = "needs_attention"
	remove = append(remove, "#needs_attention")
```

Move `var remove []string` above this block (it is declared just below today). The `endExtended`/auto-settle permission check already compares `AccountID`; leave it.

Add `FlagAttention` after `Archive`:

```go
// FlagAttention records why the job stopped on a recurrence (card_archived,
// category_archived). The cursor and the schedule keys are untouched, so the job
// finds it again every day and posts the pending occurrences once it is fixed.
func (r *RecurrenceRepository) FlagAttention(ctx context.Context, sp space.ResolvedSpace, recID, reason string, now time.Time) error {
	if err := sp.Require(space.Write); err != nil {
		return err
	}
	sk := RecurrenceSK(recID)
	err := r.recs.TransactWrite(ctx, txItems(r.recs.BuildRawUpdateTxItem(sp.PK(), &sk,
		"SET needs_attention = :r, updated_at = :now", "attribute_exists(pk)", nil,
		map[string]types.AttributeValue{":r": str(reason), ":now": str(now.UTC().Format(time.RFC3339Nano))})))
	if err != nil && onlyConditionFailed(err) {
		return fmt.Errorf("%w: recurrence %s", ErrNotFound, recID)
	}
	return err
}
```

In `MarkMaterialised`, make the update remove the flag: change `remove := ""` to `remove := " REMOVE needs_attention"` and the else branch to `remove = " REMOVE needs_attention, schedule_pk, schedule_sk"`.

- [ ] **Step 4: Run to verify it passes**

Run: `cd api && go build ./... && DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -race -count=1 -run 'CardRecurrence|SwitchingARecurrence|NeedsAttention|Recurrence' ./tests/integration/`
Expected: `ok` (the existing recurrence tests included).

- [ ] **Step 5: Commit**

```bash
git add api/internal/repositories/ api/tests/integration/finance_card_recurrences_test.go
git commit -m "feat(recurrences): store and edit a card target, and needs_attention"
```

---
### Task 3: Repository: post an occurrence as a purchase, once

**Files:**
- Modify: `api/internal/repositories/cards.go` (`CardRow` :36, `cardItem` methods :62, `loadCard` :115, `ListCards` :156)
- Modify: `api/internal/repositories/cards_purchases.go` (`Purchase` :28, `StatementItem` :40, `purchaseItem` :68, `itemRow` :92, `activeCard` :149, `AddPurchase` :167-252, `statement` :328-384)
- Modify: `api/internal/repositories/bills.go` (lock row in `CreateFromOccurrence` :289-314, `OccurrenceBill` :807, `ForRecurrence` :819, `MadeAfter` :860)
- Create: `api/internal/repositories/recurrence_cards.go`
- Test: `api/tests/integration/finance_card_recurrences_test.go`

**Interfaces:**
- Consumes: Task 1 (`CardCharge`, `PlaceInstallments`, `OccurrenceRef`), Task 2 (stored `card_id`, `installments`).
- Produces:
  - `CardRow.FirstMonth finance.Month`: the month the card was created in (its first statement).
  - `var ErrCardArchived = errors.New("cards: the card is archived")`, `var ErrCategoryUnusable = errors.New("cards: the category cannot take a purchase")`; `AddPurchase` wraps them together with its current sentinels (HTTP codes unchanged).
  - `Purchase.RecurrenceID, Purchase.OriginRef string`; `StatementItem.RecurrenceID string`.
  - `func (r *CardRepository) PostOccurrence(ctx context.Context, sp space.ResolvedSpace, ch finance.CardCharge, meta PostMeta, now time.Time) (created bool, p Purchase, err error)`; `ErrRecurrenceChanged` when the recurrence changed since the job read it; `ErrCardArchived` / `ErrCategoryUnusable` / `ErrCardMoved` passed through.
  - `type OccurrencePurchase struct { ID, CardID string; Statement finance.Month; Amount billing.Cents }`; `OccurrenceBill.Purchase *OccurrencePurchase` (nil for a bill).
  - `func buildStatement(card CardRow, m finance.Month, raw []map[string]types.AttributeValue) (Statement, error)` (used by Task 7).

- [ ] **Step 1: Write the failing tests**

Append to `api/tests/integration/finance_card_recurrences_test.go` (add `"sync"` to the imports):

```go
func (f cardRecFixture) charge(rec finance.Recurrence, nominal brcal.Date) finance.CardCharge {
	return finance.CardCharge{RecurrenceID: rec.ID, Nominal: nominal, CardID: rec.CardID, CategoryID: rec.CategoryID,
		Description: rec.Description, Amount: rec.Amount, Installments: rec.Installments}
}

func TestAnOccurrenceIsPostedOnceHoweverOftenAndConcurrentlyItIsAsked(t *testing.T) {
	f := newCardRecFixture(t)
	ctx := context.Background()
	rec := f.cardRecurrence(t, 28, 3990, 1)
	ch := f.charge(rec, brcal.New(2026, time.March, 28))
	meta := repositories.PostMeta{Origin: "recurrence", Actor: "scheduler"}

	var wg sync.WaitGroup
	created := make([]bool, 4)
	for i := range created {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			created[i], _, _ = f.cards.PostOccurrence(ctx, f.sp, ch, meta, time.Now())
		}(i)
	}
	wg.Wait()
	// A concurrent loser may answer a transaction conflict instead of a skip; the
	// repeat after them must then post it. Across all calls: exactly one created.
	again, _, err := f.cards.PostOccurrence(ctx, f.sp, ch, meta, time.Now())
	if err != nil {
		t.Fatalf("a later repeat: %v", err)
	}
	n := 0
	for _, c := range append(created, again) {
		if c {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d calls reported created, want exactly 1", n)
	}
	if third, _, err := f.cards.PostOccurrence(ctx, f.sp, ch, meta, time.Now()); err != nil || third {
		t.Fatalf("one more repeat = %v, %v; want (false, nil)", third, err)
	}
	if got := balance(t, f.ledger, f.sp, "subs"); got != 3990 {
		t.Fatalf("subs = %d, want the expense once", got)
	}
	apr, _ := f.cards.GetStatement(ctx, f.sp, f.card.ID, finance.Month{Year: 2026, Month: time.April})
	if len(apr.Items) != 1 || apr.Items[0].RecurrenceID != rec.ID || apr.Total != 3990 {
		t.Fatalf("April = %+v: the 28th is after the closing day (25), one item carrying its recurrence", apr)
	}
}

func TestAnOccurrenceIsNeverPostedOnTwoCardsOrAsBothABillAndAPurchase(t *testing.T) {
	f := newCardRecFixture(t)
	ctx := context.Background()
	rec := f.cardRecurrence(t, 28, 3990, 1)
	ch := f.charge(rec, brcal.New(2026, time.March, 28))
	if ok, _, err := f.cards.PostOccurrence(ctx, f.sp, ch, repositories.PostMeta{}, time.Now()); err != nil || !ok {
		t.Fatalf("first post = %v, %v", ok, err)
	}
	second, err := f.cards.CreateCard(ctx, f.sp, "Master", finance.Card{ClosingDay: 10, DueDay: 20, PayingAccountID: "bank"}, cardRecNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.recs.Update(ctx, f.sp, rec.ID, repositories.RecurrencePatch{CardID: &second.ID}, cardRecNow); err != nil {
		t.Fatal(err)
	}
	moved := ch
	moved.CardID = second.ID
	if ok, _, err := f.cards.PostOccurrence(ctx, f.sp, moved, repositories.PostMeta{}, time.Now()); err != nil || ok {
		t.Fatalf("the same occurrence on the new card = %v, %v; want (false, nil): the lock is per occurrence, not per card", ok, err)
	}
}

func TestAStaleChargeIsRefusedAsARecurrenceChange(t *testing.T) {
	f := newCardRecFixture(t)
	ctx := context.Background()
	rec := f.cardRecurrence(t, 28, 3990, 1)
	amount := billing.Cents(4990)
	if err := f.recs.Update(ctx, f.sp, rec.ID, repositories.RecurrencePatch{Amount: &amount}, cardRecNow); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.cards.PostOccurrence(ctx, f.sp, f.charge(rec, brcal.New(2026, time.March, 28)), repositories.PostMeta{}, time.Now()); !errors.Is(err, repositories.ErrRecurrenceChanged) {
		t.Fatalf("a charge read before the amount changed: %v, want ErrRecurrenceChanged", err)
	}
	if got := balance(t, f.ledger, f.sp, "subs"); got != 0 {
		t.Fatalf("subs = %d, nothing may post", got)
	}
}

func TestAnArchivedCardOrCategoryIsToldApart(t *testing.T) {
	f := newCardRecFixture(t)
	ctx := context.Background()
	rec := f.cardRecurrence(t, 28, 3990, 1)
	ch := f.charge(rec, brcal.New(2026, time.March, 28))
	if err := f.ledger.ArchiveAccount(ctx, f.sp, "subs", cardRecNow); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.cards.PostOccurrence(ctx, f.sp, ch, repositories.PostMeta{}, time.Now()); !errors.Is(err, repositories.ErrCategoryUnusable) {
		t.Fatalf("archived category: %v", err)
	}
	if err := f.ledger.ArchiveAccount(ctx, f.sp, f.card.ID, cardRecNow); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.cards.PostOccurrence(ctx, f.sp, ch, repositories.PostMeta{}, time.Now()); !errors.Is(err, repositories.ErrCardArchived) {
		t.Fatalf("archived card: %v", err)
	}
	// The HTTP purchase route keeps its codes: still ErrUnknownAccount underneath.
	if _, err := f.cards.AddPurchase(ctx, f.sp, f.card.ID, repositories.Purchase{Description: "x", CategoryID: "food", Date: brcal.New(2026, time.March, 2),
		Total: 100, Installments: make([]finance.Installment, 1)}, repositories.PostMeta{}, time.Now()); !errors.Is(err, repositories.ErrUnknownAccount) {
		t.Fatalf("AddPurchase on an archived card: %v", err)
	}
}

// Review Focus 4.
func TestAnOccurrenceWithoutDescriptionIsTitledByItsCategory(t *testing.T) {
	f := newCardRecFixture(t)
	ctx := context.Background()
	rec, err := f.recs.Create(ctx, f.sp, finance.Recurrence{Direction: finance.Payable, Amount: 1000, CategoryID: "subs", CardID: f.card.ID, Installments: 1,
		Schedule: finance.Schedule{Expression: finance.DayOfMonth{Day: 5}, Start: brcal.New(2026, time.March, 1), Adjust: finance.AdjustNone}},
		repositories.PostMeta{}, cardRecNow)
	if err != nil {
		t.Fatal(err)
	}
	_, p, err := f.cards.PostOccurrence(ctx, f.sp, f.charge(rec, brcal.New(2026, time.March, 5)), repositories.PostMeta{}, time.Now())
	if err != nil || p.Description != "Assinaturas" {
		t.Fatalf("purchase = %+v, %v; want it titled by its category", p, err)
	}
}

func TestTheHistoryListsPostedOccurrencesAndEndingSkipsThem(t *testing.T) {
	f := newCardRecFixture(t)
	ctx := context.Background()
	rec := f.cardRecurrence(t, 28, 30000, 3)
	if _, _, err := f.cards.PostOccurrence(ctx, f.sp, f.charge(rec, brcal.New(2026, time.March, 28)), repositories.PostMeta{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	hist, err := f.bills.ForRecurrence(ctx, f.sp, rec.ID, 12)
	if err != nil || len(hist) != 1 || hist[0].Purchase == nil {
		t.Fatalf("history = %+v, %v", hist, err)
	}
	if p := hist[0].Purchase; p.CardID != f.card.ID || p.Statement != (finance.Month{Year: 2026, Month: time.April}) || p.Amount != 30000 || p.ID == "" {
		t.Fatalf("posted occurrence = %+v", p)
	}
	made, err := f.bills.MadeAfter(ctx, f.sp, rec.ID, brcal.New(2026, time.March, 1))
	if err != nil || len(made) != 0 {
		t.Fatalf("MadeAfter = %+v, %v: a purchase is not a bill an end could cancel", made, err)
	}
}

func TestACardKnowsItsFirstMonth(t *testing.T) {
	f := newCardRecFixture(t)
	got, err := f.cards.GetCard(context.Background(), f.sp, f.card.ID)
	if err != nil || got.FirstMonth != (finance.Month{Year: 2026, Month: time.March}) {
		t.Fatalf("first month = %v, %v", got.FirstMonth, err)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd api && DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -race -count=1 -run 'Occurrence|ArchivedCardOrCategory|FirstMonth|History' ./tests/integration/`
Expected: FAIL to compile (`f.cards.PostOccurrence undefined`, `apr.Items[0].RecurrenceID undefined`, `got.FirstMonth undefined`).

- [ ] **Step 3: Implement**

`cards.go`: add the field and the reader, and set it in `loadCard` and `ListCards`:

```go
type CardRow struct {
	finance.Card
	Name     string
	Archived bool
	Balance  int64
	Version  int64
	// FirstMonth is the card's first statement: the month it was added in, which
	// was its open month then (CreateCard). Nothing is ever billed before it.
	FirstMonth finance.Month
}

// firstMonth reads the card's creation month from its row's created_at, in the
// civil calendar CreateCard used for its first open month.
func (i cardItem) firstMonth() (finance.Month, error) {
	at, err := time.Parse(time.RFC3339Nano, i.CreatedAt)
	if err != nil {
		return finance.Month{}, fmt.Errorf("card %s has a malformed created_at: %w", i.ID, err)
	}
	return finance.MonthOf(brcal.FromTime(at)), nil
}
```

In `loadCard` after `c, err := it.card()`: `first, err := it.firstMonth(); if err != nil { return CardRow{}, err }` and `row := CardRow{Card: c, Version: it.Version, FirstMonth: first}`. In `ListCards` the same, with `FirstMonth: first` in the appended row.

`cards_purchases.go`: the sentinels, after `ErrPurchaseRefunded`:

```go
// ErrCardArchived and ErrCategoryUnusable tell the daily job why a card
// occurrence could not post (needs_attention). They are wrapped together with
// ErrUnknownAccount / finance.ErrInvalidTransaction, so the HTTP answers of the
// purchase route do not change.
var (
	ErrCardArchived     = errors.New("cards: the card is archived")
	ErrCategoryUnusable = errors.New("cards: the category cannot take a purchase")
)
```

`activeCard`: `return CardRow{}, fmt.Errorf("%w: %w: card %s", ErrUnknownAccount, ErrCardArchived, cardID)`.

`Purchase` gains `RecurrenceID, OriginRef string`; `StatementItem` gains `RecurrenceID string`; `purchaseItem` gains

```go
	// Origin and OriginRef link a purchase the daily job posted to its
	// recurrence occurrence ("recurrence", "{recurrence}@{nominal}").
	Origin       string `dynamodbav:"origin,omitempty"`
	OriginRef    string `dynamodbav:"origin_ref,omitempty"`
	RecurrenceID string `dynamodbav:"recurrence_id,omitempty"`
```

and `itemRow` gains `RecurrenceID string `dynamodbav:"recurrence_id,omitempty"``. In `purchase()` copy `RecurrenceID: p.RecurrenceID, OriginRef: p.OriginRef`.

Replace `AddPurchase` with a wrapper over `addPurchase`:

```go
// purchaseExtras builds writes a caller adds to a purchase's transaction once
// the purchase's id and plan are known. A cancellation caused by one of them is
// errExtraFailed{index}.
type purchaseExtras func(p Purchase) ([]types.TransactWriteItem, error)

type errExtraFailed struct{ index int }

func (e errExtraFailed) Error() string { return fmt.Sprintf("cards: the caller's condition %d failed", e.index) }

func (r *CardRepository) AddPurchase(ctx context.Context, sp space.ResolvedSpace, cardID string, p Purchase, meta PostMeta, now time.Time) (Purchase, error) {
	return r.addPurchase(ctx, sp, cardID, p, meta, now, nil)
}
```

`addPurchase` is today's `AddPurchase` body with these changes:
1. the category refusals become
   `return Purchase{}, fmt.Errorf("%w: %w: category %s", ErrUnknownAccount, ErrCategoryUnusable, p.CategoryID)` (not found) and
   `return Purchase{}, fmt.Errorf("%w: %w: the purchase needs an active expense category", finance.ErrInvalidTransaction, ErrCategoryUnusable)`;
2. right after the category check: `if p.Description == "" { p.Description = cat.Name } // a recurrence may have none`;
3. the plan: `plan, err := finance.PlaceInstallments(p.Total, len(p.Installments), p.Date, card.ClosingDay, card.OpenMonth)` (the inline shift is deleted);
4. the purchase row literal adds `Origin: originOf(p), OriginRef: p.OriginRef, RecurrenceID: p.RecurrenceID`, each item row adds `RecurrenceID: p.RecurrenceID`;
5. after `items = append(items, r.cardBump(...))`:

```go
	extraIdx := len(items)
	if extras != nil {
		more, err := extras(p)
		if err != nil {
			return Purchase{}, err
		}
		items = append(items, more...)
	}
	if err := r.cards.TransactWrite(ctx, items); err != nil {
		codes := cancellationCodes(err)
		if onlyConditionFailed(err) {
			for i := extraIdx; i < len(items) && i < len(codes); i++ {
				if codes[i] == codeConditionFailed {
					return Purchase{}, errExtraFailed{index: i - extraIdx}
				}
			}
		}
		switch { /* the existing three cases, unchanged */ }
		return Purchase{}, classifyPostCancel(err, ledgerPlan.MarkerIdx)
	}
```

with the helper

```go
func originOf(p Purchase) string {
	if p.RecurrenceID != "" {
		return "recurrence"
	}
	return ""
}
```

The ledger `meta.Origin` stays `card_purchase`: the reports read every purchase alike.

Extract the body of `statement` into `buildStatement` and keep `statement` as the reader:

```go
func (r *CardRepository) statement(ctx context.Context, sp space.ResolvedSpace, card CardRow, m finance.Month) (Statement, error) {
	raw, err := r.ledger.queryPrefix(ctx, r.cards, CardPK(sp, card.ID), StatementSK(m))
	if err != nil {
		return Statement{}, err
	}
	return buildStatement(card, m, raw)
}

// buildStatement is one month of a card from its rows (the statement row, if
// closed, and the items): the frozen total when closed, the items' sum
// otherwise. Rows of other months are ignored.
func buildStatement(card CardRow, m finance.Month, raw []map[string]types.AttributeValue) (Statement, error) {
	// today's body of statement() from `s := Statement{` to `return s, nil`,
	// with `RecurrenceID: row.RecurrenceID` added to each StatementItem.
}
```

Create `api/internal/repositories/recurrence_cards.go`:

```go
package repositories

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// PostOccurrence posts one occurrence of a card recurrence as a purchase on its
// own date (spec § 3.2). The purchase id comes from the occurrence ref, and the
// transaction also writes the OCCURRENCE# lock row the bill path writes and a
// ConditionCheck on the recurrence as the job read it. So an occurrence posts
// once however often or concurrently the job runs, on whichever card, and never
// as both a bill and a purchase. created is false when it was already posted.
func (r *CardRepository) PostOccurrence(ctx context.Context, sp space.ResolvedSpace, ch finance.CardCharge, meta PostMeta, now time.Time) (bool, Purchase, error) {
	ref := finance.OccurrenceRef(ch.RecurrenceID, ch.Nominal)
	meta.IdempotencyKey = ref
	in := Purchase{Date: ch.Nominal, Description: ch.Description, CategoryID: ch.CategoryID, Total: ch.Amount,
		Installments: make([]finance.Installment, ch.Installments), RecurrenceID: ch.RecurrenceID, OriginRef: ref}
	p, err := r.addPurchase(ctx, sp, ch.CardID, in, meta, now, func(p Purchase) ([]types.TransactWriteItem, error) {
		lock, err := Encode(occurrenceLockRow{
			keys:       newKeys(sp.PK(), OccurrenceSK(ch.RecurrenceID, ch.Nominal), RetentionPermanent, now),
			PurchaseID: p.ID, CardID: p.CardID, Statement: p.Installments[0].Statement.String(), Amount: int64(p.Total),
		})
		if err != nil {
			return nil, err
		}
		return []types.TransactWriteItem{r.bills.bills.BuildPutTxItemIfAbsent(lock), r.bills.cardRecurrenceUnchanged(sp, ch)}, nil
	})
	var extra errExtraFailed
	switch {
	case errors.As(err, &extra) && extra.index == 0:
		return false, Purchase{}, nil // already posted: a re-run, a race, or before a switch of card
	case errors.As(err, &extra):
		return false, Purchase{}, ErrRecurrenceChanged
	case err != nil:
		return false, Purchase{}, err
	}
	return true, p, nil
}

// cardRecurrenceUnchanged ties a card posting to the recurrence the job read:
// still there, not archived, same card, amount, category and installments.
func (r *BillRepository) cardRecurrenceUnchanged(sp space.ResolvedSpace, ch finance.CardCharge) types.TransactWriteItem {
	return types.TransactWriteItem{ConditionCheck: &types.ConditionCheck{
		TableName: aws.String(r.recs.TableName),
		Key:       map[string]types.AttributeValue{"pk": str(sp.PK()), "sk": str(RecurrenceSK(ch.RecurrenceID))},
		ConditionExpression: aws.String("attribute_exists(pk) AND (attribute_not_exists(#arch) OR #arch = :f) AND " +
			"#card = :card AND #amt = :amt AND #cat = :cat AND #n = :n"),
		ExpressionAttributeNames: map[string]string{"#arch": "archived", "#card": "card_id", "#amt": "amount", "#cat": "category_id", "#n": "installments"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":f": &types.AttributeValueMemberBOOL{Value: false}, ":card": str(ch.CardID),
			":amt": numberValue(int64(ch.Amount)), ":cat": str(ch.CategoryID), ":n": numberValue(int64(ch.Installments)),
		},
	}}
}
```

`bills.go`: name the lock row and teach the readers about purchases.

```go
// occurrenceLockRow is OCCURRENCE#{recurrence}#{nominal}: written once per
// occurrence, with the bill it made or, for a card recurrence, the purchase.
type occurrenceLockRow struct {
	keys
	BillID     string `dynamodbav:"bill_id,omitempty"`
	PurchaseID string `dynamodbav:"purchase_id,omitempty"`
	CardID     string `dynamodbav:"card_id,omitempty"`
	Statement  string `dynamodbav:"statement,omitempty"` // the first installment's month
	Amount     int64  `dynamodbav:"amount,omitempty"`
}
```

In `CreateFromOccurrence` replace the anonymous struct with `occurrenceLockRow{keys: newKeys(...), BillID: b.ID}`.

```go
// OccurrencePurchase is a card occurrence the job posted.
type OccurrencePurchase struct {
	ID, CardID string
	Statement  finance.Month
	Amount     billing.Cents
}

type OccurrenceBill struct {
	Nominal  brcal.Date
	Bill     finance.Bill
	Purchase *OccurrencePurchase // set instead of Bill for a card occurrence
}
```

In `ForRecurrence`, decode `DecodeItems[occurrenceLockRow](res.Items)` and, inside the loop after parsing `nominal`:

```go
		if l.PurchaseID != "" {
			m, err := finance.ParseMonth(l.Statement)
			if err != nil {
				return nil, fmt.Errorf("malformed occurrence lock %q: %w", l.SK, err)
			}
			out = append(out, OccurrenceBill{Nominal: nominal, Purchase: &OccurrencePurchase{ID: l.PurchaseID, CardID: l.CardID, Statement: m, Amount: billing.Cents(l.Amount)}})
			continue
		}
```

In `MadeAfter`, decode `occurrenceLockRow` too and add `if l.BillID == "" { continue } // a card occurrence: a purchase, never cancelled by an end` before reading the bill.

- [ ] **Step 4: Run to verify it passes**

Run: `cd api && go test ./internal/... -count=1 && DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -race -count=1 -run 'Card|Occurrence|Purchase|Statement|History|Recurrence' ./tests/integration/`
Expected: both `ok` (the existing card and recurrence suites included: `TestALatePurchaseLandsOnTheOpenStatement` proves `PlaceInstallments` kept the shift).

- [ ] **Step 5: Commit**

```bash
git add api/internal/repositories/ api/tests/integration/finance_card_recurrences_test.go
git commit -m "feat(cards): post a recurrence occurrence as a purchase, once, guarded by its occurrence lock"
```

---
### Task 4: Job: post card occurrences, flag what needs attention

**Files:**
- Modify: `api/internal/services/finance_cards.go:14-19` (interface), `:66` (`WithCards`)
- Modify: `api/internal/services/finance_recurrences.go` (`recurrenceStore` :40, `Materialise` :79-136)
- Test: `api/internal/services/finance_test.go` (fakes :36-130 and :397-435)

**Interfaces:**
- Consumes: Task 1 (`CardDue`, `IsCard`, attention constants), Task 2 (`FlagAttention`), Task 3 (`PostOccurrence`, `GetCard`, `FirstMonth`, `ErrCardArchived`, `ErrCategoryUnusable`, `ErrRecurrenceChanged`, `ErrCardMoved`).
- Produces:
  - `type cardStore interface` (renamed from `cardCloser`): `DueToClose`, `CloseDue`, `ListCards`, `GetStatement` (as today) plus `GetCard(ctx, sp, cardID string) (*repositories.CardRow, error)` and `PostOccurrence(ctx, sp, ch finance.CardCharge, meta repositories.PostMeta, now time.Time) (bool, repositories.Purchase, error)`. `WithCards(c cardStore)`.
  - `recurrenceStore` gains `FlagAttention(ctx, sp, id, reason string, now time.Time) error`.
  - Card branch semantics: occurrences dated `<= today` post; cursor moves to `today` when every due charge was handled (to the last posted nominal when capped by `maxDraftsPerRun`); a broken target flags and keeps the cursor; `ErrCardMoved` is retried once in the same run, then left for the next.

- [ ] **Step 1: Write the failing tests**

In `api/internal/services/finance_test.go`, add to `fakeRecs` a `flags map[string]string` (initialised in `newFakeRecs`) and:

```go
func (f *fakeRecs) FlagAttention(_ context.Context, _ space.ResolvedSpace, id, reason string, _ time.Time) error {
	f.flags[id] = reason
	return nil
}
```

and in `MarkMaterialised`, after the cursor update, `delete(f.flags, id)`.

Give the existing `fakeCards` the two new methods so it still satisfies the interface:

```go
func (f *fakeCards) GetCard(context.Context, space.ResolvedSpace, string) (*repositories.CardRow, error) {
	return nil, repositories.ErrNotFound
}

func (f *fakeCards) PostOccurrence(context.Context, space.ResolvedSpace, finance.CardCharge, repositories.PostMeta, time.Time) (bool, repositories.Purchase, error) {
	return false, repositories.Purchase{}, errors.New("not used")
}
```

Add a dedicated fake and the tests at the end of the file:

```go
// ---- card recurrences ------------------------------------------------------------

// recCards is a card store for the card branch: one or more cards, purchases
// keyed by occurrence ref (the lock), and switchable failures.
type recCards struct {
	fakeCards
	rows    map[string]repositories.CardRow
	posted  map[string]finance.CardCharge
	postErr error // returned by every PostOccurrence while set
	moved   int   // the next `moved` calls answer ErrCardMoved
	totals  map[string]billing.Cents // "{card}|{month}" -> open/future statement total
}

func newRecCards(rows ...repositories.CardRow) *recCards {
	c := &recCards{rows: map[string]repositories.CardRow{}, posted: map[string]finance.CardCharge{}, totals: map[string]billing.Cents{}}
	for _, r := range rows {
		c.rows[r.ID] = r
	}
	return c
}

func (c *recCards) GetCard(_ context.Context, _ space.ResolvedSpace, id string) (*repositories.CardRow, error) {
	r, ok := c.rows[id]
	if !ok {
		return nil, repositories.ErrNotFound
	}
	return &r, nil
}

func (c *recCards) ListCards(context.Context, space.ResolvedSpace) ([]repositories.CardRow, error) {
	out := make([]repositories.CardRow, 0, len(c.rows))
	for _, r := range c.rows {
		out = append(out, r)
	}
	return out, nil
}

func (c *recCards) GetStatement(_ context.Context, _ space.ResolvedSpace, id string, m finance.Month) (repositories.Statement, error) {
	return repositories.Statement{Month: m, Total: c.totals[id+"|"+m.String()]}, nil
}

func (c *recCards) PostOccurrence(_ context.Context, _ space.ResolvedSpace, ch finance.CardCharge, _ repositories.PostMeta, _ time.Time) (bool, repositories.Purchase, error) {
	if c.moved > 0 {
		c.moved--
		return false, repositories.Purchase{}, repositories.ErrCardMoved
	}
	if c.postErr != nil {
		return false, repositories.Purchase{}, c.postErr
	}
	ref := finance.OccurrenceRef(ch.RecurrenceID, ch.Nominal)
	if _, ok := c.posted[ref]; ok {
		return false, repositories.Purchase{}, nil
	}
	c.posted[ref] = ch
	return true, repositories.Purchase{ID: "p-" + ref}, nil
}

// visa25 closes on the 25th and is due on the 5th; added and open in March 2026.
func visa25() repositories.CardRow {
	return repositories.CardRow{Card: finance.Card{ID: "visa", ClosingDay: 25, DueDay: 5, PayingAccountID: "bank",
		OpenMonth: finance.Month{Year: 2026, Month: time.March}}, Name: "Visa", FirstMonth: finance.Month{Year: 2026, Month: time.March}}
}

func onCard(id string, day int, n int) finance.Recurrence {
	return finance.Recurrence{ID: id, Direction: finance.Payable, Amount: 30000, CategoryID: "subs", CardID: "visa", Installments: n,
		Description: "Streaming", Schedule: finance.Schedule{Expression: finance.DayOfMonth{Day: day}, Start: day0(), Adjust: finance.AdjustNone}}
}

func day0() brcal.Date { return day(2026, time.March, 1) }

// Spec § 6.1 (job half; the real tables are Task 6).
func TestTheJobPostsEachCardOccurrenceOnceOnItsDay(t *testing.T) {
	bills, cards, recs := newFakeBills(), newRecCards(visa25()), newFakeRecs(onCard("s", 28, 1))
	jobs := NewFinanceJobs(bills, recs).WithCards(cards)

	before := jobs.Materialise(context.Background(), true, day(2026, time.March, 27), time.Now())
	if before.Examined != 0 || len(cards.posted) != 0 {
		t.Fatalf("on the 27th = %+v, posted %d: nothing is posted before its day", before, len(cards.posted))
	}
	for run := 0; run < 2; run++ {
		jobs.Materialise(context.Background(), true, day(2026, time.March, 28), time.Now())
	}
	if len(cards.posted) != 1 {
		t.Fatalf("posted %d, want exactly the 28th once", len(cards.posted))
	}
	if recs.cursors["s"] != day(2026, time.March, 28) {
		t.Fatalf("cursor = %s, want today", recs.cursors["s"])
	}
	if bills.created != 0 {
		t.Fatal("a card recurrence makes no bills")
	}
}

func TestTheJobRetriesALostCloseRaceOnce(t *testing.T) {
	cards, recs := newRecCards(visa25()), newFakeRecs(onCard("s", 28, 1))
	cards.moved = 1
	res := NewFinanceJobs(newFakeBills(), recs).WithCards(cards).Materialise(context.Background(), true, day(2026, time.March, 28), time.Now())
	if res.Failed != 0 || len(cards.posted) != 1 {
		t.Fatalf("res = %+v, posted %d: one ErrCardMoved is retried in the run", res, len(cards.posted))
	}
	cards.moved = 2
	recs2 := newFakeRecs(onCard("t", 28, 1))
	res = NewFinanceJobs(newFakeBills(), recs2).WithCards(cards).Materialise(context.Background(), true, day(2026, time.March, 28), time.Now())
	if res.Failed != 0 || !recs2.cursors["t"].IsZero() {
		t.Fatalf("res = %+v, cursor %s: a second loss waits for the next run, cursor kept", res, recs2.cursors["t"])
	}
}

// Spec § 6.4 (job half).
func TestABrokenTargetIsFlaggedNotPostedAndTheCursorStays(t *testing.T) {
	archived := visa25()
	archived.Archived = true
	cards, recs := newRecCards(archived), newFakeRecs(onCard("s", 28, 1))
	jobs := NewFinanceJobs(newFakeBills(), recs).WithCards(cards)
	today := day(2026, time.March, 28)

	res := jobs.Materialise(context.Background(), true, today, time.Now())
	if res.Failed != 0 || len(cards.posted) != 0 || recs.flags["s"] != finance.AttentionCardArchived || !recs.cursors["s"].IsZero() {
		t.Fatalf("archived card: res %+v posted %d flag %q cursor %s", res, len(cards.posted), recs.flags["s"], recs.cursors["s"])
	}

	cards.rows["visa"] = visa25() // the person picked a working card
	cards.postErr = fmt.Errorf("wrapped: %w", repositories.ErrCategoryUnusable)
	jobs.Materialise(context.Background(), true, today, time.Now())
	if recs.flags["s"] != finance.AttentionCategoryArchived || len(cards.posted) != 0 {
		t.Fatalf("archived category: flag %q posted %d", recs.flags["s"], len(cards.posted))
	}

	cards.postErr = nil
	later := day(2026, time.April, 28)
	jobs.Materialise(context.Background(), true, later, time.Now())
	if len(cards.posted) != 2 || recs.flags["s"] != "" || recs.cursors["s"] != later {
		t.Fatalf("after the fix: posted %d (want Mar 28 and Apr 28), flag %q, cursor %s", len(cards.posted), recs.flags["s"], recs.cursors["s"])
	}
}

func TestAnUnknownCardIsFlaggedAsArchived(t *testing.T) {
	recs := newFakeRecs(onCard("s", 28, 1))
	NewFinanceJobs(newFakeBills(), recs).WithCards(newRecCards()).Materialise(context.Background(), true, day(2026, time.March, 28), time.Now())
	if recs.flags["s"] != finance.AttentionCardArchived {
		t.Fatalf("flag = %q", recs.flags["s"])
	}
}

func TestAccountAndCardRecurrencesRunSideBySide(t *testing.T) {
	bills, cards := newFakeBills(), newRecCards(visa25())
	recs := newFakeRecs(monthly("rent", 10), onCard("s", 15, 1))
	jobs := NewFinanceJobs(bills, recs).WithCards(cards)
	jobs.Materialise(context.Background(), true, day(2026, time.March, 20), time.Now())
	if bills.created != 4 || len(cards.posted) != 1 {
		t.Fatalf("bills %d (want Jan..Apr), purchases %d (want Mar 15)", bills.created, len(cards.posted))
	}
}
```

(add `"fmt"` to the imports).

- [ ] **Step 2: Run to verify it fails**

Run: `cd api && go test ./internal/services/ -run 'Card|BrokenTarget|SideBySide|CloseRace' -count=1`
Expected: FAIL to compile (`cannot use cards (variable of type *recCards) as cardCloser value: missing method PostOccurrence`).

- [ ] **Step 3: Implement**

`finance_cards.go`: rename `cardCloser` to `cardStore`, add the two methods, update the `FinanceJobs.cards` field type and `WithCards(c cardStore)`:

```go
type cardStore interface {
	DueToClose(ctx context.Context, livemode bool, today brcal.Date, limit int) ([]repositories.DueCard, int, error)
	CloseDue(ctx context.Context, sp space.ResolvedSpace, cardID string, today brcal.Date, now time.Time) (int, error)
	ListCards(ctx context.Context, sp space.ResolvedSpace) ([]repositories.CardRow, error)
	GetStatement(ctx context.Context, sp space.ResolvedSpace, cardID string, m finance.Month) (repositories.Statement, error)
	GetCard(ctx context.Context, sp space.ResolvedSpace, cardID string) (*repositories.CardRow, error)
	PostOccurrence(ctx context.Context, sp space.ResolvedSpace, ch finance.CardCharge, meta repositories.PostMeta, now time.Time) (bool, repositories.Purchase, error)
}
```

`finance_recurrences.go`: add `FlagAttention` to `recurrenceStore`; in `Materialise`'s loop, right after `rec := d.Recurrence`:

```go
		if rec.IsCard() {
			j.materialiseCard(ctx, d, today, now, meta, &res)
			continue
		}
```

and add:

```go
// materialiseCard posts a card recurrence's occurrences dated up to today as
// purchases (spec § 3.2, D2). A broken target (archived card or category) is
// flagged on the recurrence and stops it with its cursor kept, so the pending
// occurrences post on the first run after the person fixes it.
func (j *FinanceJobs) materialiseCard(ctx context.Context, d repositories.DueRecurrence, today brcal.Date, now time.Time, meta repositories.PostMeta, res *JobResult) {
	rec := d.Recurrence
	if j.cards == nil {
		res.fail("recurrence %s: a card recurrence needs the card store", rec.ID)
		return
	}
	flag := func(reason string) {
		res.Skipped++
		if rec.NeedsAttention == reason {
			return
		}
		if err := j.recs.FlagAttention(ctx, d.Space, rec.ID, reason, now); err != nil {
			res.fail("recurrence %s: flagging %s: %v", rec.ID, reason, err)
		}
	}
	card, err := j.cards.GetCard(ctx, d.Space, rec.CardID)
	switch {
	case errors.Is(err, repositories.ErrNotFound):
		flag(finance.AttentionCardArchived)
		return
	case err != nil:
		res.fail("recurrence %s: reading card %s: %v", rec.ID, rec.CardID, err)
		return
	case card.Archived:
		flag(finance.AttentionCardArchived)
		return
	}
	charges, err := rec.CardDue(d.Cursor, today, card.FirstMonth.First())
	if err != nil {
		res.fail("recurrence %s: %v", rec.ID, err)
		return
	}
	capped := len(charges) > maxDraftsPerRun
	if capped {
		charges = charges[:maxDraftsPerRun]
	}
	cursor := d.Cursor
	for _, ch := range charges {
		created, _, err := j.cards.PostOccurrence(ctx, d.Space, ch, meta, now)
		if errors.Is(err, repositories.ErrCardMoved) {
			// A close won the race: once more lands on the new open statement.
			created, _, err = j.cards.PostOccurrence(ctx, d.Space, ch, meta, now)
		}
		switch {
		case err == nil:
		case errors.Is(err, repositories.ErrCardArchived):
			flag(finance.AttentionCardArchived)
			return
		case errors.Is(err, repositories.ErrCategoryUnusable):
			flag(finance.AttentionCategoryArchived)
			return
		case errors.Is(err, repositories.ErrRecurrenceChanged), errors.Is(err, repositories.ErrCardMoved):
			res.Skipped++ // the next run works from the current rule and card
			return
		default:
			res.fail("recurrence %s occurrence %s: %v", rec.ID, ch.Nominal, err)
			return
		}
		if created {
			res.Done++
		} else {
			res.Skipped++
		}
		cursor = ch.Nominal
	}
	if !capped && cursor.Before(today) {
		// Everything up to today is handled: the cursor is today, so "posted iff
		// nominal <= cursor" holds for the projection (spec § 3.4).
		cursor = today
	}
	if err := j.recs.MarkMaterialised(ctx, d.Space, rec.ID, cursor, now); err != nil {
		res.fail("recurrence %s: moving the cursor to %s: %v", rec.ID, cursor, err)
	}
}
```

`projectCards` keeps compiling (it only uses `ListCards`/`GetStatement`).

- [ ] **Step 4: Run to verify it passes**

Run: `cd api && go test ./internal/services/ -count=1`
Expected: `ok` (all existing job tests included).

- [ ] **Step 5: Commit**

```bash
git add api/internal/services/
git commit -m "feat(recurrences): the daily job posts card occurrences on their day and flags a broken target"
```

---

### Task 5: Projection: card occurrences are virtual in their statement's due month

**Files:**
- Modify: `api/internal/services/finance_recurrences.go` (`Project` :193-283)
- Modify: `api/internal/services/finance_cards.go` (`projectCards` :25-52)
- Test: `api/internal/services/finance_test.go`

**Interfaces:**
- Consumes: Task 1 (`PlaceInstallments`, `DueDate`), Task 4 (`recCards`, `visa25`, `onCard`, `cardStore`).
- Produces: `func (j *FinanceJobs) activeCards(ctx, sp) (map[string]repositories.CardRow, error)` (nil map when `j.cards == nil`); `projectCards(ctx, sp, cards map[string]repositories.CardRow, windowEnd, add)`.

- [ ] **Step 1: Write the failing tests**

Append to `api/internal/services/finance_test.go`:

```go
func monthOf(p Projection, m time.Month) ProjectionMonth {
	for _, x := range p.Months {
		if x.Month.Month == m {
			return x
		}
	}
	return ProjectionMonth{}
}

// Spec § 6.2: charged on the 28th, closing on the 25th: next month's
// statement, due on the 5th of the month after. Virtual there while not posted;
// once posted, only Payable (through the statement), never both.
func TestACardOccurrenceIsVirtualInItsStatementsDueMonthThenOnlyPayable(t *testing.T) {
	cards, recs := newRecCards(visa25()), newFakeRecs(onCard("s", 28, 1))
	jobs := NewFinanceJobs(newFakeBills(), recs).WithCards(cards)

	p, err := jobs.Project(context.Background(), testSpace(t), day(2026, time.March, 20), 4) // Mar..Jun
	if err != nil {
		t.Fatal(err)
	}
	// Mar 28 -> April statement -> due May 5; Apr 28 -> May -> June 5.
	if got := monthOf(p, time.May); got.VirtualPayable != 30000 || got.Virtual != -30000 || got.Payable != 0 {
		t.Fatalf("May before posting = %+v", got)
	}
	if got := monthOf(p, time.April); got.VirtualPayable != 0 {
		t.Fatalf("April = %+v: nothing is due in the month it is charged", got)
	}

	// The job posts Mar 28 (cursor = Mar 28); the April statement now holds it.
	recs.cursors["s"] = day(2026, time.March, 28)
	cards.totals["visa|2026-04"] = 30000
	p, _ = jobs.Project(context.Background(), testSpace(t), day(2026, time.March, 28), 4)
	if got := monthOf(p, time.May); got.Payable != 30000 || got.VirtualPayable != 0 {
		t.Fatalf("May after posting = %+v: Payable once, no virtual", got)
	}
	if got := monthOf(p, time.June); got.VirtualPayable != 30000 {
		t.Fatalf("June = %+v: the April 28 occurrence is still virtual", got)
	}
}

// Spec § 6.3 (virtual half): 3x spreads over three statements' due months.
func TestAnInstallmentCardOccurrenceSpreadsOverThreeDueMonths(t *testing.T) {
	r := onCard("s", 28, 3)
	r.Schedule.End = day(2026, time.March, 31) // one occurrence only
	jobs := NewFinanceJobs(newFakeBills(), newFakeRecs(r)).WithCards(newRecCards(visa25()))
	p, err := jobs.Project(context.Background(), testSpace(t), day(2026, time.March, 20), 6) // Mar..Aug
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []time.Month{time.May, time.June, time.July} {
		if got := monthOf(p, m); got.VirtualPayable != 10000 {
			t.Errorf("%s = %+v, want one installment (100,00)", m, got)
		}
	}
	if got := monthOf(p, time.August); got.VirtualPayable != 0 {
		t.Errorf("August = %+v", got)
	}
}

func TestACardRecurrenceOnAnArchivedCardProjectsNothing(t *testing.T) {
	archived := visa25()
	archived.Archived = true
	jobs := NewFinanceJobs(newFakeBills(), newFakeRecs(onCard("s", 28, 1))).WithCards(newRecCards(archived))
	p, _ := jobs.Project(context.Background(), testSpace(t), day(2026, time.March, 20), 4)
	for _, m := range p.Months {
		if m.VirtualPayable != 0 {
			t.Fatalf("%s = %+v: an archived card is charged nothing until the recurrence is retargeted", m.Month, m)
		}
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd api && go test ./internal/services/ -run 'CardOccurrence|InstallmentCard|ArchivedCardProjects' -count=1`
Expected: FAIL: `May before posting = {... VirtualPayable:0 ...}` (today the card recurrence is projected by its occurrence due date: March/April).

- [ ] **Step 3: Implement**

`finance_cards.go`:

```go
// activeCards is the space's active cards by id, read once per projection.
func (j *FinanceJobs) activeCards(ctx context.Context, sp space.ResolvedSpace) (map[string]repositories.CardRow, error) {
	if j.cards == nil {
		return nil, nil
	}
	rows, err := j.cards.ListCards(ctx, sp)
	if err != nil {
		return nil, err
	}
	out := make(map[string]repositories.CardRow, len(rows))
	for _, c := range rows {
		if !c.Archived {
			out[c.ID] = c
		}
	}
	return out, nil
}

func (j *FinanceJobs) projectCards(ctx context.Context, sp space.ResolvedSpace, cards map[string]repositories.CardRow, windowEnd brcal.Date, add func(due brcal.Date, amount billing.Cents)) error {
	for _, c := range cards {
		for m := c.OpenMonth; ; m = m.Add(1) {
			due := finance.DueDate(m, c.ClosingDay, c.DueDay)
			if due.After(windowEnd) {
				break
			}
			s, err := j.cards.GetStatement(ctx, sp, c.ID, m)
			if err != nil {
				return err
			}
			if s.Total > 0 {
				add(due, s.Total)
			}
		}
	}
	return nil
}
```

`Project`: before the card step, `cards, err := j.activeCards(ctx, sp); if err != nil { return Projection{}, err }`; call `j.projectCards(ctx, sp, cards, cardsEnd, …)`. In the recurrence loop, after computing `occ`:

```go
		if r.IsCard() {
			card, ok := cards[r.CardID]
			if !ok {
				continue // archived or unknown: nothing is charged until it is retargeted
			}
			for _, o := range occ {
				if o.Nominal.Before(card.FirstMonth.First()) {
					continue // the job never posts these (Task 1)
				}
				plan, err := finance.PlaceInstallments(r.Amount, r.Installments, o.Nominal, card.ClosingDay, card.OpenMonth)
				if err != nil {
					return Projection{}, fmt.Errorf("projecting recurrence %s: %w", r.ID, err)
				}
				for _, inst := range plan {
					// D4: virtual in the month the statement is due, never the month charged.
					if i, ok := slot(finance.DueDate(inst.Statement, card.ClosingDay, card.DueDay)); ok {
						window[i].Virtual -= inst.Amount
						window[i].VirtualPayable += inst.Amount
					}
				}
			}
			continue
		}
```

`occ` stays computed from `cursor + 1` (or Start): posted occurrences are statement items already counted by `projectCards`, so nothing is counted twice.

- [ ] **Step 4: Run to verify it passes**

Run: `cd api && go test ./internal/services/ -count=1`
Expected: `ok` (`TestProjectCountsCardStatementsOnTheirDueMonth` included).

- [ ] **Step 5: Commit**

```bash
git add api/internal/services/
git commit -m "feat(projection): a card occurrence is virtual in its statement's due month until posted"
```

---

### Task 6: The whole job against real tables (spec § 6.1 to § 6.4)

**Files:**
- Modify: `api/internal/app/app.go:160` and `:504` (unchanged calls; verify they compile against `cardStore`)
- Test: `api/tests/integration/finance_card_recurrences_test.go`

**Interfaces:**
- Consumes: Tasks 2 to 5 (`services.NewFinanceJobs(...).WithCards(...)`, `Materialise`, `Project`, `cardRecFixture`).
- Produces: nothing new; the spec's tests against DynamoDB.

- [ ] **Step 1: Write the tests**

Append to `api/tests/integration/finance_card_recurrences_test.go` (add `"gopkg.aoctech.app/billing/api/internal/services"` to the imports):

```go
func (f cardRecFixture) jobs() *services.FinanceJobs {
	return services.NewFinanceJobs(f.bills, f.recs).WithCards(f.cards)
}

func at(y int, m time.Month, d int) (brcal.Date, time.Time) {
	return brcal.New(y, m, d), time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
}

func (f cardRecFixture) items(t *testing.T, m time.Month) []repositories.StatementItem {
	t.Helper()
	s, err := f.cards.GetStatement(context.Background(), f.sp, f.card.ID, finance.Month{Year: 2026, Month: m})
	if err != nil {
		t.Fatal(err)
	}
	return s.Items
}

// Spec § 6.1. The work lists are shared with every test in the run: assert on
// this space's rows only, never on the job's counters.
func TestACardRecurrencePostsOnePurchasePerOccurrenceAcrossRunsAndARace(t *testing.T) {
	f := newCardRecFixture(t)
	f.cardRecurrence(t, 28, 3990, 1)
	job := f.jobs()
	today, now := at(2026, time.March, 28)
	job.Materialise(context.Background(), true, today, now)
	job.Materialise(context.Background(), true, today, now)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); job.Materialise(context.Background(), true, today, now) }()
	}
	wg.Wait()
	job.Materialise(context.Background(), true, today, now) // heals a run that lost a transaction conflict
	if got := len(f.items(t, time.April)); got != 1 {
		t.Fatalf("April holds %d items, want exactly one purchase", got)
	}
	if got := balance(t, f.ledger, f.sp, "subs"); got != 3990 {
		t.Fatalf("subs = %d, want one expense", got)
	}
}

// Spec § 6.2.
func TestAnOccurrenceAfterTheClosingDayLandsOnNextMonthAndTheProjectionFollows(t *testing.T) {
	f := newCardRecFixture(t)
	f.cardRecurrence(t, 28, 3990, 1)
	job := f.jobs()
	before, _ := at(2026, time.March, 20)
	p, err := job.Project(context.Background(), f.sp, before, 4)
	if err != nil {
		t.Fatal(err)
	}
	if p.Months[2].Month.Month != time.May || p.Months[2].VirtualPayable != 3990 || p.Months[2].Payable != 0 {
		t.Fatalf("May while virtual = %+v", p.Months[2])
	}
	today, now := at(2026, time.March, 28)
	job.Materialise(context.Background(), true, today, now)
	if got := f.items(t, time.April); len(got) != 1 || got[0].Date != today {
		t.Fatalf("April = %+v: the 28th is after the 25th", got)
	}
	p, _ = job.Project(context.Background(), f.sp, today, 4)
	if p.Months[2].Payable != 3990 || p.Months[2].VirtualPayable != 0 {
		t.Fatalf("May once posted = %+v: Payable only", p.Months[2])
	}
}

// Spec § 6.3.
func TestAThreeInstallmentRecurrenceSpreadsOverThreeStatements(t *testing.T) {
	f := newCardRecFixture(t)
	f.cardRecurrence(t, 20, 30000, 3)
	today, now := at(2026, time.March, 20)
	f.jobs().Materialise(context.Background(), true, today, now)
	for _, m := range []time.Month{time.March, time.April, time.May} {
		if got := f.items(t, m); len(got) != 1 || got[0].Amount != 10000 || got[0].Of != 3 {
			t.Fatalf("%s = %+v, want one installment of 100,00", m, got)
		}
	}
}

// Spec § 6.4.
func TestABrokenTargetPostsNothingUntilFixedThenCatchesUp(t *testing.T) {
	f := newCardRecFixture(t)
	ctx := context.Background()
	rec := f.cardRecurrence(t, 10, 3990, 1)
	if err := f.ledger.ArchiveAccount(ctx, f.sp, f.card.ID, cardRecNow); err != nil {
		t.Fatal(err)
	}
	job := f.jobs()
	d1, n1 := at(2026, time.April, 12)
	job.Materialise(ctx, true, d1, n1)
	got, cursor, _ := f.recs.GetWithCursor(ctx, f.sp, rec.ID)
	if got.NeedsAttention != finance.AttentionCardArchived || !cursor.IsZero() || balance(t, f.ledger, f.sp, "subs") != 0 {
		t.Fatalf("archived card: %+v cursor %s", got, cursor)
	}
	other, err := f.cards.CreateCard(ctx, f.sp, "Master", finance.Card{ClosingDay: 25, DueDay: 5, PayingAccountID: "bank"}, cardRecNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.recs.Update(ctx, f.sp, rec.ID, repositories.RecurrencePatch{CardID: &other.ID}, n1); err != nil {
		t.Fatal(err)
	}
	job.Materialise(ctx, true, d1, n1)
	if got := balance(t, f.ledger, f.sp, "subs"); got != 2*3990 {
		t.Fatalf("subs = %d, want March 10 and April 10 posted on the new card", got)
	}
	got, cursor, _ = f.recs.GetWithCursor(ctx, f.sp, rec.ID)
	if got.NeedsAttention != "" || cursor != d1 {
		t.Fatalf("after the fix: %+v cursor %s", got, cursor)
	}

	// f.card is archived now: this one is on the working card.
	cat, err := f.recs.Create(ctx, f.sp, finance.Recurrence{Direction: finance.Payable, Amount: 1000, CategoryID: "subs", CardID: other.ID, Installments: 1,
		Schedule: finance.Schedule{Expression: finance.DayOfMonth{Day: 15}, Start: brcal.New(2026, time.March, 1), Adjust: finance.AdjustNone}},
		repositories.PostMeta{}, n1)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.ledger.ArchiveAccount(ctx, f.sp, "subs", n1); err != nil {
		t.Fatal(err)
	}
	d2, n2 := at(2026, time.April, 16)
	job.Materialise(ctx, true, d2, n2)
	if got, _ := f.recs.Get(ctx, f.sp, cat.ID); got.NeedsAttention != finance.AttentionCategoryArchived {
		t.Fatalf("archived category: needs_attention = %q", got.NeedsAttention)
	}
	food := "food"
	if err := f.recs.Update(ctx, f.sp, cat.ID, repositories.RecurrencePatch{CategoryID: &food}, n2); err != nil {
		t.Fatal(err)
	}
	job.Materialise(ctx, true, d2, n2)
	if got := balance(t, f.ledger, f.sp, "food"); got != 2*1000 {
		t.Fatalf("food = %d, want March 15 and April 15 once the category is fixed", got)
	}
}

// Review Focus 2.
func TestSwitchingTargetNeverPostsAnOccurrenceTwice(t *testing.T) {
	f := newCardRecFixture(t)
	ctx := context.Background()
	rec, err := f.recs.Create(ctx, f.sp, finance.Recurrence{Direction: finance.Payable, Amount: 5000, CategoryID: "subs", AccountID: "bank",
		Schedule: finance.Schedule{Expression: finance.DayOfMonth{Day: 10}, Start: brcal.New(2026, time.March, 1), Adjust: finance.AdjustNone}},
		repositories.PostMeta{}, cardRecNow)
	if err != nil {
		t.Fatal(err)
	}
	job := f.jobs()
	d1, n1 := at(2026, time.March, 20)
	job.Materialise(ctx, true, d1, n1) // bills for Mar 10 and Apr 10 (horizon: end of April)
	if err := f.recs.Update(ctx, f.sp, rec.ID, repositories.RecurrencePatch{CardID: &f.card.ID}, n1); err != nil {
		t.Fatal(err)
	}
	d2, n2 := at(2026, time.May, 10)
	job.Materialise(ctx, true, d2, n2)
	if got := balance(t, f.ledger, f.sp, "subs"); got != 3*5000 {
		t.Fatalf("subs = %d, want Mar and Apr as bills and May as a purchase, each once", got)
	}
	bank := "bank"
	if err := f.recs.Update(ctx, f.sp, rec.ID, repositories.RecurrencePatch{AccountID: &bank}, n2); err != nil {
		t.Fatal(err)
	}
	job.Materialise(ctx, true, d2, n2) // bills from May 11 on: June 10
	if got := balance(t, f.ledger, f.sp, "subs"); got != 4*5000 {
		t.Fatalf("subs = %d, want June added as a bill and May not again", got)
	}
}

// Review Focus 3, against the tables.
func TestARecurrenceOlderThanTheCardSkipsWhatCameBefore(t *testing.T) {
	f := newCardRecFixture(t)
	ctx := context.Background()
	if _, err := f.recs.Create(ctx, f.sp, finance.Recurrence{Direction: finance.Payable, Amount: 1000, CategoryID: "subs", CardID: f.card.ID, Installments: 1,
		Schedule: finance.Schedule{Expression: finance.DayOfMonth{Day: 5}, Start: brcal.New(2026, time.January, 1), Adjust: finance.AdjustNone}},
		repositories.PostMeta{}, cardRecNow); err != nil {
		t.Fatal(err)
	}
	d, n := at(2026, time.March, 6)
	f.jobs().Materialise(ctx, true, d, n)
	if got := balance(t, f.ledger, f.sp, "subs"); got != 1000 {
		t.Fatalf("subs = %d, want only March 5 (the card was added in March)", got)
	}
}
```

- [ ] **Step 2: Run them**

Run: `cd api && go build ./... && DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -race -count=1 -run 'CardRecurrence|ClosingDay|ThreeInstallment|BrokenTarget|SwitchingTarget|OlderThanTheCard' ./tests/integration/`
Expected: `ok`. If one fails, it is a defect in Tasks 2 to 5: fix it there (systematic-debugging), never weaken the assertion.

- [ ] **Step 3: Run the whole Go integration suite once**

Run: `cd api && DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -race -count=1 ./tests/integration/`
Expected: `ok`.

- [ ] **Step 4: Commit**

```bash
git add api/tests/integration/finance_card_recurrences_test.go
git commit -m "test(recurrences): card occurrences against real tables (spec § 6.1 to § 6.4)"
```

---
### Task 7: Range endpoint: statement summaries with one BETWEEN query

**Files:**
- Create: `api/internal/repositories/cards_range.go`
- Modify: `api/internal/repositories/bills.go` (add `PaidAmong`)
- Modify: `api/internal/services/finance_bills.go` (passthrough)
- Modify: `api/internal/api/v1/finance_cards.go` (summary DTO, handler), `api/internal/api/v1/finance_http.go:112` (route)
- Test: `api/tests/integration/finance_card_range_test.go` (create), `api/internal/api/v1/finance_routes_test.go` (the route table test picks the route up; run it)

**Interfaces:**
- Consumes: Task 3 (`buildStatement`, `CardRow.FirstMonth`), `LedgerRepository.queryRange(ctx, b Base, pk, lo, hi)`.
- Produces:
  - `type StatementPage struct { Statements []Statement; HasMore bool }` (statements in the requested direction's order, items included; the DTO drops them).
  - `const MaxStatementPage = 12`
  - `func (r *CardRepository) StatementRange(ctx context.Context, sp space.ResolvedSpace, cardID string, from finance.Month, count int, backward bool) (StatementPage, error)`
  - `func (r *BillRepository) PaidAmong(ctx context.Context, sp space.ResolvedSpace, ids []string) (map[string]bool, error)` and `FinanceBills.PaidAmong` with the same signature.
  - HTTP `GET /cards/:id/statements?from=YYYY-MM&count=N&direction=forward|backward` → `{"data":[{"card_id","month","status","closing_date","due_date","total","bill_id?","items_count"}],"has_more":bool}`.

Bounds (spec § 4.1): backward stops at `FirstMonth`; forward stops at `max(OpenMonth + 12, month of the last STATEMENT# row)`. The last-row month is one `Limit: 1` reverse prefix Query, read **only** when the page reaches `OpenMonth + 12`. A forward `from` before `FirstMonth` starts at `FirstMonth`; a page entirely outside the bounds is empty with `has_more: false`.

- [ ] **Step 1: Write the failing tests**

Create `api/tests/integration/finance_card_range_test.go`:

```go
//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gopkg.aoctech.app/billing/api/internal/domain/brcal"
	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/repositories"
)

func months(p repositories.StatementPage) []string {
	out := make([]string, len(p.Statements))
	for i, s := range p.Statements {
		out[i] = s.Month.String()
	}
	return out
}

func TestTheRangeStopsAtTheCardsFirstMonthGoingBack(t *testing.T) {
	f, card := newCardsFixture(t) // first and open month: March 2026
	ctx := context.Background()
	p, err := f.cards.StatementRange(ctx, f.sp, card.ID, finance.Month{Year: 2026, Month: time.April}, 3, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(months(p)); got != "[2026-04 2026-03]" || p.HasMore {
		t.Fatalf("backward from April = %s has_more %v, want [2026-04 2026-03] and no more", got, p.HasMore)
	}
	p, _ = f.cards.StatementRange(ctx, f.sp, card.ID, finance.Month{Year: 2026, Month: time.February}, 3, true)
	if len(p.Statements) != 0 || p.HasMore {
		t.Fatalf("before the first month = %v %v", months(p), p.HasMore)
	}
	p, _ = f.cards.StatementRange(ctx, f.sp, card.ID, finance.Month{Year: 2025, Month: time.December}, 3, false)
	if got := fmt.Sprint(months(p)); got != "[2026-03 2026-04 2026-05]" || !p.HasMore {
		t.Fatalf("forward from before the card = %s %v", got, p.HasMore)
	}
}

func TestTheRangeReachesTheLastFutureItemOrOpenPlusTwelve(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	// No item beyond: forward stops at open + 12 (2027-03).
	p, _ := f.cards.StatementRange(ctx, f.sp, card.ID, finance.Month{Year: 2027, Month: time.January}, 3, false)
	if got := fmt.Sprint(months(p)); got != "[2027-01 2027-02 2027-03]" || p.HasMore {
		t.Fatalf("to open+12 = %s %v", got, p.HasMore)
	}
	// 24× from March: the last installment bills on 2028-02, past open + 12.
	f.purchase(t, card.ID, brcal.New(2026, time.March, 2), 240000, 24)
	p, _ = f.cards.StatementRange(ctx, f.sp, card.ID, finance.Month{Year: 2027, Month: time.January}, 3, false)
	if !p.HasMore {
		t.Fatal("an installment beyond open+12 must keep the range open")
	}
	p, _ = f.cards.StatementRange(ctx, f.sp, card.ID, finance.Month{Year: 2028, Month: time.January}, 3, false)
	if got := fmt.Sprint(months(p)); got != "[2028-01 2028-02]" || p.HasMore {
		t.Fatalf("to the last item = %s %v", got, p.HasMore)
	}
	p, _ = f.cards.StatementRange(ctx, f.sp, card.ID, finance.Month{Year: 2028, Month: time.March}, 3, false)
	if len(p.Statements) != 0 || p.HasMore {
		t.Fatalf("past the last item = %v %v", months(p), p.HasMore)
	}
}

// Review Focus 1: the last month of a range carries an item and its total.
func TestRangeTotalsEqualTheSingleMonthEndpoint(t *testing.T) {
	f, card := newCardsFixture(t)
	ctx := context.Background()
	f.purchase(t, card.ID, brcal.New(2026, time.March, 1), 90000, 3) // Mar, Apr, May
	f.purchase(t, card.ID, brcal.New(2026, time.March, 2), 5000, 1)  // Mar
	f.closeThrough(t, card.ID, brcal.New(2026, time.March, 3))      // March closed: a frozen total and a bill
	p, err := f.cards.StatementRange(ctx, f.sp, card.ID, finance.Month{Year: 2026, Month: time.March}, 3, false)
	if err != nil || len(p.Statements) != 3 {
		t.Fatalf("range = %v, %v", months(p), err)
	}
	for _, s := range p.Statements {
		one, err := f.cards.GetStatement(ctx, f.sp, card.ID, s.Month)
		if err != nil {
			t.Fatal(err)
		}
		if s.Total != one.Total || s.Status != one.Status || len(s.Items) != len(one.Items) || s.BillID != one.BillID || s.DueDate != one.DueDate {
			t.Errorf("%s: range %+v vs single %+v", s.Month, s, one)
		}
	}
	if last := p.Statements[2]; last.Month.Month != time.May || len(last.Items) != 1 || last.Total != 30000 {
		t.Fatalf("May (the range's last month) = %+v: its items must be inside the BETWEEN", last)
	}
}

func TestTheRangeEndpointAnswersSummariesAndRefusesNonsense(t *testing.T) {
	f := newFinanceEnv(t) // pinned clock 2026-03-10: the card opens in March
	var bank, food, card struct{ ID string }
	f.must(t, 201, "POST", "/accounts", `{"name":"Banco","class":"asset"}`, &bank)
	f.must(t, 201, "POST", "/accounts", `{"name":"Mercado","class":"expense","dre_group":"operating_expenses"}`, &food)
	f.must(t, 201, "POST", "/cards", fmt.Sprintf(`{"name":"Visa","closing_day":15,"due_day":25,"paying_account_id":%q}`, bank.ID), &card)
	f.must(t, 201, "POST", "/cards/"+card.ID+"/purchases", fmt.Sprintf(`{"date":"2026-03-09","description":"Geladeira","category_id":%q,"total":30000,"installments":3}`, food.ID), nil)
	f.must(t, 200, "POST", "/cards/"+card.ID+"/close", `{"month":"2026-03"}`, nil)

	var page struct {
		Data []struct {
			Month      string
			Status     string
			Total      int64
			BillID     string `json:"bill_id"`
			ItemsCount int    `json:"items_count"`
			Items      any
		}
		HasMore bool `json:"has_more"`
	}
	f.must(t, 200, "GET", "/cards/"+card.ID+"/statements?from=2026-03&count=3&direction=forward", "", &page)
	if len(page.Data) != 3 || page.Data[0].Status != "closed" || page.Data[0].BillID == "" || page.Data[0].ItemsCount != 1 || page.Data[1].Status != "open" || !page.HasMore {
		t.Fatalf("page = %+v", page)
	}
	if page.Data[0].Items != nil {
		t.Fatal("a summary carries no items")
	}
	f.must(t, 200, "GET", "/cards/"+card.ID+"/statements?from=2026-03&count=3&direction=backward", "", &page)
	if len(page.Data) != 1 || page.HasMore {
		t.Fatalf("backward from the first month = %+v", page)
	}
	for _, q := range []string{"from=2026-3", "from=2026-03&count=0", "from=2026-03&count=13", "from=2026-03&direction=sideways", ""} {
		if res := f.call(t, "GET", "/cards/"+card.ID+"/statements?"+q, ""); res.status != 422 {
			t.Errorf("?%s = %d %s, want 422", q, res.status, res.body)
		}
	}
	other := newFinanceEnv(t)
	if res := other.call(t, "GET", "/cards/"+card.ID+"/statements?from=2026-03", ""); res.status != 404 {
		t.Fatalf("another space's card = %d, want 404", res.status)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd api && DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -race -count=1 -run 'Range' ./tests/integration/`
Expected: FAIL to compile (`f.cards.StatementRange undefined`).

- [ ] **Step 3: Implement**

Create `api/internal/repositories/cards_range.go`:

```go
package repositories

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"gopkg.aoctech.app/billing/api/internal/domain/finance"
	"gopkg.aoctech.app/billing/api/internal/space"
)

// MaxStatementPage bounds one range read (spec § 4.1).
const MaxStatementPage = 12

// rangeHorizon is how far past the open month the forward bound reaches when no
// item lies further.
const rangeHorizon = 12

// StatementPage is a run of statements in the order asked, and whether another
// page exists past its far end.
type StatementPage struct {
	Statements []Statement
	HasMore    bool
}

// StatementRange reads up to count statements from `from` (inclusive), forward
// or backward, with ONE Query on the card's partition: sort keys between
// STATEMENT#lo and STATEMENT#hi$ hold every statement row and item of those
// months ('$' sorts after '#', so hi's items are inside). Totals are computed
// exactly as GetStatement computes them.
func (r *CardRepository) StatementRange(ctx context.Context, sp space.ResolvedSpace, cardID string, from finance.Month, count int, backward bool) (StatementPage, error) {
	if err := sp.Require(space.Read); err != nil {
		return StatementPage{}, err
	}
	if count < 1 || count > MaxStatementPage {
		return StatementPage{}, fmt.Errorf("%w: a page holds 1..%d statements", finance.ErrInvalidCard, MaxStatementPage)
	}
	card, err := r.loadCard(ctx, sp, cardID)
	if err != nil {
		return StatementPage{}, err
	}
	first, last := card.FirstMonth, card.OpenMonth.Add(rangeHorizon)
	// The last item's month only matters once the page reaches open + 12.
	extend := func(m finance.Month) error {
		if m.Compare(last) < 0 {
			return nil
		}
		var err error
		last, err = r.lastItemMonth(ctx, sp, card, last)
		return err
	}
	var lo, hi finance.Month
	var page StatementPage
	if backward {
		hi = from
		if err := extend(hi); err != nil {
			return StatementPage{}, err
		}
		if hi.Compare(last) > 0 {
			hi = last
		}
		if hi.Compare(first) < 0 {
			return StatementPage{}, nil
		}
		lo = hi.Add(-(count - 1))
		if lo.Compare(first) < 0 {
			lo = first
		}
		page.HasMore = lo.Compare(first) > 0
	} else {
		lo = from
		if lo.Compare(first) < 0 {
			lo = first
		}
		hi = lo.Add(count - 1)
		if err := extend(hi); err != nil {
			return StatementPage{}, err
		}
		if lo.Compare(last) > 0 {
			return StatementPage{}, nil
		}
		if hi.Compare(last) > 0 {
			hi = last
		}
		page.HasMore = hi.Compare(last) < 0
	}
	raw, err := r.ledger.queryRange(ctx, r.cards, CardPK(sp, card.ID), StatementSK(lo), StatementSK(hi)+"$")
	if err != nil {
		return StatementPage{}, err
	}
	byMonth := map[finance.Month][]map[string]types.AttributeValue{}
	for _, it := range raw {
		if sk, ok := it["sk"].(*types.AttributeValueMemberS); ok {
			if m, ok := statementMonthOf(sk.Value); ok {
				byMonth[m] = append(byMonth[m], it)
			}
		}
	}
	for m := lo; m.Compare(hi) <= 0; m = m.Add(1) {
		s, err := buildStatement(card, m, byMonth[m])
		if err != nil {
			return StatementPage{}, err
		}
		page.Statements = append(page.Statements, s)
	}
	if backward {
		slices.Reverse(page.Statements)
	}
	return page, nil
}

// lastItemMonth is the later of floor and the month of the card's last
// STATEMENT# row (a statement or an item): one reverse Query of one row.
func (r *CardRepository) lastItemMonth(ctx context.Context, sp space.ResolvedSpace, card CardRow, floor finance.Month) (finance.Month, error) {
	res, err := r.cards.Query(ctx, QueryOpts{PK: CardPK(sp, card.ID), SKPrefix: "STATEMENT#", ScanIndexForward: false, Limit: 1, ConsistentRead: true})
	if err != nil {
		return floor, err
	}
	for _, it := range res.Items {
		if sk, ok := it["sk"].(*types.AttributeValueMemberS); ok {
			if m, ok := statementMonthOf(sk.Value); ok && m.Compare(floor) > 0 {
				return m, nil
			}
		}
	}
	return floor, nil
}

// statementMonthOf reads the month of a STATEMENT#YYYY-MM[#ITEM#…] sort key.
func statementMonthOf(sk string) (finance.Month, bool) {
	rest, ok := strings.CutPrefix(sk, "STATEMENT#")
	if !ok || len(rest) < 7 {
		return finance.Month{}, false
	}
	m, err := finance.ParseMonth(rest[:7])
	return m, err == nil
}
```

`bills.go`, after `get`:

```go
// PaidAmong answers which of the given bills are paid: one BatchGetItem (the
// range endpoint's "paid" for up to 12 closed statements), retrying unprocessed
// keys. Unknown ids are simply absent.
func (r *BillRepository) PaidAmong(ctx context.Context, sp space.ResolvedSpace, ids []string) (map[string]bool, error) {
	out := map[string]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	if err := sp.Require(space.Read); err != nil {
		return nil, err
	}
	keys := make([]map[string]types.AttributeValue, len(ids))
	for i, id := range ids {
		keys[i] = map[string]types.AttributeValue{"pk": str(sp.PK()), "sk": str(BillSK(id))}
	}
	req := map[string]types.KeysAndAttributes{r.bills.TableName: {
		Keys: keys, ConsistentRead: aws.Bool(true), ProjectionExpression: aws.String("#id, #st"),
		ExpressionAttributeNames: map[string]string{"#id": "id", "#st": "status"},
	}}
	for attempt := 0; len(req) > 0; attempt++ {
		if attempt == 5 {
			return nil, fmt.Errorf("bills: %d keys still unprocessed after retries", len(req[r.bills.TableName].Keys))
		}
		res, err := r.bills.BatchGetItemRaw(ctx, &dynamodb.BatchGetItemInput{RequestItems: req})
		if err != nil {
			return nil, err
		}
		rows, err := DecodeItems[struct {
			ID     string `dynamodbav:"id"`
			Status string `dynamodbav:"status"`
		}](res.Responses[r.bills.TableName])
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			out[row.ID] = row.Status == string(finance.BillPaid)
		}
		req = res.UnprocessedKeys
	}
	return out, nil
}
```

(`dynamodb` and `aws` are already imported in `bills.go`; add them if the compiler says otherwise.) `finance_bills.go`:

```go
func (s *FinanceBills) PaidAmong(ctx context.Context, sp space.ResolvedSpace, ids []string) (map[string]bool, error) {
	return s.repo.PaidAmong(ctx, sp, ids)
}
```

`finance_cards.go` (HTTP):

```go
type statementSummaryDTO struct {
	CardID      string        `json:"card_id"`
	Month       string        `json:"month"`
	Status      string        `json:"status"` // open | future | closed | paid
	ClosingDate string        `json:"closing_date"`
	DueDate     string        `json:"due_date"`
	Total       billing.Cents `json:"total"`
	BillID      string        `json:"bill_id,omitempty"`
	ItemsCount  int           `json:"items_count"`
}

// cardStatements is the timeline's read (spec § 4.1): summaries only; the
// selected month's items come from GET /cards/:id/statements/:month.
func (h *financeHandlers) cardStatements(c fiber.Ctx) error {
	ch := &checks{}
	from, err := finance.ParseMonth(c.Query("from"))
	if err != nil {
		ch.fail("from", "invalid_format", "use YYYY-MM", "format", "YYYY-MM")
	}
	count := fiber.Query(c, "count", 3)
	if count < 1 || count > repositories.MaxStatementPage {
		ch.fail("count", "out_of_range", "between 1 and 12", "min", 1, "max", repositories.MaxStatementPage)
	}
	direction := c.Query("direction", "forward")
	if direction != "forward" && direction != "backward" {
		ch.fail("direction", "unsupported_value", "use forward or backward", "allowed", []string{"forward", "backward"})
	}
	if len(ch.errs) > 0 {
		return problem.Validation(ch.errs).Send(c)
	}
	sp := middleware.GetSpace(c)
	page, err := h.cards.StatementRange(c.Context(), sp, c.Params("id"), from, count, direction == "backward")
	if err != nil {
		return fail(c, err)
	}
	var billIDs []string
	for _, s := range page.Statements {
		if s.BillID != "" {
			billIDs = append(billIDs, s.BillID)
		}
	}
	paid, err := h.bills.PaidAmong(c.Context(), sp, billIDs)
	if err != nil {
		return fail(c, err)
	}
	out := listResponse[statementSummaryDTO]{Data: make([]statementSummaryDTO, 0, len(page.Statements)), HasMore: page.HasMore}
	for _, s := range page.Statements {
		status := s.Status
		if s.BillID != "" && paid[s.BillID] {
			status = "paid"
		}
		out.Data = append(out.Data, statementSummaryDTO{CardID: s.CardID, Month: s.Month.String(), Status: status,
			ClosingDate: s.ClosingDate.String(), DueDate: s.DueDate.String(), Total: s.Total, BillID: s.BillID, ItemsCount: len(s.Items)})
	}
	return c.JSON(out)
}
```

`finance_http.go`, beside the single-month route:

```go
		{"GET", "/cards/:id/statements", space.Read, false, func(h *financeHandlers) fiber.Handler { return h.cardStatements }},
```

`ErrNotFound` from `loadCard` already maps to 404 through `fail`.

- [ ] **Step 4: Run to verify it passes**

Run: `cd api && go test ./internal/... -count=1 && DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -race -count=1 -run 'Range|Card' ./tests/integration/`
Expected: both `ok` (the routes gate test covers the new route with `space.Read`).

- [ ] **Step 5: Commit**

```bash
git add api/internal/ api/tests/integration/finance_card_range_test.go
git commit -m "feat(cards): GET /cards/:id/statements, a range of statement summaries in one query"
```

---

### Task 8: HTTP: recurrences paid by card, their history, and the recurrence link on items

**Files:**
- Modify: `api/internal/api/v1/finance_recurrences.go` (DTO :19-49, create :89-129, patch :147-188, occurrences :258-329)
- Modify: `api/internal/api/v1/finance_cards.go` (`statementItemDTO` :35, `purchaseDTO` :63, their builders)
- Test: `api/tests/integration/finance_card_recurrences_http_test.go` (create)

**Interfaces:**
- Consumes: Tasks 1 to 3 (domain fields, `RecurrencePatch.CardID/Installments`, `OccurrenceBill.Purchase`, `PlaceInstallments`, `GetCard`).
- Produces (JSON):
  - recurrence: `account_id` (`""` on a card recurrence), `card_id?`, `installments?`, `needs_attention?` (`card_archived` | `category_archived`).
  - create body: `account_id` or `card_id` (exactly one), `installments?` (card only, default 1).
  - patch body: `card_id`, `installments` (patch rule: absent keeps; null on either is `required`).
  - occurrences: history entries gain `purchase_id?`, `card_id?`, `statement_month?`, `state: "posted"`; `bill_id` becomes `omitempty`. Upcoming entries gain `statement_month?` (card only).
  - statement item: `recurrence_id?`; purchase: `origin?` (`recurrence`), `recurrence_id?`.

- [ ] **Step 1: Write the failing test**

Create `api/tests/integration/finance_card_recurrences_http_test.go`:

```go
//go:build integration

package integration

import (
	"fmt"
	"testing"
)

// The pinned clock is 2026-03-10.
func TestARecurrenceIsPaidByCardOverHTTP(t *testing.T) {
	f := newFinanceEnv(t)
	var bank, subs, card struct{ ID string }
	f.must(t, 201, "POST", "/accounts", `{"name":"Banco","class":"asset"}`, &bank)
	f.must(t, 201, "POST", "/accounts", `{"name":"Assinaturas","class":"expense","dre_group":"operating_expenses"}`, &subs)
	f.must(t, 201, "POST", "/cards", fmt.Sprintf(`{"name":"Visa","closing_day":25,"due_day":5,"paying_account_id":%q}`, bank.ID), &card)
	rule := `"expression":{"kind":"day_of_month","day":28},"start":"2026-03-01"`

	var rec struct {
		ID           string
		AccountID    string `json:"account_id"`
		CardID       string `json:"card_id"`
		Installments int
	}
	f.must(t, 201, "POST", "/recurrences", fmt.Sprintf(`{"direction":"payable","amount":3990,"category_id":%q,"card_id":%q,%s}`, subs.ID, card.ID, rule), &rec)
	if rec.CardID != card.ID || rec.AccountID != "" || rec.Installments != 1 {
		t.Fatalf("created = %+v (installments default to 1)", rec)
	}

	for name, body := range map[string]string{
		"both targets":      fmt.Sprintf(`{"direction":"payable","amount":3990,"category_id":%q,"card_id":%q,"account_id":%q,%s}`, subs.ID, card.ID, bank.ID, rule),
		"no target":         fmt.Sprintf(`{"direction":"payable","amount":3990,"category_id":%q,%s}`, subs.ID, rule),
		"receivable":        fmt.Sprintf(`{"direction":"receivable","amount":3990,"category_id":%q,"card_id":%q,%s}`, subs.ID, card.ID, rule),
		"auto settle":       fmt.Sprintf(`{"direction":"payable","amount":3990,"category_id":%q,"card_id":%q,"auto_settle":true,%s}`, subs.ID, card.ID, rule),
		"49 installments":   fmt.Sprintf(`{"direction":"payable","amount":3990,"category_id":%q,"card_id":%q,"installments":49,%s}`, subs.ID, card.ID, rule),
		"tiny installments": fmt.Sprintf(`{"direction":"payable","amount":2,"category_id":%q,"card_id":%q,"installments":3,%s}`, subs.ID, card.ID, rule),
		"installments on account": fmt.Sprintf(`{"direction":"payable","amount":3990,"category_id":%q,"account_id":%q,"installments":2,%s}`, subs.ID, bank.ID, rule),
	} {
		if res := f.call(t, "POST", "/recurrences", body); res.status != 422 {
			t.Errorf("%s = %d %s, want 422", name, res.status, res.body)
		}
	}

	f.must(t, 200, "PATCH", "/recurrences/"+rec.ID, `{"installments":3}`, &rec)
	if rec.Installments != 3 {
		t.Fatalf("patched = %+v", rec)
	}
	if res := f.call(t, "PATCH", "/recurrences/"+rec.ID, `{"card_id":null}`); res.status != 422 {
		t.Fatalf("card_id null = %d, want 422 required", res.status)
	}

	var occ struct {
		Upcoming []struct {
			Nominal        string
			StatementMonth string `json:"statement_month"`
		}
	}
	f.must(t, 200, "GET", "/recurrences/"+rec.ID+"/occurrences", "", &occ)
	if len(occ.Upcoming) == 0 || occ.Upcoming[0].Nominal != "2026-03-28" || occ.Upcoming[0].StatementMonth != "2026-04" {
		t.Fatalf("upcoming = %+v: the 28th lands on April (closing day 25)", occ.Upcoming)
	}

	f.must(t, 200, "PATCH", "/recurrences/"+rec.ID, fmt.Sprintf(`{"account_id":%q}`, bank.ID), &rec)
	if rec.AccountID != bank.ID || rec.CardID != "" || rec.Installments != 0 {
		t.Fatalf("switched back = %+v", rec)
	}
}
```

The posted-history half (`purchase_id`, `statement_month`, `state: "posted"`) is exercised at the repository level in Task 3 (`TestTheHistoryListsPostedOccurrencesAndEndingSkipsThem`) because the HTTP env does not run the job; Step 3's DTO mapping is a direct copy of those fields.

- [ ] **Step 2: Run to verify it fails**

Run: `cd api && DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -race -count=1 -run 'PaidByCardOverHTTP' ./tests/integration/`
Expected: FAIL: `POST /recurrences = 400 … unknown field "card_id"` (decodeStrict).

- [ ] **Step 3: Implement**

`recurrenceDTO` gains:

```go
	CardID         string `json:"card_id,omitempty"`
	Installments   int    `json:"installments,omitempty"`
	NeedsAttention string `json:"needs_attention,omitempty"`
```

and `newRecurrenceDTO` sets them from `r.CardID`, `r.Installments`, `r.NeedsAttention`.

`createRecurrenceRequest` gains `CardID string `json:"card_id"`` and `Installments int `json:"installments"``. In `createRecurrence`, replace `ch.id("account_id", req.AccountID, true)` with:

```go
	switch {
	case req.AccountID != "" && req.CardID != "":
		ch.fail("card_id", "conflict", "pay with an account or a card, not both")
	case req.AccountID == "" && req.CardID == "":
		ch.fail("account_id", "required", "an account or a card is required")
	case req.CardID != "":
		ch.id("card_id", req.CardID, true)
		if req.Direction == string(finance.Receivable) {
			ch.fail("card_id", "payable_only", "a card pays expenses only")
		}
		if req.AutoSettle {
			ch.fail("auto_settle", "not_with_card", "the card's statement is what gets paid")
		}
		if req.Installments == 0 {
			req.Installments = 1
		}
		installmentChecks(ch, req.Amount, req.Installments)
	default:
		ch.id("account_id", req.AccountID, true)
		if req.Installments != 0 {
			ch.fail("installments", "card_only", "installments are for a card")
		}
	}
```

with the shared helper (also used by the purchase validator, replacing its inline switch at `finance_cards.go:268-273`):

```go
// installmentChecks is the installments rule of a purchase and of a card
// recurrence: 1..48, each at least 0,01.
func installmentChecks(c *checks, total billing.Cents, n int) {
	switch {
	case n < 1 || n > limits.MaxInstallments:
		c.fail("installments", "out_of_range", "between 1 and 48 installments", "min", 1, "max", limits.MaxInstallments)
	case total > 0 && total < billing.Cents(n):
		c.fail("installments", "installment_too_small", "each installment needs at least 0.01")
	}
}
```

Pass `CardID: req.CardID, Installments: req.Installments` into the `finance.Recurrence` literal.

`patchRecurrenceRequest` gains `CardID patch.Optional[string] `json:"card_id"`` and `Installments patch.Optional[int] `json:"installments"``. In `edit`:

```go
	if v, ok := required(ch, "card_id", r.CardID); ok {
		ch.id("card_id", v, true)
	}
	if v, ok := required(ch, "installments", r.Installments); ok && (v < 1 || v > limits.MaxInstallments) {
		ch.fail("installments", "out_of_range", "between 1 and 48 installments", "min", 1, "max", limits.MaxInstallments)
	}
	if acct, a := r.AccountID.Get(); a && acct != "" {
		if card, c := r.CardID.Get(); c && card != "" {
			ch.fail("card_id", "conflict", "pay with an account or a card, not both")
		}
	}
```

and pass `CardID: r.CardID.Ptr(), Installments: r.Installments.Ptr()` into the `RecurrencePatch`. (Amount against installments and auto_settle with a card are enforced by `Validate`, which answers 422 `invalid_recurrence`.)

Occurrences: extend the DTOs

```go
type occurrenceBillDTO struct {
	Nominal brcal.Date    `json:"nominal"`
	Due     brcal.Date    `json:"due"`
	BillID  string        `json:"bill_id,omitempty"`
	Amount  billing.Cents `json:"amount"`
	// State is paid, forecast, overdue, skipped (a bill), or posted (a card
	// purchase the job made, on the statement of statement_month).
	State          string      `json:"state"`
	PaidDate       *brcal.Date `json:"paid_date,omitempty"`
	AutoSettle     bool        `json:"auto_settle"`
	PurchaseID     string      `json:"purchase_id,omitempty"`
	CardID         string      `json:"card_id,omitempty"`
	StatementMonth string      `json:"statement_month,omitempty"`
}

type occurrenceDTO struct {
	Nominal        brcal.Date `json:"nominal"`
	Due            brcal.Date `json:"due"`
	StatementMonth string     `json:"statement_month,omitempty"`
}
```

In `recurrenceOccurrences`, the history loop starts with:

```go
		if p := m.Purchase; p != nil {
			out.History = append(out.History, occurrenceBillDTO{Nominal: m.Nominal, Due: m.Nominal, Amount: p.Amount, State: "posted",
				PurchaseID: p.ID, CardID: p.CardID, StatementMonth: p.Statement.String()})
			continue
		}
```

and the upcoming block, for a card recurrence, places each date:

```go
		var card *repositories.CardRow
		if rec.IsCard() {
			if card, err = h.cards.GetCard(c.Context(), sp, rec.CardID); err != nil && !errors.Is(err, repositories.ErrNotFound) {
				return fail(c, err)
			}
		}
		for _, o := range occ {
			d := occurrenceDTO{Nominal: o.Nominal, Due: o.Due}
			if card != nil {
				d.Due = o.Nominal // a card is charged on the day itself
				if plan, err := finance.PlaceInstallments(rec.Amount, rec.Installments, o.Nominal, card.ClosingDay, card.OpenMonth); err == nil {
					d.StatementMonth = plan[0].Statement.String()
				}
			}
			out.Upcoming = append(out.Upcoming, d)
		}
```

For a card recurrence `from` stays `max(today, cursor + 1)`: today's occurrence shows as upcoming until the job posts it. Add `"errors"` to the imports.

`finance_cards.go`: `statementItemDTO` gains `RecurrenceID string `json:"recurrence_id,omitempty"`` (set from `it.RecurrenceID` in `statementDTO`); `purchaseDTO` gains `Origin string `json:"origin,omitempty"`` and `RecurrenceID string `json:"recurrence_id,omitempty"`` (`Origin: "recurrence"` when `p.RecurrenceID != ""`).

- [ ] **Step 4: Run to verify it passes**

Run: `cd api && go test ./internal/... -count=1 && DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -race -count=1 ./tests/integration/`
Expected: both `ok` (the full integration suite: the recurrence detail/end tests read the reshaped DTOs).

- [ ] **Step 5: Commit**

```bash
git add api/internal/api/v1/ api/tests/integration/finance_card_recurrences_http_test.go
git commit -m "feat(recurrences): pay a recurrence by card over HTTP, with its statement months and history"
```

---
### Task 9: UI data layer: types, client, keys and the dev mock

**Files:**
- Modify: `ui/src/lib/api/financeTypes.ts` (`Recurrence` :43, `OccurrenceBill` :67, `Occurrence` :58, `NewRecurrence` :176, `RecurrencePatch` :185, `StatementItem` :331, `Purchase` :357)
- Modify: `ui/src/lib/api/finance.ts` (`financeKeys` :86-112, cards section :205)
- Modify: `ui/src/dev/financeMockData.ts` (seed :140-185, recurrences :503-566, cards :602-700)
- Test: `ui/src/dev/financeMockData.test.ts`

**Interfaces:**
- Consumes: the JSON of Tasks 7 and 8.
- Produces (TypeScript):

```ts
export type NeedsAttention = "card_archived" | "category_archived"
// Recurrence gains:
  card_id?: string
  installments?: number
  needs_attention?: NeedsAttention
// account_id stays `string` ("" on a card recurrence).
// Occurrence gains: statement_month?: string
// OccurrenceState gains "posted"; OccurrenceBill: bill_id?: string; purchase_id?: string; card_id?: string; statement_month?: string
// NewRecurrence: account_id?: string; card_id?: string; installments?: number
// RecurrencePatch: card_id?: string; installments?: number
// StatementItem: recurrence_id?: string; Purchase: origin?: "recurrence"; recurrence_id?: string
export interface StatementSummary {
  card_id: string; month: string; status: CardStatementStatus; closing_date: IsoDate; due_date: IsoDate
  total: Cents; bill_id?: string; items_count: number
}
export interface StatementPage {data: StatementSummary[]; has_more: boolean}
export type StatementDirection = "forward" | "backward"
```

```ts
// finance.ts
financeKeys.cardStatements: (mode: Mode, space: Space, cardId: string, openMonth: string) =>
  ["finance", mode, spaceHeader(space), "card-statements", cardId, openMonth] as const
export const listCardStatements = (c: FinanceCtx, cardId: string, from: string, count: number, direction: StatementDirection) =>
  read<StatementPage>(c, `${card(cardId)}/statements`, {from, count, direction})
```

- [ ] **Step 1: Write the failing mock tests**

Append to `ui/src/dev/financeMockData.test.ts` (it already drives `financeMock` with a `call` helper; reuse it, or add this one if it is named differently):

```ts
describe("statement ranges and card recurrences", () => {
  const live = {"X-Billing-Mode": "live", "X-Billing-Space": "personal"}
  const get = (url: string, params?: Record<string, unknown>) => financeMock({method: "get", url: `/v1.0/console/finance${url}`, headers: live, params})
  const post = (url: string, data: unknown) =>
    financeMock({method: "post", url: `/v1.0/console/finance${url}`, headers: {...live, "Idempotency-Key": crypto.randomUUID()}, data: JSON.stringify(data)})

  beforeEach(() => resetFinanceMock())

  it("pages statements forward and backward with has_more at the bounds", () => {
    const cards = get("/cards").data as {data: {id: string; open_month: string}[]}
    const card = cards.data[0]
    const fwd = get(`/cards/${card.id}/statements`, {from: card.open_month, count: 3, direction: "forward"}).data as {data: {month: string; items_count: number}[]; has_more: boolean}
    expect(fwd.data.map(s => s.month)[0]).toBe(card.open_month)
    expect(fwd.data).toHaveLength(3)
    expect(fwd.has_more).toBe(true)
    const back = get(`/cards/${card.id}/statements`, {from: card.open_month, count: 12, direction: "backward"}).data as {data: {month: string}[]; has_more: boolean}
    expect(back.has_more).toBe(false) // stops at the card's first month
    expect(back.data[0].month).toBe(card.open_month)
    expect("items" in (fwd.data[0] as object)).toBe(false)
  })

  it("creates a recurrence paid by card and refuses one with both targets", () => {
    const card = (get("/cards").data as {data: {id: string}[]}).data[0]
    const ok = post("/recurrences", {direction: "payable", amount: 3990, category_id: "mercado", card_id: card.id, expression: {kind: "day_of_month", day: 28}, start: "2026-01-01"})
    expect(ok.status).toBe(201)
    expect((ok.data as {installments: number; account_id: string}).installments).toBe(1)
    const both = post("/recurrences", {direction: "payable", amount: 3990, category_id: "mercado", card_id: card.id, account_id: "conta-corrente", expression: {kind: "day_of_month", day: 28}, start: "2026-01-01"})
    expect(both.status).toBe(422)
  })
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd ui && npx vitest run --maxWorkers=2 src/dev/financeMockData.test.ts; echo $?`
Expected: non-zero exit; the range call answers 404 (no route).

- [ ] **Step 3: Implement**

Types and client as listed under Interfaces. In the mock:

1. Seed (personal live): a card recurrence on `card-nubank` and its posted item in the open month:

```ts
      recurrences: [
        /* r-aluguel as today */,
        {id: "r-streaming", direction: "payable", amount: 3_990, category_id: "assinaturas", account_id: "", card_id: "card-nubank", installments: 1,
          description: "Streaming", expression: {kind: "day_of_month", day: 2}, start: "2026-01-01", business_day_adjust: "none", auto_settle: false, archived: false},
      ],
```

add an active `acct("assinaturas", "Assinaturas", "expense", 0, {dre_group: "operating_expenses"})`, and push one item `{card: "card-nubank", month: ym(0), key: "p-stream#1", purchase_id: "p-stream", description: "Streaming", category_id: "assinaturas", date: iso(ty, tm, 2), number: 1, of: 1, kind: "installment", amount: 3_990, recurrence_id: "r-streaming"}` (with its purchase row) where `[ty, tm] = monthOffset(today, 0)`. Extend `itemOut` to copy `recurrence_id`.

2. `POST /recurrences`: accept `card_id`/`installments`; answer 422 `invalid_recurrence` when both or neither target is set, when `card_id` comes with `direction: "receivable"` or `auto_settle: true`; default `installments: 1` and `account_id: ""` on a card one. `PATCH`: a non-empty `card_id` clears `account_id`/`auto_settle` (installments default 1), a non-empty `account_id` clears `card_id`/`installments`.

3. Range route, inside `if (cardMatch)` before the single-month route:

```ts
    if (sub === "statements" && !arg && method === "get") {
      const from = String(r.params?.from ?? "")
      const count = Number(r.params?.count ?? 3)
      const backward = r.params?.direction === "backward"
      if (!/^\d{4}-\d{2}$/.test(from) || !(count >= 1 && count <= 12)) {
        return problem(422, "/problems/validation", "Validation", "invalid range", "validation_failed")
      }
      const first = c.first_month ?? c.open_month
      const lastItem = s.items.filter(i => i.card === c.id).map(i => i.month).sort().at(-1) ?? first
      const last = [ymAdd(c.open_month, 12), lastItem].sort().at(-1)!
      const months: string[] = []
      let m = backward ? (from > last ? last : from) : (from < first ? first : from)
      while (months.length < count && m >= first && m <= last) { months.push(m); m = ymAdd(m, backward ? -1 : 1) }
      const has_more = backward ? (months.at(-1) ?? first) > first : (months.at(-1) ?? last) < last
      const data = months.map(month => { const {items, ...rest} = statementOf(c, month); return {...rest, items_count: items.length} })
      return ok({data, has_more})
    }
```

(`first_month` is a mock-only field on the seeded card, set to `ym(-2)` so the seed has history; a card created in the session has none and starts at its open month.)

4. `/recurrences/:id/occurrences` for a card recurrence: `history` lists items carrying its `recurrence_id` as `{nominal: date, due: date, purchase_id, card_id, statement_month: month, amount, state: "posted"}`; `upcoming` entries carry `statement_month` computed like the purchase route's `first` month.

- [ ] **Step 4: Run to verify it passes**

Run: `cd ui && npx vitest run --maxWorkers=2 src/dev/financeMockData.test.ts; echo $?`
Expected: exit `0`. Then `npx tsc --noEmit -p .; echo $?` → `0` (the `bill_id?` change surfaces every reader; fix each by falling back to `purchase_id`).

- [ ] **Step 5: Commit**

```bash
git add ui/src/lib/api/ ui/src/dev/
git commit -m "feat(finance-ui): types, client and mock for card recurrences and statement ranges"
```

---

### Task 10: Timeline paging logic (pure)

**Files:**
- Create: `ui/src/lib/finance/statementTimeline.ts`
- Test: `ui/src/lib/finance/statementTimeline.test.ts`
- Modify: `ui/src/components/finance/CardsView.tsx:57-61` (delete `shiftMonth`, import it from here)

**Interfaces:**
- Consumes: `StatementPage`, `StatementSummary`, `StatementDirection` (Task 9).
- Produces:

```ts
export const PAGE_SIZE = 3
export const AROUND = 3 // statements each side of the open one on opening
export interface PageParam {from: string; direction: StatementDirection; count: number}
export interface TimelinePage extends StatementPage {param: PageParam}
export function shiftMonth(ym: string, n: number): string
export function initialParam(open: string): PageParam            // forward from open-3, 7 statements
export function nextParam(last: TimelinePage): PageParam | undefined
export function previousParam(first: TimelinePage): PageParam | undefined
export function flatten(pages: TimelinePage[]): StatementSummary[] // ascending, unique by month
export function windowAt(months: string[], start: string, size: number): {from: number; to: number}
export function stepWindow(months: string[], start: string, size: number, dir: -1 | 1): string
export function keepVisible(months: string[], start: string, size: number, selected: string): string
export function edges(months: string[], start: string, size: number): {before: number; after: number}
```

- [ ] **Step 1: Write the failing tests**

Create `ui/src/lib/finance/statementTimeline.test.ts`:

```ts
import {describe, expect, it} from "vitest"

import type {StatementSummary} from "@/lib/api/financeTypes"
import {edges, flatten, initialParam, keepVisible, nextParam, previousParam, shiftMonth, stepWindow, type TimelinePage, windowAt} from "@/lib/finance/statementTimeline"

const s = (month: string): StatementSummary => ({card_id: "c", month, status: "future", closing_date: `${month}-03`, due_date: `${month}-10`, total: 0, items_count: 0})
const page = (direction: "forward" | "backward", months: string[], has_more: boolean, from = months[0]): TimelinePage =>
  ({data: months.map(s), has_more, param: {from, direction, count: months.length}})

describe("statement timeline paging", () => {
  it("shifts months across years", () => {
    expect(shiftMonth("2026-01", -1)).toBe("2025-12")
    expect(shiftMonth("2026-11", 3)).toBe("2027-02")
  })

  it("opens with one request for 3 before, the open one and 3 after", () => {
    expect(initialParam("2026-03")).toEqual({from: "2025-12", direction: "forward", count: 7})
  })

  it("asks beyond the loaded edge in each direction, and stops when there is no more", () => {
    const first = page("forward", ["2025-12", "2026-01", "2026-02", "2026-03", "2026-04", "2026-05", "2026-06"], true)
    expect(nextParam(first)).toEqual({from: "2026-07", direction: "forward", count: 3})
    expect(previousParam(first)).toEqual({from: "2025-11", direction: "backward", count: 3})
    const older = page("backward", ["2025-11", "2025-10", "2025-09"], true, "2025-11")
    expect(previousParam(older)).toEqual({from: "2025-08", direction: "backward", count: 3})
    expect(previousParam(page("backward", ["2025-11"], false))).toBeUndefined()
    expect(previousParam(page("backward", [], false))).toBeUndefined()
    expect(nextParam(page("forward", ["2027-03"], false))).toBeUndefined()
  })

  it("flattens pages in month order without duplicates", () => {
    const pages = [page("backward", ["2025-11", "2025-10"], true), page("forward", ["2025-12", "2026-01"], true), page("forward", ["2026-01", "2026-02"], false)]
    expect(flatten(pages).map(x => x.month)).toEqual(["2025-10", "2025-11", "2025-12", "2026-01", "2026-02"])
  })

  it("windows, steps a page at a time and keeps the selection visible", () => {
    const months = ["2025-12", "2026-01", "2026-02", "2026-03", "2026-04", "2026-05", "2026-06", "2026-07", "2026-08", "2026-09"]
    expect(windowAt(months, "2025-12", 7)).toEqual({from: 0, to: 7})
    expect(windowAt(months, "2025-01", 7)).toEqual({from: 0, to: 7}) // a start before the loaded months clamps
    expect(stepWindow(months, "2025-12", 7, 1)).toBe("2026-03")
    expect(stepWindow(months, "2026-03", 7, 1)).toBe("2026-03") // already at the last full window
    expect(stepWindow(months, "2026-03", 7, -1)).toBe("2025-12")
    expect(keepVisible(months, "2025-12", 7, "2026-07")).toBe("2026-01")
    expect(keepVisible(months, "2026-03", 7, "2026-01")).toBe("2026-01")
    expect(edges(months, "2025-12", 7)).toEqual({before: 0, after: 3})
    expect(edges(months, "2026-03", 7)).toEqual({before: 3, after: 0})
  })
})
```

- [ ] **Step 2: Run to verify it fails**

Run: `cd ui && npx vitest run --maxWorkers=2 src/lib/finance/statementTimeline.test.ts; echo $?`
Expected: non-zero (module not found).

- [ ] **Step 3: Implement**

Create `ui/src/lib/finance/statementTimeline.ts`:

```ts
import type {StatementDirection, StatementPage, StatementSummary} from "@/lib/api/financeTypes"

/** Statements fetched per step beyond the loaded edge (spec D5). */
export const PAGE_SIZE = 3
/** Statements each side of the open one on opening. */
export const AROUND = 3

export interface PageParam {from: string; direction: StatementDirection; count: number}
/** A page as the API answered it, with the request that asked for it. */
export interface TimelinePage extends StatementPage {param: PageParam}

export function shiftMonth(ym: string, n: number): string {
  const [y, m] = ym.split("-").map(Number)
  const i = y * 12 + (m - 1) + n
  return `${Math.floor(i / 12)}-${String((i % 12) + 1).padStart(2, "0")}`
}

/** One request opens the timeline: forward from 3 before the open month, 7 statements.
 *  The API starts a forward page at the card's first month when `from` is earlier. */
export const initialParam = (open: string): PageParam => ({from: shiftMonth(open, -AROUND), direction: "forward", count: 2 * AROUND + 1})

/** Next pages are always forward, after the last loaded month. */
export function nextParam(last: TimelinePage): PageParam | undefined {
  const end = last.data.at(-1)
  if (last.param.direction !== "forward" || !last.has_more || !end) return undefined
  return {from: shiftMonth(end.month, 1), direction: "forward", count: PAGE_SIZE}
}

/** Previous pages are always backward, before the first loaded month. The opening
 *  page is forward and says nothing about the past: ask once, the answer's
 *  has_more decides after that. A backward page lists newest first. */
export function previousParam(first: TimelinePage): PageParam | undefined {
  if (first.param.direction === "forward") {
    return {from: shiftMonth(first.data[0]?.month ?? first.param.from, -1), direction: "backward", count: PAGE_SIZE}
  }
  const earliest = first.data.at(-1)
  return first.has_more && earliest ? {from: shiftMonth(earliest.month, -1), direction: "backward", count: PAGE_SIZE} : undefined
}

/** Every loaded statement, oldest first, each month once. */
export function flatten(pages: TimelinePage[]): StatementSummary[] {
  const byMonth = new Map<string, StatementSummary>()
  for (const p of pages) for (const s of p.data) byMonth.set(s.month, s)
  return [...byMonth.values()].sort((a, b) => a.month.localeCompare(b.month))
}

function startIndex(months: string[], start: string, size: number): number {
  const i = months.findIndex(m => m >= start)
  const last = Math.max(0, months.length - size)
  return i < 0 ? last : Math.min(i, last)
}

/** The visible slice [from, to) for a window starting at month `start`. Anchoring
 *  by month, not index, keeps the same statements on screen when older ones load. */
export function windowAt(months: string[], start: string, size: number): {from: number; to: number} {
  const from = startIndex(months, start, size)
  return {from, to: Math.min(months.length, from + size)}
}

/** The window's start a page earlier or later, within what is loaded. */
export function stepWindow(months: string[], start: string, size: number, dir: -1 | 1): string {
  if (months.length === 0) return start
  const i = startIndex(months, start, size) + dir * PAGE_SIZE
  return months[Math.max(0, Math.min(i, Math.max(0, months.length - size)))]
}

/** The window's start moved just enough for `selected` to be visible. */
export function keepVisible(months: string[], start: string, size: number, selected: string): string {
  const {from, to} = windowAt(months, start, size)
  const i = months.indexOf(selected)
  if (i < 0 || (i >= from && i < to)) return months[from] ?? start
  return i < from ? months[i] : months[Math.max(0, i - size + 1)]
}

/** How many loaded statements lie outside the window on each side. */
export function edges(months: string[], start: string, size: number): {before: number; after: number} {
  const {from, to} = windowAt(months, start, size)
  return {before: from, after: months.length - to}
}
```

In `CardsView.tsx`, delete the local `shiftMonth` and `import {shiftMonth} from "@/lib/finance/statementTimeline"` (it is still used until Task 12 replaces the nav).

- [ ] **Step 4: Run to verify it passes**

Run: `cd ui && npx vitest run --maxWorkers=2 src/lib/finance/statementTimeline.test.ts src/components/finance/CardsView.test.tsx; echo $?`
Expected: exit `0`.

- [ ] **Step 5: Commit**

```bash
git add ui/src/lib/finance/statementTimeline.ts ui/src/lib/finance/statementTimeline.test.ts ui/src/components/finance/CardsView.tsx
git commit -m "feat(finance-ui): statement timeline paging and window logic"
```

---
### Task 11: Recurrences screen: pay with a card, needs attention, card history; the archive warning

Use the `impeccable` skill for this task (shape the form change, the attention state and the history entries; then polish), following `ui/DESIGN.md`. Phone layout is its own: the form stacks one column, the attention line sits under the meta, the history entries keep their timeline.

**Files:**
- Modify: `ui/src/components/finance/RecurrencesView.tsx` (row :112-160, detail :166-210, panel :276-483)
- Modify: `ui/src/components/finance/PurchasePanel.tsx:21` (export the installment options)
- Modify: `ui/src/app/(finance)/finance/recurrences/page.tsx`
- Modify: `ui/src/components/finance/AccountsView.tsx` (archive confirmation :150-157)
- Modify: `ui/src/locales/pt-BR/bills.json`, `ui/src/locales/en/bills.json`, `ui/src/locales/pt-BR/finance.json`, `ui/src/locales/en/finance.json`
- Test: `ui/src/components/finance/RecurrencesView.test.tsx`, `ui/src/components/finance/AccountsView.test.tsx`

**Interfaces:**
- Consumes: Task 9 types and client (`listCards`, `card_id`, `installments`, `needs_attention`, `OccurrenceBill.purchase_id/statement_month`, `Occurrence.statement_month`).
- Produces: `RecurrencesView({focus?: string})` (opens that recurrence's detail and scrolls to it); `export const installmentOptions` in `PurchasePanel.tsx`; route `/finance/recurrences?recurrence=<id>` (used by Task 12's marker).

Copy (pt-BR; en in parentheses). No em dash anywhere.

```json
// bills.json → "rec"
"payWithCard": "Cartão", "payWithAccount": "Conta",            // (Card, Account) option group labels
"installments": "Parcelas",                                     // (Installments)
"cardNote": "Cada ocorrência entra como compra no cartão, no próprio dia.", // (Each occurrence is posted as a card purchase on its own day.)
"attention": {
  "card_archived": "Cartão arquivado; escolha outro cartão ou uma conta",     // (Card archived; choose another card or an account)
  "category_archived": "Categoria arquivada; escolha outra categoria",       // (Category archived; choose another category)
  "fix": "Corrigir"                                                          // (Fix)
},
"state": { "posted": "Na fatura de {{month}}" },                 // (On the {{month}} statement)
"onStatement": "fatura de {{month}}"                             // (statement of {{month}})
// finance.json → "accounts"
"archiveCardWarning_one": "{{count}} recorrência é paga com este cartão e vai parar até você escolher outro cartão ou conta.",
"archiveCardWarning_other": "{{count}} recorrências são pagas com este cartão e vão parar até você escolher outro cartão ou conta."
// (1 recurrence is paid with this card and will stop until you choose another card or account. / {{count}} recurrences …)
```

- [ ] **Step 1: Write the failing tests**

Append to `ui/src/components/finance/RecurrencesView.test.tsx` (it already has `serve…` helpers built on `vi.spyOn(finance, …)`; add `listCards` to them returning `[CARD]` where `CARD` is `{id: "visa", name: "Visa", closing_day: 25, due_day: 5, paying_account_id: "cc", open_month: "2026-03", balance: 0, archived: false, brand: "visa", last4: "4242"}`):

```tsx
describe("paid by card", () => {
  it("offers active cards beside the accounts for an expense, and installments once a card is chosen", async () => {
    serveWith({cards: [CARD]})
    const create = vi.spyOn(finance, "createRecurrence").mockResolvedValue({} as Recurrence)
    renderWithQuery(<RecurrencesView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Nova recorrência"}))
    const drawer = await screen.findByRole("dialog")
    await userEvent.type(within(drawer).getByLabelText("Valor"), "39,90")
    await pick("Categoria", "Mercado", drawer)
    expect(await optionsOf("Pagar com", drawer)).toEqual(["Conta corrente", "Visa •••• 4242"])
    await pick("Pagar com", "Visa •••• 4242", drawer)
    expect(within(drawer).queryByRole("switch")).not.toBeInTheDocument() // no auto-settle on a card
    expect(within(drawer).getByText("Cada ocorrência entra como compra no cartão, no próprio dia.")).toBeInTheDocument()
    await pick("Parcelas", "3x", drawer)
    await userEvent.click(within(drawer).getByRole("button", {name: "Criar recorrência"}))
    await waitFor(() => expect(create).toHaveBeenCalledWith(expect.anything(),
      expect.objectContaining({card_id: "visa", installments: 3, account_id: undefined, auto_settle: undefined}), expect.any(String)))
  })

  it("offers no card for an income", async () => {
    serveWith({cards: [CARD]})
    renderWithQuery(<RecurrencesView/>)
    await userEvent.click(await screen.findByRole("button", {name: "Nova recorrência"}))
    const drawer = await screen.findByRole("dialog")
    await userEvent.click(within(drawer).getByRole("button", {name: "A receber"}))
    expect(await optionsOf("Receber em", drawer)).toEqual(["Conta corrente"])
  })

  it("says when a recurrence needs attention and opens its edit from there", async () => {
    serveWith({cards: [CARD], recurrences: [{...CARD_REC, needs_attention: "card_archived"}]})
    renderWithQuery(<RecurrencesView/>)
    const row = (await screen.findByText("Streaming")).closest("li") as HTMLElement
    expect(within(row).getByText("Cartão arquivado; escolha outro cartão ou uma conta")).toBeInTheDocument()
    await userEvent.click(within(row).getByRole("button", {name: "Corrigir"}))
    expect(await screen.findByRole("dialog", {name: "Editar recorrência"})).toBeInTheDocument()
  })

  it("shows posted occurrences with their statement month, and where upcoming ones will land", async () => {
    serveWith({cards: [CARD], recurrences: [CARD_REC]})
    vi.spyOn(finance, "getRecurrenceOccurrences").mockResolvedValue({
      history: [{nominal: "2026-02-28", due: "2026-02-28", purchase_id: "p1", card_id: "visa", statement_month: "2026-03", amount: 3990, state: "posted"}],
      upcoming: [{nominal: "2026-03-28", due: "2026-03-28", statement_month: "2026-04"}],
    })
    renderWithQuery(<RecurrencesView/>)
    const row = (await screen.findByText("Streaming")).closest("li") as HTMLElement
    await userEvent.click(within(row).getByRole("button", {name: /Ver/}))
    expect(await within(row).findByText("Na fatura de Mar/26")).toBeInTheDocument()
    expect(within(row).getByText(/fatura de Abr\/26/)).toBeInTheDocument()
  })

  it("opens the recurrence named in the address", async () => {
    serveWith({cards: [CARD], recurrences: [CARD_REC]})
    vi.spyOn(finance, "getRecurrenceOccurrences").mockResolvedValue({history: [], upcoming: []})
    renderWithQuery(<RecurrencesView focus="r-stream"/>)
    expect(await screen.findByRole("region", {name: /Streaming/})).toBeInTheDocument()
  })

  it("has no em dash in what it shows", async () => {
    serveWith({cards: [CARD], recurrences: [{...CARD_REC, needs_attention: "category_archived"}]})
    renderWithQuery(<RecurrencesView/>)
    await screen.findByText("Streaming")
    expect(document.body.textContent).not.toContain("—")
  })
})
```

with `const CARD_REC: Recurrence = {id: "r-stream", direction: "payable", amount: 3990, category_id: "food", account_id: "", card_id: "visa", installments: 1, description: "Streaming", expression: {kind: "day_of_month", day: 28}, start: "2026-01-01", business_day_adjust: "none", auto_settle: false, archived: false}` and `serveWith({cards, recurrences})` extending the file's existing serve helper (mock `listCards`, `listRecurrences`, `listAccounts`, `getFinanceSpaces` with all verbs).

Append to `ui/src/components/finance/AccountsView.test.tsx`:

```tsx
it("warns, before archiving a card, how many recurrences it pays", async () => {
  serveAccounts({accounts: [...ACCOUNTS, {id: "visa", name: "Visa", class: "liability", system: false, archived: false, balance: 0}],
    cards: [{id: "visa", name: "Visa", closing_day: 3, due_day: 10, paying_account_id: "cc", open_month: "2026-03", balance: 0, archived: false}]})
  vi.spyOn(finance, "listRecurrences").mockResolvedValue({data: [
    {id: "a", direction: "payable", amount: 1, category_id: "food", account_id: "", card_id: "visa", installments: 1, expression: {kind: "day_of_month", day: 1}, start: "2026-01-01", business_day_adjust: "none", auto_settle: false, archived: false},
    {id: "b", direction: "payable", amount: 1, category_id: "food", account_id: "", card_id: "visa", installments: 1, expression: {kind: "day_of_month", day: 2}, start: "2026-01-01", business_day_adjust: "none", auto_settle: false, archived: true},
  ], has_more: false})
  renderWithQuery(<AccountsView/>)
  const row = (await screen.findByText("Visa")).closest("li") as HTMLElement
  await userEvent.click(within(row).getByRole("button", {name: "Arquivar"}))
  expect(await within(row).findByText(/1 recorrência é paga com este cartão/)).toBeInTheDocument()
})
```

(`serveAccounts` is the file's existing serve helper; extend it with `cards` if it does not mock `listCards` yet; the archived recurrence is not counted.)

- [ ] **Step 2: Run to verify they fail**

Run: `cd ui && npx vitest run --maxWorkers=2 src/components/finance/RecurrencesView.test.tsx src/components/finance/AccountsView.test.tsx; echo $?`
Expected: non-zero; `optionsOf("Pagar com")` lists only `["Conta corrente"]`.

- [ ] **Step 3: Implement**

`PurchasePanel.tsx`: rename `countOptions` to `export const installmentOptions` (update its one use).

`RecurrencesView.tsx`:

1. `RecurrencesView({focus}: {focus?: string})`: add `const cards = useQuery({queryKey: financeKeys.cards(ctx.mode, ctx.space), queryFn: () => listCards(ctx)})`; pass `cards={cards.data?.data ?? []}` to `RecurrencePanel` and `card={cards.data?.data.find(c => c.id === r.card_id)}`, `focused={r.id === focus}` to `RecurrenceRow`.

2. `RecurrenceRow`: the meta shows the target's name (`account ?? (card ? cardLabel(card) : "")`, where `cardLabel` moves to `CardBrandMark.tsx` as an export so both screens share it); `const [open, setOpen] = useState(focused)` and, when `focused`, a ref on the row's root with `useEffect(() => { if (focused) ref.current?.scrollIntoView?.({block: "center"}) }, [focused])`. Under the meta, when `rec.needs_attention`:

```tsx
      {rec.needs_attention && (
        <p role="status" className="mt-1 flex flex-wrap items-center gap-2 text-sm text-foreground">
          <Badge tone="attention"><AlertCircle aria-hidden/>{t(`bills.rec.attention.${rec.needs_attention}`)}</Badge>
          {can("finance.write") && <Button size="sm" variant="ghost" onClick={onEdit}>{t("bills.rec.attention.fix")}</Button>}
        </p>
      )}
```

(pass it through `LedgerRow`'s children slot, which already renders under the row.)

3. `RecurrenceDetail`: `made` keys on `o.bill_id ?? o.purchase_id ?? o.nominal`; for `o.state === "posted"` the state text is `t("bills.rec.state.posted", {month: monthShort(o.statement_month!)})` and there is no action; upcoming entries of a card recurrence add `t("bills.rec.onStatement", {month: monthShort(o.statement_month)})` to their state text when `statement_month` is present. `past` keeps `posted` entries (they are history).

4. `RecurrencePanel`: new state `const [target, setTarget] = useState(editing?.card_id || editing?.account_id || "")` replacing `account`, and `const [installments, setInstallments] = useState(editing?.installments ?? 1)`. Options:

```tsx
  const payCards = direction === "payable" ? cards.filter(c => !c.archived) : []
  const isCard = payCards.some(c => c.id === target)
  const targetOptions = [
    ...assets.map(a => ({value: a.id, label: accountName(a)})),
    ...payCards.map(c => ({value: c.id, label: cardLabel(c), icon: <CardBrandMark brand={c.brand ?? "other"}/>})),
  ]
```

The "Pagar com" `Select` uses `targetOptions` (field error key stays `account_id`; also `fe` learns `card_id` and `installments`, `useFieldErrors([... "card_id", "installments"])`). Switching direction to receivable clears a card target (`if (isCard) setTarget("")`). When `isCard`:

```tsx
          {isCard && (
            <>
              <Field label={t("bills.rec.installments")} htmlFor="rc-inst" error={fe.of("installments")}
                hint={amount ? installmentsPreview(amount, installments) : undefined}>
                <Select {...selectCopy()} id="rc-inst" value={String(installments)} onValueChange={v => { setInstallments(Number(v)); fe.clear("installments") }} options={installmentOptions()}/>
              </Field>
              <p className="text-sm text-muted-foreground sm:col-span-2">{t("bills.rec.cardNote")}</p>
            </>
          )}
```

The auto-settle switch renders only when `!isCard` (`showAuto && !isCard`). `ready` uses `target !== ""`. Create body:

```ts
        ...(isCard ? {card_id: target, installments} : {account_id: target, auto_settle: autoSettle || undefined}),
```

Patch body: `if (target !== (editing.card_id || editing.account_id)) { if (isCard) body.card_id = target; else body.account_id = target }`, `if (isCard && installments !== (editing.installments ?? 1)) body.installments = installments`, and `auto_settle` only when `!isCard`.

5. `app/(finance)/finance/recurrences/page.tsx`: wrap in `<Suspense>` like the cards page and pass `focus={useSearchParams().get("recurrence") ?? undefined}`.

`AccountsView.tsx`: the account row's archive confirmation, for a `liability` account that is a card, reads the recurrences (`useQuery({queryKey: financeKeys.recurrences(...), queryFn: () => listRecurrences(ctx), enabled: confirming && account.class === "liability"})`) and, when `n = data.filter(r => r.card_id === account.id && !r.archived).length` is above 0, shows `t("finance.accounts.archiveCardWarning", {count: n})` above the buttons (class `text-foreground`).

Add the copy above to both locales.

- [ ] **Step 4: Run to verify they pass**

Run: `cd ui && npx vitest run --maxWorkers=2 src/components/finance/RecurrencesView.test.tsx src/components/finance/AccountsView.test.tsx src/components/finance/PurchasePanel.test.tsx; echo $?`
Expected: exit `0`. Then check the phone layout in the mock (`npm run dev:mock`, 375px wide, `/finance/recurrences`): the target select, installments and note stack in one column; the attention badge wraps under the meta; nothing overflows horizontally.

- [ ] **Step 5: Commit**

```bash
git add ui/src/
git commit -m "feat(finance-ui): pay a recurrence by card, show what needs attention, warn before archiving a card in use"
```

---

### Task 12: Cards screen: the statement timeline

Use the `impeccable` skill for this task (`/impeccable` shape, then craft and polish against `ui/DESIGN.md`: hairline depth, one raised thing at most, status colour on the badge only, tabular figures for money, `motion-reduce` alternatives). The phone layout is its own: one summary card per screen in a scroll-snap strip, swiped; previous/next buttons stay as the non-gesture way (DESIGN.md: "the gesture is never the only way").

**Files:**
- Create: `ui/src/components/finance/StatementTimeline.tsx`
- Create: `ui/src/components/finance/statementStatus.ts` (moved out of `CardsView.tsx`)
- Modify: `ui/src/components/finance/CardsView.tsx` (`CardsView` :71-154, `StatementBlock` :176-227, `ItemRow` :254-287)
- Modify: `ui/src/locales/pt-BR/finance.json`, `ui/src/locales/en/finance.json` (`cards`)
- Test: `ui/src/components/finance/CardsView.test.tsx`

**Interfaces:**
- Consumes: Task 9 (`listCardStatements`, `financeKeys.cardStatements`, `StatementSummary`), Task 10 (all of `statementTimeline.ts`), Task 11 (`cardLabel` in `CardBrandMark.tsx`, `/finance/recurrences?recurrence=`), existing `statementState`, `STATUS`, `monthShort`, `shortDate`, `signedMoney`, `useMediaQuery`, `NARROW`.
- Produces: `StatementTimeline({card, selected, onSelect}: {card: Card; selected: string; onSelect: (month: string) => void})`.

Copy (`finance.json` → `cards`; en in parentheses):

```json
"timeline": "Faturas do cartão",                  // (Card statements)
"earlier": "Faturas anteriores",                  // (Earlier statements)
"later": "Próximas faturas",                      // (Later statements)
"itemsCount_zero": "Sem compras", "itemsCount_one": "{{count}} compra", "itemsCount_other": "{{count}} compras", // (No purchases / 1 purchase / {{count}} purchases)
"summaryLabel": "Fatura de {{month}} • {{state}} • {{total}}", // (Statement of {{month}} • {{state}} • {{total}})
"recurring": "recorrente",                        // (recurring)
"openRecurrence": "Abrir a recorrência de {{name}}" // (Open the recurrence of {{name}})
```

The existing `cards.prev` / `cards.next` keys are replaced by `earlier` / `later` (delete them from both locales). The overdue badge keeps its existing label "Vencida": the spec's "atrasada" names the same state, and the screen already says "Vencida" everywhere.

- [ ] **Step 1: Write the failing tests**

In `ui/src/components/finance/CardsView.test.tsx`, replace `serve` so the range endpoint answers from a small model (open month 2026-03, first month 2025-06, last item 2027-06) and records every request:

```tsx
const FIRST = "2025-06", LAST = "2027-06"
const summary = (month: string): StatementSummary => ({
  card_id: "visa", month, status: month < "2026-03" ? "paid" : month === "2026-03" ? "open" : "future",
  closing_date: `${month}-03`, due_date: `${month}-10`, total: 10000, items_count: 1,
})

function serve(verbs: Verb[], cards: Card[] = [CARD]) {
  vi.spyOn(finance, "getFinanceSpaces").mockResolvedValue({spaces: [{selector: "personal", kind: "personal_default", display_name: "Pessoal", verbs, manage_people: false}], organizations_unavailable: false})
  vi.spyOn(finance, "listAccounts").mockResolvedValue({data: ACCOUNTS, has_more: false})
  vi.spyOn(finance, "listCards").mockResolvedValue({data: cards, has_more: false})
  vi.spyOn(finance, "listPurchases").mockResolvedValue({data: [PURCHASE], has_more: false})
  const range = vi.spyOn(finance, "listCardStatements").mockImplementation(async (_c, _id, from, count, direction) => {
    const out: StatementSummary[] = []
    let m = direction === "forward" && from < FIRST ? FIRST : from
    while (out.length < count && m >= FIRST && m <= LAST) { out.push(summary(m)); m = shiftMonth(m, direction === "forward" ? 1 : -1) }
    const edge = out.at(-1)?.month
    return {data: out, has_more: direction === "forward" ? !!edge && edge < LAST : !!edge && edge > FIRST}
  })
  const items = vi.spyOn(finance, "getCardStatement").mockImplementation(async (_c, _id, month) =>
    statement(month, month === "2026-03" ? "open" : "future", month === "2026-03" ? 1 : 2))
  return {range, items}
}

/** Months each range request covered, to prove nothing loaded is asked again. */
function covered(range: ReturnType<typeof serve>["range"]): string[] {
  return range.mock.calls.flatMap(([, , from, count, direction]) =>
    Array.from({length: count}, (_, i) => shiftMonth(from, direction === "forward" ? i : -i)).filter(m => m >= FIRST && m <= LAST))
}

const inWindow = () => screen.getAllByRole("button", {name: /^Fatura de /}).filter(b => b.closest("[data-in-window]")).map(b => b.dataset.month)
```

(import `shiftMonth` from `@/lib/finance/statementTimeline` and `StatementSummary` from the types; existing tests that used `serve(...)` as a spy on `getCardStatement` now use `serve(...).items`.)

Replace the two navigation tests with:

```tsx
  // Spec § 6.6.
  it("opens on the open statement with three before and three after, selected", async () => {
    serve(ALL)
    renderWithQuery(<CardsView/>)
    await waitFor(() => expect(inWindow()).toEqual(["2025-12", "2026-01", "2026-02", "2026-03", "2026-04", "2026-05", "2026-06"]))
    const open = screen.getByRole("button", {name: /^Fatura de Mar\/26/})
    expect(open).toHaveAttribute("aria-pressed", "true")
    expect(within(open).getByText("Aberta")).toBeInTheDocument()
    expect(await screen.findByText("Geladeira")).toBeInTheDocument() // the open statement's items, below
  })

  it("pages beyond the loaded edge in both directions without asking for a loaded month again", async () => {
    const {range} = serve(ALL)
    renderWithQuery(<CardsView/>)
    await waitFor(() => expect(inWindow()).toContain("2026-03"))
    for (let i = 0; i < 3; i++) await userEvent.click(screen.getByRole("button", {name: "Próximas faturas"}))
    await waitFor(() => expect(inWindow()).toContain("2026-12"))
    for (let i = 0; i < 6; i++) await userEvent.click(screen.getByRole("button", {name: "Faturas anteriores"}))
    await waitFor(() => expect(inWindow()[0] <= "2025-09").toBe(true))
    const months = covered(range)
    expect(new Set(months).size).toBe(months.length) // every month asked once
  })

  it("stops at the card's first statement", async () => {
    serve(ALL)
    renderWithQuery(<CardsView/>)
    const earlier = await screen.findByRole("button", {name: "Faturas anteriores"})
    for (let i = 0; i < 6; i++) if (!(earlier as HTMLButtonElement).disabled) await userEvent.click(earlier)
    await waitFor(() => expect(inWindow()[0]).toBe(FIRST))
    await waitFor(() => expect(earlier).toBeDisabled())
  })

  it("moves the selection with the arrow keys and loads that statement's items", async () => {
    const {items} = serve(ALL)
    renderWithQuery(<CardsView/>)
    const open = await screen.findByRole("button", {name: /^Fatura de Mar\/26/})
    open.focus()
    await userEvent.keyboard("{ArrowRight}")
    expect(screen.getByRole("button", {name: /^Fatura de Abr\/26/})).toHaveAttribute("aria-pressed", "true")
    await waitFor(() => expect(items).toHaveBeenCalledWith(expect.anything(), "visa", "2026-04"))
    expect(await screen.findByText(/2\/3/)).toBeInTheDocument()
  })

  it("marks an item a recurrence posted, linking to it", async () => {
    serve(ALL)
    vi.spyOn(finance, "getCardStatement").mockResolvedValue({...statement("2026-03", "open", 1),
      items: [{purchase_id: "p9", description: "Streaming", category_id: "food", date: "2026-03-02", number: 1, of: 1, kind: "installment", amount: 3990, recurrence_id: "r-stream"}]})
    renderWithQuery(<CardsView/>)
    const link = await screen.findByRole("link", {name: "Abrir a recorrência de Streaming"})
    expect(link).toHaveAttribute("href", "/finance/recurrences?recurrence=r-stream")
    expect(link).toHaveTextContent("recorrente")
  })

  it("has no em dash in what it shows", async () => {
    serve(ALL)
    renderWithQuery(<CardsView/>)
    await screen.findByText("Geladeira")
    expect(document.body.textContent).not.toContain("—")
  })

  describe("on a phone", () => {
    beforeEach(() => {
      vi.stubGlobal("matchMedia", (query: string) => ({matches: query.includes("max-width"), media: query, addEventListener: () => undefined, removeEventListener: () => undefined}))
    })
    afterEach(() => vi.unstubAllGlobals())

    const strip = () => screen.getByRole("list", {name: "Faturas do cartão"})
    // jsdom does no layout: give the strip a width and a scroll position it keeps.
    const sized = (el: HTMLElement, width: number) => {
      Object.defineProperty(el, "clientWidth", {configurable: true, value: width})
      Object.defineProperty(el, "scrollLeft", {configurable: true, writable: true, value: 0})
    }

    it("shows one statement at a time and selects the one swiped to", async () => {
      const {items} = serve(ALL)
      renderWithQuery(<CardsView/>)
      await screen.findByRole("button", {name: /^Fatura de Mar\/26/})
      const el = strip()
      sized(el, 300)
      const index = within(el).getAllByRole("button").findIndex(b => b.dataset.month === "2026-04")
      el.scrollLeft = index * 300
      el.dispatchEvent(new Event("scroll"))
      await waitFor(() => expect(screen.getByRole("button", {name: /^Fatura de Abr\/26/})).toHaveAttribute("aria-pressed", "true"))
      await waitFor(() => expect(items).toHaveBeenCalledWith(expect.anything(), "visa", "2026-04"))
    })

    // Review Focus 5.
    it("keeps the card on screen when older statements load on a phone", async () => {
      serve(ALL)
      renderWithQuery(<CardsView/>)
      await screen.findByRole("button", {name: /^Fatura de Mar\/26/})
      const el = strip()
      sized(el, 300)
      el.scrollLeft = 0 // the first loaded statement: the edge, so older ones load
      el.dispatchEvent(new Event("scroll"))
      const shown = within(el).getAllByRole("button")[0].dataset.month!
      await waitFor(() => expect(within(el).getAllByRole("button")[0].dataset.month).not.toBe(shown))
      const index = within(el).getAllByRole("button").findIndex(b => b.dataset.month === shown)
      expect(el.scrollLeft).toBe(index * 300)
    })
  })
```

- [ ] **Step 2: Run to verify they fail**

Run: `cd ui && npx vitest run --maxWorkers=2 src/components/finance/CardsView.test.tsx; echo $?`
Expected: non-zero; no button named `Fatura de …`.

- [ ] **Step 3: Implement**

Create `ui/src/components/finance/StatementTimeline.tsx`:

```tsx
"use client"

import {Badge, Button, Skeleton} from "@aoctech/ui"
import {useInfiniteQuery} from "@tanstack/react-query"
import {ChevronLeft, ChevronRight} from "lucide-react"
import {useEffect, useLayoutEffect, useRef, useState} from "react"
import {useTranslation} from "react-i18next"

import {STATUS, statementState} from "@/components/finance/statementStatus"
import {ErrorBlock} from "@/components/portal/ErrorBlock"
import {financeKeys, listCardStatements} from "@/lib/api/finance"
import type {Card, StatementSummary} from "@/lib/api/financeTypes"
import {
  edges, flatten, initialParam, keepVisible, nextParam, type PageParam, previousParam, shiftMonth, stepWindow, type TimelinePage, windowAt,
} from "@/lib/finance/statementTimeline"
import {monthShort, todayIso} from "@/lib/finance/today"
import {useFinanceCtx} from "@/lib/finance/useFinanceSpaces"
import {shortDate, signedMoney} from "@/lib/format"
import {NARROW, useMediaQuery} from "@/lib/hooks/useMediaQuery"

/** Under lg the row shows five statements; from lg, seven (spec D5: 3 + open + 3). */
const MID = "(max-width: 63.999rem)"

/**
 * The card's statements as a row of summaries (spec § 4.2): on opening, the open
 * one selected with three before and three after; previous/next step a page and
 * load the next page beyond what is loaded, prefetching as the window reaches an
 * edge. On a phone, one summary per screen in a strip that is swiped.
 */
export function StatementTimeline({card, selected, onSelect}: {card: Card; selected: string; onSelect: (month: string) => void}) {
  const {t} = useTranslation()
  const ctx = useFinanceCtx()
  const phone = useMediaQuery(NARROW)
  const mid = useMediaQuery(MID)
  const size = phone ? 1 : mid ? 5 : 7
  const q = useInfiniteQuery({
    queryKey: financeKeys.cardStatements(ctx.mode, ctx.space, card.id, card.open_month),
    queryFn: async ({pageParam}): Promise<TimelinePage> =>
      ({...(await listCardStatements(ctx, card.id, pageParam.from, pageParam.count, pageParam.direction)), param: pageParam}),
    initialPageParam: initialParam(card.open_month) as PageParam,
    getNextPageParam: nextParam,
    getPreviousPageParam: previousParam,
  })
  const all = flatten(q.data?.pages ?? [])
  const months = all.map(s => s.month)
  const [start, setStart] = useState(() => shiftMonth(card.open_month, -3))
  const {from, to} = windowAt(months, phone ? selected : start, size)
  const {before, after} = edges(months, phone ? selected : start, size)

  // Prefetch: when the window touches the loaded edge, load the page beyond it.
  useEffect(() => {
    if (!q.isSuccess) return
    if (before === 0 && q.hasPreviousPage && !q.isFetchingPreviousPage) void q.fetchPreviousPage()
    if (after === 0 && q.hasNextPage && !q.isFetchingNextPage) void q.fetchNextPage()
  }, [q, before, after])

  const select = (month: string) => {
    onSelect(month)
    setStart(s => keepVisible(months, s, size, month))
  }
  const step = (dir: -1 | 1) => {
    if (phone) {
      const i = months.indexOf(selected) + dir
      if (months[i]) select(months[i])
      return
    }
    setStart(s => stepWindow(months, s, size, dir))
  }

  // Phone: the strip follows the selection, and the selection follows a swipe.
  const strip = useRef<HTMLOListElement>(null)
  const firstShown = useRef<string | undefined>(undefined)
  useLayoutEffect(() => {
    const el = strip.current
    if (!phone || !el) return
    const prev = firstShown.current
    firstShown.current = months[0]
    if (prev && months[0] !== prev) {
      // Older statements were prepended: keep the card under the finger.
      el.scrollLeft += months.indexOf(prev) * el.clientWidth
    }
  }, [phone, months[0]]) // eslint-disable-line react-hooks/exhaustive-deps
  useLayoutEffect(() => {
    const el = strip.current
    if (!phone || !el) return
    const i = months.indexOf(selected)
    if (i >= 0 && Math.round(el.scrollLeft / (el.clientWidth || 1)) !== i) el.scrollLeft = i * el.clientWidth
  }, [phone, selected]) // eslint-disable-line react-hooks/exhaustive-deps
  const onScroll = () => {
    const el = strip.current
    if (!phone || !el || !el.clientWidth) return
    const month = months[Math.round(el.scrollLeft / el.clientWidth)]
    if (month && month !== selected) onSelect(month)
  }

  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return
    e.preventDefault()
    const next = months[months.indexOf(selected) + (e.key === "ArrowRight" ? 1 : -1)]
    if (!next) return
    select(next)
    requestAnimationFrame(() => strip.current?.querySelector<HTMLButtonElement>(`[data-month="${next}"]`)?.focus())
  }

  if (q.isLoading) return <div className="flex gap-2" aria-busy>{Array.from({length: 3}, (_, i) => <Skeleton key={i} className="h-24 flex-1"/>)}</div>
  if (q.error) return <ErrorBlock error={q.error} onRetry={() => void q.refetch()}/>

  const atStart = phone ? months.indexOf(selected) <= 0 : before === 0
  const atEnd = phone ? months.indexOf(selected) >= months.length - 1 : after === 0
  return (
    <div className="flex items-stretch gap-1">
      <Button variant="ghost" size="sm" aria-label={t("finance.cards.earlier")} className="self-center"
        disabled={atStart && !q.hasPreviousPage} aria-busy={q.isFetchingPreviousPage || undefined} onClick={() => step(-1)}>
        <ChevronLeft aria-hidden className="size-4"/>
      </Button>
      <ol ref={strip} aria-label={t("finance.cards.timeline")} onKeyDown={onKeyDown} onScroll={onScroll}
        className="flex min-w-0 flex-1 gap-2 max-sm:snap-x max-sm:snap-mandatory max-sm:overflow-x-auto max-sm:[scrollbar-width:none]">
        {all.map((s, i) => (
          <li key={s.month} data-in-window={i >= from && i < to ? "" : undefined}
            className={`max-sm:w-full max-sm:shrink-0 max-sm:snap-center sm:min-w-0 sm:flex-1 ${i >= from && i < to ? "" : "sm:hidden"}`}>
            <SummaryCard summary={s} pressed={s.month === selected} onPress={() => select(s.month)}/>
          </li>
        ))}
      </ol>
      <Button variant="ghost" size="sm" aria-label={t("finance.cards.later")} className="self-center"
        disabled={atEnd && !q.hasNextPage} aria-busy={q.isFetchingNextPage || undefined} onClick={() => step(1)}>
        <ChevronRight aria-hidden className="size-4"/>
      </Button>
    </div>
  )
}

function SummaryCard({summary: s, pressed, onPress}: {summary: StatementSummary; pressed: boolean; onPress: () => void}) {
  const {t} = useTranslation()
  const state = statementState(s, todayIso())
  const {tone, icon: Icon} = STATUS[state]
  const closed = s.status === "closed" || s.status === "paid"
  return (
    <button type="button" data-month={s.month} aria-pressed={pressed} onClick={onPress}
      aria-label={t("finance.cards.summaryLabel", {month: monthShort(s.month), state: t(`finance.cards.status.${state}`), total: signedMoney(s.total)})}
      className={`flex h-full w-full flex-col gap-1 rounded-lg border p-3 text-left transition-colors duration-150 motion-reduce:transition-none
        ${pressed ? "border-brand-600 bg-surface" : "border-border hover:bg-surface"}`}>
      <span className="flex items-center justify-between gap-2">
        <span className="text-sm font-medium tabular-nums">{monthShort(s.month)}</span>
        <Badge tone={tone}><Icon aria-hidden/>{t(`finance.cards.status.${state}`)}</Badge>
      </span>
      <span data-numeric className="text-base font-semibold tabular-nums">{signedMoney(s.total)}</span>
      <span className="text-xs text-muted-foreground">
        {t(closed ? "finance.cards.closedOn" : "finance.cards.closesOn", {date: shortDate(s.closing_date)})} • {t("finance.cards.dueOn", {date: shortDate(s.due_date)})}
      </span>
      <span className="text-xs text-muted-foreground">{t("finance.cards.itemsCount", {count: s.items_count})}</span>
    </button>
  )
}
```

The `/impeccable` pass decides the final visual details (border weight of the selected card, density at 5 vs 7 columns, whether the dates line wraps); keep the roles, names and `data-*` hooks the tests use.

`CardsView.tsx`:
- move `StatementState`, `STATUS` and `statementState` (:31-55) into a new `ui/src/components/finance/statementStatus.ts` (exported there) and keep `export {statementState} from "@/components/finance/statementStatus"` in `CardsView.tsx` for its test; the timeline imports from the new module, so the two components never import each other;
- replace `const [month, setMonth] = useState<string | null>(null)` usage: `const shown = month ?? card?.open_month ?? ""` stays; render, in place of the bare `StatementBlock` line:

```tsx
      <StatementTimeline key={card.id} card={card} selected={shown} onSelect={setMonth}/>

      <StatementBlock key={`${card.id}-${shown}`} card={card} month={shown} accounts={all}/>
```

- `StatementBlock` loses `onMonth` and its prev/next header (the `div` with the two chevron buttons and the month label); it keeps `Summary`, the actions and the items. Change its `section` label to `t("finance.cards.statement") + " " + monthShort(month)` so the region still names the month.
- `ItemRow`: when `item.recurrence_id`, append to the meta:

```tsx
        {item.recurrence_id && (
          <>
            {" • "}
            <Link href={`/finance/recurrences?recurrence=${encodeURIComponent(item.recurrence_id)}`}
              aria-label={t("finance.cards.openRecurrence", {name: item.description})}
              className="inline-flex items-center gap-1 text-foreground underline-offset-4 hover:underline touch-target">
              <Repeat aria-hidden className="size-3"/>{t("finance.cards.recurring")}
            </Link>
          </>
        )}
```

(import `Link` from `next/link` and `Repeat` from `lucide-react`; drop the now unused `ChevronLeft`/`ChevronRight`/`shiftMonth` imports.)

Add the copy to both locales; delete `cards.prev` and `cards.next`.

- [ ] **Step 4: Run to verify they pass**

Run: `cd ui && npx vitest run --maxWorkers=2 src/components/finance/CardsView.test.tsx src/lib/finance/statementTimeline.test.ts; echo $?`
Expected: exit `0`. Then in the mock (`npm run dev:mock`): at 1280px the row shows 7 summaries with Mar selected; at 900px, 5; at 375px one per screen, swipe moves between them and older ones load without the card jumping; arrows move the selection; "recorrente" links to the seeded Streaming recurrence and opens its detail.

- [ ] **Step 5: Commit**

```bash
git add ui/src/
git commit -m "feat(finance-ui): the Cards tab is a statement timeline, paged both ways, swiped on a phone"
```

---

### Task 13: Whole-branch verification

**Files:** none new.

**Interfaces:** consumes everything above.

- [ ] **Step 1: Go, unit then integration (not at the same time as the UI suite)**

Run: `cd api && go vet ./... && go test ./... -count=1`
Expected: exit `0`.
Run: `cd api && DYNAMODB_ENDPOINT=http://localhost:8124 go test -tags integration -race -count=1 ./tests/integration/`
Expected: `ok`.

- [ ] **Step 2: UI, after Go finished**

Run: `cd ui && npx vitest run --maxWorkers=2; echo $?`
Expected: `0`.
Run: `cd ui && npm run lint && npx tsc --noEmit -p . && npm run build; echo $?`
Expected: `0`.

- [ ] **Step 3: Copy and naming checks**

Run: `grep -rn "—" ui/src/locales/ | grep -v '"—"' ; echo "exit $?"`
Expected: no match (`exit 1`): no em dash in interface copy (a lone `"—"` empty-value cell is allowed).
Run: `grep -rnE '"/(faturas|cartoes|recorrencias)' ui/src api/internal ; echo "exit $?"`
Expected: no match: routes are English.

- [ ] **Step 4: Spec walk-through**

Read spec § 3 to § 6 once more against the running mock and the tests; every § 6 item maps to a passing test (see Self-Review below). Then use superpowers:finishing-a-development-branch.

---

## Self-Review

**Spec coverage.**
- § 2 D1 (one target, card payable only): Task 1 `Validate`, Task 2 repository, Task 8 HTTP, Task 11 form. D2 (posted on its date by the job): Task 1 `CardDue`/`NextMaterialiseDate`, Task 4. D3 (installments 1..48, ≥ 0,01): Tasks 1, 8, 11. D4 (virtual in the due month, never twice): Task 5, Task 6. D5 (3 + open + 3, swipe, paging, cache): Tasks 10, 12. D6 (summaries endpoint, items separate): Task 7, Task 12. D7 (English routes): Global Constraints, Task 13 step 3.
- § 3.1 model and validation (active card of the space, category rules of a purchase, auto_settle refused, patch switches): Tasks 1, 2, 8.
- § 3.2 posting branch, idempotency from `OccurrenceRef`, cursor as today, late post on the open statement, origin and ref on the purchase, archived card → `needs_attention`, archive warning: Tasks 3, 4, 6, 11.
- § 3.3 occurrence history (posted with purchase id and statement month, upcoming with statement month): Tasks 3, 8, 11.
- § 3.4 projection: Task 5, Task 6.
- § 4.1 range endpoint (one BETWEEN, bounds, `has_more`, totals as `statement()`): Task 7. § 4.2 screen: Task 12 (picker and header kept as they are).
- § 5 failure behaviour: lost close race → retry then next run (Task 4 `TestTheJobRetriesALostCloseRaceOnce`); archived category → `category_archived` (Tasks 4, 6); another space's card → 404 (Task 7 HTTP test, Task 2 create).
- § 6 tests: 1 → Task 3 `TestAnOccurrenceIsPostedOnce…`, Task 4 `TestTheJobPostsEachCardOccurrenceOnceOnItsDay`, Task 6 `TestACardRecurrencePostsOnePurchasePerOccurrenceAcrossRunsAndARace`; 2 → Task 5 `TestACardOccurrenceIsVirtualInItsStatementsDueMonthThenOnlyPayable`, Task 6 `TestAnOccurrenceAfterTheClosingDay…`; 3 → Task 5 `TestAnInstallmentCardOccurrenceSpreadsOverThreeDueMonths`, Task 6 `TestAThreeInstallmentRecurrenceSpreadsOverThreeStatements`; 4 → Task 4 `TestABrokenTargetIsFlagged…`, Task 6 `TestABrokenTargetPostsNothingUntilFixedThenCatchesUp`; 5 → Task 7 (three range tests + HTTP); 6 → Task 12 (opens 3+3, pages without refetch, first-month stop, phone swipe, no em dash).

**Placeholder scan.** No TBD/TODO; each code step carries its code. Two steps describe edits to long existing functions by naming the exact lines that change (`Update`'s SET/REMOVE block, `addPurchase`'s five changes, `RecurrencePanel`'s target state) rather than reprinting 150 unchanged lines; the changed code itself is given in full.

**Type consistency.** `CardCharge` (Task 1) is what `CardDue` returns, `PostOccurrence` takes and `recCards` stores. `cardStore` (Task 4) lists `GetCard`/`PostOccurrence` exactly as `CardRepository` defines them in Task 3. `RecurrencePatch.CardID/Installments` (Task 2) are what Task 8's `edit` fills. `StatementPage` (Go, Task 7) is internal; the TS `StatementPage` (Task 9) is the JSON. `TimelinePage.param` (Task 10) is set in Task 12's `queryFn`. `financeKeys.cardStatements` takes `openMonth` in Tasks 9 and 12.

**Review Focus.** Five lines above, each with its test in the owning task: BETWEEN bound (Task 7), target switch (Task 6), start before the card (Tasks 1 and 6), no description (Task 3), phone prepend (Task 12).

## Decisions worth the owner's attention

1. **The occurrence lock is shared with the bill path** (`OCCURRENCE#{rec}#{nominal}` in the bills table, now with `purchase_id`). It is what makes an occurrence post once across re-runs, races and a switch of card or of target, and it is where the history reads card occurrences from (no scan of card partitions).
2. **Occurrences before the card's first month are skipped**, not posted onto the open statement (Review Focus 3). Late posts after the card exists still land on the open statement, as the spec says.
3. **The purchase is dated on the nominal day**, ignoring the business-day adjustment (cards charge on weekends).
4. **Switching to a card clears `auto_settle` silently**; sending `auto_settle: true` with a card is refused.
5. **An empty recurrence description titles the purchase with the category's name.**
6. **The timeline shows 5 summaries between `sm` and `lg`** (7 from `lg`, 1 on a phone): 7 at tablet width would cramp the money figures.
7. **The overdue label stays "Vencida"** (the spec says "atrasada"; the screen already uses "Vencida").
8. **Archiving a card only warns** (count of active recurrences using it, computed client-side from `GET /recurrences`); the API does not refuse it, and the job then flags `card_archived`.
9. **Range paid status uses one BatchGetItem** for the closed statements' bills instead of one GetItem each.
