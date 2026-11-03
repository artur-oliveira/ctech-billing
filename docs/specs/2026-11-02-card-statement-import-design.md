# Card statement import (Inter CSV first)

**Status: implemented** (plan `docs/plans/2026-11-02-card-statement-import.md`). See "As built" at the end for where the code differs from the proposal.

## Intent

People prefer to export their card statement from the bank and import it than to key every purchase by hand.
Others key purchases during the month and import at the end to catch what they forgot. Both must work, on the
**same statement, as many times as they like**, without ever duplicating a purchase.

The owner (the first user) does the second: purchases entered by hand during the month, then the Inter CSV
imported to find what is missing. A real run of that file against production data (read-only, 2026-11-02, card
"Inter", statement 2026-11) shaped every rule below.

## Goals

- Import the Inter credit card CSV into one card statement, repeatably and idempotently.
- Pair each file line with a purchase the person already entered, when there is one, and ask them to confirm
  each pair (with a "confirm all" button for the pairs on screen).
- Create what has no pair as a purchase, the way the person creates it by hand today.
- A bad line never blocks the rest of the file; the whole file is validated before anything is stored.
- Make the format a plug-in: one parser per bank, Inter first, others later without touching the flow.

## Non-goals

- OFX/PDF/other banks, and any bank API sync (the BYOK Inter connection is separate).
- Auto-merge without confirmation.
- Importing the *payment* of the previous statement (it is not a purchase; offered as "ignore", never created).
- Editing or deleting purchases from the import screen.
- Importing into a statement other than the card's open one (see Constraints).

## Anti-goals (a failure even if it technically works)

- Importing the same file twice adds anything.
- A purchase the person typed by hand shows up a second time next to its imported twin.
- A confirmed pairing silently changes an amount, a date or a category the person chose.
- The statement total after import differs from the bank's by more than the lines the person explicitly ignored.

## What the real data showed (Inter, statement 2026-11, 82 file lines vs 72 system items)

CSV columns: `Data, Lançamento, Categoria, Tipo, Valor`; UTF-8 with BOM; `Valor` is `R$<nbsp>1.234,56`,
negative as `-R$<nbsp>5,00`; no transaction id, no statement month (only the file name `...-2026-11.csv`).

1. **Non-installment lines**: every one pairs with a manual purchase on the **same amount and a date 0 or 1 day
   apart** (53 same day, 7 one day off). A 2-day window is enough.
2. **Installment lines** (`Tipo` = `Parcela k/n`, 14 of 82): the file's `Data` is the *original* purchase date
   (months back) and `Valor` is one installment. The owner registers an old installment purchase as a **new
   purchase with the remaining installments, starting on this statement**: `of = n − k + 1`, `number = 1`, date =
   the day it was keyed. Verified on all 12 matched installment pairs. Date and `k/n` are therefore useless for
   pairing; **amount + remaining count** are the signal.
3. **Amounts can differ by cents** on installments: manual 309,48 (1/5) vs bank 309,46 (`Parcela 8/12`); the
   remaining count (5) agrees. Tolerance: ≤ R$ 0,05 **and** equal remaining count.
4. **Grouped entry**: the owner keyed one purchase "Compras Shein (Agrupadas)" 108,34 in 10x for four file lines
   `Parcela 3/12` of 23,83 + 26,98 + 30,98 + 26,55 (sum 108,34, remaining 10). One purchase ↔ several lines.
5. **No pair at all** (the forgotten ones): 4 purchases of 29/10, two of 07–08/10, one refund line, and the
   payment line `PAGAMENTO ON LINE -R$ 5.052,57`. A manual R$ 25,00 "Recarga Celular" has no file line (the file
   has R$ 20,00): unpaired on both sides, and that is correct.
6. Reconciliation: file sum without the payment line 4.642,17 − system 4.358,20 = 283,97 = exactly the sum of the
   unpaired file lines minus the unpaired manual items. The pairing rules close the books with no residue.

## The flow

1. **Upload** (card, statement month, file). The month is suggested from the file name (`-YYYY-MM.`) and from the
   card's open month; the person confirms it. Parsing and validation of the whole file happen first; rejected
   lines are reported (line number + reason) and the rest proceed.
2. **Stored as pending lines** under an import, like the existing account-statement import (`IMPORT#` rows,
   per-line status, 90-day TTL). Nothing becomes a purchase yet.
3. **Idempotency**: each line gets a key = hash(card, statement month, date, amount, normalised description,
   `Tipo`, ordinal among identical lines), claimed with a lock row per card (reuse `statement.Keys` and the lock
   scheme of `repositories/imports.go`). A line already claimed — by an earlier import of the same or an
   overlapping file — is counted as a duplicate and not stored. Two real identical lines stay two lines (ordinal).
   A line already *resolved* stays resolved: re-importing never reopens a decision.
4. **Suggestions** per pending line, computed against the card's items on that statement that no line has claimed:
   - *pair*: same amount (installments: ≤ R$ 0,05 and same remaining count), date within 2 days (installments:
     no date rule);
   - *group*: ≥ 2 unclaimed `Parcela` lines whose amounts sum to one unclaimed purchase item with the same
     remaining count (small subset search, bounded);
   - *ignore*: payment lines (`PAGAMENTO…`), suggested by default.
5. **Decision per line**: confirm pair / confirm group / create purchase / ignore. "Confirm all" confirms every
   pair and group currently suggested. A confirmed pair records the link only: it changes nothing in the ledger,
   exactly like `Link` in the existing import. Pairs are 1-to-1: an item is claimed by at most one line.
