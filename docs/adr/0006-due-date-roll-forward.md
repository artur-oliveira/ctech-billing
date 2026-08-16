# ADR 0006 — Weekend/holiday due dates roll **forward**

Status: Accepted (2026-08-15) · Records decision **D5** · Closes the question left open in
`OVERVIEW.md:127-130`

## Context

A due date landing on a Saturday, Sunday or national holiday is a day the customer cannot be
expected to pay. The spec left roll-forward vs roll-backward undecided.

Roll-backward charges the customer earlier than the contract says. Roll-forward gives them the
next day they can actually act on.

## Decision

**Roll forward to the next business day.** National holidays only.

The holiday calendar is a **pure function, not a maintained table**: eight fixed-date national
holidays plus moveable feasts derived from Easter (Gauss/Meeus/Butcher), namely Carnival
(Easter − 48 and − 47), Good Friday (Easter − 2) and Corpus Christi (Easter + 60). Tests check
against a table of known Easter dates; production code computes.

Municipal and state holidays are **out** — that is a per-customer-location feature nobody has asked
for, and adding it silently would make due dates depend on address data billing does not hold.

## Consequences

- **The dunning clock starts from the adjusted date**, never the original. Otherwise the customer
  is late for a day on which they could not pay.
- **Roll-forward may push a due date into the following month** (31/12 landing on a holiday). This
  is allowed and it does **not** move the invoice's accrual period: `period_start` and `period_end`
  are computed *before* the adjustment and never move. Without that rule, twelve annual cycles
  silently become eleven or thirteen.
- "Today" is decided in `America/Sao_Paulo`; timestamps are stored in UTC.

## Test cases that are mandatory, not optional

Carnival, Good Friday, Corpus Christi, month boundary, year boundary, and a holiday adjacent to a
weekend (Friday holiday → due Monday).

## Limits accepted

National-only means a customer in a city with a big local holiday may get a due date they cannot
act on locally. Accepted: PIX settles on weekends and holidays anyway; this rule protects the
*expectation*, and adding municipal calendars would require storing addresses we chose not to keep.

## Reopen if

A customer segment is materially harmed by a municipal holiday. The fix would be a per-organization
calendar, not a global one.
