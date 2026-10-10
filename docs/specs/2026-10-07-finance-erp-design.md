# Spec — Finance: a management ledger in the billing console ("mini ERP")

Status: **Design approved, not implemented** · 2026-10-07 ·
Decisions: [ADR 0024](../adr/0024-billing-keeps-a-management-ledger.md),
[ADR 0025](../adr/0025-spaces-personal-and-organization.md),
[ADR 0026](../adr/0026-finance-retention-by-purge.md) ·
Priority: **ahead of the remaining console work** (C10–C16) in [`PLAN.md`](../../PLAN.md)

## 1. What this is

A section of the **console** ("Finanças") where a person or an organization records what they
receive and what they pay — recurring or not — and sees, per space, profit and loss **now** and
**over the coming months**.

- Payables and receivables (*contas a pagar / a receber*), created by hand, by recurrences, by card
  purchases, by statement imports, or by billing's own paid invoices.
- Accounts the user holds elsewhere: checking, cash, investment, credit card.
- Two reports from one record: **cash flow** (regime de caixa) and an **income statement by
  accrual** (DRE por competência).
- Credit cards with installments (*parcelamento*), as Brazilians use them.
- OFX/CSV statement import with reconciliation against what was forecast.

It is **not custody**. Every amount here is a record of money held somewhere else — a bank, a
drawer, CTech Ledger. Nothing in this module moves money, and the §9 test of the 2026-08-15
assessment ("can the holder turn this into money on their account?") still answers *no*.
See ADR 0024.

### Who uses it

| Space | Who | What they see |
|---|---|---|
| **Personal** | every signed-in CTech user, organization or not | their own finances only |
| **Organization** | members of an organization in ctech-account | that organization's finances, by role |

There is **no consolidated "everything" view** across spaces. It was considered and dropped: it
would be the only cross-tenant read on a request path, which ADR 0003 makes inexpressible on
purpose, and the segmented view is cheaper and enough.

### Tiers

This v1 **is** the Free tier: records and a P&L view, no money integrations. **Nothing in v1 is
gated by plan.** Paid tiers arrive with the integrations (§ 12) and each needs its own spec:

| Tier | Adds | Blocked by |
|---|---|---|
| Free | this spec | — |
| Custom (BYOK) | the customer's own Inter account: PIX with due date / boleto collection, statement sync | its own spec; Inter `cobv`/boleto support does not exist anywhere in the family yet |
| BaaS | an account backed by CTech Ledger (Asaas) | Asaas production approval; no PJ sub-account flow exists |

**The subscription belongs to the space (2026-10-07):**
- a **personal** space's subscription belongs to the user, who pays and manages it;
- an **organization** space's subscription belongs to **the organization**. Any member holding
  `owner` or `admin` in ctech-account may pay and manage it, and it does not move when ownership is
  transferred.

In tenant zero the paying `Customer` is therefore either a person (`USER_{sub}`) or an organization
(`ORG_{organization_id}`). This follows ctech-account's organizations spec ("the organization
becomes the billed party") and **departs from how `ctech-dfe` bills today**: dfe keys the customer
on the owner's user (`USER_{sub}`, `SnapshotForOrg`/`OwnerOf`). Its per-user pricing is a quota
*inside* the organization's subscription (opaque price metadata, ADR 0008), counted by dfe, and is
no reason to make a person the payer. **Cross-repo follow-up in `ctech-dfe`**: move its
subscription to the organization.

Also decided (2026-10-07):
- **The portal gets the same space selector as the console**
  ([ADR 0025](../adr/0025-spaces-personal-and-organization.md), amendment):
  - *Pessoal* shows the user's own CTech invoices and subscriptions, as today;
  - an organization shows that organization's, resolved through the same server-side membership
    check, never from the header alone;
  - `owner` and `admin` see, pay and manage; other roles do not see the organization in the portal
    selector.
- **On the invoice PDF, an organization customer is named by its designated billing company**
  (ADR 0022: legal name and CNPJ from ctech-account). When a natural person is required — the
  organization has no company, or the document must carry a CPF — it is issued in the name of the
  organization's owner.

## 2. Spaces and modes

A **space** is the owner of every finance row, and it is the leading part of every partition key:

```
S = {organization_id}#{mode}      organization space
S = USER#{sub}#{mode}             personal space
mode ∈ {live, test}
```

Organization ids are UUIDv7 issued by ctech-account and can never begin with `USER#`.

