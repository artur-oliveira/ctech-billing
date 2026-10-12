#!/usr/bin/env bash
# Uploads the BYOK webhook CA bundle (spec 2026-10-11 § 6.1) to billing's
# truststore bucket and prints the object's version id, which goes into
# byok_truststore_version (terraform/billing/environments/<env>.tfvars).
# Runbook: docs/runbooks/byok.md § CA bundle.
#
#   AWS_PROFILE=ctech terraform/scripts/seed-byok-truststore.sh prod [extra-ca.pem ...]
#
# The bundle starts from Inter's production webhook CA the wallet already
# trusts (prod-ctech-wallet-pix-gateway-truststore/inter-webhook-ca.crt, spec
# § 15 item 1: the same "chave pública do Inter"), then appends each extra PEM
# given (the sandbox CA, a pilot's own download). Only public CA certificates
# pass through here; nothing secret is read or printed.
set -euo pipefail
ENV="${1:?usage: $0 <dev|stage|prod> [extra-ca.pem ...]}"
shift
case "$ENV" in dev | stage | prod) ;; *)
  echo "unknown environment: $ENV" >&2
  exit 2
  ;;
esac
REGION=us-east-1
WALLET_BUCKET="${WALLET_TRUSTSTORE_BUCKET:-prod-ctech-wallet-pix-gateway-truststore}"
BUCKET="$ENV-ctech-billing-byok-truststore"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

aws s3 cp --region "$REGION" --only-show-errors "s3://$WALLET_BUCKET/inter-webhook-ca.crt" "$work/inter-ca-bundle.pem"
for extra in "$@"; do
  grep -q 'BEGIN CERTIFICATE' "$extra" || { echo "$extra is not a PEM certificate" >&2; exit 2; }
  printf '\n' >>"$work/inter-ca-bundle.pem"
  cat "$extra" >>"$work/inter-ca-bundle.pem"
done
if grep -q 'PRIVATE KEY' "$work/inter-ca-bundle.pem"; then
  echo "refusing: the bundle holds a private key" >&2
  exit 2
fi
echo "certificates in the bundle: $(grep -c 'BEGIN CERTIFICATE' "$work/inter-ca-bundle.pem")"
sha256sum "$work/inter-ca-bundle.pem" | cut -d' ' -f1 | sed 's/^/sha256: /'
aws s3api put-object --region "$REGION" --bucket "$BUCKET" --key inter-ca-bundle.pem \
  --body "$work/inter-ca-bundle.pem" --content-type application/x-pem-file --query VersionId --output text |
  sed 's/^/byok_truststore_version = /'
