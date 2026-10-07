# ADR 0024 — Billing keeps a management ledger, and it is not custody

Status: Accepted (2026-10-07) · Supersedes in part the 2026-08-15 assessment § 14 items 3 and 11 and
§ 20.2 ("billing is not a ledger, not an ERP") · Spec:
[`2026-10-07-finance-erp-design.md`](../specs/2026-10-07-finance-erp-design.md)

## Context

The console is gaining a finance section: payables, receivables, recurrences, credit cards, statement
import, cash flow and an accrual income statement, for organizations and for individuals. That is a
ledger and it is a small ERP. The assessment said billing would be neither, and it said so for a
reason worth keeping: a **custody** ledger in billing would be a second, unregulated copy of what
`ctech-wallet` (now displayed as *CTech Ledger*) holds under its Asaas custody rules.

Three homes were considered:

| Option | Cost |
|---|---|
| Extend `ctech-wallet` | One wallet per `(user, type)` enforced by marker rows; `LoadWallets` returns a fixed `(real, game, sandbox)` tuple; no organizations ("not multi-tenant"); a stored balance that **means custody** and sits under a legal audit. User-typed records in that table would mix "what we hold" with "what you told us". |
| New service | Clean boundary; one more repo, deploy and integration for a feature that lives in the billing console and feeds on `invoice.paid`. |
| **Module in billing** | Same console, same spaces, `invoice.paid` in-process. Needs this ADR. |

## Decision

**Billing keeps a management ledger, as its own module, with internal double entry.** It records
money held elsewhere and moves none.

- The § 9 test of the assessment still decides the line: *can the holder turn this into money on
  their account?* Here, no, so it is not wallet's.
- **Double entry is internal and invisible.** Users see accounts and categories; under them are
  ledger accounts whose transactions sum to zero, after Fowler's *Accounting Patterns*. It is what
  makes credit-card installments, accrual vs cash, and transfers fall out of one invariant instead
  of three special cases.
- **Entries are immutable.** Corrections are reversal or replacement adjustments, which is the
  same philosophy as `CreditNote`.
- **No balance here is ever presented as money CTech holds.** An account backed by CTech Ledger
  (later) *reads* wallet; wallet remains the record of what moved.
- The vocabulary avoids "carteira", "wallet" and "banco" for the same reason as the CTech Ledger
  rename.

## Consequences

- `ledger_*` tables in billing, beside invoices. The assessment's warning sign — "a table with a
  customer balance column means the project failed" — now needs its qualifier: *a custody balance*.
  Code review should refuse any path where a finance balance authorises a payment, a withdrawal or a
  charge.
- OVERVIEW.md's purpose grows a second line: billing is also where a space sees its finances.

## Limits accepted

- Two ledgers in the family with different meanings. The name of each table says which one it is
  (`wallet_ledger_entries` vs `billing_ledger_transactions`), and this ADR is where the difference is
  argued.

## Reopen if

A finance feature needs to **move** money (pay a bill from the platform, sweep a balance). That is
custody or payment initiation, and it belongs to wallet or to a BYOK rail with its own ADR, not to
this module.
