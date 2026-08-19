# The service's own role. Compute is not created here yet (see README), but the
# role and its policy are: they are what any compute has to assume, and getting
# the permission boundary right is independent of what runs inside it.

data "aws_iam_policy_document" "assume" {
  statement {
    effect  = "Allow"
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "billing" {
  name               = local.name
  assume_role_policy = data.aws_iam_policy_document.assume.json
}

data "aws_iam_policy_document" "table_access" {
  # Item-level access to the table and its indexes. Note what is absent:
  # CreateTable, UpdateTable and DeleteTable. The service must never be able to
  # change its own schema — that is Terraform's job, and a service that can
  # create a table can quietly create the wrong one and work against it.
  statement {
    sid    = "TableItemAccess"
    effect = "Allow"
    actions = [
      "dynamodb:GetItem",
      "dynamodb:BatchGetItem",
      "dynamodb:Query",
      "dynamodb:PutItem",
      "dynamodb:UpdateItem",
      "dynamodb:DeleteItem",
      "dynamodb:BatchWriteItem",
      "dynamodb:TransactGetItems",
      "dynamodb:TransactWriteItems",
      "dynamodb:ConditionCheckItem",
      "dynamodb:DescribeTable",
    ]
    resources = concat(
      [for t in aws_dynamodb_table.billing : t.arn],
      [for t in aws_dynamodb_table.billing : "${t.arn}/index/*"],
    )
  }

  # ADR 0002 forbids Scan on any tenant read path: a Scan ignores the partition
  # key, which is the one thing making cross-tenant access unexpressible. Here
  # that rule stops being a convention someone can forget in review and becomes
  # something the platform refuses. An explicit Deny also survives a later policy
  # attachment that would otherwise grant it.
  #
  # If an access pattern ever genuinely needs a full-table read — an export, a
  # migration — it gets its own role, deliberately, and not this one.
  statement {
    sid       = "NoScansEver"
    effect    = "Deny"
    actions   = ["dynamodb:Scan"]
    resources = ["*"]
  }
}

resource "aws_iam_role_policy" "table_access" {
  name   = "${local.name}-dynamodb"
  role   = aws_iam_role.billing.id
  policy = data.aws_iam_policy_document.table_access.json
}

data "aws_iam_policy_document" "ssm_read" {
  # The path is local.ssm_prefix, not a second spelling of it. It used to read
  # `/ctech/...` while ssm.tf published `/ctech-billing/...`, so the role could
  # not read a single one of its own parameters — the kind of mismatch that only
  # surfaces at boot, on the day it matters.
  statement {
    effect    = "Allow"
    actions   = ["ssm:GetParameter", "ssm:GetParameters", "ssm:GetParametersByPath"]
    resources = ["arn:aws:ssm:${var.aws_region}:${var.aws_account}:parameter${local.ssm_prefix}/*"]
  }

  # Other roots' parameters, read by /opt/app/env.sh at start: the Valkey base
  # URL, ctech-account's issuer/JWKS URLs, and wallet's internal base URL.
  # Listed leaf by leaf rather than as `/ctech-account/*` — this role reads
  # three specific facts about its neighbours, not their namespaces.
  statement {
    effect  = "Allow"
    actions = ["ssm:GetParameter", "ssm:GetParameters"]
    resources = [
      for path in [
        local.shared_ssm.valkey_url,
        local.account_ssm.internal_base_url,
        local.account_ssm.app_url,
        local.account_ssm.internal_jwks_url,
        local.wallet_ssm.internal_base_url,
      ] : "arn:aws:ssm:${var.aws_region}:${var.aws_account}:parameter${path}"
    ]
  }

  # SecureString values are KMS-encrypted, so reading one needs Decrypt as well
  # as GetParameter. Scoped to SSM's own key by a condition rather than granted
  # against every key in the account: this role decrypts its configuration, not
  # anybody else's data.
  statement {
    effect    = "Allow"
    actions   = ["kms:Decrypt"]
    resources = ["arn:aws:kms:${var.aws_region}:${var.aws_account}:key/*"]
    condition {
      test     = "StringEquals"
      variable = "kms:ViaService"
      values   = ["ssm.${var.aws_region}.amazonaws.com"]
    }
  }
}

# Dunning sends reminders through SES. Scoped to sending only: this role may
# emit a message, and cannot read a bounce, manage an identity, or see anybody
# else's sending statistics.
#
# `ses:FromAddress` pins which identity it may send as. Without the condition,
# any verified identity in the account is fair game — including ctech-account's,
# which is the address customers are told to trust for password resets.
data "aws_iam_policy_document" "ses_send" {
  statement {
    effect    = "Allow"
    actions   = ["ses:SendEmail"]
    resources = ["*"]

    condition {
      test     = "StringEquals"
      variable = "ses:FromAddress"
      values   = [var.email_from]
    }
  }
}

resource "aws_iam_role_policy" "ses_send" {
  name   = "${local.name}-ses"
  role   = aws_iam_role.billing.id
  policy = data.aws_iam_policy_document.ses_send.json
}

resource "aws_iam_role_policy" "ssm_read" {
  name   = "${local.name}-ssm"
  role   = aws_iam_role.billing.id
  policy = data.aws_iam_policy_document.ssm_read.json
}

# Managed by AWS: SSM Session Manager access, which is how the other CTech
# services are reached for deploys and debugging instead of SSH.
resource "aws_iam_role_policy_attachment" "ssm_managed" {
  role       = aws_iam_role.billing.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

resource "aws_iam_instance_profile" "billing" {
  name = local.name
  role = aws_iam_role.billing.name
}

# ── What the instance itself needs ────────────────────────────────────────────
# Everything below exists because a line in terraform/assets/bootstrap.sh.tftpl
# calls it. Nothing is here speculatively: an instance role is the one place
# where "it might need this later" costs something permanent.

data "aws_iam_policy_document" "instance" {
  # deploy.sh pulls the release zip; the bootstrap head-object decides whether
  # there is one yet. Scoped to this service's prefix — a compromised billing
  # instance must not be able to read another service's artifact, let alone
  # replace it (there is no Put here at all; CI writes, the instance reads).
  statement {
    sid       = "ReadOwnReleaseArtifacts"
    effect    = "Allow"
    actions   = ["s3:GetObject"]
    resources = ["arn:aws:s3:::${data.aws_ssm_parameter.deployments_bucket.value}/${local.s3_prefix}/*"]
  }

  # The shared bootstrap scripts published by ctech-cdk's Ec2ScriptsStack. Read
  # on every boot, before anything else runs.
  statement {
    sid       = "ReadSharedEc2BootstrapScripts"
    effect    = "Allow"
    actions   = ["s3:GetObject"]
    resources = ["arn:aws:s3:::${var.environment}-ctech-ec2-scripts/*"]
  }

  statement {
    sid       = "ListOwnReleasePrefix"
    effect    = "Allow"
    actions   = ["s3:ListBucket"]
    resources = ["arn:aws:s3:::${data.aws_ssm_parameter.deployments_bucket.value}"]
    condition {
      test     = "StringLike"
      variable = "s3:prefix"
      values   = ["${local.s3_prefix}/*"]
    }
  }

  # upload-logs.sh ships rotated logs. Write-only, and only under this service's
  # prefix: the archive is evidence, and a role that can read it back can also
  # quietly delete the day it went wrong.
  statement {
    sid       = "ArchiveOwnLogs"
    effect    = "Allow"
    actions   = ["s3:PutObject"]
    resources = ["arn:aws:s3:::${data.aws_ssm_parameter.logs_bucket.value}/${local.s3_prefix}/*"]
  }

  # The CloudWatch agent.
  statement {
    sid    = "CloudWatchAgent"
    effect = "Allow"
    actions = [
      "logs:CreateLogStream",
      "logs:PutLogEvents",
      "logs:DescribeLogStreams",
      "logs:DescribeLogGroups",
    ]
    resources = [
      "${aws_cloudwatch_log_group.app.arn}:*",
      "${aws_cloudwatch_log_group.nginx.arn}:*",
    ]
  }

  # PutMetricData takes no resource ARN; the namespace condition is the only
  # scoping AWS offers, and without it the role can write to anybody's metrics.
  statement {
    sid       = "CloudWatchAgentMetrics"
    effect    = "Allow"
    actions   = ["cloudwatch:PutMetricData"]
    resources = ["*"]
    condition {
      test     = "StringEquals"
      variable = "cloudwatch:namespace"
      values   = ["${local.metric_namespace}/Host"]
    }
  }

  # update-realip.sh reads the CloudFront origin-facing managed prefix list.
  # These two actions have no resource-level permissions.
  statement {
    sid    = "ReadCloudFrontPrefixList"
    effect = "Allow"
    actions = [
      "ec2:DescribeManagedPrefixLists",
      "ec2:GetManagedPrefixListEntries",
    ]
    resources = ["*"]
  }

  # job.sh asks which instance is the leader before running the sweep or the
  # reconciler. Read-only, and describe has no resource-level permissions —
  # note what is absent: SetInstanceHealth, TerminateInstanceInAutoScalingGroup.
  # HAProxy owns instance health; the app must not be able to replace itself.
  statement {
    sid       = "DescribeOwnAutoScalingGroup"
    effect    = "Allow"
    actions   = ["autoscaling:DescribeAutoScalingGroups"]
    resources = ["*"]
  }
}

resource "aws_iam_role_policy" "instance" {
  name   = "${local.name}-instance"
  role   = aws_iam_role.billing.id
  policy = data.aws_iam_policy_document.instance.json
}