**Test mode exists here too**, as real partition isolation (ADR 0003), never a display flag:
- a paid test invoice in billing posts to the test ledger;
- a future CTech Ledger account in test mode reads the **sandbox** wallet;
- a future BYOK account in test mode talks to Inter's sandbox.

No row ever crosses modes.

### 2.1 Space resolution — the selector is not authority

The console sends `X-Billing-Space: personal` or `X-Billing-Space: org:{id}`. That header is a
**request, never a permission**. The rules (ADR 0025):

1. `personal` carries no id. It always resolves to `USER#{sub of the token}`. There is no syntax
   for "another user's personal space".
2. `org:{id}` is checked against ctech-account membership **for the token's `sub`** on every
   request, before any DynamoDB read.
3. No membership answers **404 `/problems/space-not-found`**, identical to an id that does not
   exist, so the response does not reveal whether an organization exists.
4. Finance repositories accept only a `ResolvedSpace` value, constructible only by the resolver.
   A handler cannot build a key from the raw header; this is a compile-time property, not a review
   rule.
5. Every id in a path or body (`bill_id`, `account_id`, …) is looked up **inside** the resolved
   partition, so an id from another space is simply not found.
6. Membership answers are cached 60 s, positive and negative, like `ctech-dfe`'s reach check.
   **Accepted limit:** a removed member may keep reading for up to 60 s.
7. If ctech-account cannot be reached, access **fails closed**.

The space is part of **every console query key**, for the same reason the mode already is: without
it, switching space renders the previous space's rows out of the cache.

## 3. Domain model

All names in code are English. Money is centavos (`int64`), with the single rounding policy the
billing domain already has (`MulDiv`, half away from zero). Dates are the civil `Date` type.

### 3.1 Double entry, invisible to the user

Built on Fowler's *Accounting Patterns*: **Account**, **Accounting Entry**, **Accounting
Transaction**, **Posting Rule**, and corrections by **Reversal / Replacement Adjustment**.

- **`LedgerAccount`**, with a `class`:
  - `asset`: the user's checking, cash and investment accounts;
  - `liability`: the user's credit cards, plus system *payables*;
  - `income` / `expense`: what the user calls **categories**;
  - `equity`: the system *opening balance*.

  System accounts (payables, receivables, opening balance) are created with the space and never
  shown as such.
- **`LedgerTransaction`**: one business fact. It holds N ≥ 2 **`LedgerEntry`** legs whose amounts
  **sum to zero**, or nothing is written. A transaction carries one date, which every one of its entries shares (every fact in v1 is single-date). Transactions and entries are
  **immutable**.
- Every leg on a cash account carries a **flow**, chosen by the posting rule: the bill's category
  for a settlement, *none* for a transfer or an opening balance. The cash flow reads it; a leg
  posted before flows existed counts as cash under *Sem categoria*.
- **Corrections** never edit:
  - *reversal*: post the exact opposite of a transaction;
  - *replacement*: reversal plus the corrected transaction, linked to each other.
- **Balances are derived** from entries. The stored `balance` and monthly `SUMMARY` rows (§ 5) are
  a cache of that derivation, updated in the same write and **rebuildable** by a command that
  recomputes and compares them.

### 3.2 How the two reports come from the same entries

| Fact | Debit | Credit | Dated |
|---|---|---|---|
| Expense recognised (bill materialised / created) | expense category | payables | competence date |
| Expense paid (settlement) | payables | checking | payment date |
| Revenue recognised | receivables | income category | competence date |
| Revenue received | checking | receivables | payment date |
| Transfer between own accounts | destination | origin | transfer date |
| Card purchase R$ 1.200 in 12× | expense category (**full amount**) | card (liability) | purchase date |
| Card statement paid | card | checking | payment date |
| Opening balance | the account | opening balance (equity) | opening date |

- **DRE by accrual** = entries on `income`/`expense` accounts, by entry date.
- **Cash flow** = entries on `asset` accounts, by entry date.

A **card purchase in installments enters the DRE whole, at the purchase date**, because that is
when the expense happened. Cash leaves installment by installment, as each statement is paid.

A settlement for a different amount than the bill (interest, fine, discount) posts the difference
to a category the user picks; the system seeds *Juros e multas* and *Descontos obtidos*.

### 3.3 DRE groups and categories

The system fixes the DRE groups. Users create categories under them and can never build a DRE that
does not add up.