6. **Create** (a line with no pair): a card purchase, category from the saved bank-category mapping (editable,
   saved per card, falls back to the person choosing). Amount in cents, never float.
   - À vista: the file's date and amount.
   - `Parcela k/n`: **as the owner keys it by hand** — installment amount × (n − k + 1) as the total, n − k + 1
     installments, allocated starting on the chosen statement. The original date and `k/n` are kept in the
     description note. *(Builder: the purchase date must be one that allocates to the chosen statement; use a
     date inside that statement's period — see Constraints.)*
7. **Refund line** (negative, not the payment): becomes a standalone credit (see Decided with the owner) in the
   mapped category, after the person confirms it.
8. The result screen shows added, paired, created, ignored, duplicates, rejected, and the statement total
   against the file total (excluding ignored lines).

## Constraints

- **Only the open statement accepts purchases.** A closed statement is never changed (spec § 3.6), and a
  purchase dated in a closed month lands on the open one. To import a closed statement the person first reopens
  it (`POST /cards/:id/reopen`, shipped in #293); the upload screen offers it when the target month is closed
  and unpaid, and refuses a paid one.
- Money is integer cents. Idempotent POSTs (`Idempotency-Key`). RFC 7807 errors with `code`, pt-BR + en copy.
- Bounded: file ≤ 1 MiB and ≤ 2000 lines (same limits as the account import); matching reads only the target
  statement's items (one partition query), no scan, no N+1.
- No new AWS resource, no custom metric, no alarm. Reuse the existing imports table and its TTL.
- Realtime: statement/import changes publish the same events the account import uses.
- Family: the bank-format parser contract and the dedup/lock code are reusable by ctech-go-common only if a
  second service needs them; nothing here is billing-specific enough to move now. Check `_analysis/` first.

## Decided with the owner

- **Standalone credit is in this build** ("crédito avulso"). The system has no way to enter a credit that is not
  the refund of a known purchase (`Refund` reverses a purchase), and the file's `-R$ 5,00` needs one: without it
  the statement total can never match the bank's. Minimal shape: on the card's open statement, card liability ↓,
  an expense category ↓ (the category comes from the bank-category mapping, like any purchase), one `credit`
  statement item, idempotent. Usable by the import (a refund line with no purchase to pair becomes one) and by
  hand from the card screen. It is its own piece of the plan and ships with its own tests.
- **Window** (2 days) and **cents tolerance** (5) are constants, not settings.

## Calls made while writing (the author's, not the owner's — check them)

- **Claim = a lock row per card item** (`PAIR#<item key>`, conditional put) written in the same transaction that
  marks the line resolved. It enforces 1-to-1, is readable from the item side (a purchase keyed by hand later is
  simply unclaimed), and needs no new attribute on the purchase.
- **Ordinal** is per (card, statement month, signature) *within one file*; a later file that holds the same two
  lines plus a third claims the first two as duplicates and adds the third. A resolved line stays resolved
  forever, even if the purchase it created or paired is later edited or deleted.
- **Created installment purchase date**: `min(statement closing date, today)`, which allocates to the target
  statement. If the statement's period has not begun (today ≤ previous closing date), the upload is refused.
- **Reopen is a separate, explicit action** before the upload, never inside the import request. The import does
  not re-close: the reopened statement closes by its own grace period or "Fechar fatura agora".
- **Group**: every line in a group shares one remaining count (n − k + 1) equal to the purchase's; the sum must
  equal the purchase total within R$ 0,05 in total. Subset search capped (e.g. ≤ 6 lines per group, ≤ 20
  candidate lines).
- **Ambiguity** (several lines match one item or the reverse): deterministic — closest date, then file order.
  The person can pick another candidate from the list; suggestions are stable across re-imports.
- **Unmapped bank category**: pairing and "confirm all" never need a category; creating a purchase does. "Create
  all unpaired" is disabled for lines whose bank category is unmapped until the person maps it (saved per card).
- **Eligible pair targets**: only `installment`-kind items (a purchase's installment or its single charge) of
  the target statement. Credits, advances and carries are never pair targets.
- **Total check**: the result screen shows system total vs file total (ignored lines excluded) as information
  and a warning on mismatch; it never blocks.

## Left to the builder

Table/key layout of the card import lines, the lock key format, the group-suggestion search bound, endpoint
names, the screen layout, the bank-category mapping storage, test fixtures (a trimmed copy of the real file with
descriptions anonymised — never commit the real one).

## As built

- Upload `POST /cards/:id/imports` (`{statement_month, bank:"inter", content}`); review `GET /card-imports/:id`
  (lines + suggestions); `POST /card-imports/:id/confirm` (1 line = pair, 2..6 = group), `/confirm-all`,
  `/lines/:n/create`; ignore/reopen reuse `/imports/:id/lines/:n/ignore|reopen`; `GET|PUT /cards/:id/bank-categories`;
  standalone credit `POST /cards/:id/credits`. Errors: `statement_not_open`, `statement_not_begun`, `pair_taken`,
  `category_unmapped`, `payment_line`, `invalid_bank_categories`, `not_card_import`.
- Storage reuses the account-import tables: lock scope `<cardID>#<month>`, a `CARDPAIR#` lock per paired item
  (no TTL, like the FITID locks), the line's `bill_id` holds the purchase it paired with or became. New line
  attributes: `k`, `of`, `bank_category`; import attributes `kind`, `statement_month`.
- The standalone credit posts as an adjustment (reverse of a card purchase) and has no edit/delete in this build.
- Installment lines the bank prints as `Parcela k/n` are stored as `K`/`Of` (not `N`, already the line number).
- Cost: no new resource. One small lock row per confirmed pair and per bank-category mapping; reads are one
  statement-partition query plus one `CARDPAIR#` prefix query per review.
