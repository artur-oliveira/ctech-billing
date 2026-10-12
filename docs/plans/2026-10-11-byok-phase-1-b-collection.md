# BYOK phase 1, part B: due-date Pix collection Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Amendment 2026-10-11 — no legal gate (owner decision; spec amendment "no legal gate").** This plan was written
> with a legal gate; the gate is withdrawn. Where the text below says otherwise, this note wins:
>
> - **No `BYOK_LIVE_COLLECTION` / `BYOK_LIVE_PROVIDERS`, no `config.ByokCleared`, no ADR 0030.** Part A removed them from
>   config, Terraform and the outbound Lambda; the Lambda accepts every known op (its allowlist is a security control
>   only: unknown op/provider/mode refused, `SimulatePayment` sandbox-only).
> - **Task 3:** no `PayoutPendingClearance`, no `ErrByokNotCleared` / `byok_collection_not_cleared`, no `ChargeGate.Cleared`,
>   no `cleared` argument to `NewChargeGates`. Drop the table rows "byok pending clearance" and "byok live flag off" and
>   `TestRemovingTheProviderStopsCollectionAtOnce`; keep connection, scopes, certificate, entitlement and quota.
> - **Tasks 5, 6, 8, 13:** `newCollectEnv(t, collectLive)` has no `clearedProviders(...)` argument and no `setCleared`.
> - **Task 10:** `UseForCollection` sets a live tenant straight to `payout_rail: byok`, `payout_provider`, `enabled`
>   and registers the webhook at once (live as test); there is no `PromoteCleared`.
> - **Task 11:** `cmd/byok` has no promotion step (spec § 5 step 5 is gone); drop `TestPromotionWaitsForClearance`.
> - **Task 15, deploy step 7 ("Live clearance")** is dropped: live collection is available once part B ships.
> - **Review Focus 3** becomes: an unknown op, provider or mode sent straight to the Lambda never reaches a bank
>   (part A `TestLiveCollectOpsReachTheProviderAndUnknownOpsAreRefused`).
> - **Spec coverage:** "§ 10 legal gate" and the `pending_clearance` rows no longer apply.

**Goal:** A billing tenant whose organization has a collection-capable connection collects its invoices by Pix with due date (Inter `cobv`) into its own bank account: the charge is created at finalization, confirmed only after re-querying the provider (webhook, hourly sweep or statement line), excess payments are flagged and refunded only by the merchant, and live collection stays refused until the provider is cleared (`BYOK_LIVE_COLLECTION` + `BYOK_LIVE_PROVIDERS`). Test mode collects against Inter's sandbox through CTech's own integration.

**Architecture:** `Organization` gains `payout_rail` (`wallet` default, `byok`) and `payout_provider`; `AuthorizeCharge` stays the single gate and now takes a `ChargeGate` (connection facts, clearance, entitlement, quota) assembled by one builder. `services.ByokCollector` creates, updates, cancels and confirms charges through Part A's `byokrpc.Session`; `PaymentAttempt` gains `byok` fields and a `CHARGEREF#` pointer written in the attempt's transaction. Webhooks arrive at one mTLS host (`billing-byok-hooks[-env].aoctech.app`, API Gateway HTTP API, regional, truststore with Inter's CA), a credential-less webhook Lambda parses references and calls `POST /v1.0/internal/byok/notify` with an M2M token from a new ctech-account client. `cmd/byok` gains the sweep, the daily webhook audit, promotion and the callback-retry tool. Nothing on this rail calls the wallet client.

**Tech Stack:** as part A, plus API Gateway v2 (Terraform `aws_apigatewayv2_*`), S3 (versioned truststore), ctech-account `cmd/createclient`.

**Spec:** [`docs/specs/2026-10-11-byok-inter-phase-1.md`](../specs/2026-10-11-byok-inter-phase-1.md) §§ 3.6 (steps 1, 2, 4), 5 (steps 1, 4, 5, retry tool), 6, 7, 8.4, 9 (collection side), 10, 11.2–11.3 (console), 12.1 (collection routes, notify), 12.2 (Hook, CHARGEREF, attempt, excess, invoice settings), 14, 16 steps 2, 3, 5, 6, 17 items 1, 2, 3, 5, 6, 9, 10, 12. Requires [part A](2026-10-11-byok-phase-1-a-connection-and-statement.md) merged (its Interfaces are consumed by name). Part C (quotas, usage) plugs into this part's `ChargeGate` builder and `cmd/byok` registry.

## Scope decisions (read before executing)

1. **`AuthorizeCharge(g ChargeGate) error` replaces `AuthorizeCharge()`.** One function, one argument that carries every fact the BYOK rail needs; the wallet rail ignores it. Every caller builds the argument through `services.ChargeGates.For(ctx, org)`, so no path can forget a fact. The console's `can_charge` uses the same builder.
2. **The charge is created after `Finalize` commits, outside its transaction**, by `Invoicer` calling `ChargeCreator.Issued(ctx, inv)` (nil-safe hook). A failure leaves the invoice issued and a `PENDING` attempt with no artefact (spec § 7.2); checkout and the sweep retry. The attempt row and its `CHARGEREF#` pointer are written **before** the provider call (the txid is ours), unlike the wallet rail (wallet returns the id): a crash after the provider created the charge leaves a row that the retry completes with an idempotent `PUT` + `GET`.
3. **Late fees are configured per tenant and copied to the invoice at finalization**, in basis points: `fine_bp` (multa, % once) and `monthly_interest_bp` (juros, % per month, calendar days). Inputs in the console are percentages with two decimals; the wire is basis points.
4. **`validadeAposVencimento` = the last dunning step's offset (days after the due date), at least 1.** The invoice's copied schedule decides how long billing keeps chasing; the Pix stays payable exactly as long.
5. **An excess payment is a row, never a refund** (D19). `EXCESS#{payment_id}` under the invoice (`TableInvoices`), with the amount above `amount_due + late charges` or the whole amount for a second payment; console notice + e-mail; "Devolver" is the merchant's explicit act.
6. **Finanças, issuer side, for the BYOK rail**: the paid invoice posts with the cash leg on the collecting provider's BYOK account (`InvoiceFact.AccountID`), ref = payment id, plus the late charge as a second line on "Juros e multas" (income side: the seeded `juros-multas` is an expense category, so this plan seeds `juros-multas-recebidos` "Juros e multas recebidos", income, financial result). The statement line then links to that transaction instead of posting (spec § 8.4).
7. **The notify route is not behind the tenant resolver.** It is `RequireM2MScope("internal:billing:byok-notify")` only, and the hook row decides the tenant. Token audience is billing's (`internal:billing` resolves to `https://billing.aoctech.app` in ctech-account's catalog once the manifest is published).
8. **Webhook secrets** are 32 random bytes, base64url, stored as SHA-256 hex; rotation keeps the previous hash 24 h. The sandbox hook id is the literal `sandbox` (one per environment), its row created by `cmd/byok` on first run in test mode.
9. **Cost (owner rule):** API Gateway HTTP API (US$ 1/M) and a 128 MB Lambda only on payment notices; no REST API, no WAF, no NAT. The truststore bucket is one object. See part A's "Custos de infra".

## Global Constraints

- All of part A's Global Constraints apply (commands, UI rules, no em dash, no attribution, cost rule, never Go and UI suites at once).
- Routes, exactly: `PUT /v1.0/console/collection`, `POST /v1.0/console/invoices/:id/charge`, `POST /v1.0/console/invoices/:id/simulate-payment` (test only), `POST /v1.0/console/excess-payments/:id/refund`, `POST /v1.0/console/connections/{provider}/webhook` `{replace_existing}`, `DELETE /v1.0/console/connections/{provider}` `{cancel_open_charges}`, `POST /v1.0/internal/byok/notify`. Webhook paths: `POST /w/{provider}/{hook_id}/{secret}/pix` and `POST /w/{provider}/{hook_id}/{secret}/pix/pix`.
- Hosts: `billing-byok-hooks.aoctech.app` (prod), `billing-byok-hooks-{env}.aoctech.app` (dev, stage); Cloudflare record **DNS-only**.
- New problem codes: `byok_collection_not_cleared` (409), `byok_not_connected` (409), `byok_collection_scopes_missing` (409), `byok_open_charges` (409, with `count`), `byok_webhook_conflict` (409, with `existing_host`), `customer_tax_id_required` (422), `refund_window_closed` (422), `byok_quota_exceeded` and `subscription_limit_reached` (part C).
- `payout_status` values: `not_configured | pending_custody | pending_clearance | enabled`. `payout_rail`: `"" | wallet | byok` (`""` reads `wallet`). `payout_provider`: `inter`.
- Inter txid: `"ctb" + attempt ULID` (29 chars). Refund id: `"rf" + ULID` (28 chars).
- Scope: `internal:billing:byok-notify`, `visibility: internal`, never `m2m`. Client: `billing-byok-webhook`; SSM `/ctech-billing/{env}/byok-webhook/client-id` (String) and `/client-secret` (SecureString).
- No code path on the BYOK rail calls `internal/wallet` (Task 13 pins it).

## Review Focus

1. **A webhook body claiming payment for a charge that is still open, or for another organization's charge, or arriving at another connection's hook with a valid reference of this one**: nothing is settled; only `GetCharge` with the hook organization's own credentials decides. Task 8 `TestNoWebhookPayloadSettlesAnything`.
2. **Webhook, sweep and statement line waking the same paid charge at the same moment**: one `SUCCEEDED`, one invoice `PAID`, one Finanças cash entry. Task 6 `TestThreeConcurrentWakeUpsSettleOnce`, Task 7 `TestAPaidInvoiceAndItsLineMakeOneCashEntryWhicheverArrivesFirst`.
3. **The provider removed from `BYOK_LIVE_PROVIDERS` after a tenant is `enabled`**: the next finalization creates no charge (`byok_collection_not_cleared`), and the Lambda refuses a direct call. Task 3 `TestRemovingTheProviderStopsCollectionAtOnce`.
4. **Two payments on one `cobv` (two people scanned the QR) and one payment above the amount with late fees**: first settles the invoice, the second is an excess row; the late fee within the computed amount is not excess. Task 6 `TestExcessAndLateChargesAreToldApart`.
5. **Disconnect while charges are open, and while the merchant's ERP owns the Pix webhook**: refused without `cancel_open_charges`; with it, charges removed and attempts failed before credentials go; a foreign webhook URL is never deleted. Task 10 `TestDisconnectOrderAndForeignWebhook`.

---

## File Structure

