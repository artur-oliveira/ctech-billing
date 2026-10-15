environment = "prod"

# Tenant zero (ADR 0012): the organization whose customers the consumer portal
# serves. It must be the `organization.id` of the plan `cmd/seed` applies
# (api/tenants/ctech.json), and nothing checks that the two agree — when they
# disagree every portal user gets 403 "nenhuma conta de cobrança para este
# usuário", which reads like missing customer data rather than a typo here.
portal_organization_id = "ctech"

max_size  = 2
os_family = "alpine"
# BYOK webhooks (docs/runbooks/byok.md § 10): the version of
# inter-ca-bundle.pem uploaded by terraform/scripts/seed-byok-truststore.sh
# (Inter's webhook intermediate + root CA, the same bundle the wallet trusts;
# sha256 804d9856312184e8554cf957c9d22b35ed82055e435dca7b664b0c6b64f98ca4).
byok_truststore_version = "2ZaTHrSZRLn9z4s4wf3AHt.4xVO.IQiI"

# CTech's own organization: the only connection that may pay Pix out (wallet withdrawals).
byok_payout_account_orgs = "01a04ed6-1af9-745e-bcf7-d3b66fe52321"

# Realtime (docs/runbooks/realtime.md#rollout): the change log, the publisher and
# GET /v1.0/ws. Nothing reaches a user until a UI build names a surface.
realtime_enabled = true
