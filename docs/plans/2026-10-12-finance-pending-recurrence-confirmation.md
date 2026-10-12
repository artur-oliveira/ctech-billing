# Finance recurring expenses with amount confirmation

Status: implementation plan. Daily and business-day schedules are implemented; pending confirmation is not yet implemented.

## Goal

Support expected recurring expenses whose final amount varies. An occurrence is a reminder until the person confirms its actual amount. Confirmation creates the bill or card purchase; an estimate must never settle a bill or post a purchase by itself.

## Delivery plan

1. **Extend recurrence rules.** Add daily and business-day expressions to the persisted expression union, Go evaluator, preview API, and UI editor. Business days use `brcal.IsBusinessDay`, so weekends and national holidays are excluded. Keep date exceptions and existing schedule start/end semantics.
2. **Persist pending occurrences.** Store one occurrence row under the recurrence's existing `(recurrence_id, nominal_date)` identity with `state=pending_confirmation`, expected amount, destination and category snapshot. The daily job creates those rows idempotently and advances the recurrence cursor only after each write. Do not create a bill, ledger transaction, or card purchase at this stage.
3. **Project pending estimates.** Include pending occurrence estimates in account projections and the card statement forecast, but expose them as estimates and keep them separate from booked totals. A skipped occurrence removes the estimate from projection.
4. **Confirm or skip.** Add idempotent write routes to confirm with the actual amount and skip an occurrence. Confirmation conditionally replaces the pending row with a bill or purchase and its occurrence lock in one DynamoDB transaction. Check the recurrence target and category are still usable; a concurrent confirm returns the existing result. Confirmation never uses auto-settle.
5. **Expose the workflow.** Show pending occurrences in the recurrence timeline and a finance inbox/agenda. Let the person confirm the suggested amount, edit it, or skip. Support both account and card targets; card confirmation posts installments only after confirmation.
6. **Compatibility and rollout.** Existing fixed recurrences retain current behavior. Deploy backend support before the UI; old clients ignore the new occurrence state. Bound daily job work, retain per-occurrence locks, and add transaction, retry, projection, and cross-space authorization tests before release.

## Data and API contract

- Recurrence gains `amount_mode: fixed | expected`; `expected` requires a positive expected amount and rejects `auto_settle`.
- Occurrence history gains `pending_confirmation`, `expected_amount`, and the destination metadata needed to render the confirmation affordance.
- `POST /recurrences/:id/occurrences/:date/confirm` takes the actual amount and an `Idempotency-Key`; `POST .../skip` records a skipped occurrence, also idempotently.
- Pending occurrence rows stay in the resolved finance space partition and use the existing occurrence key, so retries and duplicate jobs cannot create a second financial event.

## Operational safeguards

- Never write ledger or card statement rows before confirmation.
- Confirmation checks ownership, active destination/category, and recurrence state in the same transaction as booking.
- Preserve the estimate and actual amount in audit metadata; do not rewrite previously confirmed occurrences when the recurrence changes.
- Bound the pending window and provide a way to find overdue confirmations without scanning all finance data.

## Expanded finance UX delivery plan

The following requests share this PR and reuse the same finance-space, authorization, idempotency, and query-cache boundaries.

1. **Action inbox (in progress).** The UI aggregates bounded open-bill previews, recurrence problems, and import summaries in one additional read; links lead to existing editors. Pending recurrence confirmations remain blocked on the occurrence API.
2. **Variable recurrences (planned).** Persist `amount_mode=expected`; the scheduler writes a pending occurrence, never a bill or card purchase. Confirm/skip are atomic and idempotent; projection labels estimates separately from booked totals.
3. **Card forecast (implemented in this PR).** A card-scoped server read computes recurrence estimates for a statement; the UI keeps estimates separate from actual items and loads them only when requested.
4. **Quick entry (partially implemented).** Suggestions use already-loaded bill/purchase history, prefix matching is case-insensitive, and selection fills amount/category/account. Next: keyboard focus/submit shortcuts, preserving accessible form controls.
5. **Configurable alerts (planned).** Persist per-space lead times and channels; job delivery is deduplicated by (space, event, date, channel). Start with in-app/email notifications; browser permission is strictly optional. Requires a trusted user-email source before enabling email delivery.
6. **Monthly review (implemented in this PR).** The focused summary compares the selected complete month with the prior complete month; by default it opens the latest closed month to avoid comparing partial periods.
7. **Undo (partially implemented).** A successful bill settlement offers a 10-second undo using the existing unsettle route. Add transactional undo tokens for cancellations, refunds, and reversals before exposing those actions as reversible.

Do not claim the expanded set is complete until pending occurrences and card forecasts have backend tests, alerts are persisted and deduplicated, and every destructive undo has a server-side compensating action.
