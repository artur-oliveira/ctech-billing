# Billing as a participant in ctech-account's account-deletion saga
# (ctech-account docs/specs/2026-10-06-account-deletion-saga-protocol.md § 3, § 7).
#
# ctech-account owns the topic and publishes the ARN in SSM; billing owns its
# queue, its DLQ and its subscription. The state table is not here: it is a key
# of api/internal/repositories/schema.json, so dynamodb.tf creates it.

data "aws_ssm_parameter" "erasure_topic_arn" {
  name = "/ctech/${var.environment}/account/erasure-topic-arn"
}

resource "aws_sqs_queue" "erasure_dlq" {
  name                      = "${local.name}-erasure-dlq"
  message_retention_seconds = 1209600
  sqs_managed_sse_enabled   = true
}

# The Go service derives this name from ENVIRONMENT (internal/app/erasure.go,
# erasureQueueName) and resolves the URL with GetQueueUrl, so the userdata,
# which is at its 16 KiB ceiling, carries nothing new.
#
# Visibility 900 s is well over twice the slowest purge (one read of tenant
# zero's invoices plus a handful of finance spaces). After five receives a
# message is parked in the DLQ. There is no alarm on it: the family declined
# them (ctech-wallet #103, ctech-poker #427), so OPERATIONS.md says to read the
# DLQ depth when a deletion stays in `purging` in ctech-account.
resource "aws_sqs_queue" "erasure" {
  name                       = "${local.name}-erasure"
  visibility_timeout_seconds = 900
  receive_wait_time_seconds  = 20
  message_retention_seconds  = 1209600
  sqs_managed_sse_enabled    = true
  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.erasure_dlq.arn
    maxReceiveCount     = 5
  })
}

data "aws_iam_policy_document" "erasure_queue" {
  statement {
    effect    = "Allow"
    actions   = ["sqs:SendMessage"]
    resources = [aws_sqs_queue.erasure.arn]
    principals {
      type        = "Service"
      identifiers = ["sns.amazonaws.com"]
    }
    condition {
      test     = "ArnEquals"
      variable = "aws:SourceArn"
      values   = [data.aws_ssm_parameter.erasure_topic_arn.value]
    }
  }
}

resource "aws_sqs_queue_policy" "erasure" {
  queue_url = aws_sqs_queue.erasure.id
  policy    = data.aws_iam_policy_document.erasure_queue.json
}

# The filter is on the `services` MESSAGE ATTRIBUTE ctech-account sets on every
# publish, not on the body: a service-scoped request for another service never
# reaches this queue. Raw delivery, so the body is the message itself
# (erasure.Decode accepts the envelope too).
resource "aws_sns_topic_subscription" "erasure" {
  topic_arn            = data.aws_ssm_parameter.erasure_topic_arn.value
  protocol             = "sqs"
  endpoint             = aws_sqs_queue.erasure.arn
  raw_message_delivery = true
  filter_policy_scope  = "MessageAttributes"
  filter_policy        = jsonencode({ services = ["billing"] })

  depends_on = [aws_sqs_queue_policy.erasure]
}

data "aws_iam_policy_document" "erasure_consume" {
  statement {
    effect = "Allow"
    actions = [
      "sqs:GetQueueUrl",
      "sqs:ReceiveMessage",
      "sqs:DeleteMessage",
      "sqs:ChangeMessageVisibility",
      "sqs:GetQueueAttributes",
    ]
    resources = [aws_sqs_queue.erasure.arn]
  }
}

resource "aws_iam_role_policy" "erasure_consume" {
  name   = "${local.name}-erasure-queue"
  role   = aws_iam_role.billing.id
  policy = data.aws_iam_policy_document.erasure_consume.json
}

# The dedicated confidential client that acks purges to ctech-account. Its only
# grant is internal:account:erasure-ack, kept apart from the membership client.
# The service reads both values at start (internal/app/erasure.go,
# erasureClientParams), not through the userdata.
#
# Same discipline as the collection secrets: Terraform creates the parameters
# and never their values. The operator creates the client in ctech-account and
# runs `aws ssm put-parameter --overwrite` for each value. Until then the
# consumer logs that the client is unset and does not start. The role already
# reads and decrypts everything under local.ssm_prefix (iam.tf, ssm_read).
resource "aws_ssm_parameter" "erasure_client" {
  for_each = {
    id     = "${local.ssm_prefix}/erasure-client-id"
    secret = "${local.ssm_prefix}/erasure-client-secret"
  }

  name  = each.value
  type  = "SecureString"
  value = "SET-OUT-OF-BAND"

  lifecycle {
    ignore_changes = [value]
  }
}
