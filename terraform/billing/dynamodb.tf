# The billing tables. One per entity, with an entity's children in their
# parent's table — the layout is ADR 0002.
#
# The schema is not written here. It is read from
# `api/internal/repositories/schema.json`, the same file the Go service embeds.
# It used to exist twice — once as Go for the integration tests, once as HCL for
# the real thing — with a test that parsed this file with regexes to prove the
# two agreed. That test worked and it should never have been necessary: two
# definitions of one schema drift, and the way that drift is found is a query
# that passes every test and fails in production. One file with two readers
# cannot drift.
#
# Adding a table or an index is an edit to that JSON and nothing else.

locals {
  schema = jsondecode(file("${path.module}/../../api/internal/repositories/schema.json"))
}

resource "aws_dynamodb_table" "billing" {
  for_each = local.schema

  name         = "${local.table_prefix}_${each.key}"
  billing_mode = "PAY_PER_REQUEST"

  hash_key  = each.value.hash_key
  range_key = try(each.value.range_key, null)

  # Only key attributes are declared. Everything else on a row is schemaless, and
  # DynamoDB rejects an attribute definition no index uses.
  dynamic "attribute" {
    for_each = each.value.attributes
    content {
      name = attribute.key
      type = attribute.value
    }
  }

  dynamic "global_secondary_index" {
    for_each = each.value.indexes
    content {
      name = global_secondary_index.value.name

      key_schema {
        attribute_name = global_secondary_index.value.hash_key
        key_type       = "HASH"
      }

      dynamic "key_schema" {
        for_each = try([global_secondary_index.value.range_key], [])
        content {
          attribute_name = key_schema.value
          key_type       = "RANGE"
        }
      }

      # ALL on every index, deliberately. A narrower projection is one that has
      # to be widened the first time a screen needs one more field, and widening
      # a projection means rebuilding the index on a live table.
      projection_type = "ALL"
    }
  }

  # Retention (ADR 0009) is expressed per item, as an attribute written at
  # creation. This only enables the mechanism; what expires and when is decided
  # in api/internal/repositories/retention.go.
  #
  # Enabled on every table even though several hold nothing that expires: a row
  # with no ttl attribute is never expired, so the cost of enabling it is zero
  # and the cost of having it off on the day a table gains an expiring row is a
  # migration nobody remembers is needed.
  ttl {
    attribute_name = "ttl"
    enabled        = true
  }

  point_in_time_recovery {
    enabled = var.point_in_time_recovery
  }

  server_side_encryption {
    enabled = true
  }

  deletion_protection_enabled = var.deletion_protection

  lifecycle {
    # Belt and braces alongside deletion_protection_enabled: that flag is
    # enforced by AWS, this one is enforced by Terraform before it ever calls
    # AWS. A billing table is not something to lose to a bad plan.
    prevent_destroy = true
  }
}