| Group | Class |
|---|---|
| `gross_revenue` (Receita bruta) | income |
| `deductions` (Deduções) | expense |
| `costs` (Custos) | expense |
| `operating_expenses` (Despesas operacionais) | expense |
| `financial_result` (Resultado financeiro) | income or expense |
| `other` (Outras receitas/despesas) | income or expense |

Categories are archived, never deleted: entries reference them forever. A space is seeded with
default categories, with a different set for personal and for organization spaces.

### 3.4 Bills (payables and receivables)

A **`Bill`** is a forecast or recognised obligation.

| Field | Notes |
|---|---|
| `direction` | `payable` \| `receivable` |
| `amount` | centavos |
| `account_id` | the account expected to pay or receive; changeable until settled |
| `category_id` | |
| `competence_date` | when the fact belongs (DRE) |
| `due_date` | when it is owed |
| `paid_date` | set on settlement (cash) |
| `status` | `forecast` \| `paid` \| `canceled` |
| `origin` | `manual` \| `recurrence` \| `billing_invoice` \| `card_statement` \| `import` |
| `origin_ref` | recurrence id + nominal date, invoice id, statement id, import line |
| `payment_group` | the card statement a bill belongs to, when it does |
| `transaction_ids` | the ledger transactions it produced |

Lifecycle:
- **Creation** posts the *recognition* transaction at the competence date.
- **Settlement** (*baixa*) posts the cash transaction. It can be:
  - manual;
  - automatic when a source confirms (billing invoice paid, later Inter/Ledger);
  - automatic on the due date, if its recurrence has `auto_settle`.
- **Cancellation** posts a reversal of the recognition. A paid bill cannot be canceled; it is
  corrected by reversing its settlement first, and both stay visible.

### 3.5 Recurrences

Built on Fowler's *Recurring Events for Calendars*: a recurrence holds a **temporal expression**,
never a list of dates.

```go
type TemporalExpression interface {
    Includes(d Date) bool
}
// Occurrences(expr, from, to) and Next(expr, after) are derived from Includes,
// with the obvious fast paths per expression type.
```

Expressions in v1:

| Expression | Example | Notes |
|---|---|---|
| `DayOfMonth(n)` | every 10th | clamps to the month's last day, like billing's anchor |
| `WorkdayOfMonth(n)` | 5th business day | `n = -1` is the last business day; uses `brcal`, so already a business day |
| `NthWeekdayOfMonth(weekday, n)` | 2nd Monday, last Friday (`n = -1`) | |
| `Weekly(weekday, every)` | every other Friday | `every` counts weeks from an `anchor` day that falls on the weekday (set to the first occurrence on or after `start`) |
| `Yearly(month, day)` | every 15 March | clamps 29 Feb |
| `Difference(a, b)` | "every 10th except December" | `b` may be a `Dates([...])` set of skipped days |

Stored as a typed JSON tree, validated on write. `Union` and `Intersection` are not in v1: no case
needs them yet, and the tree format admits them later without migration.

**`Recurrence`** fields:
- `direction`, `amount`, `category_id`, `account_id`, `description`;
- `expression`, `start`, `end?`;
- `business_day_adjust`: none or roll-forward via `brcal`. It is applied **after** the expression
  and never to `WorkdayOfMonth`;
- `competence`: the occurrence date by default;
- `auto_settle` (bool).

**Projection without materialising everything:**
- Forecasts beyond the horizon come from `Occurrences(range)`, computed on read.
- The daily job materialises occurrences inside the **horizon** (the current month and the next)
  as `Bill`s, which become the working list and can each be edited (amount, account, due date).
- An occurrence's identity is `(recurrence_id, nominal_date)`, guarded by a conditional
  `OCCURRENCE#` lock row, so a re-run never duplicates.
- Editing a recurrence changes only occurrences **not yet materialised**, the same rule as the
  dunning policy copied onto an invoice.
- **An end that leaves nothing to come ends it** (UX batch 3): when the new end leaves no occurrence
  after today and none the job still owes after its cursor, the edit is refused (422
  `recurrence_would_end`) unless it also asks to archive, and then the end and the archive are one
  conditional write. The console asks first: "Isso encerra a recorrência; ela será arquivada."
