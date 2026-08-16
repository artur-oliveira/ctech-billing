#!/usr/bin/env bash
#
# Publishes the static export's route list to the CloudFront KeyValueStore that
# backs the url-rewrite function (see terraform/billing/frontend.tf).
#
# One key per route ("/dashboard" -> "1"). The function rewrites a hit to
# "<route>.html" and a miss to "/404.html". The distribution has no
# custom_error_response, because those are distribution-wide and would replace
# the API's RFC 7807 problem bodies on 403/404 — the API is served from the same
# distribution, on /v1/*.
#
# This file is a copy of ctech-dfe/ui/scripts/publish-routes.sh. It is duplicated
# rather than shared because the two repositories have no common package for
# shell, and the alternative — a script fetched at deploy time from another repo
# — makes every deploy depend on that repo being readable.
#
# Run AFTER the S3 sync so the key set can never claim a route the bucket does
# not have.
#
# Usage: publish-routes.sh <kvs-arn> <export-dir>
set -euo pipefail

KVS_ARN="${1:?usage: publish-routes.sh <kvs-arn> <export-dir>}"
EXPORT_DIR="${2:?usage: publish-routes.sh <kvs-arn> <export-dir>}"

# Max keys per UpdateKeys call. The API caps the request at 3 MB; batching keeps
# each call small and the ETag round-trips few.
BATCH_SIZE=50

kvs() { aws cloudfront-keyvaluestore --region us-east-1 "$@"; }
etag() { kvs describe-key-value-store --kvs-arn "$KVS_ARN" --query ETag --output text; }

# out/nfe/emit.html -> /nfe/emit ; out/index.html -> dropped (defaultRootObject)
desired=$(
  find "$EXPORT_DIR" -name '*.html' -printf '%P\n' \
    | sed -e 's|\.html$||' -e 's|/index$||' \
    | grep -vx 'index' \
    | sed 's|^|/|' \
    | sort -u
)

if [ -z "$desired" ]; then
  echo "No routes found in ${EXPORT_DIR} — refusing to wipe the route store." >&2
  exit 1
fi

current=""
next_token=""
while :; do
  if [ -n "$next_token" ]; then
    page=$(kvs list-keys --kvs-arn "$KVS_ARN" --max-results "$BATCH_SIZE" --next-token "$next_token")
  else
    page=$(kvs list-keys --kvs-arn "$KVS_ARN" --max-results "$BATCH_SIZE")
  fi
  current+=$(echo "$page" | jq -r '.Items[].Key')$'\n'
  next_token=$(echo "$page" | jq -r '.NextToken // empty')
  [ -n "$next_token" ] || break
done
current=$(echo "$current" | sed '/^$/d' | sort -u)

puts=$(comm -23 <(echo "$desired") <(echo "$current"))
deletes=$(comm -13 <(echo "$desired") <(echo "$current"))

apply() {
  local flag="$1" shorthand="$2"
  [ -n "$shorthand" ] || return 0
  # shellcheck disable=SC2086 — each entry is one shorthand argument.
  kvs update-keys --kvs-arn "$KVS_ARN" --if-match "$(etag)" "$flag" $shorthand >/dev/null
}

echo "$puts" | sed '/^$/d' | xargs -r -n "$BATCH_SIZE" | while read -r batch; do
  args=""
  for route in $batch; do args+="Key=${route},Value=1 "; done
  apply --puts "$args"
done

echo "$deletes" | sed '/^$/d' | xargs -r -n "$BATCH_SIZE" | while read -r batch; do
  args=""
  for route in $batch; do args+="Key=${route} "; done
  apply --deletes "$args"
done

echo "Routes published: $(echo "$desired" | wc -l) total, $(echo "$puts" | sed '/^$/d' | wc -l) added, $(echo "$deletes" | sed '/^$/d' | wc -l) removed"
