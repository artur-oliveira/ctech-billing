# Tenant zero on its own Inter connection

How CTech collects and pays through its own Inter account (BYOK spec 2026-10-11 § 12.6; wallet ledger spec 2026-10-14). Everything here is an **operator** action: a named person, audited, run on the Billing instance. None of it is reachable from the Console, on purpose.

All commands run on the Billing instance through `aws ssm send-command --profile ctech` (instance: the one tagged `prod-ctech-billing`), as `/opt/app/job.sh byok …`. They need `-mode=live` (no default) and `-operator=<your name>`, which lands in the audit trail.

## State as of 2026-10-14

- Inter app created for Billing with a **dedicated random Pix key** (never the Wallet's: Inter keeps one notice address per key). The account number is sent **with its check digit, digits only, no leading zeros** (Inter's `x-conta-corrente`); without the digit `GET /saldo` answers `401 Login/senha inválido`.
- Connected live in the Console. The Pix notice points at `billing-byok-hooks.aoctech.app` (registered on the dedicated key).
- Tenant zero is on the BYOK rail with a 2 % fine and 1 % monthly interest.
- A live R$ 0,10 invoice (number 9) was issued and paid by Pix; the webhook settled it in about three seconds, and the statement line carried the payer.

## Commands

| What | Command |
|---|---|
| See the state | `byok -tenant-rail=byok -mode=live -operator=NAME -dry-run` |
| Move the rail to BYOK / back | `byok -tenant-rail=byok\|wallet -mode=live -operator=NAME` |
| Set the late fees (basis points; 200 = 2 %) | add `-fine-bp=200 -interest-bp=100` to the rail command |
| Re-test the stored credentials and record what they can do now | `byok -tenant-refresh -mode=live -operator=NAME` |
| Issue a one-off invoice (prints the checkout link) | `byok -tenant-invoice=CUSTOMER_ID -mode=live -operator=NAME -invoice-amount-cents=10 -invoice-reference=REF "-invoice-description=TEXT"` |
| Pay a Pix out to a CPF's key | `byok -tenant-payout=CPF -mode=live -operator=NAME -payout-amount-cents=10 -payout-reference=REF` (a dry run; add `-yes` to send) |

A repeated `-invoice-reference` or `-payout-reference` returns the same invoice or payout; it never issues or pays twice.

## Paying out requires

1. The Inter app has the **Pix payment** permissions (`pagamento-pix.read/write`) and the Banking webhook ones (`webhook-banking.read/write`). The Console guide deliberately leaves them off for other tenants.
2. The connection **knows** it can pay: run `-tenant-refresh` once after the permissions are ticked (a connection made before api-commons v1.22 recorded only statement and collection). Expect `capabilities` to include `payout`.
3. The outbound Lambda allows money out for the account organization only if it is in `BYOK_PAYOUT_ACCOUNT_ORGS` (terraform `byok_payout_account_orgs`; prod names CTech's own organization only). Any other organization is refused at the Lambda whatever the API asks.
4. Payments from the Inter account are **self-approved** (no manual approval per Pix in Inter's Gestão de Aprovações).

## First live payout (do once, by hand)

Inter's reference does not list the payment `status` values, so the provider maps only the ones that clearly mean paid or refused and leaves the rest `processing`. Before the Wallet pays users through this path:

1. `-tenant-payout=<your CPF> … -payout-amount-cents=10 -payout-reference=proof-1` (dry run, then with `-yes`).
2. Read the raw responses: `aws logs tail /aws/lambda/prod-ctech-billing-byok-outbound --since 10m --profile ctech | grep '/banking/v2/pix'`. Note the `status` the lookup returns when the payment is done.
3. If it is not one of the values in `api-commons/inter/payout.go` (`payoutStatus`), add it there with a test, release, bump, deploy. Until then the payout stays `processing` and the sweep alarms after six hours.

## Rollback

- Rail back to the Wallet: `-tenant-rail=wallet`. Invoices already issued keep the Pix charge they have; the sweep keeps confirming them.
- Stop all money out: empty `byok_payout_account_orgs` in the tfvars and apply.