- **Ending cancels what it made after the end** (UX batch 5, the owner's decision; batch 3 kept
  them). On end-and-archive, every bill the recurrence made for a nominal date after the new end
  that is **not paid** is cancelled through the ordinary cancel path (the bill's guarded update and
  the reversal of its recognition, one transaction per bill). A paid one stays: real money moved
  and the statement shows it. The confirmation names them before anything is sent: "Os 2
  lançamentos previstos depois do fim (10/10 e 10/11) serão cancelados." and "O já pago (10/12)
  continua: o pagamento está no extrato." PATCH answers the cancelled ids in `canceled_bill_ids`.
  **Ruling — an ordered, re-runnable sequence, not one transaction:** the end and the archive are
  written first (the conditional update above), then each forecast bill after the end is
  cancelled. One `TransactWriteItems` would carry ~10 items per bill toward the 100-item limit and
  would fail whole when any single bill moved. Archived first, nothing new is made for those dates;
  the `OCCURRENCE#` locks stay, so the job never makes them again; a failure half-way answers an
  error with the rule archived, and the same PATCH again cancels only what is still open. A bill
  paid or cancelled between the confirmation and the request is refused by the cancel guard (still a
  forecast, same transaction list) and stays as it is; one only edited is tried once more.
- **A recurrence's detail** (`GET /recurrences/:id/occurrences`, read verb, inside the space) lists
  the latest bills it made, each paid, forecast, overdue or skipped (its bill cancelled), and the
  next dates the rule will make (computed on read, from after the cursor, never before today; none
  once archived).
- The projection shows forecast and realised visually apart. A materialised bill is recognised in
  the DRE at its competence date — that is what accrual means — but it is never part of the
  **cash** result until settled. F1's "resultado realizado" is cash; F7's DRE is accrual, and the
  screen says which one it is showing.

### 3.6 Credit cards

A card is a `liability` account with:
- `closing_day`, `due_day`;
- `default_paying_account_id`;
- optionally `brand` (visa, mastercard, elo, amex, hipercard, diners, other) and `last4` (exactly
  four digits), only to tell cards apart (UX batch 3). Never the full number.

| Entity | Notes |
|---|---|
| `CardPurchase` | date, description, category, `total`, `installments` (1–48), and the allocation of each installment to a statement month. Posts **one** transaction (expense ↔ card) for the full amount. |
| `CardStatement` | per card per month: `open` → `closed` (on closing day) → `paid`. Its total is the sum of the installments allocated to it plus credits. On close the job freezes the total and creates the `Bill` (`origin = card_statement`, payable, due on `due_day`) that is settled from a checking account. |

Rules:
- **Allocation:**
  - a purchase on or before the closing day falls into that month's statement, otherwise the next;
  - installment *k* falls *k − 1* statements later;
  - remainders from integer division go to the **first** installment, so the installments always
    sum to the total. This is the same property the pro-rata calculator pins.
- **Refund (estorno):** a reversal of the purchase transaction, plus a credit item on the current
  open statement for the installments already billed. Installments not yet billed are removed.
- **Advance (antecipação):** moves remaining installments to the current open statement. It is an
  allocation change only; the DRE already holds the full amount.
