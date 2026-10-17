# Spec 2026-10-11 § 17 item 4: the api role cannot read the credentials key;
# the outbound Lambda can; there is no legal gate (owner decision 2026-10-11);
# no VPC attachment.
# Plan only, against mock providers: no credentials, nothing reaches AWS.
mock_provider "aws" {
  mock_data "aws_ssm_parameter" {
    defaults = { value = "mock" }
  }
  mock_data "aws_vpc" {
    defaults = { cidr_block = "10.0.0.0/16" }
  }
  # The mock invents a random string for computed JSON; IAM resources validate it.
  mock_data "aws_iam_policy_document" {
    defaults = { json = "{\"Version\":\"2012-10-17\",\"Statement\":[]}" }
  }
}
mock_provider "archive" {}

# The erasure topic ARN is read from ctech-account's SSM parameter; the mock
# would invent a string that is not an ARN, which the subscription validates.
override_data {
  target = data.aws_ssm_parameter.erasure_topic_arn
  values = { value = "arn:aws:sns:us-east-1:123456789012:dev-account-user-erasure" }
}


# ARNs are computed after apply; known values let the policy statements be read
# at plan time.
override_resource {
  target          = aws_lambda_function.byok_outbound
  override_during = plan
  values          = { arn = "arn:aws:lambda:us-east-1:123456789012:function:dev-ctech-billing-byok-outbound" }
}
variables {
  environment = "dev"
}

run "the_api_never_reads_the_credentials_key" {
  command = plan

  assert {
    condition = alltrue([
      for st in concat(data.aws_iam_policy_document.ssm_read.statement, data.aws_iam_policy_document.byok_api.statement) :
      !anytrue([for r in st.resources : strcontains(r, "/byok/")]) || alltrue([for r in st.resources : !strcontains(r, "/byok/credentials-key") && !endswith(r, "/byok/*")])
    ])
    error_message = "the api role must not read /byok/credentials-key"
  }

  assert {
    condition     = anytrue([for st in data.aws_iam_policy_document.byok_outbound.statement : contains(st.resources, local.byok_credentials_key_arn)])
    error_message = "the outbound Lambda reads the credentials key"
  }

  assert {
    condition     = length([for k in keys(aws_lambda_function.byok_outbound.environment[0].variables) : k if startswith(k, "BYOK_LIVE_")]) == 0
    error_message = "there is no legal gate (owner decision 2026-10-11): no BYOK_LIVE_* variable"
  }

  assert {
    condition     = alltrue([for st in data.aws_iam_policy_document.byok_api.statement : alltrue([for r in st.resources : !strcontains(r, ":parameter/")])])
    error_message = "the api reads no BYOK parameter: it only invokes the outbound Lambda"
  }

  assert {
    condition     = aws_lambda_function.byok_outbound.vpc_config == null || length(aws_lambda_function.byok_outbound.vpc_config) == 0
    error_message = "the outbound Lambda is not VPC-attached (IPv4 egress without NAT)"
  }
}

# Money out: the outbound Lambda gets the organizations that may pay Pix out
# from a variable, and prod names only CTech's own.
run "payout_organizations_reach_the_lambda" {
  command = plan

  variables {
    byok_payout_account_orgs = "org-a,org-b"
  }

  assert {
    condition     = aws_lambda_function.byok_outbound.environment[0].variables["BYOK_PAYOUT_ACCOUNT_ORGS"] == "org-a,org-b"
    error_message = "the outbound Lambda must receive byok_payout_account_orgs"
  }
}

run "nobody_may_pay_out_by_default" {
  command = plan

  assert {
    condition     = aws_lambda_function.byok_outbound.environment[0].variables["BYOK_PAYOUT_ACCOUNT_ORGS"] == ""
    error_message = "without the variable no organization may pay out"
  }
}
