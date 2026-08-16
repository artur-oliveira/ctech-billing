environment = "prod"

# Tenant zero (ADR 0012): the organization whose customers the consumer portal
# serves. It must be the `organization.id` of the plan `cmd/seed` applies
# (api/tenants/ctech.json), and nothing checks that the two agree — when they
# disagree every portal user gets 403 "nenhuma conta de cobrança para este
# usuário", which reads like missing customer data rather than a typo here.
portal_organization_id = "ctech"

max_size = 3
