#!/usr/bin/env bash
# Creates the BYOK credentials key (spec 2026-10-11 § 3.3) once per environment:
# 32 random bytes, base64, as an SSM SecureString only the outbound Lambda reads.
#
#   AWS_PROFILE=ctech terraform/scripts/create-byok-credentials-key.sh prod
#
# Refuses to overwrite (losing the key makes every stored connection unreadable;
# merchants would have to upload their credentials again). Never prints the
# value, and never puts it on a command line (it reaches the AWS CLI through a
# private temporary file, so it is not visible in the process list). Back the value up with the
# account's break-glass material, the same way as field-encryption-key
# (terraform/billing/ssm.tf): docs/runbooks/byok.md says how without printing it.
set -euo pipefail
ENV="${1:?usage: $0 <dev|stage|prod>}"
case "$ENV" in dev | stage | prod) ;; *) echo "unknown environment: $ENV" >&2; exit 2 ;; esac
NAME="/ctech-billing/$ENV/byok/credentials-key"
if aws ssm get-parameter --region us-east-1 --name "$NAME" --query Parameter.Name --output text >/dev/null 2>&1; then
  echo "$NAME already exists; refusing to replace it" >&2
  exit 1
fi
# The value goes through a private temporary file (mode 0600, removed on exit),
# never through argv, so it is not visible in the process list.
umask 077
TMP="$(mktemp)"
trap 'rm -f "$TMP"' EXIT
head -c 32 /dev/urandom | base64 | tr -d '\n' > "$TMP"
aws ssm put-parameter --region us-east-1 --name "$NAME" --type SecureString --value "file://$TMP" --no-overwrite >/dev/null
echo "created $NAME (value not shown)"
