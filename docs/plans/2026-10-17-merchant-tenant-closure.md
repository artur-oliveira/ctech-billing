# Merchant tenant closure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** An owner can close a merchant tenant: request, e-mail confirmation, a 7-day read-only grace, blockers, then an irreversible purge, with an export offered first.

**Spec:** [`docs/specs/2026-10-17-merchant-tenant-deletion.md`](../specs/2026-10-17-merchant-tenant-deletion.md). Defaults chosen for its open questions (2026-10-17): (1) invoices are erased after the export (the 5-year duty is the taxpayer's); (2) active customer subscriptions **block** (`billing.tenant_active_subscriptions`); (3) export is a job-built zip delivered as a presigned link; (4) closing does **not** unregister webhooks at the bank (R23 limit stays).

## Safety rails (apply to every phase)

- Everything ships **dark**: `TENANT_CLOSURE_ENABLED` is off by default; with it off the routes are not mounted and the worker does nothing.
- Tenant zero (`PORTAL_ORGANIZATION_ID`) can never be closed, in any layer (service, worker, purger).
- The purge has a **dry-run** that reports the keys it would delete, and a test classifies every table (like the finance `notSpaceScoped` map).
- No change to the account-deletion participant: `billing.merchant_tenant` clears by itself once the tenant row is gone.

## Phases (each its own PR, mergeable on its own)

1. **Lifecycle:** table `tenant_closures`, `domain/closure` state machine, repository, service (request, confirm, cancel, blockers, worker step), the read-only gate (M2M and console writes, jobs), config flag. No purge yet: a closure that reaches the end of grace stops at `ready_to_purge`.
2. **Purge:** `TenantPurger` over every table and pointer partition, dry-run, classification test, tombstone, worker step to `closed`.
3. **Surfaces:** console routes (owner only), public confirm/cancel links, e-mails, console UI.
4. **Export:** the zip job and presigned link, offered on the consequences screen.

Phase 1 tasks are in the commit history of `feat/tenant-closure-lifecycle`; phases 2-4 get their task list when their PR starts (they depend on what phase 1 settles).
