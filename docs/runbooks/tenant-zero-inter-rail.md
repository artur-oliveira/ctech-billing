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
| Record that an invoice's money was returned outside Billing (moves no money) | `byok -tenant-credit=INVOICE_ID -mode=live -operator=NAME -credit-amount-cents=10 "-credit-reason=TEXT" -credit-ref=BANK_REF` |

A repeated `-invoice-reference` or `-payout-reference` returns the same invoice or payout; it never issues or pays twice.

## Paying out requires

1. The Inter app has the **Pix payment** permissions (`pagamento-pix.read/write`) and the Banking webhook ones (`webhook-banking.read/write`). The Console guide deliberately leaves them off for other tenants.
2. The connection **knows** it can pay: run `-tenant-refresh` once after the permissions are ticked (a connection made before api-commons v1.22 recorded only statement and collection). Expect `capabilities` to include `payout`.
3. The outbound Lambda allows money out for the account organization only if it is in `BYOK_PAYOUT_ACCOUNT_ORGS` (terraform `byok_payout_account_orgs`; prod names CTech's own organization only). Any other organization is refused at the Lambda whatever the API asks.
4. Payments from the Inter account are **self-approved** (no manual approval per Pix in Inter's Gestão de Aprovações).

## First live payout — done 2026-10-14

A live R$ 0,10 payout to the owner's CPF key (`-tenant-payout`, reference `proof-payout-20261014`) arrived. What Inter answered:

- `POST /banking/v2/pix` → 200, `tipoRetorno: PROCESSADO`, `codigoSolicitacao` (the id a lookup uses).
- First lookup: `status: ENVIADO`, `endToEnd` empty, history `TRANSACAO_CRIADA → TRANSACAO_APROVADA → PIX_ENVIADO`.
- A minute later (the sweep): `status: PAGO`, `endToEnd` filled, history ending `PIX_PAGO`. Billing marked the payout `done` with that end-to-end id.

So: `ENVIADO` is "sent, not confirmed yet" (processing) and `PAGO` is done (api-commons v1.24.0 maps both). Failure statuses have not been observed; the provider treats only clearly refusing words as failed and leaves every other unknown text `processing` with an error log, so a new word is noticed, never guessed. Re-run this check whenever Inter changes its API.

## Rollback

- Rail back to the Wallet: `-tenant-rail=wallet`. Invoices already issued keep the Pix charge they have; the sweep keeps confirming them.
- Stop all money out: empty `byok_payout_account_orgs` in the tfvars and apply.

## The bank's notice about Pix payments we send

A withdrawal is a Billing payout. Billing asks the bank about it 3, 6, 12, 25, 45 and 90 seconds after sending it, and the bank also tells us: Inter's Banking webhook `pix-pagamento` (`PUT /banking/v2/webhooks/pix-pagamento`, scope `webhook-banking.write`) calls the same mTLS host as the Pix notices, on the path `/w/inter/<hook>-payout/<secret>/pix-pagamento`. The notice settles nothing by itself: it makes billing ask the bank about the tenant's processing payouts. The body was not documented where it could be read, so it is read leniently and every request is in the log of `prod-ctech-billing-byok-webhook` (`inter webhook request`).

```
/opt/app/job.sh byok -tenant-payout-webhook=show     -mode=live -operator=NAME
/opt/app/job.sh byok -tenant-payout-webhook=register -mode=live -operator=NAME   # -replace to take over someone else's
/opt/app/job.sh byok -tenant-payout-webhook=remove   -mode=live -operator=NAME
```

Order of events for a withdrawal: payout created (`processing`) -> the bank settles -> billing settles the payout and writes `payout.done` (or `payout.failed`) in the same transaction -> the event is delivered to the wallet at once -> the wallet completes or reverses the withdrawal.
