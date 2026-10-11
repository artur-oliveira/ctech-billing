#!/usr/bin/env bash
# Runs the invoice customer-index backfill against an environment with its
# secret read from SSM (never printed). Needs the operator's AWS profile: the
# backfill Scans, which the service role is denied.
#   run.sh ENV            dry run: report what is missing
#   run.sh ENV -apply     write it
set -uo pipefail
env=${1:?usage: run.sh ENV [-apply]}
export AWS_PROFILE=${AWS_PROFILE:-ctech}
export AWS_REGION=${AWS_REGION:-$(aws configure get region)}
g() { aws ssm get-parameter --with-decryption --name "$1" --query Parameter.Value --output text; }
export TABLE_PREFIX="${env}_billing"
export FIELD_ENCRYPTION_KEY=$(g "/ctech-billing/$env/billing/field-encryption-key") || exit 1
cd "$(dirname "$0")/../.."
go run ./cmd/backfill-invoice-lookup ${2:-}
