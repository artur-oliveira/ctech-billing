output "table_names" {
  description = "Physical DynamoDB table names, {env}_billing_{table}. The service builds each from TABLE_PREFIX and a logical name it holds as a constant."
  value       = sort([for t in aws_dynamodb_table.billing : t.name])
}

output "table_arns" {
  value = sort([for t in aws_dynamodb_table.billing : t.arn])
}

output "role_arn" {
  description = "The role any billing compute must assume."
  value       = aws_iam_role.billing.arn
}

output "instance_profile_name" {
  value = aws_iam_instance_profile.billing.name
}

output "table_prefix" {
  description = "TABLE_PREFIX for the service's environment."
  value       = local.table_prefix
}

output "asg_name" {
  description = "The ASG HAProxy routes to, and the one CI targets with SSM RunCommand to deploy."
  value       = aws_autoscaling_group.this.name
}

output "api_domain" {
  description = "Public hostname served by the HAProxy edge."
  value       = local.api_domain
}

output "internal_domain" {
  description = "Private hostname other CTech services call, including wallet's notify-back."
  value       = local.internal_domain
}

output "app_domain" {
  description = "Public hostname of the consumer portal. The CNAME to the distribution below is set in DNS, outside this root — same as api_domain."
  value       = local.app_domain
}

output "frontend_bucket" {
  description = "Bucket the static export syncs into."
  value       = aws_s3_bucket.frontend.id
}

output "frontend_distribution_id" {
  description = "CloudFront distribution CI invalidates after a sync."
  value       = aws_cloudfront_distribution.frontend.id
}

output "frontend_distribution_domain" {
  description = "What app_domain must CNAME to."
  value       = aws_cloudfront_distribution.frontend.domain_name
}

output "frontend_route_store_arn" {
  description = "KeyValueStore the url-rewrite function reads. ui/scripts/publish-routes.sh writes it after every sync."
  value       = aws_cloudfront_key_value_store.routes.arn
}

output "release_artifact_key" {
  description = "S3 key inside the shared deployments bucket that a new instance bootstraps from."
  value       = local.current_artifact_key
}
