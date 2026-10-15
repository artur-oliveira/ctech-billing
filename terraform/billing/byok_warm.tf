# Keeping the two BYOK Lambdas warm. A payment notice crosses both (the webhook
# function, then the outbound one to re-read the charge) and a cold start of each
# is seconds the person waiting on the screen feels. EventBridge Scheduler calls
# each every five minutes: the webhook function answers /warm after priming its
# client token, the outbound one answers the "warm" op without touching a bank.
#
# Cost: 2 schedules x 8,640 invocations a month = about 17,000, at US$ 1 per
# million for the scheduler (under two cents) plus Lambda requests and a few
# milliseconds of 128 MB each, inside the free tier. No NAT, no VPC.
data "aws_iam_policy_document" "scheduler_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["scheduler.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "byok_warm" {
  name               = "${var.environment}-ctech-billing-byok-warm"
  assume_role_policy = data.aws_iam_policy_document.scheduler_assume.json
}

resource "aws_iam_role_policy" "byok_warm" {
  name = "invoke-the-byok-lambdas"
  role = aws_iam_role.byok_warm.id
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = "lambda:InvokeFunction"
      Resource = [aws_lambda_function.byok_webhook.arn, aws_lambda_function.byok_outbound.arn]
    }]
  })
}

resource "aws_scheduler_schedule" "byok_webhook_warm" {
  name                = "${local.byok_webhook_name}-warm"
  description         = "Keeps the BYOK webhook Lambda warm and its token primed"
  schedule_expression = "rate(5 minutes)"
  flexible_time_window {
    mode = "OFF"
  }
  target {
    arn      = aws_lambda_function.byok_webhook.arn
    role_arn = aws_iam_role.byok_warm.arn
    # An API Gateway HTTP API event, the shape the function takes: only the path matters.
    input = jsonencode({ rawPath = "/warm", requestContext = { http = { method = "POST" } } })
    retry_policy {
      maximum_retry_attempts = 0
    }
  }
}

resource "aws_scheduler_schedule" "byok_outbound_warm" {
  name                = "${local.byok_outbound_name}-warm"
  description         = "Keeps the BYOK outbound Lambda warm"
  schedule_expression = "rate(5 minutes)"
  flexible_time_window {
    mode = "OFF"
  }
  target {
    arn      = aws_lambda_function.byok_outbound.arn
    role_arn = aws_iam_role.byok_warm.arn
    input    = jsonencode({ op = "warm" })
    retry_policy {
      maximum_retry_attempts = 0
    }
  }
}
