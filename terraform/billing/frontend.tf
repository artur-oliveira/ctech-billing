# The consumer portal: a Next.js static export in S3, served by CloudFront,
# with the API forwarded from the same distribution.
#
# This is a port of @aoctech/cdk's `createNextjsStaticFrontend`
# (ctech-cdk/lib/nextjs-static-frontend.ts) into HCL, because billing's
# infrastructure root is Terraform while dfe's and poker's are CDK. The resource
# set, the names and the rewrite function are deliberately identical so the two
# can be diffed by eye; anything that differs below is commented as to why.
#
# The one structural idea worth restating: **the bucket is not a website
# endpoint.** It is private, reached only through an Origin Access Control, and
# the ".html" suffix that a static export needs is added by a CloudFront
# Function reading a KeyValueStore of known routes. A bucket website endpoint
# would be public, would answer over plain HTTP, and would have no way to tell
# "/invoice" (a real route) from "/invioce" (a typo that must 404).

data "aws_ssm_parameter" "acm_cert_arn" {
  name = "/ctech/global/acm/cert-arn"
}

resource "aws_s3_bucket" "frontend" {
  bucket = local.frontend_bucket
}

resource "aws_s3_bucket_public_access_block" "frontend" {
  bucket                  = aws_s3_bucket.frontend.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "frontend" {
  bucket = aws_s3_bucket.frontend.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

# Versioning outside dev only. It is what makes a bad deploy recoverable
# without a rebuild, and the objects are a few hundred kilobytes of JavaScript —
# but in dev it is a bucket that grows forever for a deploy nobody would roll
# back.
resource "aws_s3_bucket_versioning" "frontend" {
  bucket = aws_s3_bucket.frontend.id
  versioning_configuration {
    status = var.environment == "dev" ? "Suspended" : "Enabled"
  }
}

# Non-current versions are the rollback window, not an archive. Thirty days is
# well past the point where rolling back to a build is something anybody would
# do.
resource "aws_s3_bucket_lifecycle_configuration" "frontend" {
  bucket     = aws_s3_bucket.frontend.id
  depends_on = [aws_s3_bucket_versioning.frontend]

  rule {
    id     = "expire-noncurrent"
    status = "Enabled"
    filter {}
    noncurrent_version_expiration {
      noncurrent_days = 30
    }
    abort_incomplete_multipart_upload {
      days_after_initiation = 7
    }
  }
}

resource "aws_cloudfront_origin_access_control" "frontend" {
  name                              = local.frontend_oac_name
  description                       = "OAC for ${local.frontend_bucket}"
  origin_access_control_origin_type = "s3"
  signing_behavior                  = "always"
  signing_protocol                  = "sigv4"
}

# Only this distribution may read the bucket. The SourceArn condition is the
# half that matters: without it the policy trusts the CloudFront service
# principal generally, which is every CloudFront distribution in every AWS
# account.
data "aws_iam_policy_document" "frontend_bucket" {
  statement {
    sid       = "AllowCloudFrontRead"
    actions   = ["s3:GetObject"]
    resources = ["${aws_s3_bucket.frontend.arn}/*"]

    principals {
      type        = "Service"
      identifiers = ["cloudfront.amazonaws.com"]
    }

    condition {
      test     = "StringEquals"
      variable = "AWS:SourceArn"
      values   = [aws_cloudfront_distribution.frontend.arn]
    }
  }
}

resource "aws_s3_bucket_policy" "frontend" {
  bucket = aws_s3_bucket.frontend.id
  policy = data.aws_iam_policy_document.frontend_bucket.json
}

# The route manifest. CI writes one key per exported route ("/invoices" -> "1")
# after the S3 sync, so the store can never advertise a page the bucket does not
# have. See ui/scripts/publish-routes.sh.
resource "aws_cloudfront_key_value_store" "routes" {
  name    = local.frontend_kvs_name
  comment = "Known routes of the ${var.environment} billing portal"
}

# Maps a clean URL onto the static export's file layout.
#
# A hit becomes "<route>.html"; a miss becomes "/404.html" — and the miss is
# decided from the key-value store rather than from S3, because a 404 that has
# to round-trip to the bucket is a 404 that costs an origin request. The
# distribution deliberately has **no** custom_error_response: those are
# distribution-wide and would replace the API's RFC 7807 problem bodies on every
# 403 and 404 the API itself returns.
resource "aws_cloudfront_function" "url_rewrite" {
  name    = local.frontend_func_name
  runtime = "cloudfront-js-2.0"
  comment = "Clean URLs for the billing portal's static export"
  publish = true

  key_value_store_associations = [aws_cloudfront_key_value_store.routes.arn]

  code = <<-JS
    import cf from 'cloudfront';

    const kvs = cf.kvs();

    async function handler(event) {
      var uri = event.request.uri;
      if (uri === '/' || /\.[^/]+$/.test(uri)) return event.request;
      var route = uri.endsWith('/') ? uri.slice(0, -1) : uri;
      event.request.uri = (await kvs.exists(route)) ? route + '.html' : '/404.html';
      return event.request;
    }
  JS
}

# One policy for both behaviours, the bucket's and the API's.
#
# `connect-src` is the directive that has to be right: the portal is same-origin
# with its own API (the /v1/* behaviour below), but the OAuth client talks to
# ctech-account directly, so both accounts hostnames are listed. `frame-ancestors
# 'none'` is what stops the payment screen being framed by somebody else's page.
resource "aws_cloudfront_response_headers_policy" "frontend" {
  name    = "${var.environment}-CtechBilling-security-headers"
  comment = "Security headers for the ${var.environment} billing portal"

  security_headers_config {
    content_type_options {
      override = true
    }
    frame_options {
      frame_option = "DENY"
      override     = true
    }
    strict_transport_security {
      access_control_max_age_sec = 63072000
      include_subdomains         = true
      preload                    = true
      override                   = true
    }
    referrer_policy {
      referrer_policy = "strict-origin-when-cross-origin"
      override        = true
    }
    content_security_policy {
      override = true
      content_security_policy = join("; ", [
        "default-src 'self'",
        "base-uri 'self'",
        "object-src 'none'",
        "frame-ancestors 'none'",
        "form-action 'self'",
        "img-src 'self' data:",
        "font-src 'self' data:",
        # 'unsafe-inline' is Next's inline bootstrap and hydration payload. It
        # goes when the app moves to a nonce, which needs a request-time header
        # a static export has nowhere to set.
        "style-src 'self' 'unsafe-inline'",
        "script-src 'self' 'unsafe-inline'",
        "connect-src 'self' https://${local.accounts_api_domain} https://${local.accounts_app_domain}",
      ])
    }
  }

  custom_headers_config {
    items {
      header   = "Permissions-Policy"
      value    = "camera=(), microphone=(), geolocation=(), payment=(), usb=()"
      override = true
    }
  }
}

resource "aws_cloudfront_distribution" "frontend" {
  enabled             = true
  comment             = "CTech Billing portal - ${var.environment}"
  default_root_object = "index.html"
  aliases             = [local.app_domain]
  price_class         = "PriceClass_100"
  http_version        = "http2and3"
  is_ipv6_enabled     = true

  origin {
    origin_id                = "s3"
    domain_name              = aws_s3_bucket.frontend.bucket_regional_domain_name
    origin_access_control_id = aws_cloudfront_origin_access_control.frontend.id
  }

  # The same HAProxy hostname the public API answers on. Routing it through the
  # distribution is what makes the browser same-origin with the API, so the
  # portal never needs a CORS preflight and the API never needs to allow an
  # origin.
  origin {
    origin_id   = "api"
    domain_name = local.api_domain

    custom_origin_config {
      http_port                = 80
      https_port               = 443
      origin_protocol_policy   = "https-only"
      origin_ssl_protocols     = ["TLSv1.2"]
      origin_read_timeout      = 60
      origin_keepalive_timeout = 60
    }
  }

  default_cache_behavior {
    target_origin_id       = "s3"
    viewer_protocol_policy = "redirect-to-https"
    allowed_methods        = ["GET", "HEAD", "OPTIONS"]
    cached_methods         = ["GET", "HEAD"]
    compress               = true
    # CachingOptimized, the AWS managed policy. The build's hashed assets are
    # immutable and the HTML is synced with `no-cache`, so the split is decided
    # by the object's own Cache-Control rather than here.
    cache_policy_id            = "658327ea-f89d-4fab-a63d-7e88639e58f6"
    response_headers_policy_id = aws_cloudfront_response_headers_policy.frontend.id

    function_association {
      event_type   = "viewer-request"
      function_arn = aws_cloudfront_function.url_rewrite.arn
    }
  }

  dynamic "ordered_cache_behavior" {
    for_each = local.frontend_api_patterns
    content {
      path_pattern           = ordered_cache_behavior.value
      target_origin_id       = "api"
      viewer_protocol_policy = "https-only"
      allowed_methods        = ["GET", "HEAD", "OPTIONS", "PUT", "POST", "PATCH", "DELETE"]
      cached_methods         = ["GET", "HEAD"]
      compress               = true
      # CachingDisabled and AllViewerExceptHostHeader. The API's answers are
      # per-caller by definition; caching one customer's invoice list at the
      # edge is the worst bug this file could contain. The host header is
      # dropped so HAProxy routes on the origin's own hostname.
      cache_policy_id            = "4135ea2d-6df8-44a3-9df3-4b5a84be39ad"
      origin_request_policy_id   = "b689b0a8-53d0-40ab-baf2-68738e2966ac"
      response_headers_policy_id = aws_cloudfront_response_headers_policy.frontend.id

      # No function association: the rewrite exists to add ".html" to a page
      # request, and running it over the API would corrupt every path it sees.
    }
  }

  viewer_certificate {
    acm_certificate_arn      = data.aws_ssm_parameter.acm_cert_arn.value
    ssl_support_method       = "sni-only"
    minimum_protocol_version = "TLSv1.2_2021"
  }

  restrictions {
    geo_restriction {
      restriction_type = "none"
    }
  }
}
