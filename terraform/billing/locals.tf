locals {
  name = "${var.environment}-ctech-billing"

  # {env}_billing_{table} is the company naming standard. The prefix the Go
  # service reads is the first two segments — `dev_billing` — exactly as
  # ctech-dfe passes `dev_dfe`; api/internal/repositories.TableName appends the
  # logical table name to it.
  table_prefix = var.table_prefix != "" ? var.table_prefix : "${var.environment}_billing"

  # Table and index names are not declared here. They come from
  # api/internal/repositories/schema.json, which dynamodb.tf reads and the Go
  # service embeds — one file, two readers, nothing to keep in step.

  ssm_prefix = "/ctech-billing/${var.environment}/billing"

  ssm_paths = {
    table_prefix = "${local.ssm_prefix}/table-prefix"
    role_arn     = "${local.ssm_prefix}/role-arn"

    # Collection secrets (ADR 0004). Declared here so the path is one string the
    # IAM policy and the compute userdata both read, rather than a convention two
    # files agree on until one of them is edited.
    wallet_client_id      = "${local.ssm_prefix}/wallet-client-id"
    wallet_client_secret  = "${local.ssm_prefix}/wallet-client-secret"
    wallet_webhook_secret = "${local.ssm_prefix}/wallet-webhook-secret"
    checkout_link_secret  = "${local.ssm_prefix}/checkout-link-secret"

    # Encrypts the stored values that are personal data on their own — today the
    # customer's CPF/CNPJ. Not a collection secret: rotating one of those costs a
    # re-issued link, rotating this one makes existing tax ids unreadable, so it
    # is declared apart from them to keep the two from being treated alike.
    field_encryption_key = "${local.ssm_prefix}/field-encryption-key"

    # The verified SES identity dunning reminders come from. A plain String: it
    # is an address, printed on every email it sends.
    email_from = "${local.ssm_prefix}/email-from"

    # Where the front-end deploy job finds what to write to. CI cannot run
    # `terraform output` — it holds a role that may sync a bucket and nothing
    # more, deliberately — so the three values it needs are published here.
    frontend_bucket          = "${local.ssm_prefix}/frontend-bucket"
    frontend_distribution_id = "${local.ssm_prefix}/frontend-distribution-id"
    frontend_route_store_arn = "${local.ssm_prefix}/frontend-route-store-arn"
  }

  # ── Compute ────────────────────────────────────────────────────────────────

  asg_name = local.name
  # The two ports and the health path are read by nginx.conf, by the HAProxy
  # route, by deploy.sh's post-deploy probe and by the security group. One
  # definition, four readers.
  app_port    = 8004 # the Go binary; config.Config.Port has the same default
  nginx_port  = 8080 # what HAProxy connects to
  health_path = "/v1.0/health-check"

  s3_prefix            = "ctech-billing"
  current_artifact_key = "${local.s3_prefix}/api/current.zip"

  log_group_app            = "/ctech-billing/${var.environment}/app"
  log_group_nginx          = "/ctech-billing/${var.environment}/nginx"
  metric_namespace         = "CtechBilling/${var.environment}"
  metric_payment_integrity = "PaymentIntegrityAlarms"

  # Valkey logical DB. ctech-cdk's convention: /0 ctech-dfe cache, /1 ws pub/sub,
  # /2 ctech-wallet. Billing owns /3 — it caches ctech-account's JWKS, and a
  # cache that shares a keyspace with another service's locks is a bug waiting
  # for a key collision.
  valkey_db = 3

  # ── Domains ────────────────────────────────────────────────────────────────
  # Same domainForEnv(env, prefix) shape as every other CTech service.

  base_domain       = "aoctech.app"
  private_zone_name = "internal.${local.base_domain}"

  api_domain      = var.environment == "prod" ? "billing-api.${local.base_domain}" : "billing-api-${var.environment}.${local.base_domain}"
  app_domain      = var.environment == "prod" ? "billing.${local.base_domain}" : "billing-${var.environment}.${local.base_domain}"
  internal_domain = var.environment == "prod" ? "billing.${local.private_zone_name}" : "billing-${var.environment}.${local.private_zone_name}"

  app_domain_url            = "https://${local.app_domain}"
  internal_lbalancer_domain = "lbalancer.${local.private_zone_name}"

  # ctech-account's two public hostnames. The portal's OAuth client talks to the
  # API one directly (token exchange, silent refresh) and is redirected to the
  # app one, so both have to appear in the front end's connect-src — a CSP that
  # omits them turns every login into a console error nobody sees.
  accounts_api_domain = var.environment == "prod" ? "accounts-api.${local.base_domain}" : "accounts-${var.environment}-api.${local.base_domain}"
  accounts_app_domain = var.environment == "prod" ? "accounts.${local.base_domain}" : "accounts-${var.environment}.${local.base_domain}"

  # ── Front end ──────────────────────────────────────────────────────────────
  # The portal is a Next.js static export in S3 behind CloudFront, the same
  # shape as every other CTech front end (@aoctech/cdk's
  # createNextjsStaticFrontend). This root is Terraform rather than CDK, so the
  # construct is ported here; the resource set and the naming are deliberately
  # identical so the two can be read against each other.

  frontend_bucket    = "${var.environment}-ctech-billing-frontend"
  frontend_kvs_name  = "${var.environment}-ctech-billing-routes"
  frontend_oac_name  = "${var.environment}-ctech-billing-oac"
  frontend_func_name = "${var.environment}-ctech-billing-url-rewrite"

  # The browser origins the API answers cross-origin. Exactly the portal's own
  # domain and nothing else: the checkout and the portal are the same origin,
  # and no other page has a reason to read a billing response.
  #
  # It reaches the instances as CORS_ALLOWED_ORIGINS, which config.Load requires
  # in production — a prod deployment without it refuses to boot rather than
  # serving a wildcard.
  cors_allowed_origins = [local.app_domain_url]

  # Kept, and no longer the path the app takes. The portal now calls
  # `${local.api_domain}` directly, because going back through this
  # distribution meant CloudFront → Cloudflare → HAProxy for a request with one
  # destination (ADR 0013's amendment). These behaviours stay as the rollback:
  # setting NEXT_PUBLIC_API_URL back to empty restores same-origin without a
  # Terraform change. `/.well-known/*` is not a rollback — a client discovering
  # billing from the portal's own hostname still resolves it here.
  frontend_api_patterns = ["/v1.0/*", "/.well-known/*"]

  private_hosted_zone_id_parameter = "/ctech/global/dns/private-hosted-zone-id"

  # ── Other roots' SSM ───────────────────────────────────────────────────────
  # Read, never written. These are the seam between roots that must not share
  # Terraform state, and the paths are ctech-cdk's.

  shared_ssm = {
    vpc_id                 = "/ctech/${var.environment}/network/vpc-id"
    edge_security_group_id = "/ctech/${var.environment}/network/alb-sg-id"
    valkey_url             = "/ctech/${var.environment}/valkey/url"
    deployments_bucket     = "/ctech/${var.environment}/s3/deployments-bucket"
    logs_bucket            = "/ctech/${var.environment}/s3/logs-bucket"
    routes                 = "/ctech/${var.environment}/lbalancer/routes"
    ec2_scripts_bucket     = "/ctech/${var.environment}/ec2-scripts/bucket"
    ec2_scripts_version    = "/ctech/${var.environment}/ec2-scripts/version"
  }

  account_ssm = {
    internal_base_url = "/ctech-account/${var.environment}/internal-base-url"
    app_url           = "/ctech-account/${var.environment}/app-url"
    internal_jwks_url = "/ctech-account/${var.environment}/internal-jwks-url"
  }

  wallet_ssm = {
    internal_base_url = "/ctech-wallet/${var.environment}/internal-base-url"
  }
}
