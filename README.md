# ctech-billing

Recurring subscription and metered billing service for the CTech ecosystem.

`ctech-billing` owns the **subscription and invoice domain**: plans, subscriptions, billing
cycles, pro-rata, invoice generation, and dunning. It does not move money itself — every
charge is collected by delegating to [`ctech-wallet`](../ctech-wallet), which owns the ledger
and the PIX/Boleto payment rails.

Status: **planning — no implementation yet.** See [OVERVIEW.md](OVERVIEW.md) for the product
spec, [ARCHITECTURE.md](ARCHITECTURE.md) for the technical design, and [PLAN.md](PLAN.md) for
the phased build plan.

## Relationship to other CTech services

- **ctech-account** — issues the M2M (client-credentials) tokens that authorize external
  services (e.g. `ctech-dfe`) to create invoices, and the user tokens that authorize a
  customer to view/cancel their own subscriptions.
- **ctech-wallet** — executes every charge (debit from balance, or PIX/Boleto collection) and
  is the source of truth for "was this invoice actually paid."
- **ctech-dfe** — first consumer: will create subscriptions/invoices for DF-e plans, and is
  the natural place to auto-emit the NFS-e (service tax invoice) CTech itself owes on every
  paid `ctech-billing` invoice (see OVERVIEW.md § Suggested Features).
- **ctech-poker** — future consumer, likely only for real-money-mode entry fees / rake
  reporting, if that model is adopted.
