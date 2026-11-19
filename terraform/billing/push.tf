# Scheduled Web Push (docs/specs/2026-11-18-pending-reminder-web-push.md): EventBridge Scheduler runs push-tick every 15
# minutes of São Paulo time; the tick reads the reminders due in the slot, applies Billing's rule and invokes push-sender,
# which holds the VAPID key and sends. Both are inert until an operator writes the VAPID parameters
# (go run gopkg.aoctech.app/api-commons/pushsvc/cmd/push-keygen -service billing -env <env> prints the commands).
#
# Cost: ~2,900 tick invocations a month, inside the Scheduler (14 M free) and Lambda free tiers; no NAT, no VPC, no KMS key
# of ours, no SQS, no alarm. The push table is declared in api/internal/repositories/schema.json like every table.
locals {
  push_tick_name   = "${var.environment}-ctech-billing-push-tick"
  push_sender_name = "${var.environment}-ctech-billing-push-sender"
  push_ssm_prefix  = "/ctech-billing/${var.environment}/push"
  push_param_arn   = { for n in ["vapid-private-key", "vapid-private-key-previous", "vapid-public-key"] : n => "arn:aws:ssm:${var.aws_region}:${var.aws_account}:parameter${local.push_ssm_prefix}/${n}" }
  push_table_arn   = aws_dynamodb_table.billing["push"].arn
}

# The VAPID subject: the push service may contact this address about the sender. A plain String, set to the app URL.
resource "aws_ssm_parameter" "push_subject" {
  name  = "${local.push_ssm_prefix}/subject"
  type  = "String"
  value = local.app_domain_url
}

# ── push-sender ──────────────────────────────────────────────────────────────
resource "aws_iam_role" "push_sender" {
  name               = local.push_sender_name
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
}

resource "aws_cloudwatch_log_group" "push_sender" {
  name              = "/aws/lambda/${local.push_sender_name}"
  retention_in_days = 14
}

resource "aws_iam_role_policy" "push_sender" {
  name = "${local.push_sender_name}-policy"
  role = aws_iam_role.push_sender.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        # The devices of a person (Query) and the delete of a device that is gone (the count row moves in the same
        # transaction). Never a Put, never a Scan.
        Sid      = "Devices"
        Effect   = "Allow"
        Action   = ["dynamodb:Query", "dynamodb:DeleteItem", "dynamodb:UpdateItem"]
        Resource = [local.push_table_arn]
      },
      {
        Sid      = "VapidKeys"
        Effect   = "Allow"
        Action   = ["ssm:GetParameter"]
        Resource = concat(values(local.push_param_arn), [aws_ssm_parameter.push_subject.arn])
      },
      {
        # SecureStrings use the aws/ssm key: decrypt through SSM only, for these parameters only.
        Sid      = "DecryptViaSsm"
        Effect   = "Allow"
        Action   = ["kms:Decrypt"]
        Resource = ["arn:aws:kms:${var.aws_region}:${var.aws_account}:key/*"]
        Condition = {
          StringEquals = { "kms:ViaService" = "ssm.${var.aws_region}.amazonaws.com" }
          StringLike   = { "kms:EncryptionContext:PARAMETER_ARN" = values(local.push_param_arn) }
        }
      },
      {
        Sid      = "Logs"
        Effect   = "Allow"
        Action   = ["logs:CreateLogStream", "logs:PutLogEvents"]
        Resource = ["${aws_cloudwatch_log_group.push_sender.arn}:*"]
      },
      {
        Sid      = "Alerts"
        Effect   = "Allow"
        Action   = ["sns:Publish"]
        Resource = [local.alerts_topic_arn]
      },
    ]
  })
}

# Terraform creates the function from the placeholder; CI ships the code (api.yml, `make build-lambdas`) and Terraform never
# reverts it.
resource "aws_lambda_function" "push_sender" {
  function_name = local.push_sender_name
  role          = aws_iam_role.push_sender.arn
  runtime       = "provided.al2023"
  handler       = "bootstrap"
  architectures = ["arm64"]
  memory_size   = 128
  timeout       = 30
  filename      = data.archive_file.byok_placeholder.output_path

  environment {
    variables = {
      ENVIRONMENT                              = var.environment
      TABLE_PREFIX                             = local.table_prefix
      AWS_USE_DUALSTACK_ENDPOINT               = "true"
      ALERTS_TOPIC_ARN                         = local.alerts_topic_arn
      PUSH_VAPID_PRIVATE_KEY_SSM_PATH          = "${local.push_ssm_prefix}/vapid-private-key"
      PUSH_VAPID_PRIVATE_KEY_PREVIOUS_SSM_PATH = "${local.push_ssm_prefix}/vapid-private-key-previous"
      PUSH_VAPID_PUBLIC_KEY_SSM_PATH           = "${local.push_ssm_prefix}/vapid-public-key"
      PUSH_SUBJECT_SSM_PATH                    = aws_ssm_parameter.push_subject.name
    }
  }

  lifecycle {
    ignore_changes = [filename, source_code_hash]
  }

  depends_on = [aws_cloudwatch_log_group.push_sender, aws_iam_role_policy.push_sender]
}

