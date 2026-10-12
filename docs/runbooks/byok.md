# Runbook: BYOK (bring your own key), phase 1 part A

Spec: [`docs/specs/2026-10-11-byok-inter-phase-1.md`](../specs/2026-10-11-byok-inter-phase-1.md) (with its two
2026-10-11 amendments: test mode uses the wallet's Inter integration; **no legal gate**). Decisions:
[ADR 0029](../adr/0029-byok-phase-1.md). Only `prod` exists; every command below is for `prod`.

## 1. The credentials key (once, before the first deploy of part A)

```sh
AWS_PROFILE=ctech terraform/scripts/create-byok-credentials-key.sh prod
```

It creates the SSM SecureString `/ctech-billing/prod/byok/credentials-key` (32 random bytes, base64), refuses to
replace an existing one, never prints the value and passes it to the AWS CLI through a private temporary file (never
argv). Created in prod on 2026-10-12. Only the outbound Lambda's
role can read it; the api cannot.

**Back it up** with the account's break-glass material, the same way as `field-encryption-key`. Losing it makes
every stored connection unreadable (merchants would upload their credentials again). The person storing the backup
reads it once, straight into the vault, and pastes it nowhere else:

```sh
AWS_PROFILE=ctech aws ssm get-parameter --region us-east-1 --with-decryption \
  --name /ctech-billing/prod/byok/credentials-key --query Parameter.Value --output text
```

## 2. Test mode: the shared sandbox integration

CTech's Inter integration is the wallet's. The outbound Lambda reads
`/ctech-wallet/prod/inter/{client-id,client-secret,mtls-cert,mtls-key}` by IAM grant, once per cold start; nothing is
copied. Billing keeps two non-secret parameters of its own, created by Terraform with the value `SET-OUT-OF-BAND`:

```sh
AWS_PROFILE=ctech aws ssm put-parameter --region us-east-1 --overwrite --type String \
  --name /ctech-billing/prod/byok/inter-sandbox/account --value <sandbox account digits>
AWS_PROFILE=ctech aws ssm put-parameter --region us-east-1 --overwrite --type String \
  --name /ctech-billing/prod/byok/inter-sandbox/pix-key --value <sandbox EVP key>
```

Both come from Inter's sandbox for this integration (the account number of the sandbox account, and an EVP key
created there). Terraform ignores later changes to their values. A new value reaches the Lambda on its next cold
start; to force one, update its configuration with the same values or wait for the next code deploy.

Smoke test: console, test mode, Conexões bancárias, Banco Inter, **Ativar Inter (teste)**, then **Sincronizar
agora** answers 202.

If Inter's sandbox refuses the production integration's credentials (the risk recorded in the spec amendment), test
mode answers `byok_provider_unavailable`. The fix is a dedicated sandbox integration whose four values go into
`/ctech-billing/prod/byok/inter-sandbox/{client-id,client-secret,cert,key}` (SecureStrings) and
`BYOK_SANDBOX_CREDENTIAL_PARAMS` plus the role's read grant in `terraform/billing/byok.tf` pointed at them.

## 3. Deploy order (part A)

1. ctech-go-common `v1.18.0` tagged (`byok`, `inter`).
2. `@aoctech/ui` `0.5.0` published (`Steps`, `FileDrop`).
3. Billing's pull request merged: `deploy.yml` runs Terraform (the Lambda from its placeholder, its role, the sandbox
   parameters), then the API job (**Deploy BYOK Lambdas** before the instances), then the frontend.

Checks:

```sh
AWS_PROFILE=ctech aws lambda get-function-configuration --region us-east-1 \
  --function-name prod-ctech-billing-byok-outbound --query '{state:State,update:LastUpdateStatus,runtime:Runtime,arch:Architectures}'
# State Active, LastUpdateStatus Successful, provided.al2023, arm64
```