- A closed statement is not changed. A late correction lands on the next open statement.
- A statement closes on its closing day (clamped to the month's end) and is due on the first due
  day strictly after it. A statement whose total is negative (refunds exceeding charges) carries it
  as a credit to the next one; a zero total closes with no bill.

### 3.7 Import and reconciliation

- **Formats:** OFX (bank and card statements), and CSV with a column mapping the user saves per
  account.
- **The raw file is never stored.** It is parsed in the request and discarded (LGPD minimisation).
  Parsed lines are kept 90 days for the reconciliation screen.
- **Idempotency:** `FITID` for OFX, or a hash of (account, date, amount, description, ordinal among
  identical lines) for CSV, guarded by a conditional lock row. Importing the same file twice adds
  nothing.
- **Reconciliation** offers each line against open bills in the same account by matching amount and
  due date within ±5 days:
  - **match**: confirming settles that bill with the line's date and amount;
  - **new**: the user picks a category and it becomes a bill created and settled at once;
  - **ignore**: kept, so it is not offered again.
  - **link** (6.6 follow-up): a bill the recurrence's `auto_settle` already paid is offered too, only in
    the exact case — same account and direction, exactly the line's amount, paid within ±5 days, not
    linked to another line. Confirming it posts **nothing** (the money is already recorded): it marks
    the line reconciled and ties line and bill, conditionally, so two lines never hold one bill. A
    payment made by the job is told apart from a manual one by the settlement's `origin =
    auto_settle`. A different amount (interest, discount) is not offered; the user ignores the line.
- **Undoing a payment does not reopen a line.** A line that settled, created or was linked to a bill
  stays reconciled when that bill's payment is undone: the bank statement says the money moved, so
  the statement resolves the line.
- **Expiry is shown where it matters:** the pending list says lines stay 90 days after the import, and
  a pending line in its last 15 days says when it expires. No banner.
- **Opening balance from the statement** (UX batch 5). An OFX bank statement's `LEDGERBAL`
  (`BALAMT` on `DTASOF`; `AVAILBAL` is not it) proposes the opening balance of an account that
  starts with the file: **`LEDGERBAL` minus the sum of every parsed line** (credits in, debits out),
  **dated the day before the file's first line**, so the opening plus the file's lines lands on the
  bank's balance. Computed from the whole file at upload (`statement.OpeningFrom`) and kept on the
  import row; the upload and the import's detail answer it as `opening_proposal` only while the
  account has **no opening balance and no entry**. The console asks "Usar o saldo do extrato como
  saldo inicial?", shows the arithmetic, and says what can make it drift: a line the person ignores
  is in the bank's balance but never enters the account, so the account then differs from the
  statement by that amount (and lines the parser could not read are out of the sum too). Posting
  needs an explicit confirmation: `POST /imports/:id/opening-balance`, `finance.configure` like any
  opening balance, no body (amount and date are the server's), through the ordinary rule and its
  `OPENING#` marker, inside the resolved space (another space's import is 404). Its transaction id
  derives from the import, so the same request again answers 200 with the same fact; another
  opening balance is 409 `opening_balance_exists`, an account with entries 409
  `account_has_entries`, a file without `LEDGERBAL` 422 `no_statement_balance`. The entries are read
  before the write (they are many rows, not one condition): a first entry racing the request can
  land beside the opening, which stays once-only and reversible from the statement.
- Nothing is settled without a confirmation in v1. Auto-confirming exact matches can come later.

### 3.8 Billing integration in v1

`invoice.paid`, handled in-process by a **posting rule**, not by HTTP:

1. **Organization that issued the invoice:** a receivable `Bill`, `origin = billing_invoice`:
   - created and settled at once;
   - competence = the invoice's period start, cash = `paid_at`;
   - category = a system-seeded *Assinaturas* under `gross_revenue`;
   - account = the space's default receiving account, which the user chooses in F8.

   Today only tenant zero has real collection, so this carries real money only for CTech until BYOK
   exists. In test mode it works for everyone.
2. **The paying customer's own space**, when it has one: a payable `Bill` with the same dates,
   settled, under a seeded *Assinaturas CTech* expense category. A person customer (`user_id`) posts
   to their personal space; an organization customer (§ 1) posts to that organization's space. A
   space's CTech subscriptions appear in its own finances by themselves.

Both are idempotent by invoice id. A `CreditNote` against a paid invoice posts a reversal in both
spaces for the credited amount.

Revenue linked to a subscription is **only realised when an invoice is paid**. Its projection
comes from billing: open invoices plus the subscription's next renewals, computed on read.

**As built (6.7, 2026-10-09)** — [plan](../plans/2026-10-09-finance-6.7-billing-integration.md):
- The issuing organization's space is found through `Organization.AccountOrganizationID`, a link to the
  ctech-account organization set by the tenant plan (billing's tenant ids are not account ids:
  tenant zero is `ctech`). An unlinked tenant posts nothing on the issuer side.
- The payer side is **tenant zero only** (a third-party merchant's `user_id` is that merchant's claim)
  and **person customers only**: organization customers are not modelled yet, so a customer with a
  `user_id` posts to that person's personal space.
- The cash account is the space's default receiving account **on both sides**. With none (or an
  archived one), a space holding **exactly one** active bank or cash account receives there; with
  zero or several candidates it gets nothing (a warning), and it is not created by the posting.
