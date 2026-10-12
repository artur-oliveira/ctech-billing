# BYOK webhooks (spec 2026-10-11 § 6): one mTLS host for every merchant,
# billing-byok-hooks[-env].aoctech.app, in front of a credential-less Lambda
# that tells the api which charges to re-query. The wallet's pix-gateway
# pattern in Terraform (ADR 0010). Cost: HTTP API at US$ 1 per million calls
# and a 128 MB Lambda invoked only on payment notices; no REST API, no WAF,
# no NAT; the truststore is one small S3 object.

# The truststore: Inter's webhook CA (and any CA a pilot's download adds), as
# one PEM bundle uploaded by terraform/scripts/seed-byok-truststore.sh. The
# object is not managed here; its version id is var.byok_truststore_version.
resource "aws_s3_bucket" "byok_truststore" {
  bucket = "${var.environment}-ctech-billing-byok-truststore"
  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_s3_bucket_versioning" "byok_truststore" {
  bucket = aws_s3_bucket.byok_truststore.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_public_access_block" "byok_truststore" {
  bucket                  = aws_s3_bucket.byok_truststore.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# The Lambda's own M2M client (ctech-account, scope internal:billing:byok-
# notify), created by ctech-account's create-billing-byok-webhook-client.sh
# into these two parameters. Read at the first call, so the function deploys
# before the client exists and answers 503 meanwhile (the provider retries).
resource "aws_iam_role" "byok_webhook" {
  name               = local.byok_webhook_name
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
}

resource "aws_cloudwatch_log_group" "byok_webhook" {
  name              = "/aws/lambda/${local.byok_webhook_name}"
  retention_in_days = 14
}

data "aws_iam_policy_document" "byok_webhook" {
  statement {
    sid     = "ReadOwnClient"
    actions = ["ssm:GetParameter"]
    resources = [
      "arn:aws:ssm:${var.aws_region}:${var.aws_account}:parameter${local.byok_webhook_ssm_prefix}/client-id",
      "arn:aws:ssm:${var.aws_region}:${var.aws_account}:parameter${local.byok_webhook_ssm_prefix}/client-secret",
    ]
  }
  statement {
    sid       = "Logs"
    actions   = ["logs:CreateLogStream", "logs:PutLogEvents"]
    resources = ["${aws_cloudwatch_log_group.byok_webhook.arn}:*"]
  }
  statement {
    sid       = "DecryptViaSsm"
    actions   = ["kms:Decrypt"]
    resources = ["arn:aws:kms:${var.aws_region}:${var.aws_account}:key/*"]
    condition {
      test     = "StringEquals"
      variable = "kms:ViaService"
      values   = ["ssm.${var.aws_region}.amazonaws.com"]
    }
  }
  # No DynamoDB, no provider credential, no billing secret.
}

resource "aws_iam_role_policy" "byok_webhook" {
  name   = "${local.byok_webhook_name}-policy"
  role   = aws_iam_role.byok_webhook.id
  policy = data.aws_iam_policy_document.byok_webhook.json
}

resource "aws_lambda_function" "byok_webhook" {
  function_name = local.byok_webhook_name
  role          = aws_iam_role.byok_webhook.arn
  runtime       = "provided.al2023"
  handler       = "bootstrap"
  architectures = ["arm64"]
  memory_size   = 128
  timeout       = 10
  filename      = data.archive_file.byok_placeholder.output_path

  environment {
    variables = {
      ENVIRONMENT                = var.environment
      BILLING_API_URL            = "https://${local.api_domain}"
      CTECH_TOKEN_URL            = "https://${local.accounts_api_domain}/v1.0/token"
      BYOK_WEBHOOK_CLIENT_PARAM  = "${local.byok_webhook_ssm_prefix}/client-id"
      BYOK_WEBHOOK_SECRET_PARAM  = "${local.byok_webhook_ssm_prefix}/client-secret"
      AWS_USE_DUALSTACK_ENDPOINT = "true"
    }
  }

  lifecycle {
    ignore_changes = [filename, source_code_hash]
  }

  depends_on = [aws_cloudwatch_log_group.byok_webhook, aws_iam_role_policy.byok_webhook]
}

resource "aws_apigatewayv2_api" "byok_hooks" {
  name                         = "${var.environment}-ctech-billing-byok-hooks"
  protocol_type                = "HTTP"
  disable_execute_api_endpoint = true # the mTLS custom domain is the only way in
}

resource "aws_apigatewayv2_integration" "byok_hooks" {
  api_id                 = aws_apigatewayv2_api.byok_hooks.id
  integration_type       = "AWS_PROXY"
  integration_uri        = aws_lambda_function.byok_webhook.invoke_arn
  payload_format_version = "2.0"
}

# § 6.2: the registered path and the provider's documented suffix variant.
# No $default route: any other path is 404 without reaching the Lambda.
resource "aws_apigatewayv2_route" "byok_hooks" {
  for_each = toset([
    "POST /w/{provider}/{hook}/{secret}/{product}",
    "POST /w/{provider}/{hook}/{secret}/{product}/{suffix}",
  ])
  api_id    = aws_apigatewayv2_api.byok_hooks.id
  route_key = each.value
  target    = "integrations/${aws_apigatewayv2_integration.byok_hooks.id}"
}

resource "aws_apigatewayv2_stage" "byok_hooks" {
  api_id      = aws_apigatewayv2_api.byok_hooks.id
  name        = "$default"
  auto_deploy = true
  default_route_settings {
    throttling_burst_limit = 50
    throttling_rate_limit  = 20
  }
}

resource "aws_lambda_permission" "byok_hooks" {
  statement_id  = "AllowApiGatewayInvoke"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.byok_webhook.function_name
  principal     = "apigateway.amazonaws.com"
  source_arn    = "${aws_apigatewayv2_api.byok_hooks.execution_arn}/*/*"
}

# Regional with mTLS (§ 6.1): created once the CA bundle's version is known.
resource "aws_apigatewayv2_domain_name" "byok_hooks" {
  count       = var.byok_truststore_version == "" ? 0 : 1
  domain_name = local.byok_hooks_domain
  domain_name_configuration {
    certificate_arn = var.wildcard_certificate_arn
    endpoint_type   = "REGIONAL"
    security_policy = "TLS_1_2"
  }
  mutual_tls_authentication {
    truststore_uri     = "s3://${aws_s3_bucket.byok_truststore.bucket}/inter-ca-bundle.pem"
    truststore_version = var.byok_truststore_version
  }
}

resource "aws_apigatewayv2_api_mapping" "byok_hooks" {
  count       = length(aws_apigatewayv2_domain_name.byok_hooks)
  api_id      = aws_apigatewayv2_api.byok_hooks.id
  domain_name = aws_apigatewayv2_domain_name.byok_hooks[0].id
  stage       = aws_apigatewayv2_stage.byok_hooks.id
}

# The Cloudflare record: a DNS-only (grey cloud) CNAME to this target.
output "byok_hooks_target" {
  value = length(aws_apigatewayv2_domain_name.byok_hooks) == 0 ? "" : aws_apigatewayv2_domain_name.byok_hooks[0].domain_name_configuration[0].target_domain_name
}

output "byok_truststore_bucket" {
  value = aws_s3_bucket.byok_truststore.bucket
}
