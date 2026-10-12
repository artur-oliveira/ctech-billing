#!/usr/bin/env bash
# Subscribes CTech's own ORG_ customer in tenant zero (account organization
# 01a04ed6-1af9-745e-bcf7-d3b66fe52321) to price_billing_unlimited_internal_monthly
# (BYOK spec § 12.6). Idempotent: the Idempotency-Key is fixed per environment.
#
# In prod (looked up 2026-10-11) that customer is cus_01M4KYF8BKHPSAJ2YGHGKY10BY
# and its only subscription is DF-e's (price_dfe_unlimited_internal_monthly), so
# this creates a second subscription, on prod_billing; it is not a plan change
# (one entitling prod_billing subscription per customer: it has none yet).
#
# TOKEN is an M2M access token of a tenant-zero credential with
# billing:subscriptions:write; the operator mints it and exports it. Nothing
# secret is printed.
#   BILLING_API=https://billing-api.aoctech.app TOKEN=... CUSTOMER_ID=cus_... api/cmd/seed/pilot-subscription.sh prod
set -euo pipefail
ENV="${1:?usage: $0 <dev|stage|prod>}"
: "${BILLING_API:?}" "${TOKEN:?}" "${CUSTOMER_ID:?}"
curl -fsS -X POST "$BILLING_API/v1.0/subscriptions" \
  -H "Authorization: Bearer $TOKEN" -H "Idempotency-Key: pilot-byok-$ENV" -H "Content-Type: application/json" \
  -d "{\"customer_id\":\"$CUSTOMER_ID\",\"items\":[{\"price_id\":\"price_billing_unlimited_internal_monthly\",\"quantity\":1}]}" |
  jq '{id, status}'
