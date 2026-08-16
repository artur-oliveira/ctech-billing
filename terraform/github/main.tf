# The four roles GitHub Actions assumes, one per thing a deploy does.
#
# Four and not one, because they are four different blast radii. `-gha-infra`
# runs Terraform and therefore has to be able to change anything; `-gha-api`
# uploads a zip and restarts a service; `-gha-frontend` writes static files;
# `-gha-scopes` reads three parameters. A single role would give the job that
# syncs HTML the rights of the job that can destroy the DynamoDB tables.
#
# The OIDC provider itself is not created here. It is one per account and
# ctech-cdk's global stack owns it, published at /ctech/global/oidc/provider-arn.

data "aws_ssm_parameter" "oidc_provider_arn" {
  name = "/ctech/global/oidc/provider-arn"
}

locals {
  github_owner = split("/", var.github_repo)[0]
  github_name  = split("/", var.github_repo)[1]

  # `ref:refs/heads/<branch>` and nothing else. `repo:owner/name:*` would trust
  # every ref in the repository, which includes every pull request branch and
  # every tag anybody can push.
  #
  # **Two spellings per branch, and the second one is why OIDC works at all.**
  # GitHub emits the sub claim as `repo:owner/name:...` for older repositories,
  # but as `repo:owner@<ownerId>/name@<repoId>:...` for repositories with
  # immutable IDs enabled — which is what a repository created (or deleted and
  # recreated) recently gets. Matching only the first spelling produces
  # `Not authorized to perform sts:AssumeRoleWithWebIdentity`, twelve times, with
  # nothing in the message naming the claim that failed.
  #
  # This is the same pair ctech-cdk's `githubTrustPrincipal` builds
  # (`ctech-cdk/lib/github-deploy-roles.ts`) and that every sibling repository's
  # oidc-stack ships. Billing pins the ref where they wildcard it, which is the
  # one place this policy is deliberately tighter than theirs.
  #
  # The `@*` wildcards cover the numeric ids only. The owner and repository names
  # stay literal, and a GitHub account name cannot contain `@`, so there is no
  # name somebody could register that matches this pattern.
  deploy_subjects = flatten([
    for branch in var.deploy_branches : [
      "repo:${var.github_repo}:ref:refs/heads/${branch}",
      "repo:${local.github_owner}@*/${local.github_name}@*:ref:refs/heads/${branch}",
    ]
  ])

  roles = {
    infra    = "Terraform for ctech-billing"
    api      = "API artifact upload and rolling deploy for ctech-billing"
    frontend = "Static portal deploy for ctech-billing"
    scopes   = "OAuth scope manifest publishing for ctech-billing"
  }
}

data "aws_iam_policy_document" "assume" {
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]

    principals {
      type        = "Federated"
      identifiers = [data.aws_ssm_parameter.oidc_provider_arn.value]
    }

    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }

    # StringLike, not StringEquals: the immutable-id spelling above carries `*`
    # where the numeric ids go. A value with no wildcard in it behaves exactly as
    # StringEquals would, so the literal spelling is not loosened by this.
    #
    # `aud` stays StringEquals. It has no wildcard and never should — that
    # condition is what stops a token minted for some other audience from being
    # replayed here.
    condition {
      test     = "StringLike"
      variable = "token.actions.githubusercontent.com:sub"
      values   = local.deploy_subjects
    }
  }
}

resource "aws_iam_role" "gha" {
  for_each = local.roles

  name               = "ctech-billing-gha-${each.key}"
  description        = each.value
  assume_role_policy = data.aws_iam_policy_document.assume.json
  # An hour. Long enough for a CloudFront distribution update, short enough that
  # a leaked credential is a leaked hour.
  max_session_duration = 3600
}

# ── infra ────────────────────────────────────────────────────────────────────
# Terraform creates IAM roles, DynamoDB tables, ASGs, CloudFront distributions
# and SSM parameters, and it must also be able to read state and delete what it
# created. Enumerating that is a policy that gets one action short of complete
# on every future change and fails halfway through an apply. This is the same
# call ctech-cdk's global stack makes for its own infra role.
resource "aws_iam_role_policy_attachment" "infra_admin" {
  role       = aws_iam_role.gha["infra"].name
  policy_arn = "arn:aws:iam::aws:policy/AdministratorAccess"
}

