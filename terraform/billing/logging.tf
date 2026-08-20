# Log groups. Nothing else: the CloudWatch agent ships app and nginx logs here,
# and no custom metric is derived from them.

resource "aws_cloudwatch_log_group" "app" {
  name              = local.log_group_app
  retention_in_days = var.environment == "prod" ? 30 : 7
}

resource "aws_cloudwatch_log_group" "nginx" {
  name              = local.log_group_nginx
  retention_in_days = var.environment == "prod" ? 30 : 7
}
