# docs/specs/2026-11-18-pending-reminder-web-push.md: the scheduled Web Push reminder. Plan only, mock providers.
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

override_data {
  target = data.aws_ssm_parameter.erasure_topic_arn
  values = { value = "arn:aws:sns:us-east-1:123456789012:dev-account-user-erasure" }
}

override_resource {
  target          = aws_lambda_function.push_sender
  override_during = plan
  values          = { arn = "arn:aws:lambda:us-east-1:123456789012:function:dev-ctech-billing-push-sender" }
}
override_resource {
  target          = aws_lambda_function.push_tick
  override_during = plan
  values          = { arn = "arn:aws:lambda:us-east-1:123456789012:function:dev-ctech-billing-push-tick" }
}
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

override_resource {
  target          = aws_dynamodb_table.billing
  override_during = plan
  values          = { arn = "arn:aws:dynamodb:us-east-1:123456789012:table/dev_billing_any" }
}
override_resource {
  target          = aws_cloudwatch_log_group.push_sender
  override_during = plan
  values          = { arn = "arn:aws:logs:us-east-1:123456789012:log-group:/aws/lambda/dev-ctech-billing-push-sender" }
}
override_resource {
  target          = aws_cloudwatch_log_group.push_tick
  override_during = plan
  values          = { arn = "arn:aws:logs:us-east-1:123456789012:log-group:/aws/lambda/dev-ctech-billing-push-tick" }
}
override_resource {
  target          = aws_ssm_parameter.push_subject
  override_during = plan
  values          = { arn = "arn:aws:ssm:us-east-1:123456789012:parameter/ctech-billing/dev/push/subject" }
}

variables {
  environment = "dev"
}

run "the_schedule_runs_every_fifteen_minutes_in_sao_paulo_time" {
  command = plan

  assert {
    condition     = aws_scheduler_schedule.push_tick.schedule_expression == "cron(0/15 * * * ? *)" && aws_scheduler_schedule.push_tick.schedule_expression_timezone == "America/Sao_Paulo"
    error_message = "every 15 minutes, in São Paulo time (the slots are São Paulo time)"
  }
  assert {
    condition     = aws_scheduler_schedule.push_tick.flexible_time_window[0].mode == "OFF"
    error_message = "no flexible window: a reminder must not drift to another slot"
  }
}

run "both_lambdas_are_arm64_provided_al2023_from_a_placeholder" {
  command = plan

  assert {
    condition = alltrue([for f in [aws_lambda_function.push_tick, aws_lambda_function.push_sender] :
    f.runtime == "provided.al2023" && f.architectures[0] == "arm64" && f.handler == "bootstrap"])
    error_message = "the same runtime as the BYOK Lambdas; CI ships the code"
  }
  assert {
    condition     = aws_lambda_function.push_tick.function_name == "dev-ctech-billing-push-tick" && aws_lambda_function.push_sender.function_name == "dev-ctech-billing-push-sender"
    error_message = "CI deploys dist/lambda/<name>.zip to {env}-ctech-billing-<name>"
  }
  assert {
    condition     = aws_lambda_function.push_tick.reserved_concurrent_executions == 1
    error_message = "one tick at a time: two overlapping runs would read the same slot"
  }
}

run "the_tick_can_invoke_only_the_sender_and_never_scan" {
  command = plan

  assert {
    condition = length([for s in jsondecode(aws_iam_role_policy.push_tick.policy).Statement : s if contains(flatten([s.Action]), "lambda:InvokeFunction")]) == 1
    error_message = "exactly one invoke statement"
  }
  assert {
    condition = alltrue([for s in jsondecode(aws_iam_role_policy.push_tick.policy).Statement :
    !contains(flatten([s.Action]), "dynamodb:Scan") && !contains(flatten([s.Action]), "dynamodb:PutItem") && !contains(flatten([s.Action]), "dynamodb:DeleteItem")])
    error_message = "the tick never Scans, never puts, never deletes: it reads reminders and marks a day"
  }
}

run "the_sender_cannot_write_a_reminder" {
  command = plan

  assert {
    condition = alltrue([for s in jsondecode(aws_iam_role_policy.push_sender.policy).Statement :
    !contains(flatten([s.Action]), "dynamodb:PutItem") && !contains(flatten([s.Action]), "dynamodb:Scan")])
    error_message = "the sender reads devices and deletes a gone one; it never writes a reminder"
  }
  assert {
    condition = anytrue([for s in jsondecode(aws_iam_role_policy.push_sender.policy).Statement : contains(flatten([s.Action]), "ssm:GetParameter")])
    error_message = "the sender reads the VAPID keys"
  }
}

run "the_push_table_comes_from_the_schema_with_both_indexes_and_a_ttl" {
  command = plan

  assert {
    condition     = contains(keys(aws_dynamodb_table.billing), "push")
    error_message = "the push table is declared in api/internal/repositories/schema.json, like every table"
  }
  assert {
    condition = toset([for i in aws_dynamodb_table.billing["push"].global_secondary_index : i.name]) == toset(["endpoint-index", "slot-index"])
    error_message = "the two indexes go-common's pushsvc queries"
  }
  assert {
    condition     = aws_dynamodb_table.billing["push"].ttl[0].enabled == true && aws_dynamodb_table.billing["push"].ttl[0].attribute_name == "ttl"
    error_message = "subscriptions expire by TTL unless a sync renews them"
  }
}