| Repo / file | Responsibility |
|---|---|
| ctech-account `api/cmd/createclient/create-billing-byok-webhook-client.sh` | create the webhook Lambda's M2M client |
| `api/internal/domain/billing/organization.go`, `charge_gate.go` | rail, provider, `pending_clearance`, `ChargeGate`, `AuthorizeCharge` |
| `api/internal/domain/billing/payment.go`, `collection.go` | `MethodBYOK`, attempt fields, `CollectionSettings`, `ExcessPayment`, txid |
| `api/internal/repositories/payments.go`, `byok.go`, `organizations.go`, `invoices.go` | attempt + CHARGEREF, hooks, excess rows, rail/settings writes |
| `api/internal/services/charge_gates.go` | the single `ChargeGate` builder |
| `api/internal/services/byok_collector.go`, `byok_confirm.go`, `byok_refunds.go`, `byok_webhooks.go`, `byok_disconnect.go` | collection, confirmation, refunds, hooks, disconnect |
| `api/internal/services/collecting.go`, `invoicing.go`, `finance_invoices.go` | rail dispatch, `Issued` hook, BYOK posting |
| `api/cmd/byok-webhook/` | webhook Lambda |
| `api/internal/api/v1/byok_notify.go`, `console_collection.go` | routes |
| `terraform/billing/byok_webhook.tf` | API Gateway, domain, truststore, webhook Lambda |
| `ui/src/components/collection/*`, console invoice detail | collection settings, Pix on invoice, excess, simulate, webhook conflict |

---

### Task 1: ctech-account — the `billing-byok-webhook` client (no code; script only)

**Repo:** ctech-account, branch `chore/billing-byok-webhook-client`. Existing pattern: `api/cmd/createclient` (creates an M2M client and writes its id/secret to SSM with `Overwrite=false`) and `api/cmd/createclient/grant-billing-oauth-client-read.sh`. The scope `internal:billing:byok-notify` is **billing's** (published by billing's manifest in Task 2's deploy); ctech-account's `createclient` validates requested scopes against the published catalog, so this script runs only after billing's manifest publish reached the environment.

**Files:**
- Create: `api/cmd/createclient/create-billing-byok-webhook-client.sh`

- [ ] **Step 1: Write the script**

```bash
#!/usr/bin/env bash
# Creates ctech-billing's BYOK webhook Lambda client (client_credentials, scope
# internal:billing:byok-notify) in one environment and stores its id and secret
# in billing's SSM namespace, read only by that Lambda.
# Runbook: ctech-billing docs/runbooks/byok.md § Webhook client.
#
#   AWS_PROFILE=ctech api/cmd/createclient/create-billing-byok-webhook-client.sh prod
#
# Run only after ctech-billing's scope manifest with internal:billing:byok-notify
# is published in that environment. Never prints the secret (it goes straight
# to SSM). Re-running fails at create (the client exists): that is the guard.
set -euo pipefail
ENV="${1:?usage: $0 <dev|stage|prod>}"
case "$ENV" in dev | stage | prod) ;; *) echo "unknown environment: $ENV" >&2; exit 2 ;; esac
cd "$(dirname "$0")/../.."
unset VALKEY_URL
AWS_REGION=us-east-1 TABLE_PREFIX="$ENV" go run ./cmd/createclient \
  -client-id "billing-byok-webhook-$ENV" \
  -name "CTech Billing BYOK webhook ($ENV)" \
  -scopes "internal:billing:byok-notify" \
  -ssm-path-client "/ctech-billing/$ENV/byok-webhook/client-id" \
  -ssm-path-secret "/ctech-billing/$ENV/byok-webhook/client-secret"
```

- [ ] **Step 2: Check it parses and that createclient accepts the flags**

Run: `bash -n api/cmd/createclient/create-billing-byok-webhook-client.sh && go run ./api/cmd/createclient -h 2>&1 | grep -c ssm-path`
Expected: no syntax error; `2`.

- [ ] **Step 3: Commit and PR**

```bash
chmod +x api/cmd/createclient/create-billing-byok-webhook-client.sh
git add api/cmd/createclient/create-billing-byok-webhook-client.sh
git commit -m "chore: script to create ctech-billing's BYOK webhook client"
gh pr create --title "chore: create ctech-billing's BYOK webhook client" --body "Operator script, same pattern as grant-billing-oauth-client-read.sh. Run per environment after billing publishes internal:billing:byok-notify."
```

No code change in ctech-account is needed: `createclient` already writes both SSM parameters and validates the scope against the catalog.

### Task 2: billing — the notify scope in the manifest

**Files:**
- Modify: `api/internal/oauthresource/scope-manifest.json`, `api/internal/middleware/scope.go`
- Test: `api/internal/oauthresource/resource_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestTheByokNotifyScopeIsInternalAndNeverMachineGrantable(t *testing.T) {
	m, err := ManifestDocument()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range m.Scopes {
		if s.Name == "internal:billing:byok-notify" {
			if s.Visibility != "internal" || s.M2M || s.Descriptions["pt-BR"] == "" || s.Descriptions["en"] == "" {
				t.Fatalf("%+v", s)
			}
			return
		}
	}
	t.Fatal("internal:billing:byok-notify missing")
}
```

- [ ] **Step 2: Run it** — `go test ./internal/oauthresource/ -run ByokNotify` → FAIL, missing.

- [ ] **Step 3: Add the scope**

```json
    {
      "name": "internal:billing:byok-notify",
      "descriptions": {
        "pt-BR": "Avisar o CTech Billing de um pagamento recebido por uma conexão bancária (uso interno).",
        "en": "Notify CTech Billing of a payment received through a bank connection (internal use)."
      },
      "visibility": "internal",
      "status": "active"
    }
```

and `ScopeByokNotify = "internal:billing:byok-notify"` in `scope.go` (not in `AllScopes`: no user or merchant client may hold it; the manifest is the list ctech-account knows).

- [ ] **Step 4: Run** `go test ./internal/oauthresource/ ./internal/middleware/ -race -count=1` → `ok`.

- [ ] **Step 5: Commit** `git commit -am "feat(byok): internal notify scope for the webhook Lambda"`.

### Task 3: Domain — the rail, `pending_clearance` and `AuthorizeCharge(ChargeGate)`

**Files:**
- Modify: `api/internal/domain/billing/organization.go`, `api/internal/services/collecting.go:196`, `api/internal/api/v1/console_settings.go:109`, `api/internal/provision/plan.go:166`, `api/internal/problem/problem.go`
- Create: `api/internal/domain/billing/charge_gate.go`, `api/internal/services/charge_gates.go`
- Test: `api/internal/domain/billing/charge_gate_test.go`, `api/tests/integration/byok_gate_test.go`

**Interfaces:**
- Produces:

```go
type PayoutRail string // RailWallet "wallet", RailBYOK "byok"
const PayoutPendingClearance PayoutStatus = "pending_clearance"
// Organization gains: PayoutRail PayoutRail `dynamodbav:"payout_rail,omitempty" json:"payout_rail,omitempty"`; PayoutProvider string `dynamodbav:"payout_provider,omitempty" json:"payout_provider,omitempty"`; Collection CollectionSettings `dynamodbav:"collection,omitempty" json:"collection,omitempty"`
func (o *Organization) Rail() PayoutRail // "" → wallet
type ConnectionFacts struct{ Found, Active, Expired, Collection bool }
type ChargeGate struct{ Connection ConnectionFacts; Cleared bool; Entitled bool; QuotaExceeded bool }
var ErrByokNotConnected, ErrByokCertificateExpired, ErrByokScopesMissing, ErrByokNotCleared, ErrByokQuotaExceeded error
func (o *Organization) AuthorizeCharge(g ChargeGate) error
// services
type ChargeGates struct{ /* byok repo, cfg cleared func, entitlement func, quota func */ }
func NewChargeGates(repo *repositories.ByokRepository, cleared func(string) bool, entitled func(ctx context.Context, org *billing.Organization) (bool, error)) *ChargeGates
func (g *ChargeGates) WithQuota(q func(ctx context.Context, org *billing.Organization) (exceeded bool, err error)) *ChargeGates // part C
func (g *ChargeGates) For(ctx context.Context, org *billing.Organization) (billing.ChargeGate, error)
```

- [ ] **Step 1: Write the failing table test** (spec § 17 item 3)

```go
package billing

import (
	"errors"
	"testing"
)

func TestAuthorizeChargeIsOneTableOverEveryFact(t *testing.T) {
	ok := ChargeGate{Connection: ConnectionFacts{Found: true, Active: true, Collection: true}, Cleared: true, Entitled: true}
	cases := []struct {
		name string
		org  Organization
		g    ChargeGate
		want error
	}{
		{"wallet enabled", Organization{PayoutStatus: PayoutEnabled}, ChargeGate{}, nil},
		{"wallet not enabled", Organization{PayoutStatus: PayoutPendingCustody}, ChargeGate{}, ErrPayoutNotEnabled},
		{"byok live ok", Organization{Livemode: true, PayoutRail: RailBYOK, PayoutProvider: "inter", PayoutStatus: PayoutEnabled}, ok, nil},
		{"byok pending clearance", Organization{Livemode: true, PayoutRail: RailBYOK, PayoutStatus: PayoutPendingClearance}, ok, ErrPayoutNotEnabled},
		{"byok no connection", Organization{PayoutRail: RailBYOK, PayoutStatus: PayoutEnabled}, ChargeGate{Cleared: true, Entitled: true}, ErrByokNotConnected},
		{"byok expired", Organization{PayoutRail: RailBYOK, PayoutStatus: PayoutEnabled}, ChargeGate{Connection: ConnectionFacts{Found: true, Expired: true, Collection: true}}, ErrByokCertificateExpired},
		{"byok statement only", Organization{PayoutRail: RailBYOK, PayoutStatus: PayoutEnabled}, ChargeGate{Connection: ConnectionFacts{Found: true, Active: true}}, ErrByokScopesMissing},
		{"byok live flag off", Organization{Livemode: true, PayoutRail: RailBYOK, PayoutStatus: PayoutEnabled}, ChargeGate{Connection: ok.Connection, Entitled: true}, ErrByokNotCleared},
		{"byok test ignores the flag", Organization{PayoutRail: RailBYOK, PayoutStatus: PayoutEnabled}, ChargeGate{Connection: ok.Connection}, nil},
		{"byok live not entitled", Organization{Livemode: true, PayoutRail: RailBYOK, PayoutStatus: PayoutEnabled}, ChargeGate{Connection: ok.Connection, Cleared: true}, ErrPayoutNotEnabled},
		{"byok live suspended", Organization{Livemode: true, PayoutRail: RailBYOK, PayoutStatus: PayoutEnabled, SuspendedAt: "2026-10-01T00:00:00Z"}, ok, ErrTenantSuspended},
		{"byok quota refused", Organization{Livemode: true, PayoutRail: RailBYOK, PayoutStatus: PayoutEnabled}, ChargeGate{Connection: ok.Connection, Cleared: true, Entitled: true, QuotaExceeded: true}, ErrByokQuotaExceeded},
	}
	for _, c := range cases {
		if err := c.org.AuthorizeCharge(c.g); !errors.Is(err, c.want) && !(err == nil && c.want == nil) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
}
```

`api/tests/integration/byok_gate_test.go`:

```go
func TestRemovingTheProviderStopsCollectionAtOnce(t *testing.T) {
	env := newCollectEnv(t, collectLive, clearedProviders("inter")) // enabled BYOK live tenant, fake provider, cleared
	inv := env.finalizeInvoice(t, 9990)
	if a := env.attempt(t, inv); a.ChargeRef == "" {
		t.Fatal("cleared: a charge is created at finalization")
	}
	env.setCleared() // BYOK_LIVE_PROVIDERS empty, same process
	inv2 := env.finalizeInvoice(t, 9990)
	if env.attempts(t, inv2) != 0 {
		t.Fatal("no attempt once the provider is no longer cleared")
	}
	if env.fake.Calls(byok.OpCreateDueCharge) != 1 {
		t.Fatal("the provider saw one creation")
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/domain/billing/ -run AuthorizeCharge` → FAIL, `undefined: ChargeGate`.

