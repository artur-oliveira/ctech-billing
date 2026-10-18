# Category month report (realised, scheduled, forecast)

`GET /v1/finance/reports/category-month?month=YYYY-MM` (read, one month, 23 back .. 11 ahead of today).

Per category, income and expense apart, magnitudes (an expense of R$ 10 is +1000):

| Field       | Meaning |
|-------------|---------|
| `realised`  | recognised in the ledger and not waiting on a settlement (settled bills, card purchases, imports) |
| `scheduled` | open bills competent in the month (owed, not paid) |
| `forecast`  | recurrence occurrences nominal in the month that the job has not materialised, card recurrences included (whole amount, in the purchase month, as the accrual DRE does) plus pending-confirmation occurrences |
| `total`     | the three added: what the month is expected to close at |

Basis is accrual (competence / nominal date), like the DRE, not due date like the projection.

## How it is computed (no new reads)

- The DRE's cached `SUMMARY` rows of the month (one range Query) already hold every bill at its competence month, paid or open, because creating a bill recognises it. `scheduled` is carved out of that figure, not added to it: `realised = ledger - open`.
- Open bills come from the sparse open index, paged as the projection does (statement bills are skipped: not recognised).
- `forecast` reads the recurrences with cursors and the pending occurrences, as `Project` does, but only the target month is expanded (`Schedule.Occurrences(max(cursor+1, month start), month end)`), so cost does not grow with the window.
- Nothing per category, no N+1.

ponytail: the open index is ordered by due date; competence cannot narrow it. Add a competence-ordered index if a space holds thousands of open bills.

Code: `domain/finance/category_month.go` (fold), `services.FinanceJobs.MonthFlows` (open and forecast lines), `api/v1/finance_reports.go` (`categoryMonth`).
