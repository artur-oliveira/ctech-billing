# ctech-billing — Development Plan

> This is a phased roadmap, not a bite-sized TDD task list — implementation has not started.
> Each phase below should end with something demoable/testable before the next begins.

## Phase 0 — Foundations (infra + skeleton)
- Repo skeleton matching company convention: `cmd/`, `internal/`, `Dockerfile`, `Makefile`,
  `docker-compose.test.yml` (mirror `ctech-wallet/api`'s layout exactly).
- CDK stack under `cdk/`, importing shared VPC/IAM/tagging constructs from `ctech-cdk`
  (do not redefine them — this is the #1 cross-stack duplication risk called out in the
  company-wide audit).
- Confirm datastore choice against `ctech-wallet`'s actual choice (see ARCHITECTURE.md § 1)
  before writing a single migration.
- CI pipeline (lint, unit tests, `cdk synth` on PR) — copy the existing pattern from
  `ctech-wallet` or `ctech-account`'s CI config rather than designing a new one.

## Phase 1 — Domain core (no external integrations)
- `Plan`, `Subscription`, `Invoice`, `UsageRecord`, `CreditNote` models + storage layer.
- Plan versioning (OVERVIEW.md § 7).
- Pro-rata calculator as a pure, unit-tested function (OVERVIEW.md § 6) — this is the highest
  bug-risk logic in the service; get it under test before anything else depends on it.
- Brazilian holiday calculator as a pure, unit-tested function (ARCHITECTURE.md § 4).
- Billing-cycle due-date computation combining both of the above.
- No HTTP API yet — prove the domain logic in isolation first.

## Phase 2 — M2M + user-facing API
- M2M endpoints: create/cancel subscription, report usage, read invoice — authenticated via
  `ctech-account` client-credentials, scoped per `product_key`.
- User-facing endpoints: list/view own subscriptions, cancel own subscription — authenticated
  via `ctech-account` user token.
- Idempotency-key enforcement middleware (OVERVIEW.md § 9.4) — applied once, at the HTTP layer,
  to every mutating route.
- Contract tests against a mocked `ctech-account` token issuer.

## Phase 3 — Invoice generation + Wallet integration
- Scheduled invoice-generation job (ARCHITECTURE.md § 5), idempotent by construction.
- Wallet charge client + webhook receiver (ARCHITECTURE.md § 3) — **blocked on confirming the
  real `ctech-wallet` charge API**, do this phase only after that contract is settled with
  whoever owns `ctech-wallet`.
- Reconciliation job for missed webhooks.
- End-to-end test: subscribe → invoice generated on correct date → wallet charge → webhook →
  invoice marked PAID. This is the MVP's core demo.

## Phase 4 — Dunning, audit, observability
- Dunning retry policy + auto-cancel on exhaustion (OVERVIEW.md § 9.2).
- Append-only audit log for state transitions (ARCHITECTURE.md § 6).
- Webhook delivery system for downstream consumers (OVERVIEW.md § 9.3).
- Metrics + alarms on scheduler health and charge success rate.

## Phase 5 — First real integration (ctech-dfe)
- Wire `ctech-dfe`'s two example plans (DF-e Basic fixed, DF-e Sob Demanda metered) end to end
  in a staging environment.
- Wire the suggested `invoice.paid → NFS-e emission` call to `ctech-dfe` (OVERVIEW.md § 9.1) —
  this is the feature most likely to surface real business-requirements gaps (tax rules,
  service codes), so do it against a real consumer, not synthetically.

## Explicitly deferred (post-MVP, do not build now)
- Invoice PDF generation/storage.
- Base-fee-plus-overage hybrid plan type.
- Multi-currency.
- Self-serve plan upgrade/downgrade UI.

## Open decisions that block Phase 3 and should be resolved before this plan is executed
1. Confirm `ctech-wallet`'s actual charge/webhook API shape (this doc's § 3 is a proposal).
2. Confirm roll-forward vs roll-backward for holiday/weekend due dates (OVERVIEW.md § 5).
3. Confirm `ctech-wallet`'s datastore choice to decide `ctech-billing`'s own (ARCHITECTURE.md § 1).