- [ ] **Step 3: Implement**

`api/internal/domain/billing/charge_gate.go`:

```go
package billing

import (
	"errors"
	"fmt"
)

type PayoutRail string

const (
	RailWallet PayoutRail = "wallet"
	RailBYOK   PayoutRail = "byok"
)

// PayoutPendingClearance: a BYOK tenant whose provider is not legally cleared
// yet (spec § 7.1). cmd/byok promotes it when the provider is cleared.
const PayoutPendingClearance PayoutStatus = "pending_clearance"

var (
	ErrByokNotConnected       = errors.New("byok: no active connection for the collecting provider")
	ErrByokCertificateExpired = errors.New("byok: the connection's certificate expired")
	ErrByokScopesMissing      = errors.New("byok: the connection lacks the collection scopes")
	ErrByokNotCleared         = errors.New("byok: live collection through this provider is not cleared")
	ErrByokQuotaExceeded      = errors.New("byok: the plan's charge quota is used up")
)

// ConnectionFacts is what the gate needs from the BYOK connection.
type ConnectionFacts struct{ Found, Active, Expired, Collection bool }

// ChargeGate is every fact AuthorizeCharge decides on, built in one place
// (services.ChargeGates.For) so no collection path can leave one out.
type ChargeGate struct {
	Connection    ConnectionFacts
	Cleared       bool // live: BYOK_LIVE_COLLECTION and the provider listed, read at call time
	Entitled      bool // live: an entitling prod_billing subscription (R4)
	QuotaExceeded bool // live: plan with overage "refuse" past quota_charges_month (part C)
}

func (o *Organization) Rail() PayoutRail {
	if o.PayoutRail == "" {
		return RailWallet
	}
	return o.PayoutRail
}
```

`organization.go`: the fields listed in Interfaces, and:

```go
func (o *Organization) AuthorizeCharge(g ChargeGate) error {
	if o.PayoutStatus != PayoutEnabled {
		return fmt.Errorf("%w: payout_status is %q", ErrPayoutNotEnabled, o.PayoutStatus)
	}
	if o.Rail() == RailWallet {
		return nil
	}
	switch {
	case !g.Connection.Found:
		return ErrByokNotConnected
	case g.Connection.Expired:
		return ErrByokCertificateExpired
	case !g.Connection.Active:
		return ErrByokNotConnected
	case !g.Connection.Collection:
		return ErrByokScopesMissing
	}
	if !o.Livemode {
		return nil // test mode reaches the sandbox only (spec § 9)
	}
	switch {
	case !g.Cleared:
		return ErrByokNotCleared
	case o.Suspended():
		return ErrTenantSuspended
	case !g.Entitled:
		return fmt.Errorf("%w: no entitling prod_billing subscription", ErrPayoutNotEnabled)
	case g.QuotaExceeded:
		return ErrByokQuotaExceeded
	}
	return nil
}
```

`api/internal/services/charge_gates.go`:

```go
package services

type ChargeGates struct {
	repo     *repositories.ByokRepository
	cleared  func(string) bool
	entitled func(context.Context, *billing.Organization) (bool, error)
	quota    func(context.Context, *billing.Organization) (bool, error)
}

func NewChargeGates(repo *repositories.ByokRepository, cleared func(string) bool,
	entitled func(context.Context, *billing.Organization) (bool, error)) *ChargeGates {
	return &ChargeGates{repo: repo, cleared: cleared, entitled: entitled}
}

func (g *ChargeGates) WithQuota(q func(context.Context, *billing.Organization) (bool, error)) *ChargeGates {
	g.quota = q
	return g
}

// For reads nothing for a wallet-rail tenant.
func (g *ChargeGates) For(ctx context.Context, org *billing.Organization) (billing.ChargeGate, error) {
	var out billing.ChargeGate
	if org.Rail() != billing.RailBYOK {
		return out, nil
	}
	conn, err := g.repo.GetConnection(ctx, org.AccountOrganizationID, org.Livemode, org.PayoutProvider)
	switch {
	case errors.Is(err, repositories.ErrNotFound):
		return out, nil
	case err != nil:
		return out, err
	}
	out.Connection = billing.ConnectionFacts{Found: true, Active: conn.State == byokconn.StateActive,
		Expired: conn.State == byokconn.StateExpired, Collection: conn.Has(byok.CapabilityCollection)}
	if !org.Livemode {
		return out, nil
	}
	out.Cleared = g.cleared(org.PayoutProvider)
	if out.Entitled, err = g.entitled(ctx, org); err != nil {
		return out, err
	}
	if g.quota != nil {
		if out.QuotaExceeded, err = g.quota(ctx, org); err != nil {
			return out, err
		}
	}
	return out, nil
}
```

`entitled` is `Applications.entitlement` lifted into a function over the organization's `ORG_` customer in tenant zero (`CustomerRepository.GetByOrganization(tenantZero, true, org.AccountOrganizationID)` then `ListByCustomer` + `OwnerKey == MerchantOwnerKey && IsEntitled()`); tenant zero itself is always entitled. Wire `NewChargeGates(byokRepo, cfg.ByokCleared, entitled)` in `app.Build`/`BuildByok`, pass it to `Collector` (`WithGates`) and to the console settings handler.

Update callers: `collecting.go:196` becomes `gate, err := c.gates.For(ctx, org); … org.AuthorizeCharge(gate)` (and `Pay` dispatches by rail, Task 5); `console_settings.go:109` `CanCharge: org.AuthorizeCharge(gate) == nil`; `provision/plan.go:166` accepts `PayoutPendingClearance`. `problem.go`:

```go
	case errors.Is(err, billing.ErrByokNotCleared):
		return Conflict("collection through this provider is not cleared yet").WithCode("byok_collection_not_cleared")
	case errors.Is(err, billing.ErrByokNotConnected):
		return Conflict("no active bank connection for collection").WithCode("byok_not_connected")
	case errors.Is(err, billing.ErrByokCertificateExpired):
		return Conflict("the bank connection's certificate expired").WithCode("byok_certificate_expired")
	case errors.Is(err, billing.ErrByokScopesMissing):
		return Conflict("the bank integration lacks the collection scopes").WithCode("byok_collection_scopes_missing")
	case errors.Is(err, billing.ErrByokQuotaExceeded):
		return Conflict("the plan's charges are used up this month; upgrade to keep collecting").WithCode("byok_quota_exceeded")
```

`OrganizationRepository.SetPayoutRail(ctx, org, rail, provider, status, actor, requestID, now)` (one `StatusChange` on `payout_status` with `Set` of `payout_rail`/`payout_provider`, audit action `payout.rail_changed`, `Before`/`After` filled), used by Tasks 9 and 11.

