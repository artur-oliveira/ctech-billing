# Published so the compute root — and any operator — reads the table name and
# the role from one place instead of reconstructing the naming convention. This
# is the same pattern ctech-lbalancer uses to consume ctech-cdk's network
# outputs: SSM is the seam between roots that must not share state.

# The prefix, not the table names. The service builds a physical name from
# TABLE_PREFIX plus a logical name it holds as a constant, so publishing ten
# names here would be publishing something nothing reads — and something that
# would go stale the first time a table is added.
resource "aws_ssm_parameter" "table_prefix" {
  name  = local.ssm_paths.table_prefix
  type  = "String"
  value = local.table_prefix
}

resource "aws_ssm_parameter" "role_arn" {
  name  = local.ssm_paths.role_arn
  type  = "String"
  value = aws_iam_role.billing.arn
}

# The collection secrets (ADR 0004): billing's wallet client credentials, the
# HMAC key that authenticates wallet's notify-back, and the key that signs public
# payment links.
#
# Terraform creates the parameters and **never** their values. Each is written
# with a placeholder and then ignored forever, so the real secret is set once, out
# of band (`aws ssm put-parameter --overwrite`), and never lands in state or in a
# plan output. A `terraform apply` after a rotation must not quietly put the old
# value back, which is exactly what managing the value here would do.
#
# A placeholder is not a working configuration, and that is deliberate: the
# service refuses to mount its payment routes rather than starting with a secret
# anybody who has read this file already knows (see internal/app.Build).
resource "aws_ssm_parameter" "collection_secrets" {
  for_each = {
    wallet_client_id      = local.ssm_paths.wallet_client_id
    wallet_client_secret  = local.ssm_paths.wallet_client_secret
    wallet_webhook_secret = local.ssm_paths.wallet_webhook_secret
    checkout_link_secret  = local.ssm_paths.checkout_link_secret
  }

  name  = each.value
  type  = "SecureString"
  value = "SET-OUT-OF-BAND"

  lifecycle {
    ignore_changes = [value]
  }
}

# Field encryption (ARCHITECTURE.md § 7, ADR 0009). Same out-of-band discipline
# as the secrets above, and one difference worth stating: this key is not
# rotatable today. Losing it does not cost a re-issued payment link, it makes
# every stored tax id permanently unreadable — so it is backed up wherever the
# rest of the account's break-glass material lives, not only here.
#
# The service refuses to start without it (config.Load), which is deliberate: a
# deployment that silently wrote CPFs in the clear would look completely healthy.
resource "aws_ssm_parameter" "field_encryption_key" {
  name  = local.ssm_paths.field_encryption_key
  type  = "SecureString"
  value = "SET-OUT-OF-BAND"

  lifecycle {
    ignore_changes = [value]
  }
}

# The address dunning reminders come from. A String, not a SecureString: it is
# printed on every email it sends. It is still set out of band, because it has
# to match a verified SES identity and Terraform does not own that.
resource "aws_ssm_parameter" "email_from" {
  name  = local.ssm_paths.email_from
  type  = "String"
  value = "SET-OUT-OF-BAND"

  lifecycle {
    ignore_changes = [value]
  }
}
