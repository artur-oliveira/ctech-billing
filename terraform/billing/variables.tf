variable "environment" {
  type = string
  validation {
    condition     = contains(["dev", "stage", "prod"], var.environment)
    error_message = "environment must be dev, stage, or prod."
  }
}

variable "aws_region" {
  type    = string
  default = "us-east-1"
}

variable "aws_account" {
  type    = string
  default = "868899309401"
}

# table_prefix is what the Go service reads as TABLE_PREFIX and prepends to every
# logical table name. It defaults to `{environment}_billing`, which with the
# logical name appended gives the company standard `{env}_billing_{table}` — the
# same shape as ctech-dfe's `dev_dfe_nfses`. It is a variable only so a throwaway
# stack can be stood up beside a real one without colliding.
variable "table_prefix" {
  type    = string
  default = ""
}

# deletion_protection defaults to on. This table holds commercial documents with
# a five-year legal retention floor (ADR 0009); an accidental `terraform destroy`
# against it is not a recoverable mistake. Turn it off only for a scratch stack.
variable "deletion_protection" {
  type    = bool
  default = true
}

# point_in_time_recovery costs money per GB and is the only thing that makes a
# bad write recoverable. It stays on outside dev.
variable "point_in_time_recovery" {
  type    = bool
  default = true
}

variable "instance_type" {
  type    = string
  default = "t4g.nano"
}

# One instance, deliberately, until there is traffic to size against.
#
# The two scheduled jobs run on the leader instance (see the bootstrap asset),
# so raising max_size is safe — but it is the change that makes the leader
# election load-bearing rather than decorative, and it costs an instance-refresh
# window of reduced capacity because min_healthy_percentage is 100.
variable "min_size" {
  type    = number
  default = 1
}

variable "max_size" {
  type    = number
  default = 1

  validation {
    condition     = var.max_size >= var.min_size
    error_message = "max_size must be at least min_size."
  }
}

# PORTAL_ORGANIZATION_ID names tenant zero (ADR 0012): the organization whose
# customers the consumer portal serves. Empty is the correct default — the
# service answers 404 on every portal route rather than pointing the portal at
# whichever organization happens to be first.
variable "portal_organization_id" {
  type    = string
  default = ""
}

# The identity dunning reminders are sent from.
#
# It is a variable and not just the SSM value because the IAM condition needs it
# at plan time: a policy that read the parameter would grant whatever the
# parameter said at apply time, which is the opposite of pinning it.
#
# **A domain identity does not widen this.** Verifying `aoctech.app` in SES does
# let the account send as any address on the domain — but the role's policy
# pins `ses:FromAddress` to exactly this string, deliberately, so that a bug in
# dunning cannot send as ctech-account's address, which is the one customers are
# told to trust for password resets. The domain identity is what makes the
# address sendable; this variable is what makes it the only one.
#
# It must equal the SSM `email-from` parameter, which is set out of band. They
# are two copies of one fact and nothing checks that they agree: if the
# parameter is the address and this is not, every reminder is refused by IAM at
# send time rather than at deploy time.
variable "email_from" {
  description = "Verified SES sender address for dunning reminders; must match the email-from SSM parameter"
  type        = string
  default     = "billing@aoctech.app"
}