- [ ] **Step 4: Run** `gofmt -l ./internal && go vet ./... && go test ./... -race -count=1 && make test-integration` → `ok` (the integration test needs Task 5's collector; run it at the end of Task 5 if this task lands first).

- [ ] **Step 5: Commit** `git commit -m "feat(billing): payout rail and provider, pending_clearance, AuthorizeCharge over one ChargeGate"`.

### Task 4: Data — BYOK attempts, the charge pointer, collection settings, excess rows

**Files:**
- Modify: `api/internal/domain/billing/payment.go`, `api/internal/domain/billing/invoice.go` (`Collection CollectionSettings`, copied at finalize), `api/internal/repositories/payments.go`, `api/internal/repositories/rows.go`, `api/internal/repositories/keys.go`, `api/internal/repositories/invoices.go` (`Finalize` copies `org.Collection`), `api/internal/repositories/organizations.go` (`SetCollection`)
- Create: `api/internal/domain/billing/collection.go`
- Test: `api/internal/domain/billing/collection_test.go`, `api/tests/integration/byok_attempts_test.go`

**Interfaces:**

```go
const MethodBYOK PaymentMethod = "byok"
// PaymentAttempt gains: Provider string `provider,omitempty`; Product string `product,omitempty` ("pix_due"); ChargeRef string `charge_ref,omitempty`; Payable string `payable,omitempty`; PayableUntil brcal.Date `payable_until,omitempty`; CheckedAt time.Time `checked_at,omitempty`; PaymentID string `payment_id,omitempty`; LateCharges Cents `late_charges,omitempty`; CreatedAt time.Time `created_at`; CancelledAt time.Time `cancelled_at,omitempty`; UsageReported bool `usage_reported,omitempty`
func InterTxid(attemptID string) string // "ctb" + strings.TrimPrefix(attemptID, "pa_")
type CollectionSettings struct{ FineBasisPoints, MonthlyInterestBasisPoints int } // 0..2000 and 0..1000 (≤ 20 %, ≤ 10 %/month)
func (s CollectionSettings) Validate() error
type ExcessPayment struct{ ID, OrganizationID string; Livemode bool; InvoiceID, AttemptID, PaymentID string; Amount Cents; Status string /* open|refunding|refunded|refund_failed|kept */; RefundID string; PaidAt time.Time }
// repositories
func ChargeRefPK(provider string, livemode bool, ref string) string // "CHARGEREF#{provider}#{mode}#{ref}"
type ChargeRef struct{ TenantID, AccountOrganizationID, InvoiceID, AttemptID string; AttemptNumber int }
func (r *PaymentRepository) CreateByokAttempt(ctx, a *billing.PaymentAttempt, accountOrgID string, now time.Time) error // attempt + CHARGEREF in one tx; schedule keys CHARGE_RECONCILE kept for the sweep
func (r *PaymentRepository) SetByokCharge(ctx, a *billing.PaymentAttempt, payable string, until brcal.Date, now time.Time) error
func (r *PaymentRepository) TouchChecked(ctx, a *billing.PaymentAttempt, now time.Time) error
func (r *PaymentRepository) ChargeRef(ctx, provider string, livemode bool, ref string) (*ChargeRef, error) // ErrNotFound
func (r *PaymentRepository) PendingByok(ctx, livemode bool, opened brcal.Date, limit int, start map[string]types.AttributeValue) (*Page[billing.PaymentAttempt], error) // CHARGE_RECONCILE partition filtered to method byok
func (r *PaymentRepository) RecordExcess(ctx, e *billing.ExcessPayment, now time.Time) (created bool, err error) // EXCESS#{payment_id}, create-only
func (r *PaymentRepository) ListExcess(ctx, orgID string, livemode bool, invoiceID string) ([]billing.ExcessPayment, error)
func (r *PaymentRepository) GetExcess(ctx, orgID string, livemode bool, excessID string) (*billing.ExcessPayment, error) // lookup_pk EXCESS#{id}
func (r *PaymentRepository) SetExcessStatus(ctx, e *billing.ExcessPayment, status, refundID string, now time.Time) error
```

The `CHARGEREF#` row lives in the `byok` table (spec § 12.2); `PaymentRepository` gains `byok Base` for it and writes it in the same `TransactWriteItems` as the attempt (cross-table transactions are allowed). `ByokRepository.ChargeRefOwner` (part A) reads the same row.

- [ ] **Step 1: Write the failing tests**

```go
func TestInterTxidFollowsBacen(t *testing.T) {
	id := "pa_" + "01JABCDEFGHJKMNPQRSTVWXYZ0"
	got := InterTxid(id)
	if len(got) < 26 || len(got) > 35 || !alnum(got) || !strings.HasPrefix(got, "ctb") {
		t.Fatalf("%q", got)
	}
}

func TestCollectionSettingsBounds(t *testing.T) {
	if (CollectionSettings{FineBasisPoints: 2001}).Validate() == nil || (CollectionSettings{MonthlyInterestBasisPoints: -1}).Validate() == nil {
		t.Fatal("out of range accepted")
	}
	if (CollectionSettings{FineBasisPoints: 200, MonthlyInterestBasisPoints: 100}).Validate() != nil {
		t.Fatal("2 % and 1 %/month refused")
	}
}
```

```go
//go:build integration
func TestABYOKAttemptAndItsPointerAreWrittenTogether(t *testing.T) {
	ctx := ctxT(t)
	payments := repositories.NewPaymentRepository(testDB, testCfg)
	a := newByokAttempt(t) // ID pa_…, InvoiceID, ChargeRef = InterTxid(ID), Provider inter
	if err := payments.CreateByokAttempt(ctx, a, "acct-org", now()); err != nil {
		t.Fatal(err)
	}
	ref, err := payments.ChargeRef(ctx, "inter", a.Livemode, a.ChargeRef)
	if err != nil || ref.AttemptID != a.ID || ref.AccountOrganizationID != "acct-org" {
		t.Fatalf("%+v %v", ref, err)
	}
	if err := payments.CreateByokAttempt(ctx, a, "acct-org", now()); !errors.Is(err, repositories.ErrAttemptExists) {
		t.Fatalf("again: %v", err)
	}
	created, _ := payments.RecordExcess(ctx, &billing.ExcessPayment{ID: "ex_1", OrganizationID: a.OrganizationID, Livemode: a.Livemode, InvoiceID: a.InvoiceID, PaymentID: "E2", Amount: 100, Status: "open"}, now())
	again, _ := payments.RecordExcess(ctx, &billing.ExcessPayment{ID: "ex_2", OrganizationID: a.OrganizationID, Livemode: a.Livemode, InvoiceID: a.InvoiceID, PaymentID: "E2", Amount: 100, Status: "open"}, now())
	if !created || again {
		t.Fatal("one excess row per payment id")
	}
}
```

- [ ] **Step 2: Run** → FAIL, undefined.

- [ ] **Step 3: Implement** as the interfaces say. `CreateAttempt` keeps refusing an automatic attempt without `WalletChargeID` unless `Method == MethodBYOK` (then `ChargeRef` is required). The excess row: `pk = TenantPK`, `sk = InvoiceSK(invoiceID) + "#EXCESS#" + paymentID`, `lookup_pk = "{mode}#EXCESS#{id}"`, retention five years. `Finalize` adds `"collection"` to its `Set` map from the organization (the invoicer reads `org` already when it resolves the policy; pass it in through a new `FinalizeExtras{Collection CollectionSettings}` argument rather than a second read).

- [ ] **Step 4: Run** `go test ./... -race -count=1 && make test-integration` → `ok`.

- [ ] **Step 5: Commit** `git commit -m "feat(billing): BYOK payment attempts with a charge pointer, collection settings, excess payment rows"`.

### Task 5: `ByokCollector` — create at finalization, checkout, new attempt, void and credit note

**Files:**
- Create: `api/internal/services/byok_collector.go`
- Modify: `api/internal/services/invoicing.go` (`ChargeCreator` hook after every `Finalize`), `api/internal/services/collecting.go` (`Pay` dispatch by rail; `ErrTestModeNotPayable` only on the wallet rail), `api/internal/api/v1/console.go` (`voidInvoice`, `creditInvoice` call the collector), `api/internal/api/v1/checkout.go` (session for a BYOK attempt: payable, QR, due date, "pague até")
- Test: `api/tests/integration/byok_collect_test.go`

**Interfaces:**
- Consumes: Tasks 3, 4; part A `ByokConnections.Session`, `byokrpc.Call`.
- Produces:

```go
type ChargeCreator interface{ Issued(ctx context.Context, inv *billing.Invoice, now time.Time) }
type ByokCollector struct{ /* invoices, payments, customers, orgs, gates, conns, finance */ }
func NewByokCollector(...) *ByokCollector
func (c *ByokCollector) Issued(ctx context.Context, inv *billing.Invoice, now time.Time)              // never fails the invoice; logs
func (c *ByokCollector) Ensure(ctx context.Context, inv *billing.Invoice, actor, requestID string, now time.Time) (*billing.PaymentAttempt, error) // create or complete the pending attempt; used by checkout, the sweep and the "Gerar novo Pix" route
func (c *ByokCollector) NewAttempt(ctx context.Context, inv *billing.Invoice, actor, requestID string, now time.Time) (*billing.PaymentAttempt, error) // only after the previous FAILED; new due date = today + 3 days
func (c *ByokCollector) Voided(ctx context.Context, inv *billing.Invoice, now time.Time) error   // CancelCharge; ErrConcluded → Confirm
func (c *ByokCollector) Credited(ctx context.Context, inv *billing.Invoice, now time.Time) error // UpdateCharge(amount_due); ErrConcluded → Confirm
var ErrCustomerTaxIDRequired = errors.New("byok: the customer has no tax id")
```

Rules (spec § 7.2): zero-total → nothing; `AuthorizeCharge(gate)` refused → no attempt (logged with the reason; the console shows the reason on the invoice from the gate); customer without tax id → no attempt, `ErrCustomerTaxIDRequired` (`422 customer_tax_id_required`); attempt written first (`CreateByokAttempt`), then `CreateDueCharge` with `Due = inv.DueDate`, `ValidityDays = max(1, last dunning offset)`, `Amount = inv.AmountDue()`, fine/interest from `inv.Collection`, `Debtor{customer name, decrypted tax id digits}`, `Message = "Fatura #N"`; on success `SetByokCharge(payable, due + validity)`. In test mode the session is the shared sandbox connection (`KindSandboxShared`) and `Limits.Pix` applies (`429 byok_sandbox_busy`).

- [ ] **Step 1: Write the failing tests**

```go
func TestFinalizationCreatesOneCobvWithTheInvoiceTerms(t *testing.T) {
	env := newCollectEnv(t, collectLive, clearedProviders("inter"))
	env.setCollection(t, billing.CollectionSettings{FineBasisPoints: 200, MonthlyInterestBasisPoints: 100})
	inv := env.finalizeInvoice(t, 9990)
	a := env.attempt(t, inv)
	ch, _ := env.fake.Charge(a.ChargeRef)
	if a.Method != billing.MethodBYOK || ch.Amount != 9990 || a.Payable == "" || a.PayableUntil.IsZero() {
		t.Fatalf("%+v %+v", a, ch)
	}
	env.setCollection(t, billing.CollectionSettings{FineBasisPoints: 900})
	if got := env.reloadInvoice(t, inv).Collection.FineBasisPoints; got != 200 {
		t.Fatalf("an issued invoice keeps its terms: %d", got)
	}
}

func TestAFailedCreationLeavesTheInvoiceIssuedAndCheckoutRetries(t *testing.T) {
	env := newCollectEnv(t, collectLive, clearedProviders("inter"))
	env.fake.FailNext(byok.OpCreateDueCharge, byok.ErrUnavailable)
	inv := env.finalizeInvoice(t, 5000)
	if inv.Status != billing.InvoiceOpen || env.attempt(t, inv).Payable != "" {
		t.Fatal("issued, attempt pending without artefact")
	}
	s := env.checkout(t, inv)
	if s.PixCode == "" || env.fake.Calls(byok.OpCreateDueCharge) != 2 {
		t.Fatalf("checkout completes the same attempt: %+v", s)
	}
	if env.attempts(t, inv) != 1 {
		t.Fatal("one attempt per invoice")
	}
}

func TestACustomerWithoutTaxIDGetsNoCharge(t *testing.T) {
	env := newCollectEnv(t, collectLive, clearedProviders("inter"))
	inv := env.finalizeInvoiceFor(t, env.customerWithoutTaxID(t), 5000)
	if env.attempts(t, inv) != 0 {
		t.Fatal("no attempt")
	}
	if r := env.consoleWrite(t, "POST", "/v1.0/console/invoices/"+inv.ID+"/charge", "{}"); r.code() != "customer_tax_id_required" {
		t.Fatalf("%d %s", r.status, r.body)
	}
}

func TestVoidRemovesTheChargeAndAPaidChargeWinsTheRace(t *testing.T) {
	env := newCollectEnv(t, collectTest, nil)
	inv := env.finalizeInvoice(t, 5000)
	env.fake.Pay(env.attempt(t, inv).ChargeRef, 5000, 0, now())
	env.fake.FailNext(byok.OpCancelCharge, byok.ErrConcluded)
	env.void(t, inv)
	if got := env.reloadInvoice(t, inv); got.Status != billing.InvoicePaid {
		t.Fatalf("the payment wins over the void: %s", got.Status)
	}
}

func TestTestModeIsCollectableOnTheBYOKRailOnly(t *testing.T) {
	env := newCollectEnv(t, collectTest, nil)
	if s := env.checkout(t, env.finalizeInvoice(t, 100)); s.PixCode == "" {
		t.Fatal("a test BYOK invoice is payable in the sandbox")
	}
	w := newCollectEnv(t, collectTestWallet, nil)
	if _, err := w.checkoutErr(t, w.finalizeInvoice(t, 100)); !errors.Is(err, services.ErrTestModeNotPayable) {
		t.Fatalf("wallet rail in test: %v", err)
	}
}
```

(`newCollectEnv` in `tests/integration/byok_collect_env_test.go`: a tenant with `payout_rail: byok`, `payout_provider: inter`, `payout_status: enabled`, an organization linked, a connection (live: merchant + sealed through the fake invoker; test: sandbox), the `byoktest.Fake` behind a fake Lambda invoker, a customer with a tax id, and the services wired as `app.Build` does. `clearedProviders` sets `cfg.ByokLiveCollection=true` and the list; `setCleared()` flips them on the same `*config.Config` the gate reads.)

- [ ] **Step 2: Run** `make test-integration` → FAIL.

- [ ] **Step 3: Implement** `byok_collector.go` with the rules above; `Invoicer` gains `WithCharges(c ChargeCreator)` and calls `c.Issued(ctx, inv, now)` after each successful `Finalize` that leaves the invoice `OPEN` with `AmountDue() > 0` (the three call sites at `invoicing.go:171,187,334`). `Collector.Pay`: after loading `org`, `if org.Rail() == billing.RailBYOK { return c.payByok(...) }` which calls `byok.Ensure` and returns a `CheckoutSession` whose `PixCode` is the attempt's `Payable`, `ExpiresAt` = end of `PayableUntil` in São Paulo; the wallet path keeps `ErrTestModeNotPayable` (move the test-mode guard below the rail dispatch). The checkout view adds `due_date` and `payable_until` to its JSON (`checkout.go:77`), and the page renders "pague até {date}". The QR image is rendered by the UI from `pix_code` (`qrcode.react`, already a dependency) as today.

- [ ] **Step 4: Run** `gofmt -l ./internal ./tests && go vet ./... && go test ./... -race -count=1 && make test-integration` → `ok`.

- [ ] **Step 5: Commit** `git commit -m "feat(byok): Pix with due date at finalization, checkout, new attempt, void and credit-note updates"`.

### Task 6: Confirmation — re-query, settle once, late charges, excess

**Files:**
- Create: `api/internal/services/byok_confirm.go`
- Modify: `api/internal/services/collecting.go` (export `SettleAttempt` used by both rails: the existing `TransitionAttempt` + `settleInvoice` pair)
- Test: `api/tests/integration/byok_confirm_test.go`

**Interfaces:**

```go
type WakeCause string // WakeWebhook, WakeSweep, WakeStatement, WakeManual
func (c *ByokCollector) Confirm(ctx context.Context, ref repositories.ChargeRef, provider string, livemode bool, cause WakeCause, now time.Time) (Outcome, error)
type Outcome string // OutcomeSettled, OutcomeAlreadySettled, OutcomeOpen, OutcomeExpired, OutcomeIgnored
```

Algorithm (spec § 7.4):

```go
func (c *ByokCollector) Confirm(ctx context.Context, ref repositories.ChargeRef, provider string, livemode bool, cause WakeCause, now time.Time) (Outcome, error) {
	a, err := c.payments.GetAttempt(ctx, ref.TenantID, livemode, ref.InvoiceID, ref.AttemptNumber)
	if err != nil {
		return "", err
	}
	org, err := c.orgs.Get(ctx, ref.TenantID, livemode)
	if err != nil {
		return "", err
	}
	sess, err := c.session(ctx, org)
	if err != nil {
		return "", err
	}
	ch, err := byokrpc.Call(ctx, sess, byok.CapabilityCollection, func(cr byok.Credentials, t byok.Token) (byok.Charge, error) {
		return sess.P.GetCharge(ctx, cr, t, a.ChargeRef)
	})
	if err != nil {
		return "", err // never inferred from a failure
	}
	_ = c.payments.TouchChecked(ctx, a, now)
	switch ch.Status {
	case byok.ChargeOpen:
		return OutcomeOpen, nil
	case byok.ChargeExpired, byok.ChargeRemoved:
		if len(ch.Payments) == 0 {
			if a.Status == billing.AttemptPending {
				a.FailureReason = string(ch.Status)
				_, err := c.payments.TransitionAttempt(ctx, a, billing.AttemptFailed, billing.CausePaymentFailed, actorByok(cause), "", now)
				return OutcomeExpired, err
			}
			return OutcomeExpired, nil
		}
	}
	first := ch.Payments[0]
	if first.Amount < int64(a.Amount) {
		slog.ErrorContext(ctx, "byok charge paid below the amount due", "attempt_id", a.ID)
		return "", fmt.Errorf("%w: attempt %s", ErrAmountMismatch, a.ID)
	}
	outcome := OutcomeAlreadySettled
	if a.Status == billing.AttemptPending {
		a.PaymentID, a.LateCharges = first.ID, billing.Cents(first.Amount-int64(a.Amount))
		if err := c.collector.SettleAttempt(ctx, a, billing.CauseByokConfirmed, actorByok(cause), "", now); err != nil {
			return "", err
		}
		outcome = OutcomeSettled
	} else if a.Status == billing.AttemptSucceeded {
		_ = c.collector.SettleAttempt(ctx, a, billing.CauseByokConfirmed, actorByok(cause), "", now) // finishes a half-done settlement
	}
	// Excess (D19): every further payment, and anything above amount + late charges.
	allowed := int64(a.Amount) + first.LateCharges
	if first.Amount > allowed {
		c.excess(ctx, a, first.ID+"X", first.Amount-allowed, first.PaidAt, now)
	}
	for _, p := range ch.Payments[1:] {
		c.excess(ctx, a, p.ID, p.Amount, p.PaidAt, now)
	}
	return outcome, nil
}
```

(`billing.CauseByokConfirmed` is a new cause added to `paymentCauses` and to the attempt transition map; `actorByok(cause)` is `"service:byok-" + string(cause)`. `excess` writes `RecordExcess` and, when created, sends the "Pagamento em duplicidade" e-mail to the connection's `NotifyEmail`. `GetAttempt` is a new `GetItem` by `PaymentAttemptSK`.)

- [ ] **Step 1: Write the failing tests**

```go
func TestSettlementComesOnlyFromTheProvider(t *testing.T) {
	env := newCollectEnv(t, collectTest, nil)
	inv := env.finalizeInvoice(t, 5000)
	ref := env.chargeRef(t, inv)
	if out, _ := env.byok.Confirm(ctxT(t), ref, "inter", false, services.WakeWebhook, now()); out != services.OutcomeOpen {
		t.Fatalf("open charge: %s", out)
	}
	env.fake.Pay(ref.Ref, 5000, 0, now())
	if out, _ := env.byok.Confirm(ctxT(t), ref, "inter", false, services.WakeSweep, now()); out != services.OutcomeSettled {
		t.Fatalf("paid: %s", out)
	}
}

func TestThreeConcurrentWakeUpsSettleOnce(t *testing.T) {
	env := newCollectEnv(t, collectTest, nil)
	inv := env.finalizeInvoice(t, 5000)
	ref := env.chargeRef(t, inv)
	env.fake.Pay(ref.Ref, 5000, 0, now())
	var wg sync.WaitGroup
	outs := make([]services.Outcome, 3)
	for i, cause := range []services.WakeCause{services.WakeWebhook, services.WakeSweep, services.WakeStatement} {
		wg.Add(1)
		go func() { defer wg.Done(); outs[i], _ = env.byok.Confirm(ctxT(t), ref, "inter", false, cause, now()) }()
	}
	wg.Wait()
	settled := 0
	for _, o := range outs {
		if o == services.OutcomeSettled {
			settled++
		}
	}
	if settled != 1 || env.reloadInvoice(t, inv).Status != billing.InvoicePaid || env.auditCount(t, inv, "invoice.paid") != 1 {
		t.Fatalf("%v", outs)
	}
}

func TestExcessAndLateChargesAreToldApart(t *testing.T) {
	env := newCollectEnv(t, collectTest, nil)
	inv := env.finalizeInvoice(t, 10000)
	ref := env.chargeRef(t, inv)
	env.fake.Pay(ref.Ref, 10300, 300, now()) // R$ 3,00 of multa + juros
	env.fake.Pay(ref.Ref, 10000, 0, now())   // somebody paid the same QR again
	_, _ = env.byok.Confirm(ctxT(t), ref, "inter", false, services.WakeWebhook, now())
	ex := env.excess(t, inv)
	if len(ex) != 1 || ex[0].Amount != 10000 {
		t.Fatalf("the late fee is not excess; the second payment is: %+v", ex)
	}
	if a := env.attempt(t, inv); a.LateCharges != 300 {
		t.Fatalf("late charges %d", a.LateCharges)
	}
}

func TestAnUnderpaidChargeAlarmsAndSettlesNothing(t *testing.T) {
	env := newCollectEnv(t, collectTest, nil)
	inv := env.finalizeInvoice(t, 10000)
	ref := env.chargeRef(t, inv)
	env.fake.Pay(ref.Ref, 9000, 0, now())
	if _, err := env.byok.Confirm(ctxT(t), ref, "inter", false, services.WakeSweep, now()); !errors.Is(err, services.ErrAmountMismatch) {
		t.Fatalf("%v", err)
	}
	if env.reloadInvoice(t, inv).Status == billing.InvoicePaid {
		t.Fatal("never a partial payment")
	}
}
```

- [ ] **Step 2: Run** → FAIL. **Step 3: Implement** as above. **Step 4: Run** the Go suite and integration → `ok`.

- [ ] **Step 5: Commit** `git commit -m "feat(byok): confirm only from the provider, settle once, record late charges and excess payments"`.

### Task 7: Finanças — the BYOK invoice posting and statement linking (§ 8.4)

**Files:**
- Modify: `api/internal/repositories/finance_invoices.go` (`InvoiceFact.AccountID`, `LateCharges`; when set, used instead of `receivingAccount`, and a second settlement leg on `juros-multas-recebidos`), `api/internal/services/finance_invoices.go` (issuer side of a BYOK-rail invoice: the connection's `LedgerAccountID`, ref = payment id), `api/internal/domain/finance/categories.go` (`juros-multas-recebidos`)
- Create: `api/internal/services/byok_linker.go` (implements part A's `LineLinker`)
- Test: `api/tests/integration/byok_finance_link_test.go`

**Linker rule** (`Link(ctx, sp, conn, line)`): `ChargeRef` → `CHARGEREF#` pointer of this organization (else not handled). If the invoice is `PAID` and its issuer bill exists (`InvoiceBillID(sp, invoiceID)`), `LinkBankLine(…, bill's settlement tx id)` → handled. If not paid: `Confirm(…, WakeStatement)`; if that settles, link in the same run → handled. If still unpaid and the line is older than 24 h → not handled (posts unclassified) and an alert. Excess and refund lines link the same way through `PaymentID` → the excess row's posting (Task 9 posts excess refunds).

- [ ] **Step 1: Write the failing test** (spec § 17 item 11)

```go
func TestAPaidInvoiceAndItsLineMakeOneCashEntryWhicheverArrivesFirst(t *testing.T) {
	for _, order := range []string{"payment-first", "line-first"} {
		env := newCollectEnv(t, collectTest, nil)
		inv := env.finalizeInvoice(t, 5000)
		ref := env.chargeRef(t, inv)
		env.fake.Pay(ref.Ref, 5000, 0, now())
		line := byok.StatementLine{ID: "L-" + order, Date: byokToday(), Direction: byok.Credit, Amount: 5000, ChargeRef: ref.Ref}
		if order == "payment-first" {
			_, _ = env.byok.Confirm(ctxT(t), ref, "inter", false, services.WakeWebhook, now())
		}
		env.fake.AddLines(line)
		env.syncNow(t)
		if order == "line-first" && env.reloadInvoice(t, inv).Status != billing.InvoicePaid {
			t.Fatal("the line woke the confirmation")
		}
		if got := env.byokAccountBalance(t); got != 5000 {
			t.Fatalf("%s: balance %d, want one entry of 5000", order, got)
		}
	}
}
```

- [ ] **Step 2–4:** run (FAIL), implement, run Go + integration (`ok`). In `FinanceInvoices.Paid`, the issuer target for an invoice whose org rail is `byok` gets `fact.AccountID = conn.LedgerAccountID` (one `GetConnection`), and the fact's description stays the invoice's; when the connection is gone (disconnected after payment) the posting falls back to `receivingAccount` (never a synced account, part A decision 6).

- [ ] **Step 5: Commit** `git commit -m "feat(finance): BYOK invoices post into the provider's account and link their statement line"`.

### Task 8: Webhook path — hooks, the notify route

**Files:**
- Modify: `api/internal/repositories/byok.go` (hook rows), `api/internal/api/v1/router.go` (`registerByokNotify`)
- Create: `api/internal/services/byok_webhooks.go`, `api/internal/api/v1/byok_notify.go`
- Test: `api/internal/services/byok_webhooks_test.go`, `api/tests/integration/byok_notify_test.go`

**Interfaces:**

```go
type Hook struct{ ID, Provider, AccountOrganizationID string; Livemode bool; SecretHash, PreviousSecretHash string; PreviousValidUntil time.Time }
func (r *ByokRepository) PutHook(ctx, h *Hook, now time.Time) error     // pk HOOK#{id}, sk HOOK
func (r *ByokRepository) GetHook(ctx, id string) (*Hook, error)
func (r *ByokRepository) DeleteHook(ctx, id string) error
func NewHookSecret() (secret, hash string) // 32 bytes base64url; sha256 hex
func HookMatches(h *Hook, secret string, now time.Time) bool // constant time against current and, until PreviousValidUntil, previous
func HookURL(host, provider, hookID, secret string) string // https://{host}/w/{provider}/{hook}/{secret}
type NotifyRequest struct{ Provider, HookID, Secret, Product, Account string; ChargeRefs, PaymentIDs []string }
func (w *ByokWebhooks) Notify(ctx context.Context, in NotifyRequest, now time.Time) (status int)
```

`Notify` (spec § 6.3): hook lookup; provider equality; `HookMatches` → else `404` and an operator alert at most once per hour per hook (Valkey `SET NX EX 3600`); account check against the connection's `AccountNumber` for merchant hooks (the sandbox hook skips it: the api never reads the sandbox account parameter, and the `CHARGEREF#…#test#` pointer, which only billing writes, decides the tenant; recorded as a known deviation from spec § 6.3 item 2 in the runbook) → mismatch `200` + alert; per ref: `ChargeRef` must exist and, in live, belong to the hook's organization, else ignored and counted; `Confirm(…, WakeWebhook)`; any `Confirm` error → `503`; else `200`. The payload's amounts never reach this code (the request has none).

Route: `v1.Post("/internal/byok/notify", auth, middleware.RequireM2MScope(middleware.ScopeByokNotify), h.notify)` — no tenant resolver; body ≤ 64 KiB, at most 100 refs.

- [ ] **Step 1: Write the failing tests** (spec § 17 item 5)

```go
func TestHookSecretsCompareInConstantTimeAndRotateWithOverlap(t *testing.T) {
	s1, h1 := NewHookSecret()
	s2, h2 := NewHookSecret()
	hook := &repositories.Hook{SecretHash: h2, PreviousSecretHash: h1, PreviousValidUntil: time.Now().Add(time.Hour)}
	if !HookMatches(hook, s2, time.Now()) || !HookMatches(hook, s1, time.Now()) || HookMatches(hook, s1, time.Now().Add(2*time.Hour)) {
		t.Fatal("current always, previous 24 h only")
	}
	if HookMatches(hook, s2[:len(s2)-1]+"A", time.Now()) {
		t.Fatal("wrong secret")
	}
	if !strings.Contains(readSource(t, "byok_webhooks.go"), "subtle.ConstantTimeCompare") {
		t.Fatal("the compare is constant time")
	}
}
```

```go
//go:build integration
func TestNoWebhookPayloadSettlesAnything(t *testing.T) {
	a := newCollectEnv(t, collectLive, clearedProviders("inter"))
	b := a.secondTenant(t) // another organization, its own connection and hook
	invA := a.finalizeInvoice(t, 5000)
	refA := a.chargeRef(t, invA).Ref
	tok := a.m2mToken(t, "internal:billing:byok-notify")
	for name, body := range map[string]string{
		"wrong secret":        a.notifyBody(a.hookID, "wrong", "inter", []string{refA}),
		"provider mismatch":   a.notifyBody(a.hookID, a.secret, "other", []string{refA}),
		"other org's hook":    a.notifyBody(b.hookID, b.secret, "inter", []string{refA}),
		"account mismatch":    a.notifyBodyAccount(a.hookID, a.secret, "999", []string{refA}),
		"claims an open charge": a.notifyBody(a.hookID, a.secret, "inter", []string{refA}),
	} {
		r := a.post(t, "/v1.0/internal/byok/notify", tok, body)
		if r.status >= 500 || a.reloadInvoice(t, invA).Status == billing.InvoicePaid {
			t.Fatalf("%s: %d, invoice %s", name, r.status, a.reloadInvoice(t, invA).Status)
		}
	}
	if r := a.post(t, "/v1.0/internal/byok/notify", a.m2mToken(t, "billing:invoices:read"), a.notifyBody(a.hookID, a.secret, "inter", []string{refA})); r.status != 403 {
		t.Fatalf("scope: %d", r.status)
	}
	a.fake.Pay(refA, 5000, 0, now())
	if r := a.post(t, "/v1.0/internal/byok/notify", tok, a.notifyBody(a.hookID, a.secret, "inter", []string{refA})); r.status != 200 || a.reloadInvoice(t, invA).Status != billing.InvoicePaid {
		t.Fatal("a real payment, re-queried, settles")
	}
}

func TestAFailedRequeryAnswers503SoTheProviderRetries(t *testing.T) {
	a := newCollectEnv(t, collectTest, nil)
	inv := a.finalizeInvoice(t, 5000)
	a.fake.FailNext(byok.OpGetCharge, byok.ErrUnavailable)
	r := a.post(t, "/v1.0/internal/byok/notify", a.m2mToken(t, "internal:billing:byok-notify"), a.notifyBody("sandbox", a.secret, "inter", []string{a.chargeRef(t, inv).Ref}))
	if r.status != 503 {
		t.Fatalf("%d", r.status)
	}
}
```

- [ ] **Step 2–4:** FAIL → implement → `go test ./... -race` and `make test-integration` → `ok`.

- [ ] **Step 5: Commit** `git commit -m "feat(byok): webhook hooks with path secrets and the internal notify route"`.

### Task 9: Refunds of excess payments and credited invoices

**Files:**
- Create: `api/internal/services/byok_refunds.go`
- Modify: `api/internal/api/v1/console.go` (`creditInvoice` accepts `refund_via_pix: true` on a BYOK-paid invoice), `api/internal/problem/problem.go` (`refund_window_closed`)
- Test: `api/tests/integration/byok_refunds_test.go`

**Interfaces:**

```go
func (c *ByokCollector) RefundExcess(ctx context.Context, tenant middleware.Tenant, excessID, actor, requestID string, now time.Time) (*billing.ExcessPayment, error)
func (c *ByokCollector) RefundCredit(ctx context.Context, inv *billing.Invoice, cn *billing.CreditNote, actor, requestID string, now time.Time) error
func (c *ByokCollector) FollowRefunds(ctx context.Context, livemode bool, now time.Time) []error // the sweep: GetRefund for refunding rows
var ErrRefundWindowClosed = errors.New("byok: the 90-day refund window closed")
```

Rules (spec § 7.5): gate via `AuthorizeCharge` (Refund is `collect`); window `PaidAt + 90 days`; refund id `"rf" + ULID` stored on the row **before** the call (idempotent retry); status follows `GetRefund`; Finanças posts the refund as a cash-out of the BYOK account against the original category (`RecordInvoiceCredit` for a credit note; a new `RecordExcessRefund` for an excess row, which first records the excess itself as an income on `outras-receitas` when it arrived, so the refund nets it).

- [ ] **Step 1: Write the failing tests**

```go
func TestAnExcessIsRefundedOnlyWhenTheMerchantAsks(t *testing.T) {
	env := newCollectEnv(t, collectTest, nil)
	inv := env.finalizeInvoice(t, 1000)
	ref := env.chargeRef(t, inv)
	env.fake.Pay(ref.Ref, 1000, 0, now())
	env.fake.Pay(ref.Ref, 1000, 0, now())
	_, _ = env.byok.Confirm(ctxT(t), ref, "inter", false, services.WakeWebhook, now())
	if env.fake.Calls(byok.OpRefund) != 0 {
		t.Fatal("nothing is refunded automatically (D19)")
	}
	ex := env.excess(t, inv)[0]
	r := env.consoleWrite(t, "POST", "/v1.0/console/excess-payments/"+ex.ID+"/refund", "{}")
	if r.status != 200 || env.fake.Calls(byok.OpRefund) != 1 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	env.consoleWrite(t, "POST", "/v1.0/console/excess-payments/"+ex.ID+"/refund", "{}")
	if env.fake.Calls(byok.OpRefund) != 1 {
		t.Fatal("a second press does not refund twice")
	}
}

func TestRefundWindowClosesAfterNinetyDays(t *testing.T) {
	env := newCollectEnv(t, collectTest, nil)
	ex := env.excessPaidAt(t, now().AddDate(0, 0, -91))
	if r := env.consoleWrite(t, "POST", "/v1.0/console/excess-payments/"+ex.ID+"/refund", "{}"); r.code() != "refund_window_closed" {
		t.Fatalf("%d %s", r.status, r.body)
	}
}
```

- [ ] **Step 2–5:** FAIL → implement → run → commit `feat(byok): merchant-initiated refunds of excess payments and credited invoices`.

### Task 10: Webhook registration, conflict, disconnect (steps 1, 2, 4), promotion

**Files:**
- Modify: `api/internal/services/byok_connections.go` (`Disconnect` prepends charges and webhook, then gate reset), `api/internal/api/v1/console_connections.go` (`POST …/webhook`, `DELETE` body), `api/internal/services/byok_webhooks.go`
- Create: `api/internal/services/byok_collection_setup.go` (`UseForCollection`, `PromoteCleared`, `RegisterWebhook`)
- Test: `api/tests/integration/byok_disconnect_test.go`, `api/tests/integration/byok_webhook_registration_test.go`

**Interfaces:**

```go
func (s *ByokConnections) RegisterWebhook(ctx context.Context, sp space.ResolvedSpace, provider string, replaceExisting bool, actor, requestID string, now time.Time) (*byokconn.Connection, error) // ErrWebhookConflict{ExistingHost}
type ErrWebhookConflict struct{ ExistingHost string } // 409 byok_webhook_conflict, existing_host in the body
func (s *ByokConnections) UseForCollection(ctx context.Context, sp space.ResolvedSpace, tenant *billing.Organization, provider string, settings billing.CollectionSettings, actor, requestID string, now time.Time) error // PUT /console/collection
func (s *ByokConnections) PromoteCleared(ctx context.Context, conn *byokconn.Connection, now time.Time) error // cmd/byok step 5
func (s *ByokConnections) DisconnectAll(ctx context.Context, sp space.ResolvedSpace, provider string, cancelOpen, force bool, actor, requestID string, now time.Time) error // ErrOpenCharges{Count}
type ErrOpenCharges struct{ Count int } // 409 byok_open_charges
```

Rules: registration (spec § 6.4) `GetWebhook` → none: `RegisterWebhook(HookURL)` + `registered`; ours: nothing; foreign: `conflict` (no write) unless `replaceExisting` (audited `byok.webhook_replaced` with the replaced host only, never the path); re-registering rotates the secret (previous valid 24 h). In live, registration happens only when cleared (the Lambda would refuse anyway); before that `webhook_status` stays `not_registered` and the console says confirmation is hourly. `UseForCollection`: requires `collection` capability, sets `payout_rail=byok`, `payout_provider`, status `enabled` (test, or live and cleared) or `pending_clearance` (live, not cleared), stores `org.Collection`; tenant zero is refused here (moving tenant zero is the operator's audited action, runbook). Disconnect order (spec § 3.6): count open BYOK attempts of the tenant (`PendingByok` for the tenant: a per-tenant sparse key `byok_open_pk = {tenant}#{mode}#BYOK_OPEN` on the attempt row while `PENDING`, so this is one key-condition Query); refuse with `ErrOpenCharges` unless `cancelOpen`; cancel each (`CancelCharge`; `ErrConcluded` → `Confirm`), attempt `FAILED` (`byok_disconnected`); `GetWebhook` → delete only if the URL starts with our host and hook id; credentials (part A); hook row deleted; gate reset (`not_configured`, rail and provider cleared, audited) when this provider is the tenant's; e-mail. `force` ("Desconectar mesmo assim") skips provider calls that fail with `ErrUnavailable` and returns the list of refs and the webhook URL the merchant must remove.

- [ ] **Step 1: Write the failing tests** (spec § 17 items 9, 10)

```go
func TestDisconnectOrderAndForeignWebhook(t *testing.T) {
	env := newCollectEnv(t, collectTest, nil)
	inv := env.finalizeInvoice(t, 1000)
	if r := env.consoleWrite(t, "DELETE", "/v1.0/console/connections/inter", `{}`); r.code() != "byok_open_charges" {
		t.Fatalf("%d %s", r.status, r.body)
	}
	env.fake.SetWebhookURL(byok.ProductPix, "https://erp.example.com/pix")
	if r := env.consoleWrite(t, "DELETE", "/v1.0/console/connections/inter", `{"cancel_open_charges":true}`); r.status != 204 {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if ch, _ := env.fake.Charge(env.chargeRefOf(inv)); ch.Status != byok.ChargeRemoved {
		t.Fatal("the open charge was removed before the credentials")
	}
	if env.attempt(t, inv).Status != billing.AttemptFailed || env.fake.WebhookURL(byok.ProductPix) != "https://erp.example.com/pix" {
		t.Fatal("attempt failed; a foreign webhook is never deleted")
	}
	if org := env.tenant(t); org.PayoutStatus != billing.PayoutNotConfigured || org.PayoutRail != "" {
		t.Fatalf("gate reset: %+v", org)
	}
	if r := env.post(t, "/v1.0/internal/byok/notify", env.m2mToken(t, "internal:billing:byok-notify"), env.notifyBody(env.hookID, env.secret, "inter", nil)); r.status != 404 {
		t.Fatalf("a late callback after disconnect is 404: %d", r.status)
	}
}

func TestAForeignWebhookIsReplacedOnlyWithConfirmation(t *testing.T) {
	env := newCollectEnv(t, collectTest, nil)
	env.fake.SetWebhookURL(byok.ProductPix, "https://erp.example.com/pix")
	r := env.consoleWrite(t, "POST", "/v1.0/console/connections/inter/webhook", `{"replace_existing":false}`)
	if r.code() != "byok_webhook_conflict" || !strings.Contains(string(r.body), "erp.example.com") || env.fake.WebhookURL(byok.ProductPix) != "https://erp.example.com/pix" {
		t.Fatalf("%d %s", r.status, r.body)
	}
	if r := env.consoleWrite(t, "POST", "/v1.0/console/connections/inter/webhook", `{"replace_existing":true}`); r.status != 200 || !strings.HasPrefix(env.fake.WebhookURL(byok.ProductPix), "https://billing-byok-hooks-") {
		t.Fatalf("%d", r.status)
	}
}
```

- [ ] **Step 2–5:** FAIL → implement → run → commit `feat(byok): webhook registration with conflict handling, collection setup, promotion and the full disconnect`.

### Task 11: `cmd/byok` — sweep, daily webhook audit, promotion, `-retry-callbacks`

**Files:**
- Modify: `api/cmd/byok/main.go`, `api/internal/services/byok_job.go` (register `StepSweep`, `StepAudit`, `StepPromote`)
- Test: `api/tests/integration/byok_job_collect_test.go`

Rules (spec § 5): **sweep** per connection's tenant: `PENDING` BYOK attempts re-queried when `checked_at` older than 1 h (attempt created in the last 48 h) or 24 h (older), through `Confirm(…, WakeSweep)`; attempts without an artefact get `Ensure` (creation retry); `FollowRefunds`. **audit** once a day, first run at or after 06:00 São Paulo (guard `audited_on` on the connection): `GetWebhook` ≠ our URL while `registered` → `overwritten` + banner + alert; `ListCallbacks(since 24 h)` → any non-2xx → operator alert and `Confirm` of those refs at once. **promote**: `pending_clearance` tenants of this connection whose provider is now cleared → `enabled` (audited `payout.promoted`) and `RegisterWebhook` (conflict leaves `conflict`). **`-retry-callbacks -since=2026-10-10T00:00:00Z`**: per live connection, `ListCallbacks(since)` → refs with non-2xx → `RetryCallbacks` in batches of 50, at most 5 calls per minute (sleep 12 s between batches); prints counts only.

Test-mode bootstrap: on the first test-mode run, `cmd/byok` creates the sandbox hook row (`HOOK#sandbox`) if absent and registers it on the sandbox Pix key through any sandbox connection's session (the shared integration), printing nothing secret.

- [ ] **Step 1: Write the failing tests**

```go
func TestTheSweepConfirmsWhatTheWebhookMissed(t *testing.T) {
	env := newCollectEnv(t, collectTest, nil)
	inv := env.finalizeInvoice(t, 1000)
	env.fake.Pay(env.chargeRefOf(inv), 1000, 0, now())
	env.runJob(t, now().Add(2*time.Hour))
	if env.reloadInvoice(t, inv).Status != billing.InvoicePaid {
		t.Fatal("the hourly sweep is the safety net")
	}
}

func TestTheDailyAuditMarksAnOverwrittenWebhook(t *testing.T) {
	env := newCollectEnv(t, collectTest, nil)
	env.registerWebhook(t)
	env.fake.SetWebhookURL(byok.ProductPix, "https://erp.example.com/pix")
	env.runJob(t, saoPaulo(2026, 10, 12, 6, 30))
	if env.connection(t).WebhookStatus != byokconn.WebhookOverwritten {
		t.Fatal("overwritten")
	}
}

func TestPromotionWaitsForClearance(t *testing.T) {
	env := newCollectEnv(t, collectLive, nil) // not cleared
	env.useForCollection(t)
	if env.tenant(t).PayoutStatus != billing.PayoutPendingClearance {
		t.Fatal("pending clearance")
	}
	env.runJob(t, now())
	if env.tenant(t).PayoutStatus != billing.PayoutPendingClearance {
		t.Fatal("not promoted while not cleared")
	}
	env.setCleared("inter")
	env.runJob(t, now())
	if env.tenant(t).PayoutStatus != billing.PayoutEnabled {
		t.Fatal("promoted once cleared")
	}
}
```

- [ ] **Step 2–5:** FAIL → implement → run → commit `feat(byok): hourly sweep, daily webhook audit, promotion and the callback retry tool`.

### Task 12: The webhook Lambda and its infrastructure

**Files:**
- Create: `api/cmd/byok-webhook/main.go`, `api/cmd/byok-webhook/handler.go`, `api/cmd/byok-webhook/handler_test.go`, `terraform/billing/byok_webhook.tf`, `terraform/billing/tests/byok_webhook.tftest.hcl`
- Modify: `api/Makefile` (`build-lambdas` adds `byok-webhook`), `terraform/billing/locals.tf` (`byok_hooks_domain`), `terraform/billing/variables.tf` (`acm_certificate_arn` for `*.aoctech.app`, read from `/ctech/{env}/acm/wildcard-certificate-arn` if ctech-cdk publishes it, else a variable)

**Handler** (`api/cmd/byok-webhook/handler.go`): API Gateway HTTP API v2 payload; path `w/{provider}/{hook}/{secret}/{product}` or with a repeated suffix (`…/pix/pix`): the handler splits the path, requires `provider` in `{inter}`, `product == "pix"` and, when 6 segments, the sixth equal to the fifth; parses with `inter.NewProvider(nil).ParseCallback` (parsing needs no client; credentials never present); posts `{provider, hook_id, secret, product, account, charge_refs, payment_ids}` to `BILLING_API_URL + /v1.0/internal/byok/notify` with an `oauth2client` token (client id from env, secret read once at cold start from SSM `/ctech-billing/{env}/byok-webhook/client-secret`); returns billing's status (`200`, `404`, `503`), `400` on an unparseable body. Logs carry the hook id and the count of refs, never the secret or the body.

```go
func splitPath(p string) (provider, hook, secret string, product byok.Product, ok bool) {
	seg := strings.Split(strings.Trim(p, "/"), "/")
	if len(seg) < 5 || len(seg) > 6 || seg[0] != "w" {
		return
	}
	if len(seg) == 6 && seg[5] != seg[4] {
		return
	}
	return seg[1], seg[2], seg[3], byok.Product(seg[4]), seg[4] == "pix" && seg[1] == "inter"
}
```

`handler_test.go` pins: both `…/pix` and `…/pix/pix` reach billing with the same body; `…/rec`, `…/pix/rec`, extra segments → `404` without a billing call; the secret never appears in captured logs; billing's `503` passes through.

**Terraform** (`byok_webhook.tf`), each resource commented like part A's:
- `aws_s3_bucket.byok_truststore` (`{env}-ctech-billing-byok-truststore`, versioning on, public access blocked, `prevent_destroy`), the CA bundle object **not** managed by Terraform (uploaded by the runbook; `aws_apigatewayv2_domain_name` reads `truststore_uri = "s3://…/inter-ca-bundle.pem"` and `truststore_version` from a variable updated by the runbook).
- `aws_apigatewayv2_api.byok_hooks` (`protocol_type = "HTTP"`, `disable_execute_api_endpoint = true`), routes `POST /w/{provider}/{hook}/{secret}/{product}` and `POST /w/{provider}/{hook}/{secret}/{product}/{suffix}` → Lambda proxy integration; no `$default` route (anything else is 404 at the edge).
- `aws_apigatewayv2_domain_name.byok_hooks` (`domain_name = local.byok_hooks_domain`, `endpoint_type = "REGIONAL"`, `security_policy = "TLS_1_2"`, `mutual_tls_authentication { truststore_uri, truststore_version }`), `aws_apigatewayv2_api_mapping`, `aws_apigatewayv2_stage` `$default` with `auto_deploy` and throttling `burst 50 / rate 20`.
- `aws_lambda_function.byok_webhook` (128 MB, 10 s, arm64, placeholder code like part A), role reading only `/ctech-billing/{env}/byok-webhook/client-secret`, logs 14 days, env `BILLING_API_URL = https://{api_domain}`, `CTECH_TOKEN_URL`, `BYOK_WEBHOOK_CLIENT_ID` from the SSM String, `BYOK_WEBHOOK_SECRET_PARAM`, `BYOK_HOOKS_HOST`.
- `aws_lambda_permission` for API Gateway.
- Output `byok_hooks_target` (the regional domain name for the DNS-only CNAME).
- The api gets `BYOK_HOOKS_HOST` in `/etc/app-static.env` (for `HookURL`).

`byok_webhook.tftest.hcl` asserts: no `$default` route; `disable_execute_api_endpoint`; the webhook role cannot read `/byok/credentials-key` nor any `/ctech-wallet/` parameter; the domain is regional with mTLS.

- [ ] **Steps:** write the handler test (FAIL) → implement → `go test ./cmd/byok-webhook/ -race` (`ok`) → write Terraform + test (FAIL then pass: `terraform -chdir=terraform/billing test`) → commit `feat(byok): webhook Lambda and the mTLS hooks host`.

### Task 13: No wallet on the BYOK rail (spec § 17 item 1) and the console collection routes

**Files:**
- Create: `api/internal/api/v1/console_collection.go`, `api/tests/integration/byok_no_wallet_test.go`
- Modify: `api/internal/api/v1/router.go` (console routes: `PUT /collection`, `POST /invoices/:id/charge`, `POST /invoices/:id/simulate-payment`, `POST /excess-payments/:id/refund`), `api/internal/api/v1/console.go` (invoice detail: `byok` block with `payable`, `payable_until`, `provider`, `excess_payments`, `gate_reason`)

```go
func TestABYOKInvoiceNeverTouchesTheWallet(t *testing.T) {
	env := newCollectEnvWithWallet(t, collectTest, &failingWallet{t: t}) // OpenCharge/GetCharge/VerifySignature call t.Fatal
	inv := env.finalizeInvoice(t, 1000)
	env.checkout(t, inv)
	ref := env.chargeRef(t, inv)
	env.fake.Pay(ref.Ref, 1000, 0, now())
	env.fake.Pay(ref.Ref, 1000, 0, now())
	_, _ = env.byok.Confirm(ctxT(t), ref, "inter", false, services.WakeWebhook, now())
	env.consoleWrite(t, "POST", "/v1.0/console/excess-payments/"+env.excess(t, inv)[0].ID+"/refund", "{}")
	inv2 := env.finalizeInvoice(t, 1000)
	env.void(t, inv2)
	env.runJob(t, now().Add(2*time.Hour))
}

func TestSimulatePaymentIsTestModeOnly(t *testing.T) {
	live := newCollectEnv(t, collectLive, clearedProviders("inter"))
	if r := live.consoleWrite(t, "POST", "/v1.0/console/invoices/"+live.finalizeInvoice(t, 100).ID+"/simulate-payment", "{}"); r.status != 404 {
		t.Fatalf("live: %d", r.status)
	}
	test := newCollectEnv(t, collectTest, nil)
	inv := test.finalizeInvoice(t, 100)
	if r := test.consoleWrite(t, "POST", "/v1.0/console/invoices/"+inv.ID+"/simulate-payment", "{}"); r.status != 202 {
		t.Fatalf("test: %d", r.status)
	}
	test.runJob(t, now().Add(2*time.Hour))
	if test.reloadInvoice(t, inv).Status != billing.InvoicePaid {
		t.Fatal("simulated payment confirmed through the real path")
	}
}
```

Plus a static check: `go list -deps ./internal/services/` is allowed to import `internal/wallet` only through `collecting.go`'s wallet path; the test asserts no file named `byok_*.go` imports `internal/wallet` (parse imports with `go/parser`).

- [ ] **Steps:** FAIL → implement routes (owner/admin of the merchant tenant via the console group, scope `billing:organization:write`, idempotency) → run → commit `feat(byok): console collection routes; the BYOK rail never calls the wallet`.

### Task 14: UI — collection settings, the invoice's Pix, excess payments, webhook conflict, disconnect

**Files:**
- Create: `ui/src/components/collection/CollectionSettings.tsx`, `ui/src/components/collection/InvoicePix.tsx`, `ui/src/components/collection/ExcessPayments.tsx`, `ui/src/components/connections/WebhookStep.tsx`
- Modify: `ui/src/components/connections/ConnectionHealth.tsx` (actions "Usar para cobrança", "Configurar multa e juros", "Registrar aviso novamente"; webhook status), `ui/src/components/connections/DisconnectDialog.tsx` (open charges list, "Desconectar mesmo assim"), `ui/src/app/(console)/console/invoices/…` detail (Pix block, "Simular pagamento" in test, excess notice with "Devolver", `customer_tax_id_required` and gate reasons in words), `ui/src/app/(console)/console/applications` (tenant zero: rail, provider and status per merchant), `ui/src/locales/*/connections.json`, `ui/src/lib/api/connections.ts`, `ui/src/lib/api/console.ts`
- Test: one test file per new component

Rules (spec § 11.2, § 11.3): the webhook step shows "Aviso registrado", "Atenção: outro sistema recebe os avisos desta chave (erp.example.com)" with the explanation and two choices (use a dedicated EVP key, or "Substituir mesmo assim" which sends `replace_existing: true` after a confirmation `Modal`), "Sem aviso: as confirmações chegam em até uma hora" when not registered; `CollectionSettings` uses percentage inputs (two decimals, `0` to `20` and `0` to `10`) converted to basis points; `InvoicePix` shows the copy-paste code with a copy button, the QR (`qrcode.react`), due date and "pague até"; on a phone the QR is 200 px centred above the code. New strings (pt-BR; en mirrors): "Cobrança via Inter", "Usar o Inter para cobrar", "Cobrança pelo Inter em liberação", "Multa por atraso (%)", "Juros ao mês (%)", "Pix com vencimento", "Pague até {{date}}", "Copiar código Pix", "Simular pagamento", "Pagamento em duplicidade", "Recebemos {{amount}} a mais nesta fatura. O dinheiro está na sua conta; devolva se for o caso.", "Devolver", "Devolução em processamento", "Devolvido", "Registrar aviso novamente", "Substituir mesmo assim", "O cliente não tem CPF ou CNPJ cadastrado. Cadastre para gerar o Pix.", "Há {{count}} cobranças em aberto. Desconectar cancela todas.", "Desconectar mesmo assim".

- [ ] **Steps:** tests first (e.g. `CollectionSettings` converts `2,5` to `250` bp and refuses `25`; `InvoicePix` renders the QR and the copy button; `ExcessPayments` calls refund once and shows "Devolução em processamento"; `WebhookStep` never sends `replace_existing: true` without the confirmation), run (`vitest exit=1`), implement with `/impeccable`, run the whole UI suite and build (`vitest exit=0`), commit `feat(ui): collection by Pix with due date in the console`.

### Task 15: Runbook additions and the deploy of part B

**Files:** `docs/runbooks/byok.md` (sections appended)

- [ ] **Step 1: Append:**
1. **Webhook client:** after billing's deploy publishes the manifest, in ctech-account: `AWS_PROFILE=ctech api/cmd/createclient/create-billing-byok-webhook-client.sh {env}`; the Lambda reads the secret at cold start (redeploy or wait for a new container).
2. **CA bundle:** download Inter's webhook CA (`ca.crt`) from CTech's Inter account (Internet Banking → Minhas integrações → Certificado Webhook) for production, and the sandbox one; concatenate (`cat inter-prod-ca.crt inter-sandbox-ca.crt > inter-ca-bundle.pem`), `aws s3 cp inter-ca-bundle.pem s3://{env}-ctech-billing-byok-truststore/inter-ca-bundle.pem`, put the returned `VersionId` in `byok_truststore_version` (tfvars) and apply. The wallet's bucket `{env}-ctech-wallet-pix-gateway-truststore` holds the same production CA: compare with `sha256sum` (spec § 15 item 1). A pilot whose download differs: append it and repeat.
3. **DNS:** in Cloudflare, `billing-byok-hooks` (prod) / `billing-byok-hooks-{env}` CNAME → the `byok_hooks_target` output, **DNS only (grey cloud)**. Check: `curl -sS -o /dev/null -w '%{http_code}' https://billing-byok-hooks.aoctech.app/w/inter/x/y/pix` fails the TLS handshake without a client certificate (expected), never `200`.
4. **Sandbox webhook:** the first test-mode `cmd/byok` run registers `…/w/inter/sandbox/{secret}/pix` on the sandbox Pix key (`/ctech-billing/{env}/byok/inter-sandbox/pix-key`). Known deviation: the sandbox hook does not compare `x-conta-corrente` (the api never reads the sandbox account parameter); the `CHARGEREF#…#test#` pointer decides the tenant.
5. **Tenant zero on BYOK (pilot, optional):** only by an owner decision, recorded in the PR: `PUT /v1.0/console/collection` is refused for tenant zero; the operator uses `api/cmd/seed` with a tenant plan change of `payout_rail` (audited by the provisioner).
6. **Callback recovery after a webhook outage:** `ssh` to the leader, `/opt/app/job.sh byok -retry-callbacks -since=<RFC3339>`.
7. **Live clearance:** ADR 0030 + tfvars (part A § 6); after apply, `cmd/byok` promotes `pending_clearance` tenants on its next run and registers their webhooks.

- [ ] **Step 2: Commit** `git commit -m "docs: BYOK runbook, collection and webhooks"`.

## Final verification (part B)

As part A's, plus `terraform -chdir=terraform/billing test` covering both test files.

## Spec coverage (part B)

| Spec | Task |
|---|---|
| § 3.6 steps 1, 2, 4, "mesmo assim" | 10 |
| § 5 steps 1, 4, 5, `-retry-callbacks` | 11 |
| § 6.1 host, mTLS, DNS-only, CA bundle | 12, 15 |
| § 6.2 paths, both suffix variants, parser by registered product | 12 |
| § 6.3 webhook Lambda, notify route, secret compare, account check, re-query, 200/503 | 1, 2, 8, 12 |
| § 6.4 registration, conflict, rotation | 10 |
| § 7.1 rail, provider, `pending_clearance`, single gate | 3, 10, 11 |
| § 7.2 charge at finalization, txid, pointer, body, no ceiling, artefact | 4, 5 |
| § 7.3 void, credit note, expiry, new attempt | 5, 6 |
| § 7.4 confirmation, late charges, excess | 6 |
| § 7.5 refunds | 9 |
| § 7.6 no wallet | 13 |
| § 8.4 linking | 7 |
| § 9 collection side (sandbox, simulate, busy) | 5, 11, 13 |
| § 10 legal gate in API and Lambda | 3 (+ part A Task 9) |
| § 11.2, § 11.3 console | 14 |
| § 12.1 collection routes, notify | 8, 10, 13 |
| § 14 failure behaviour | 5, 6, 8, 10, 11 |
| § 16 steps 2, 3, 5, 6 | 1, 12, 15 |
| § 17 items 1, 2, 3, 5, 6, 9, 10, 12 | 13, 3, 3, 8, 6, 10, 10, 5 |
