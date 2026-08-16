output "role_arns" {
  description = "The ARNs the workflows name in `role-to-assume`. They are hardcoded there rather than looked up, because a workflow that has to authenticate before it can find out how to authenticate has a bootstrapping problem."
  value       = { for k, r in aws_iam_role.gha : k => r.arn }
}
