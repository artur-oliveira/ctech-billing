variable "aws_region" {
  type    = string
  default = "us-east-1"
}

variable "aws_account" {
  type    = string
  default = "868899309401"
}

variable "github_repo" {
  type        = string
  default     = "artur-oliveira/ctech-billing"
  description = "owner/name. Every trust policy below is scoped to it and to named branches — never to the organization, and never to a wildcard ref."
}

# The branches that may deploy, in the order the workflows map them:
# main -> prod, staging -> stage, anything else -> dev.
#
# Pull requests are deliberately absent. A PR job that can assume a deploy role
# is a PR job that can deploy, and the checks that run on a PR here
# (`terraform validate -backend=false`, `go test`, `npm run build`) are designed
# to need no credentials at all.
variable "deploy_branches" {
  type    = list(string)
  default = ["main", "staging", "dev"]
}

variable "environments" {
  type    = list(string)
  default = ["dev", "stage", "prod"]
}
