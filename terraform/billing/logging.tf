# Log groups, the HTTP status metrics derived from nginx's JSON access log, and
# the one alarm that means a human has to look.

resource "aws_cloudwatch_log_group" "app" {
  name              = local.log_group_app
  retention_in_days = var.environment == "prod" ? 30 : 7
}

resource "aws_cloudwatch_log_group" "nginx" {
  name              = local.log_group_nginx
  retention_in_days = var.environment == "prod" ? 30 : 7
}

# Every service's nginx.conf emits the same `log_format json_log`, so the
# patterns never vary — only the log group and namespace they attach to.
resource "aws_cloudwatch_log_metric_filter" "http_status" {
  for_each = {
    HTTP2XX = "{ ($.status >= 200) && ($.status < 300) }"
    HTTP3XX = "{ ($.status >= 300) && ($.status < 400) }"
    HTTP4XX = "{ ($.status >= 400) && ($.status < 500) }"
    HTTP5XX = "{ $.status >= 500 }"
  }

  name           = each.key
  log_group_name = aws_cloudwatch_log_group.nginx.name
  pattern        = each.value

  metric_transformation {
    name          = each.key
    namespace     = local.metric_namespace
    value         = "1"
    default_value = "0"
  }
}

# The two log lines that mean money is wrong, not that a request failed.
#
#   - "wallet does not know a charge billing opened" — the reconciler asked
#     wallet about a charge billing has a row for and wallet returned 404. That
#     is billing's own integration bug, and it is the only reconciliation
#     outcome that is not an ordinary customer decision.
#   - "charge settled for an unexpected amount" — wallet reports a paid amount
#     that is not the invoice total. Nothing downstream may treat that as
#     settled, and nothing automatic can decide what it should be instead.
#
# Deliberately not `$.level = "ERROR"`: a filter that also matches every failed
# request is an alarm that fires on a bad afternoon, and an alarm that fires on
# a bad afternoon is one nobody opens on the day it matters.
resource "aws_cloudwatch_log_metric_filter" "payment_integrity" {
  for_each = {
    charge_unknown  = "{ $.msg = \"wallet does not know a charge billing opened\" }"
    amount_mismatch = "{ $.msg = \"charge settled for an unexpected amount\" }"
  }

  name           = "payment-integrity-${each.key}"
  log_group_name = aws_cloudwatch_log_group.app.name
  pattern        = each.value

  metric_transformation {
    name          = local.metric_payment_integrity
    namespace     = local.metric_namespace
    value         = "1"
    default_value = "0"
  }
}

resource "aws_cloudwatch_metric_alarm" "payment_integrity" {
  alarm_name        = "${local.name}-payment-integrity"
  alarm_description = "A charge wallet does not know, or a charge settled for the wrong amount. Both mean an invoice's money state has to be established by hand — neither resolves itself."

  namespace           = local.metric_namespace
  metric_name         = local.metric_payment_integrity
  statistic           = "Sum"
  period              = 300
  evaluation_periods  = 1
  threshold           = 1
  comparison_operator = "GreaterThanOrEqualToThreshold"
  treat_missing_data  = "notBreaching"
}
