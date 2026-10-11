# Spec — Cards: recurrences paid by card, and the statement timeline

Status: **Draft for review** · 2026-10-11 ·
Builds on: [`2026-10-07-finance-erp-design.md`](2026-10-07-finance-erp-design.md) (Finanças),
`docs/plans/2026-10-08-finance-6.5-credit-cards.md` (cards, statements, installments),
[ADR 0024](../adr/0024-billing-keeps-a-management-ledger.md) (management ledger)

## 1. What this is

Two changes to Finanças' cards, decided together because the first fills the second:

1. **A recurrence can be paid by card.** A subscription charged to a card (streaming, software, phone) is a
   recurrence whose occurrences become **card purchases** on that card, landing on the right statement, instead of
   bills paid from an account.
2. **The Cards tab becomes a statement timeline.** On opening, the person sees summary cards for the 3 previous
   statements, the open one and the 3 next ones (value, closing, due date, status), and pages backwards and forwards,
   each step fetching the next page beyond what is loaded.

What it is **not**:
- Not a change to how purchases, installments, closing or statement bills work.
- Not income by card (refunds stay manual) and not recurrences on another person's card.
- Not a projection redesign: the projection keeps its columns; card amounts stay in **Payable** (§ 3.4).

## 2. Decisions

| # | Decision |
|---|---|
| D1 | "Pay with" on a recurrence is **an account or a card**, exactly one. A card only for payable (expense) recurrences. |
| D2 | An occurrence paid by card is posted as **a card purchase on its own date**, by the daily job, never earlier: a card purchase is an expense on its purchase date (ADR 0024 ledger), so a future-dated one would book an expense that has not happened. |
| D3 | A card recurrence may split each occurrence into installments (default 1, at most `limits.MaxInstallments`). |
| D4 | Until posted, an occurrence is a **virtual payable in the month its statement is due**, not in the month it is charged. Once posted it is part of the statement and leaves the virtual set: never counted twice. |
| D5 | The timeline opens on **3 previous + the open + 3 next** statements (desktop), one at a time on a phone (swipe). Paging loads 3 more beyond the last loaded page in that direction; loaded pages stay cached. |
| D6 | A new range endpoint returns statement **summaries** (no items); items load for the selected statement only. |
| D7 | Routes and endpoints in English; Portuguese only in i18n. |

## 3. Recurrences paid by card

### 3.1 Model

- `finance.Recurrence` gains `CardID string` (JSON `card_id`) and `Installments int` (JSON `installments`, card only,
  default 1). Exactly one of `AccountID` / `CardID` is set. `CardID` requires `Direction == payable`.
