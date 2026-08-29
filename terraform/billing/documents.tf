# The invoice documents bucket.
#
# Its own bucket rather than a prefix in the shared deployments or logs bucket:
# what it holds is customer documents with names and tax ids in them, and the
# other two are read by every service's deploy role. A separate bucket is what
# makes "who can read a customer's invoice" answerable from one policy.
#
# **Nothing here is public.** Objects are served by presigned URLs the API
# signs, which is why there is no website configuration, no OAC and no
# CloudFront in front of it.
resource "aws_s3_bucket" "documents" {
  bucket = "${local.name}-documents"

  # Retained on destroy, like every bucket in the family that holds something
  # somebody else is entitled to: a `terraform destroy` in the wrong workspace
  # must not take a year of invoices with it.
  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_s3_bucket_public_access_block" "documents" {
  bucket                  = aws_s3_bucket.documents.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "documents" {
  bucket = aws_s3_bucket.documents.id

  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

# Versioning, and it is not decoration. The object is written once and never
# replaced — the API's put is conditional on the key being absent — so a version
# beyond the first can only come from a mistake or an incident, and having the
# earlier one is the difference between noticing and recovering.
resource "aws_s3_bucket_versioning" "documents" {
  bucket = aws_s3_bucket.documents.id

  versioning_configuration {
    status = "Enabled"
  }
}

# No expiration rule. An invoice is kept permanently (ADR 0009) and the document
# is the invoice, so a lifecycle that expired it would delete the record the
# retention policy promises to keep. What is cleaned up is the debris: a
# multipart upload that never finished, and old versions of an object that
# should never have had a second one.
resource "aws_s3_bucket_lifecycle_configuration" "documents" {
  bucket = aws_s3_bucket.documents.id

  rule {
    id     = "AbandonMultipartUploads"
    status = "Enabled"

    filter {}

    abort_incomplete_multipart_upload {
      days_after_initiation = 7
    }
  }

  rule {
    id     = "ExpireSupersededVersions"
    status = "Enabled"

    filter {}

    noncurrent_version_expiration {
      noncurrent_days = 90
    }
  }
}

# The service reads and writes its own documents, and nothing else. No
# `s3:DeleteObject`: the document is the invoice, and a role that can delete one
# is a role that can delete the record.
data "aws_iam_policy_document" "documents" {
  statement {
    effect    = "Allow"
    actions   = ["s3:PutObject", "s3:GetObject"]
    resources = ["${aws_s3_bucket.documents.arn}/invoices/*"]
  }

  # ListBucket is scoped to the same prefix, and it is needed for the HeadObject
  # that decides whether a document has already been rendered — without it a
  # missing object answers 403 instead of 404, which reads as a broken
  # deployment rather than a first download.
  statement {
    effect    = "Allow"
    actions   = ["s3:ListBucket"]
    resources = [aws_s3_bucket.documents.arn]

    condition {
      test     = "StringLike"
      variable = "s3:prefix"
      values   = ["invoices/*"]
    }
  }
}

resource "aws_iam_role_policy" "documents" {
  name   = "${local.name}-documents"
  role   = aws_iam_role.billing.id
  policy = data.aws_iam_policy_document.documents.json
}
