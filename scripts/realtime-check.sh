#!/usr/bin/env bash
# Reads the gateway's one-line-a-minute stats from the app log group and says
# whether the rollback triggers of docs/runbooks/realtime.md are hit. No alarm,
# no custom metric: a read of logs that are already shipped.
#   realtime-check.sh ENV [MINUTES]     default 60
set -uo pipefail
env=${1:?usage: realtime-check.sh ENV [MINUTES]}
minutes=${2:-60}
export AWS_PROFILE=${AWS_PROFILE:-ctech}
group="/ctech-billing/$env/app"
since=$(( ($(date +%s) - minutes * 60) * 1000 ))

lines=$(aws logs filter-log-events --log-group-name "$group" --start-time "$since" \
  --filter-pattern '"realtime stats"' --query 'events[].message' --output text 2>&1) || { echo "$lines" >&2; exit 1; }
if [ -z "$lines" ]; then
  echo "no stats lines in the last ${minutes} min (flag off, or nobody connected)"
  exit 0
fi

# Each message is a JSON log record whose "stats" field is "k=v k=v ...".
printf '%s' "$lines" | python3 -c '
import json, re, sys
minutes = int(sys.argv[1])
total = {}
peak = {}
latency_max = 0
for raw in sys.stdin.read().split("\t"):
    try:
        stats = json.loads(raw).get("stats", "")
    except Exception:
        continue
    for k, v in (p.split("=", 1) for p in stats.split() if "=" in p):
        n = int(v)
        if k.startswith("connections_"):
            peak[k] = max(peak.get(k, 0), n)
        elif k == "write_latency_max_ms":
            latency_max = max(latency_max, n)
        else:
            total[k] = total.get(k, 0) + n

def t(prefix):
    return sum(v for k, v in total.items() if k == prefix or k.startswith(prefix + "."))

conns = max(sum(peak.values()), 1)
gaps, publish_errors = t("gap"), t("publish_errors")
slow, auth, polls = t("slow_consumer_closes"), t("auth_failures"), t("fallback_polls")
print(f"window {minutes} min   peak connections {sum(peak.values())}   {peak}")
print(f"gaps {gaps}  slow-consumer closes {slow}  publish errors {publish_errors}  auth failures {auth}  fallback polls {polls}")
print(f"write latency max {latency_max} ms")
triggers = []
if publish_errors > 0: triggers.append("publish errors > 0")
if gaps * 100 > 3 * conns * max(minutes / 60, 1): triggers.append("gap rate above 3% of connections per hour")
if latency_max > 1500: triggers.append("write latency above 1.5 s")
if slow > 0: triggers.append("slow-consumer closes under normal load")
if triggers:
    print("ROLL BACK or INVESTIGATE:", "; ".join(triggers)); sys.exit(2)
print("ok: no rollback trigger hit")
' "$minutes"