The hourly job runs on the leader at :20 (Alpine: crontab `20 * * * * /opt/app/job.sh byok`, plus `@reboot`; AL2023:
`systemctl list-timers billing-byok.timer`). The userdata change reaches the instances on the instance refresh that a
launch template change triggers. Its log is `/var/log/app/jobs.log`.

## 4. The pilot: live statement sync for CTech

CTech's organization is `01a04ed6-1af9-745e-bcf7-d3b66fe52321` (tenant zero's linked organization).

1. In CTech's Inter Internet Banking, **Integrar, Nova integração**, with the scopes of the onboarding checklist
   (`extrato.read` and, for collection, `cobv.read cobv.write pix.read pix.write webhook.read webhook.write`; never a
   `pagamento-*` scope). Wait for Inter's validation, download the certificate and the key, copy the client secret.
2. An owner or admin opens `/finance/settings/connections/inter` (Finanças, CTech's space, live) or
   `/console/connections/inter`, and sends the form. For the Pix key use a **dedicated EVP key**, never the wallet's
   Pix key: Inter's Pix webhook is one per key, and the wallet's key's webhook belongs to the wallet.
3. The catalogue gets `price_billing_unlimited_internal_monthly` with the next `api/cmd/seed/run.sh prod`.
4. The pilot subscription: CTech's `ORG_` customer in tenant zero is `cus_01M4KYF8BKHPSAJ2YGHGKY10BY` and holds only
   the DF-e subscription, so it is a new subscription, not a plan change:

   ```sh
   BILLING_API=https://billing-api.aoctech.app TOKEN=<tenant-zero M2M token with billing:subscriptions:write> \
     CUSTOMER_ID=cus_01M4KYF8BKHPSAJ2YGHGKY10BY api/cmd/seed/pilot-subscription.sh prod
   ```

   Statement sync does not depend on it (spec D16); collection (part B) does.

## 5. Certificate renewal

Merchants are e-mailed at D-90, D-30 and D-7 before their integration certificate expires and on expiry (to the
connection's notify e-mail; without SES the operator gets an alert instead). The screen shows a banner from D-90.
Renewal: the merchant downloads a new pair at Inter and sends it with **Renovar certificado**; the client id and
secret stay sealed and the api never sees the secret again.

The shared sandbox integration is the wallet's: the operator gets an alert at D-30, D-7 and D-1 of its certificate.
Renewing it in the wallet's parameters renews test mode too (the Lambda reads them at its next cold start).

## 6. There is no legal gate

Owner decision, 2026-10-11 (spec amendment): the merchant grants access to its own account through the bank's
official API, so live collection needs no clearance and there are no `BYOK_LIVE_*` flags. The Lambda's op allowlist
is a security control only (unknown op, provider or mode refused; `SimulatePayment` only against a sandbox).

## 7. Emergency stop

To stop every call to every bank at once (a leaked key, a misbehaving bank, a bug), take all concurrency from the
outbound Lambda. Every BYOK action answers `byok_provider_unavailable` and the hourly job fails safe (nothing is
posted, nothing is marked paid):

```sh
AWS_PROFILE=ctech aws lambda put-function-concurrency --region us-east-1 \
  --function-name prod-ctech-billing-byok-outbound --reserved-concurrent-executions 0
# undo:
AWS_PROFILE=ctech aws lambda delete-function-concurrency --region us-east-1 --function-name prod-ctech-billing-byok-outbound
```

One merchant only: the merchant (or an admin of the organization) disconnects in the console; CTech deletes the
credentials and the token, the Finanças history stays read-only.

## 8. Costs

The plan's "Custos de infra" table is the budget: about US$ 1/month at 10 live connections, US$ 11 at 1.000,
US$ 105 at 10.000 (all three parts). Part A adds: the outbound Lambda (256 MB arm64, free tier at the pilot), its
14-day log group, the `byok` table (on-demand, key-condition reads only), three standard SSM parameters, Valkey keys
in the existing cluster and one more hourly job on the existing leader. No NAT gateway, no KMS key, no Secrets
Manager, no new instance.
