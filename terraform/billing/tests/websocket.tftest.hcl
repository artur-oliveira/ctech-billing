# The realtime endpoints need nginx to forward Upgrade and to hold an idle
# socket (the shared server block sends `Connection ""` and cuts at 60 s). The
# block is written by bootstrap, and a `$$` that is not `$${` survives
# templatefile literally: nginx then refuses the whole config ("invalid
# variable name") and the new instance never becomes healthy. That is what
# happened on 2026-10-14; this renders both bootstraps and reads the result.
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

# The erasure topic ARN is read from ctech-account's SSM parameter; the mock
# would invent a string that is not an ARN, which the subscription validates.
override_data {
  target = data.aws_ssm_parameter.erasure_topic_arn
  values = { value = "arn:aws:sns:us-east-1:123456789012:dev-account-user-erasure" }
}


variables {
  environment = "prod"
}

run "alpine_forwards_upgrade_with_real_nginx_variables" {
  command = plan
  variables {
    os_family = "alpine"
  }

  assert {
    condition = alltrue([
      strcontains(local.bootstrap_sh, "location = /v1.0/ws {"),
      strcontains(local.bootstrap_sh, "proxy_set_header Upgrade $http_upgrade;"),
      strcontains(local.bootstrap_sh, "proxy_set_header Connection $connection_upgrade;"),
      strcontains(local.bootstrap_sh, "proxy_read_timeout 3600s;"),
    ])
    error_message = "the Alpine bootstrap must forward Upgrade on /v1.0/ws"
  }

  assert {
    condition     = !can(regex("\\$\\$[a-z_]", local.bootstrap_sh))
    error_message = "a stray $$ in the rendered bootstrap: nginx rejects $$http_upgrade as an invalid variable name"
  }

  assert {
    condition     = strcontains(local.bootstrap_sh, "REALTIME_ENABLED=false")
    error_message = "realtime must be off unless an environment turns it on"
  }
}

run "al2023_forwards_upgrade_with_real_nginx_variables" {
  command = plan
  variables {
    os_family = "al2023"
  }

  assert {
    condition = alltrue([
      strcontains(local.bootstrap_sh, "location = /v1.0/ws {"),
      strcontains(local.bootstrap_sh, "proxy_set_header Upgrade $http_upgrade;"),
      strcontains(local.bootstrap_sh, "proxy_set_header Connection $connection_upgrade;"),
    ])
    error_message = "the AL2023 bootstrap must forward Upgrade on /v1.0/ws"
  }

  assert {
    condition     = !can(regex("\\$\\$[a-z_]", local.bootstrap_sh))
    error_message = "a stray $$ in the rendered bootstrap: nginx rejects $$http_upgrade as an invalid variable name"
  }
}
