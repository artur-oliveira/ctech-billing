#!/usr/bin/env bash
# Applies tenants/ctech.json to an environment with its secrets read from SSM
# (never printed). Needs the operator's AWS profile.
#   run.sh ENV            both modes, test then live (the order the README asks)
#   run.sh ENV -dry-run   parse and validate only
set -uo pipefail
env=${1:?usage: run.sh ENV [-dry-run]}
export AWS_PROFILE=${AWS_PROFILE:-ctech}
export AWS_REGION=${AWS_REGION:-$(aws configure get region)}
g() { aws ssm get-parameter --with-decryption --name "$1" --query Parameter.Value --output text; }
export TABLE_PREFIX="${env}_billing"
export FIELD_ENCRYPTION_KEY=$(g "/ctech-billing/$env/billing/field-encryption-key") || exit 1
export WEBHOOK_SECRET_DFE=$(g "/ctech-dfe/$env/billing/webhook-secret") || exit 1
export WEBHOOK_SECRET_WALLET=$(g "/ctech-wallet/$env/billing-event-secret") || exit 1
export WEBHOOK_SECRET_POKER=$(g "/ctech/$env/poker/billing-event-secret") || exit 1
cd "$(dirname "$0")/../.."
for mode in test live; do
  echo "== $mode"
  go run ./cmd/seed -file tenants/ctech.json -mode "$mode" ${2:-} || exit 1
done
