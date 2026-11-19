# Billing Pending-Confirmation Reminder (Web Push) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Billing user who chose a time gets one daily Web Push when recurrences await confirmation.

**Architecture:** Same as Poker (`pushsvc` reminders, 15-minute tick, sender Lambda); one reminder kind, `pending`. The tick sums `ListPendingOccurrences` over the user's spaces and sends only when the total is above zero.

**Tech Stack:** Go (Fiber v3), go-common `pushsvc`, Terraform (`terraform/billing`), Next.js (console UI), `@aoctech/push-client`.

**Spec:** `docs/specs/2026-11-18-pending-reminder-web-push.md` (design in `ctech-poker/docs/specs/2026-11-18-scheduled-web-push.md`). Poker plan `ctech-poker/docs/plans/2026-11-18-scheduled-web-push.md` is the reference implementation: copy its structure, not its code.

## Global Constraints

- Load `ctech-dev-standards`. Routes under `/v1.0/`. Infra in Terraform; DynamoDB `server_side_encryption { enabled = false }` explicit (zero-idle policy); no KMS, no SQS, no `Scan`.
- go-common `v1.42.1` to `v1.44.0`; `FAMILY_GOCOMMON_MINOR` v1.44.
- Slots/time: `pushsvc` (BRT, 15 minutes). Kind `pending`. Default disabled until the user picks a time.
- "Pending" means `pending_confirmation` occurrences (`RecurrenceRepository.ListPendingOccurrences`). Card-import pending lines are out of scope until the product owner says otherwise.
- Payload: `"Você tem {N} contas aguardando confirmação"`, no names, no amounts; `navigate` is the pending-confirmation screen path.
- One send per day (`MarkSent`); `Terraform` workspaces per env; apply order table → Lambdas → schedule.

## Review Focus

- Member of several shared spaces: one push with the total, not one per space.
- User removed from a space after choosing a time: that space no longer counts (use the same membership source as the finance routes).
- Pending count of zero: nothing sent and `MarkSent` not consumed, so a recurrence generated later that day can still notify if the tick runs again in the catch-up slot only.
- A space with hundreds of pendings: counting uses `limit` and reports `"mais de 99"` instead of paging without bound.
- User with no device: skipped without querying spaces.

---

### Task 1: Preferences API and dependency

**Files:** Modify `api/go.mod`; Create `api/internal/pendingpush/service.go`, `service_test.go`, `api/internal/api/v1/push.go`, `push_test.go`; wire in `api/internal/app`.

**Interfaces:** Produces `pendingpush.Service` with `Get(ctx, sub) (Preferences, error)` / `Set(ctx, sub, Preferences) (Preferences, error)` (`Preferences{PendingReminder struct{Enabled bool; Time string}}`, JSON `pending_reminder{enabled,time}`); `GET|PUT /v1.0/push/preferences`, `POST /v1.0/push/test`, and `pushsvc.Mount` routes. Same validation as Poker's (`enabled` requires a valid `HH:MM`).

- [ ] **Step 1:** Tests `TestSetRequiresTimeWhenEnabled`, `TestSetRejectsBadTime`, `TestGetDefaultsToDisabled`, `TestPreferencesRequireAuth`. **Step 2:** `cd api && go test ./internal/pendingpush ./internal/api/v1 -run 'Preferences|Set|Get' -v` → FAIL. **Step 3:** Implement; `go get gopkg.aoctech.app/api-commons@v1.44.0 && go mod tidy`. **Step 4:** PASS. **Step 5:** Commit.

### Task 2: Rule and tick

**Files:** Create `api/internal/pendingpush/rules.go`, `rules_test.go`, `api/cmd/push-tick/main.go`, `tick.go`, `tick_test.go`, `api/cmd/push-sender/main.go`.

**Interfaces:** Consumes `RecurrenceRepository.ListPendingOccurrences(ctx, sp space.ResolvedSpace, limit int)`, the user-to-spaces lookup used by the finance routes (find it in `api/internal/services` or `repositories`; reuse, do not add a second one), `pushsvc.ReminderStore`, `Invoker`, `Store.List`. Produces `func (r *Rules) Decide(ctx, now, rem pushsvc.Reminder) (*pushsvc.Job, error)` and `Tick.Run(ctx, now) error` with the same mark-then-invoke order as Poker. `PendingCap = 99`.

- [ ] **Step 1:** Tests `TestDecideSumsAcrossSpaces`, `TestDecideNoPendingReturnsNilAndDoesNotConsumeDay`, `TestDecideIgnoresSpaceUserLeft`, `TestDecideCapsAtNinetyNine`, `TestRunSkipsUserWithoutDevice`, `TestRunOncePerDay`. **Step 2:** FAIL. **Step 3:** Implement (`MarkSent` only after `Decide` returned a job). **Step 4:** PASS `-race`. **Step 5:** Commit.

### Task 3: Terraform

**Files:** Create `terraform/billing/push.tf`, test `terraform/billing/tests/push.tftest.hcl`; Modify `terraform/billing/dynamodb.tf` (table `push` with GSIs `endpoint-index`, `slot-index`, `enabled = false` encryption, TTL), `iam.tf`, `ssm.tf` (parameter paths only; values written by the operator).

**Interfaces:** `aws_lambda_function.push_tick` / `push_sender` (arm64, `provided.al2023`), `aws_scheduler_schedule.push_tick` with `schedule_expression = "cron(0/15 * * * ? *)"` and `schedule_expression_timezone = "America/Sao_Paulo"` (pattern `byok_warm.tf`), scheduler role, least-privilege roles as in Poker's Task 4, API role gets invoke on the sender and table access.

- [ ] **Step 1:** `tftest` (pattern `tests/byok_webhook.tftest.hcl`): schedule expression and timezone, no `aws_kms_key`, table has both GSIs, sender role has no `dynamodb:PutItem` on reminder items. **Step 2:** `cd terraform/billing && terraform test` → FAIL. **Step 3:** Implement. **Step 4:** PASS, `terraform validate`, `terraform plan` shows only additions. **Step 5:** Commit.

### Task 4: Erasure/export and UI

**Files:** Modify `api/internal/` erasure and export code (follow `terraform/billing/erasure.tf` consumers; find the user-data export/erasure service) with tests; Create `ui/src/lib/push/*`, `ui/src/lib/providers/PushProvider.tsx`, `ui/src/components/NotificationSettings.tsx`, `ui/public/sw.js` (script as in DF-e), vitest tests; mount in the console settings screen.

- [ ] **Step 1:** Tests: `TestErasureDeletesPushRows`, `TestExportOmitsEndpoints`; UI as Poker Task 6 (`asks permission only on click`, `time required to enable`, `disabled config hides the screen`). **Step 2:** FAIL. **Step 3:** Implement; UI copy and structure from `ctech-poker/ui` after Poker's Task 6 lands (if that is not merged yet, from `ctech-dfe/ui/src/lib/push/*`). Respect `ui/CLAUDE.md` (no new local copies of `@aoctech/ui` components). **Step 4:** PASS, lint. **Step 5:** Commit.

### Task 5: Turn it on

- [ ] Operator writes the VAPID SSM parameters; `terraform apply` per workspace; confirm the named Deploy job succeeded; enable a device in the console, set the time two slots ahead with one pending recurrence, confirm one push, confirm a second tick sends nothing.
