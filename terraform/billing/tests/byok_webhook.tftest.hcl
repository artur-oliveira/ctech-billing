# Spec 2026-10-11 § 6.1-6.3: the webhook host. Plan only, mock providers.
mock_provider "aws" {
  mock_data "aws_ssm_parameter" {
    defaults = { value = "mock" }
  }
  mock_data "aws_vpc" {
    defaults = { cidr_block = "10.0.0.0/16" }
  }
  mock_data "aws_iam_policy_document" {
    defaults = { json = "{\"Version\":\"2012-10-17\",\"Statement\":[]}" }
  }
}
mock_provider "archive" {}

override_resource {
  target          = aws_lambda_function.byok_outbound
  override_during = plan
  values          = { arn = "arn:aws:lambda:us-east-1:123456789012:function:dev-ctech-billing-byok-outbound" }
}
override_resource {
  target          = aws_s3_bucket.byok_truststore
  override_during = plan
  values          = { arn = "arn:aws:s3:::dev-ctech-billing-byok-truststore", bucket = "dev-ctech-billing-byok-truststore" }
}

override_resource {
  target          = aws_cloudwatch_log_group.byok_webhook
  override_during = plan
  values          = { arn = "arn:aws:logs:us-east-1:123456789012:log-group:/aws/lambda/dev-ctech-billing-byok-webhook" }
}

variables {
  environment = "dev"
}

run "the_webhook_api_has_only_the_two_pix_routes" {
  command = plan

  assert {
    condition     = aws_apigatewayv2_api.byok_hooks.disable_execute_api_endpoint == true && aws_apigatewayv2_api.byok_hooks.protocol_type == "HTTP"
    error_message = "HTTP API, reachable only through the mTLS custom domain"
  }
  assert {
    condition     = length([for r in aws_apigatewayv2_route.byok_hooks : r if r.route_key == "$default"]) == 0
    error_message = "no $default route: anything else is 404 at the edge"
  }
  assert {
    condition     = length(aws_apigatewayv2_route.byok_hooks) == 2
    error_message = "exactly …/{product} and …/{product}/{suffix}"
  }
  assert {
    condition     = length(aws_apigatewayv2_domain_name.byok_hooks) == 0
    error_message = "the domain waits for the truststore version"
  }
}

run "the_webhook_lambda_holds_no_credential" {
  command = plan

  assert {
    condition = alltrue([for st in data.aws_iam_policy_document.byok_webhook.statement : alltrue([
      for r in st.resources : !strcontains(r, "/byok/") && !strcontains(r, "/ctech-wallet/") && !strcontains(r, ":table/")
    ])])
    error_message = "the webhook Lambda reads neither the credentials key, nor the wallet's parameters, nor a table"
  }
  assert {
    condition     = aws_lambda_function.byok_webhook.memory_size == 128 && aws_lambda_function.byok_webhook.architectures[0] == "arm64"
    error_message = "128 MB, arm64"
  }
}

run "the_domain_is_regional_with_mtls" {
  command = plan
  variables {
    byok_truststore_version = "v1"
  }
  assert {
    condition     = aws_apigatewayv2_domain_name.byok_hooks[0].domain_name_configuration[0].endpoint_type == "REGIONAL" && aws_apigatewayv2_domain_name.byok_hooks[0].domain_name_configuration[0].security_policy == "TLS_1_2"
    error_message = "regional, TLS 1.2"
  }
  assert {
    condition     = aws_apigatewayv2_domain_name.byok_hooks[0].mutual_tls_authentication[0].truststore_version == "v1" && endswith(aws_apigatewayv2_domain_name.byok_hooks[0].mutual_tls_authentication[0].truststore_uri, "/inter-ca-bundle.pem")
    error_message = "mTLS against the CA bundle"
  }
  assert {
    condition     = aws_apigatewayv2_domain_name.byok_hooks[0].domain_name == "billing-byok-hooks-dev.aoctech.app"
    error_message = "the env host"
  }
}