- **"Lançar minhas faturas da CTech automaticamente neste espaço"** (`post_ctech_invoices` on the
  space's `SPACE` row, on when absent, changed with `finance.configure`, audited): when off, the
  payer side (the invoice and its credit notes) writes nothing in that space. The issuer side is
  CTech's own books and is never affected. Future postings only: turning it off deletes nothing and
  turning it on replays nothing older; the replay pass reads it at replay time. Only personal spaces
  are payers today.
- Tenant zero (`ctech`) is linked to CTech's ctech-account organization
  `01a04ed6-1af9-745e-bcf7-d3b66fe52321` in `api/tenants/ctech.json`.
- Zero-total invoices post nothing. A credit note posts one adjustment (`finance.CreditBill`) dated
  the note's day, in each space where the invoice was recorded and its bill is still paid.
- Idempotent by the bill's id (space, invoice) and the credit's transaction id (space, note). The
  settlement never fails because of finance. What could not be written (a failed write, no receiving
  account yet, an unlinked tenant) stays on a durable queue armed in the same write that makes the
  invoice PAID (or issues the note), and `cmd/reconcile` replays it hourly for 30 days. It is a retry,
  not a backfill.
- Credits never sum past the invoice: billing's `credited_total` and the finance bill's `credited` move
  by compare-and-set in the same write as the note or the credit.

## 4. Persistence

Every table is `{env}_billing_{name}` and declared in `api/internal/repositories/schema.json`.
Every partition key begins with `S`.

| Table | Rows (pk → sk) | Serves |
|---|---|---|
| `ledger_accounts` | `S` → `SPACE` (settings) · `ACCOUNT#{id}` (account, category or system account, with cached `balance`) · `SUMMARY#{yyyy-mm}#{account}` (month debits/credits) · `OPENING#{account}` (once-only opening balance marker) | chart of accounts: one Query · balance: one GetItem · **DRE for a period: one range Query** `SUMMARY#2026-01` … `SUMMARY#2026-12` · cash flow and statements: one range Query per cash account on its entries (a cash account's summary mixes transfers and opening balances with cash movement) |
| `ledger_transactions` | `S` → `TX#{ulid}` (immutable header with legs embedded, origin, `adjusts`, memo, ref) · `S#ACCOUNT#{id}` → `ENTRY#{date}#{tx}#{leg}` (amount, kind, flow, memo, ref, reversal) · `S` → `REVERSAL#{tx}` marker | account statement by date range · trace from an entry to the fact that produced it and back |
| `bills` | `S` → `BILL#{ulid}` · `OCCURRENCE#{recurrence}#{date}` (materialisation lock) | payables/receivables · sparse `open-index` (`S#{direction}` → due date), present only while `forecast`, so "a pagar", "a receber" and "vencidos" never read history · `schedule-index` for auto-settle |
| `recurrences` | `S` → `RECURRENCE#{id}` | `schedule-index` (`{mode}#finance-materialize#{date}`) points at the next materialisation |
| `cards` | `S` → `CARD#{id}` (closing/due day, paying account, open month, version, close schedule key) · `S#CARD#{id}` → `PURCHASE#{id}` (installments embedded) · `STATEMENT#{yyyy-mm}` (written on close: frozen total, bill) · `STATEMENT#{yyyy-mm}#ITEM#{purchase}#{n}` | one statement with all its items: one prefix Query; an open statement's total is the sum of its items (no stored total, so a purchase never touches more than its own rows) · `schedule-index` (`{mode}#finance-close`) for closing |
| `imports` | `S` → `IMPORT#{id}` (TTL 90 days) · `S#IMPORT#{id}` → `LINE#{n}` (its lines, TTL 90 days) · `S` → `FITID#{account}#{key}` (lock, no TTL; key `F:{fitid}` or `H:{hash}`, 6.6) · `S` → `CSVMAP#{account}` (CSV columns) | import idempotency by conditional write; listing imports never reads lines |

`audit` and `idempotency` are the existing tables.

**Writing a fact is one `TransactWriteItems`:**
- the transaction header and its N entries;
- an `ADD` on each touched `balance` and `SUMMARY`. `ADD` is commutative, so no version check is
  needed;
- the bill change, if any;
- the audit row.

A typical fact is 2–4 legs and about 12 items, far below the 100-item limit.

## 5. Jobs

One binary, **`cmd/finance`**, daily, on the leader instance like the others, with the same
`@reboot` catch-up entry. Its three steps are each idempotent by construction:

1. **Materialise:** recurrences whose next date falls inside the horizon → bills, guarded by the
   `OCCURRENCE#` lock.
2. **Auto-settle:** `auto_settle` bills due today or earlier and still `forecast` → settlement on
   the due date. Choosing or retargeting `auto_settle` needs `finance.settle`, because the job then
   moves cash on the user's behalf.
3. **Close statements:** card statements whose closing day has arrived → frozen total, statement
   bill.

Runs both modes, takes `-date` so a missed day is re-runnable, and reports failure through the
`internal/jobs` shapes to the alerts topic. `schedule-index` is the only cross-tenant read and it
has no HTTP surface (ADR 0002). It is one partition per mode and job (`{mode}#finance-materialize`,
`{mode}#finance-autosettle`) with the sort key `{date}#{space owner}#{id}`, so the job reads "everything due
on or before today" and a missed day is caught up by the next run.

The rendered userdata was at ~12 KB of a 16 KiB ceiling. One more crontab line fits; if it does not,
the bootstrap moves to an S3 asset as PLAN.md already anticipates.

## 6. API

Under `/v1.0/console/finance/*`:
- the mode comes from the existing header, the space from `X-Billing-Space`;
- every mutating route goes through the existing idempotency middleware;
- the OAuth scopes are `billing:finance:read` and `billing:finance:write`, added to
  `scope-manifest.json` and `middleware.AllScopes`.

As elsewhere in the console, holding a scope is not permission: every route resolves the space and
the caller's verbs.

**Partial updates (UX batch 4).** Every edit body (`PATCH /bills/:id`, `/recurrences/:id`,
`/cards/:id`, and `PUT /settings/default-receiving-account`) reads each field in three states
(`patch.Optional`): **absent keeps** what is stored, **an explicit JSON `null` clears** an optional
field, a value replaces it. Optional fields: a recurrence's `end` and `description`, a bill's
`description`, a card's `brand` and `last4` (an empty string still clears these two, as in batch 3),
the space's default receiving account. `null` on a required field (amount, category, account, due
date, closing/due day, paying account, `auto_settle`) is a 422 `required` naming it, never "keep".
A cleared value is removed from the row, not stored as `""`. Clearing a recurrence's end, or moving
it later, re-opens the rule **from the current horizon**: when its cursor lies before the first day
of the current month (the old end passed long ago), the cursor moves forward to the day before it,
so the job makes only this month's and later occurrences. The dates between the old end and this
month are not made, so nothing is back-dated and nothing is auto-settled for them. The cursor never
moves back, so nothing already made is made again (and the `OCCURRENCE#` lock would refuse it). A
rule still running is untouched. Clearing the end does not unarchive an archived rule. The console
says so before saving ("continua a partir deste mês"). Accounts and categories
have no edit route (only create and archive), so they have nothing to clear.

### 6.1 Verbs (ADR 0023: reach in ctech-account, verbs in the product)

| Verb | Allows |
|---|---|
| `finance.read` | every screen |
| `finance.write` | bills, recurrences, card purchases, transfers, reversals |
| `finance.settle` | settlement |
| `finance.import` | upload and reconciliation |
| `finance.configure` | accounts, cards, categories, default receiving account |

v1 derives verbs **directly from the organization role** in ctech-account, with no billing-side
role table until somebody needs a finer grant:

| Role | Verbs |
|---|---|
| `owner`, `admin` | all |
| `member` | all but `finance.configure` |
| `viewer` | `finance.read` |

In the personal space, the user holds every verb.

## 7. Console

The console no longer requires an organization: every signed-in user has a personal space, so the
portal's "Console" link shows for everyone.

The space selector at the top lists *Pessoal* first, then the user's organizations from billing's
`GET /console/finance/spaces`, which reads them from ctech-account's service route
`/internal/users/:user_id/organizations` (`GET /v1.0/organizations` is first-party-only; ADR 0025,
amendment 2026-10-08).
- In the personal space the console shows **Finanças** only.
- Invoices, subscriptions, customers and catalogue appear in organization spaces.

| | Screen | Answers |
|---|---|---|
| F1 | Resumo | balance per account; realised result this month (by cash, from the entries' flows); projection 3–6 months (realised + forecast + virtual occurrences, forecast visibly distinct); overdue first |
| F2 | A pagar / a receber | `open-index` list: overdue, today, upcoming; settle (full or different amount); cancel |
| F3 | Contas e extrato | statement per account and period with running balance; transfer; reverse an entry |
| F4 | Recorrências | list and expression editor with a **preview of the next occurrences before saving**; auto-settle toggle |
| F5 | Cartões | current and future statements, purchases and installments, close/pay, refund, advance |
| F6 | Importar | upload OFX/CSV → reconciliation: matched, new, ignored |
| F7 | Relatórios | **DRE (competência)** and **Fluxo de caixa (caixa)** as tabs, by period, with one line explaining why they differ |
| F8 | Configuração | accounts, cards, categories by DRE group (archive, never delete), default receiving account |

The required states of assessment § 15 apply to every screen. Visual design goes through
`/impeccable` at implementation, on `@aoctech/ui`, in the console's compact density.

## 8. Retention and deletion (ADR 0026)

- Finance rows carry **no TTL**. They live as long as their space.
- **Personal space** data is purged when the CTech account is deleted. ctech-account's
  account-deletion spec (in progress) must emit the trigger. **Cross-repo dependency, owner:
  Artur.**
- **Organization space** data survives a member leaving and is purged when the organization is
  closed.
- Import lines: TTL 90 days after processing. Raw files: never stored.

## 9. Delivery phases

Each phase ends with something demoable.

1. **Pure domain:**
   - temporal expressions, including `WorkdayOfMonth`;
   - double entry and posting rules;
   - installment allocation.

   Table-tested, no I/O.
2. **Persistence and space:**
   - resolver, verbs and the spoofing tests;
   - ledger accounts and transactions;
   - the rebuild command.
3. **Bills and recurrences:** `cmd/finance` (materialise, auto-settle) and screens F1 (basic), F2,
   F4, F8.
4. **Reports:** F3 and F7.
5. **Cards:** F5 and statement closing in the job.
6. **Import and reconciliation:** F6.
7. **Billing integration:** § 3.8.

## 10. Invariants and tests

| Invariant | Test |
|---|---|
| Legs sum to zero or nothing is written | domain property test + repository refusal test |
| Cache equals derivation | rebuild command on a randomised history compares `balance`/`SUMMARY` |
| Entries are never edited | repository has no update path for `TX#`/`ENTRY#`; reversal test |
| Spaces and modes are isolated | integration tests against real DynamoDB, as `make test-integration` already does for tenants |
| **The selector cannot be spoofed** | user A with B's org id → 404 **and zero table reads**; `personal` ignores any id; a bill id from another space → 404; a removed member loses access when the cache expires; account unreachable → refused |
| Job is re-runnable | run three times → one bill per occurrence, one settlement, one statement close |
| Import is idempotent | same file twice → no new lines |
| Installments sum to the purchase | table test over amounts × installment counts |
| `WorkdayOfMonth` matches `brcal` | table test across holiday months (Carnaval, Corpus Christi, year ends) |

## 11. Out of scope for v1

- consolidated view across spaces;
- user-editable chart of accounts;
- budgets;
- attachments/receipts;
- multi-currency;
- auto-confirmed reconciliation;
- investment valuation (an investment account is cash-in/cash-out only);
- `Union`/`Intersection` expressions;
- billing-side role overlay.

## 12. After v1 (each its own spec)

1. **Plans and entitlements** for billing itself, sold through tenant zero (`owner_key: "billing"`,
   ADR 0021). The payer is decided (§ 1): the space's subscription, managed by its user or by
   the organization's owners and admins. Also decided: the portal's space selector, and the PDF
   naming the designated billing company (or the owner).
2. **BYOK Inter:**
   - The customer's mTLS client certificate is used only for **outbound** calls: a per-tenant
     `http.Transport` and token cache, with the credentials encrypted per space following the
     wallet's per-user Asaas key pattern.
   - Webhooks may not need a gateway or DNS record per customer. Inter presents the same client
     certificate for every account, and the Bacen pattern appends `/pix` to the registered URL, so
     one domain with Inter's CA as truststore could route `…/w/{opaque-id}/pix` with a per-tenant
     secret. **Both facts must be verified against Inter's documentation**; the fallback is a
     wildcard `*.inter.billing.aoctech.app` with records created through the Cloudflare API.
   - Needs PIX with due date (`cobv`) or boleto, which do not exist anywhere in the family today.
   - Credentials are destroyed on disconnect and on owner deletion.
   - Money lands in the customer's own bank, so CTech never intermediates it. This may take scope B
     out from under ADR 0005's payment-arrangement question for this rail; **confirm with the
     lawyer, do not assume.**
3. **CTech Ledger account**: an account whose source is the wallet, reading its events. Blocked on
   Asaas production.