# ── api ──────────────────────────────────────────────────────────────────────
# Upload the artifact, find the instances, tell them to run /opt/app/deploy.sh.
data "aws_iam_policy_document" "api" {
  statement {
    sid       = "ReadDeploymentConfig"
    actions   = ["ssm:GetParameter", "ssm:GetParameters"]
    resources = ["arn:aws:ssm:${var.aws_region}:${var.aws_account}:parameter/ctech/*"]
  }

  statement {
    sid       = "PublishArtifact"
    actions   = ["s3:PutObject", "s3:GetObject", "s3:AbortMultipartUpload"]
    resources = ["arn:aws:s3:::*-ctech-deployments/ctech-billing/*"]
  }

  statement {
    sid       = "ListArtifactBucket"
    actions   = ["s3:ListBucket"]
    resources = ["arn:aws:s3:::*-ctech-deployments"]
    condition {
      test     = "StringLike"
      variable = "s3:prefix"
      values   = ["ctech-billing/*"]
    }
  }

  # Describe is unscoped because the API has no resource-level condition for it.
  # It reveals instance ids in an account CI already deploys to.
  statement {
    sid       = "FindInstances"
    actions   = ["autoscaling:DescribeAutoScalingGroups"]
    resources = ["*"]
  }

  # SendCommand is scoped to the document *and* to instances tagged as billing's.
  # Without the tag condition this role could run an arbitrary shell command on
  # every EC2 instance in the account, which is a considerably larger permission
  # than "deploy the billing API".
  statement {
    sid       = "RunDeployScript"
    actions   = ["ssm:SendCommand"]
    resources = ["arn:aws:ssm:${var.aws_region}::document/AWS-RunShellScript"]
  }

  statement {
    sid       = "RunDeployScriptOnBillingInstances"
    actions   = ["ssm:SendCommand"]
    resources = ["arn:aws:ec2:${var.aws_region}:${var.aws_account}:instance/*"]
    condition {
      test     = "StringEquals"
      variable = "ssm:resourceTag/Project"
      values   = ["ctech-billing"]
    }
  }

  statement {
    sid       = "WatchDeploy"
    actions   = ["ssm:GetCommandInvocation", "ssm:ListCommandInvocations", "ssm:ListCommands"]
    resources = ["*"]
  }
}

resource "aws_iam_role_policy" "api" {
  name   = "deploy"
  role   = aws_iam_role.gha["api"].id
  policy = data.aws_iam_policy_document.api.json
}

# ── frontend ─────────────────────────────────────────────────────────────────
# Sync the export, publish the route manifest, invalidate the cache. Read-only
# everywhere else, including on the distribution it invalidates.
data "aws_iam_policy_document" "frontend" {
  statement {
    sid     = "SyncExport"
    actions = ["s3:PutObject", "s3:GetObject", "s3:DeleteObject", "s3:AbortMultipartUpload"]
    resources = [
      for env in var.environments : "arn:aws:s3:::${env}-ctech-billing-frontend/*"
    ]
  }

  statement {
    sid     = "ListExportBucket"
    actions = ["s3:ListBucket", "s3:ListBucketVersions"]
    resources = [
      for env in var.environments : "arn:aws:s3:::${env}-ctech-billing-frontend"
    ]
  }

  # The workflow reads the bucket name, distribution id and route-store ARN from
  # the Terraform outputs it cannot run, so it reads them from CloudFormation-
  # free sources: SSM parameters written by the billing root.
  statement {
    sid       = "ReadDeployTargets"
    actions   = ["ssm:GetParameter", "ssm:GetParameters"]
    resources = ["arn:aws:ssm:${var.aws_region}:${var.aws_account}:parameter/ctech-billing/*"]
  }

  statement {
    sid       = "InvalidateCache"
    actions   = ["cloudfront:CreateInvalidation", "cloudfront:GetInvalidation"]
    resources = ["arn:aws:cloudfront::${var.aws_account}:distribution/*"]
  }

  # The KeyValueStore API is not resource-scoped for DescribeKeyValueStore, and
  # the write calls take the store's ARN in the request rather than in the
  # policy. Scoped to the account's stores, which is as tight as the service
  # allows.
  statement {
    sid = "PublishRoutes"
    actions = [
      "cloudfront-keyvaluestore:DescribeKeyValueStore",
      "cloudfront-keyvaluestore:ListKeys",
      "cloudfront-keyvaluestore:PutKey",
      "cloudfront-keyvaluestore:DeleteKey",
      "cloudfront-keyvaluestore:UpdateKeys",
    ]
    resources = ["arn:aws:cloudfront::${var.aws_account}:key-value-store/*"]
  }
}

resource "aws_iam_role_policy" "frontend" {
  name   = "deploy"
  role   = aws_iam_role.gha["frontend"].id
  policy = data.aws_iam_policy_document.frontend.json
}

# ── scopes ───────────────────────────────────────────────────────────────────
# Three parameters, and one of them is a SecureString.
#
# The parameters themselves are ctech-account's to create — this role can read
# /ctech-account/{env}/scope-publishers/billing/* and nothing else under
# /ctech-account. Until they exist the scopes job fails loudly at the first
# get-parameter, which is the right way for a missing credential to behave.
data "aws_iam_policy_document" "scopes" {
  statement {
    sid     = "ReadPublisherCredentials"
    actions = ["ssm:GetParameter", "ssm:GetParameters"]
    resources = concat(
      [for env in var.environments :
        "arn:aws:ssm:${var.aws_region}:${var.aws_account}:parameter/ctech-account/${env}/base-url"
      ],
      [for env in var.environments :
        "arn:aws:ssm:${var.aws_region}:${var.aws_account}:parameter/ctech-account/${env}/scope-publishers/billing/*"
      ],
    )
  }

  statement {
    sid       = "DecryptPublisherSecret"
    actions   = ["kms:Decrypt"]
    resources = ["*"]
    condition {
      test     = "StringEquals"
      variable = "kms:ViaService"
      values   = ["ssm.${var.aws_region}.amazonaws.com"]
    }
  }
}

resource "aws_iam_role_policy" "scopes" {
  name   = "publish"
  role   = aws_iam_role.gha["scopes"].id
  policy = data.aws_iam_policy_document.scopes.json
}