# ── push-tick ────────────────────────────────────────────────────────────────
resource "aws_iam_role" "push_tick" {
  name               = local.push_tick_name
  assume_role_policy = data.aws_iam_policy_document.lambda_assume.json
}

resource "aws_cloudwatch_log_group" "push_tick" {
  name              = "/aws/lambda/${local.push_tick_name}"
  retention_in_days = 14
}

resource "aws_iam_role_policy" "push_tick" {
  name = "${local.push_tick_name}-policy"
  role = aws_iam_role.push_tick.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        # The reminders of a slot (the index), the devices of a person, and the day mark. Never a Put, a Delete or a Scan.
        Sid      = "Reminders"
        Effect   = "Allow"
        Action   = ["dynamodb:Query", "dynamodb:GetItem", "dynamodb:UpdateItem"]
        Resource = [local.push_table_arn, "${local.push_table_arn}/index/*"]
      },
      {
        # The rule counts the recurrences waiting for the person's confirmation (one Query, bounded to 100 rows).
        Sid      = "PendingRecurrences"
        Effect   = "Allow"
        Action   = ["dynamodb:Query"]
        Resource = [aws_dynamodb_table.billing["recurrences"].arn]
      },
      {
        Sid      = "InvokeTheSender"
        Effect   = "Allow"
        Action   = ["lambda:InvokeFunction"]
        Resource = [aws_lambda_function.push_sender.arn]
      },
      {
        Sid      = "Logs"
        Effect   = "Allow"
        Action   = ["logs:CreateLogStream", "logs:PutLogEvents"]
        Resource = ["${aws_cloudwatch_log_group.push_tick.arn}:*"]
      },
    ]
  })
}

resource "aws_lambda_function" "push_tick" {
  function_name = local.push_tick_name
  role          = aws_iam_role.push_tick.arn
  runtime       = "provided.al2023"
  handler       = "bootstrap"
  architectures = ["arm64"]
  memory_size   = 256
  timeout       = 120
  filename      = data.archive_file.byok_placeholder.output_path
  # One tick at a time: two overlapping runs would read the same slot.
  reserved_concurrent_executions = 1

  environment {
    variables = {
      ENVIRONMENT                = var.environment
      TABLE_PREFIX               = local.table_prefix
      AWS_USE_DUALSTACK_ENDPOINT = "true"
      PUSH_SENDER_FUNCTION       = aws_lambda_function.push_sender.function_name
    }
  }

  lifecycle {
    ignore_changes = [filename, source_code_hash]
  }

  depends_on = [aws_cloudwatch_log_group.push_tick, aws_iam_role_policy.push_tick]
}

# ── the schedule ─────────────────────────────────────────────────────────────
resource "aws_iam_role" "push_tick_scheduler" {
  name               = "${local.push_tick_name}-scheduler"
  assume_role_policy = data.aws_iam_policy_document.scheduler_assume.json
}

resource "aws_iam_role_policy" "push_tick_scheduler" {
  name = "invoke-the-push-tick"
  role = aws_iam_role.push_tick_scheduler.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = "lambda:InvokeFunction"
      Resource = [aws_lambda_function.push_tick.arn]
    }]
  })
}

resource "aws_scheduler_schedule" "push_tick" {
  name                         = local.push_tick_name
  description                  = "Every 15 minutes of São Paulo time: send the Web Push reminders due in this slot"
  schedule_expression          = "cron(0/15 * * * ? *)"
  schedule_expression_timezone = "America/Sao_Paulo"
  flexible_time_window {
    mode = "OFF"
  }
  target {
    arn      = aws_lambda_function.push_tick.arn
    role_arn = aws_iam_role.push_tick_scheduler.arn
    retry_policy {
      maximum_retry_attempts       = 2
      maximum_event_age_in_seconds = 900
    }
  }
}