- `AutoSettle` is meaningless for a card recurrence (the statement's bill is what gets paid) and is refused with it.
- Validation (domain `Validate` + repository): the card belongs to the space and is active (not archived); the
  category rules are the card purchase's (active, non-system expense account); installments 1..48 and each
  installment ≥ R$ 0,01 (the `AllocateInstallments` rule).
- Create and patch accept `card_id`/`installments`; switching a recurrence between account and card is a patch, and
  applies to occurrences not yet materialised or posted.

### 3.2 Posting

- `FinanceJobs.Materialise` branches per recurrence:
  - account target: unchanged (`CreateFromOccurrence`, horizon up to the end of next month, forecast bills);
  - card target: only occurrences with **date ≤ today** are processed; each becomes
    `CardRepository.AddPurchase(sp, cardID, Purchase{Date: occurrence date, Description: recurrence description,
    CategoryID, Total: amount, Installments: n}, meta{IdempotencyKey: OccurrenceRef(recurrenceID, nominal)}, now)`.
- Idempotency: the purchase id is derived from the occurrence ref (the existing `idempotentID(sp, "purchase", key)`),
  so a re-run or a race posts once; the recurrence cursor moves with `MarkMaterialised` as today.
- The "date not in the future" check stays in the HTTP route only; the job never posts a future date anyway (D2).
- A purchase dated before the card's open month lands on the open statement, as `AllocateInstallments` already does
  for late posts.
- Origin: the purchase carries `origin: recurrence` and the occurrence ref, so the statement item and the
  recurrence's occurrence history link to each other.
- Archived card: the occurrence is **not** posted; the recurrence gets `needs_attention: card_archived` (shown in the
  recurrences list), and nothing advances until the person picks another card or account. Archiving a card that
  recurrences use warns with their count first.

### 3.3 Occurrence history

`GET /recurrences/:id/occurrences` returns, for a card recurrence, posted occurrences with their purchase id and
statement month, and upcoming ones with the statement month they would land on
(`ClosingDate`-based, same math as `AllocateInstallments`).

### 3.4 Projection

`FinanceJobs.Project`: for a card recurrence, each not-yet-posted occurrence in the window contributes its
installments to **VirtualPayable** in the **due month of the statement** each installment would land on. Posted
occurrences are already statement items, counted by `projectCards` as Payable. An occurrence is "posted" iff its
nominal is at or before the recurrence's cursor, the same test account recurrences use, so there is no overlap.

## 4. Statement timeline

### 4.1 API

`GET /cards/:id/statements?from=YYYY-MM&count=N&direction=forward|backward`

- Returns `{ "data": [StatementSummary…], "has_more": bool }`, ordered by month in the requested direction from
  `from` inclusive. `count` 1..12 (default 3).
- `StatementSummary` = the existing statement DTO without `items`: `month, status (open|future|closed|paid),
  closing_date, due_date, total, bill_id?`, plus `items_count`.
- Bounds: backward stops at the card's first statement month (its creation month); forward stops at the later of
  the last month holding an item and `open_month + 12`. `has_more` says whether another page exists that way.
- Implementation: one `Query` on the card's partition with `sk BETWEEN StatementSK(a) AND StatementSK(b)` (statement
  rows and item rows), grouped by month in memory, instead of one query per month; closed totals from the statement
  row, open/future totals from the items, exactly as `statement()` computes them today.
- `GET /cards/:id/statements/:month` (items) stays as it is.

### 4.2 Screen (`/finance/cards`)

Built with `/impeccable`, following `ui/DESIGN.md`; its own phone layout.

- Card picker and card header as today (brand, name, last digits, "fecha dia X · vence dia Y", actions).
- **Timeline row**: summary cards in month order, the open statement highlighted and selected on opening, 3 before
  and 3 after on desktop. Each card: month name, status badge (paga, fechada, atrasada, aberta, futura; "atrasada" =
  closed with an unpaid bill past due, as the screen derives today), total, closing and due dates, and a hint of
  how many items.
- **Previous / next** at the row's ends: loads the next page beyond the first/last loaded month
  (`useInfiniteQuery`, both directions), keeps loaded pages, prefetches the adjacent page when the person reaches the
  edge; disabled when `has_more` is false. Keyboard: arrows move the selection.
- **Phone**: one summary card at a time, swipe left/right (snap scrolling), same paging.
- **Below the row**: the selected statement's items and actions (Fechar, Pagar, Estornar, Antecipar), as the current
  `StatementBlock` shows them.
- A recurrence's items show a small "recorrente" marker linking to the recurrence.

## 5. Failure behaviour

- The job posting a card occurrence and the person closing that statement at the same time: `AddPurchase` already
  fails with `ErrCardMoved` on a lost race; the job retries next run, and the item lands on the open statement.
- A card recurrence whose category is archived: same `needs_attention` path (`category_archived`), not a silent skip.
- Range endpoint for another space's card: `404`, like every card route.

## 6. Tests and review focus

1. A card recurrence posts exactly one purchase per occurrence across two job runs and a concurrent run.
2. An occurrence charged on the 28th with closing on the 25th lands on next month's statement; the projection
   counts it in that statement's due month while virtual, and once posted, only as Payable.
3. An installment recurrence (3x) spreads across three statements, both virtually and when posted.
4. Archived card or category: nothing posted, `needs_attention` set, cursor not advanced; fixing it posts the
   pending occurrences (up to today) on the next run.
5. Range endpoint: bounds and `has_more` at the card's first month and at the last future item; totals equal the
   single-month endpoint's for every month in a range.
6. Timeline UI: opens on the open statement with 3+3; next/previous load beyond the loaded edge without refetching
   loaded months; phone swipe; no em dash in copy.
