# ctech-billing Alpine ARM64 Migration

## Goal

Move `ctech-billing`'s EC2 fleet from AL2023 to the custom Alpine ARM64 AMI
(`/ctech/{env}/ami/alpine/arm64`, built by ctech-cdk's Packer pipeline), same
pattern already shipped for `ctech-lbalancer`
(`ctech-lbalancer/docs/specs/2026-08-23-alpine-lbalancer-migration.md`).

## Scope and why this is smaller than lbalancer

Billing runs only pre-built Go binaries + nginx (package, not compiled) — no
HAProxy-style build-from-source, so none of lbalancer's disk-headroom or
musl-link concerns apply. 8 of the 10 shared bootstrap scripts billing calls
already have an Alpine port in `ctech-cdk/assets/ec2-alpine/`:
`setup-base.sh`, `setup-app-service.sh`, `setup-nginx.sh`, `setup-realip.sh`,
`setup-ssm-env.sh`, `setup-deploy.sh`, `setup-cloudflare-ca.sh`,
`bootstrap-deploy.sh`. Two are missing (`setup-swap.sh`, `setup-logs.sh`) and
one existing script (`setup-ctech-ec2-agent.sh`) needs a small extension —
all three land in ctech-cdk as Part 1, the same cross-repo-dependency shape
lbalancer's plan used for its 5 new `ctech-ec2-agent` subcommands.

**Bonus, already free:** billing already runs the two-process (`app`/`app2`)
rolling-deploy pattern (`app_port`/`app_port_alt`, `locals.tf:46,54`). The
Alpine ports of `setup-deploy.sh` and `setup-app-service.sh` already
implement this pattern correctly, including the health-gate-per-port fix
(`restart_and_wait` probes `127.0.0.1:$PORT` directly, never through nginx).
Migrating billing to Alpine carries this forward with no extra work.

## Decisions

1. **Rollout:** direct to prod, same as lbalancer — `var.os_family`
   (`"al2023"` default | `"alpine"`) gates `image_id`, `user_data`,
   `block_device_mappings.ebs.volume_size` in `terraform/billing/compute.tf`.
   Rollback is `terraform apply -var os_family=al2023`. Unlike lbalancer,
   billing's `aws_autoscaling_group.this` already has an `instance_refresh`
   block (`strategy = "Rolling"`, `min_healthy_percentage = 100`,
   `instance_warmup = 300` — `compute.tf:227-233`), so `terraform apply`
   alone rolls the fleet; no manual instance termination step is needed.
2. **Disk:** `volume_size = 1` (GiB) for Alpine vs `4` for AL2023, swap
   stays `256` (no build-from-source to budget extra headroom for).
3. **Daily-job catch-up (`Persistent=true`):** `billing-sweep` (07:10 UTC)
   and `billing-dunning` (08:10 UTC) rely on systemd's `Persistent=true` to
   catch up a missed run on next boot. Busybox `crond` has no equivalent.
   **Accepted risk:** a missed tick is not caught up; the next day's tick
   runs normally. A DynamoDB-backed idempotency guard already makes a
   double-run safe, so the only cost of a miss is a one-day delay, not data
   loss.
   **Known trap, not to be missed later:** `compute.tf:253-280` has a
   *commented-out* `aws_autoscaling_schedule` pair that would stop the ASG
   nightly (22:00–10:00 America/Sao_Paulo = 01:00–13:00 UTC) — a window that
   contains both 07:10 and 08:10 UTC exactly. The code comment says
   `Persistent=true` exists *because of* that planned feature. If that
   schedule is ever uncommented while billing is on Alpine, the sweep and
   dunning timers will silently miss their tick **every single day**, not
   occasionally. This spec explicitly does not solve that — the user has
   noted a Lambda + EventBridge Scheduler v2 replacement as the eventual
   fix, out of scope here. Revisit before enabling that schedule.

   **Amended 2026-08-29 — the accepted risk was withdrawn.** Spot instances
   made "a missed tick" a routine event rather than a rare one: an instance
   replaced at 04:10 BRT skips that day's sweep and, an hour later, that day's
   reminders. `Persistent=true` is now reproduced by two `@reboot` crontab
   entries (`bootstrap-alpine.sh.tftpl`), which are safe for exactly the reason
   the systemd flag was — the sweep skips a period it has already billed and
   dunning stores the step each invoice has reached, so a boot after a normal
   run is a no-op. It catches up **today**, not an arbitrary date: a day missed
   entirely is still `job.sh sweep -date=…` by hand, because guessing how far
   back to walk is how a job re-bills a month.

   The nightly-shutdown trap above is unchanged and still real; the `@reboot`
   entries would cover the 10:00 boot, but a schedule whose window contains both
   ticks every day should not be enabled on the strength of a catch-up.
4. **Environments:** apply direct to prod (user decision, same precedent as
   lbalancer). No canary/dev-first rollout requested.

## Cross-repo prerequisite (ctech-cdk, Part 1 — blocks ctech-billing work)

### `assets/ec2-alpine/setup-swap.sh` (new)

Alpine port of `assets/ec2/setup-swap.sh`. Same `dd`/`chmod`/`mkswap`/
`swapon`/fstab-append sequence lbalancer inlined manually
(`bootstrap-alpine.sh.tftpl`), but as the reusable generic script (one
positional arg: swap size in MiB), since billing (and any future Alpine
service without lbalancer's build-time disk pressure) needs it as a normal
`ctech_run` call, not an inline block.

```bash
#!/bin/bash
# Alpine/OpenRC equivalent of assets/ec2/setup-swap.sh.
#
# Usage: setup-swap.sh <size-mib>
#   setup-swap.sh 256
set -euo pipefail

SIZE_MIB="${1:?setup-swap.sh: swap size in MiB required}"

if [ ! -f /var/swapfile ]; then
  dd if=/dev/zero of=/var/swapfile bs=1M count="$SIZE_MIB"
  chmod 600 /var/swapfile
  mkswap /var/swapfile
  swapon /var/swapfile
  grep -q '^/var/swapfile ' /etc/fstab || echo "/var/swapfile swap swap defaults 0 0" >> /etc/fstab
fi
```

### `assets/ec2-alpine/setup-logs.sh` (new)

Alpine port of `assets/ec2/setup-logs.sh` — S3 log archival via logrotate,
distinct from `ctech-ec2-agent logs-tail` (which streams live to CloudWatch
Logs; this ships rotated files to the archive bucket). Read the AL2023
original in full before porting; the only expected diff is `aws s3 cp`
becoming `ctech-ec2-agent s3-cp` in the upload step — no GNU-coreutils-only
flags are expected in this script (confirm during port; if `sha256sum`,
`date`, or `sed` GNU-only flags turn up, port them the same way the
lbalancer `sha256sum -c` fix did).

### `assets/ec2-alpine/setup-ctech-ec2-agent.sh` (modify)

Billing needs **two** `logs-tail` daemons — one per CloudWatch log group
(`log_group_app` covers app.log/app2.log/jobs.log, `log_group_nginx` covers
nginx access/error) — because `logsTailConfig` (`ctech-ec2-agent/logstail.go:29`)
holds exactly one `logGroup` per config file. The current script hardcodes
the OpenRC service name `ctech-ec2-agent-logs`, so calling it twice collides.

Add an optional second positional arg, a service-name suffix, defaulting to
the current name so the one existing consumer (`ValkeyStackV2`,
`lib/valkey-stack-v2.ts:154`, single log group) is unaffected:

```bash
# Usage: setup-ctech-ec2-agent.sh <config-file> [service-suffix]
#   setup-ctech-ec2-agent.sh /tmp/ctech-logs.json            # -> ctech-ec2-agent-logs
#   setup-ctech-ec2-agent.sh /tmp/ctech-logs-app.json app    # -> ctech-ec2-agent-logs-app
SUFFIX="${2:-}"
SERVICE_NAME="ctech-ec2-agent-logs${SUFFIX:+-$SUFFIX}"
CONFIG_DEST="/etc/ctech-ec2-agent/logs${SUFFIX:+-$SUFFIX}.json"
```
`install -m 0644 "$CONFIG" "$CONFIG_DEST"`, and the generated
`/etc/init.d/$SERVICE_NAME` / `pidfile="/run/$SERVICE_NAME.pid"` /
`command_args="logs-tail -config $CONFIG_DEST"` all use the computed names
instead of the hardcoded ones. `rc-update add "$SERVICE_NAME" default` /
`rc-service "$SERVICE_NAME" start` at the end.

## ctech-billing changes (Part 2)

### `terraform/billing/variables.tf`

```hcl
variable "os_family" {
  type    = string
  default = "al2023"
  validation {
    condition     = contains(["al2023", "alpine"], var.os_family)
    error_message = "os_family must be al2023 or alpine."
  }
}
```

### `terraform/billing/compute.tf`

Add alongside the existing `data.aws_ssm_parameter.al2023_arm64_ami` (line
43):

```hcl
data "aws_ssm_parameter" "alpine_arm64_ami" {
  count = var.os_family == "alpine" ? 1 : 0
  name  = "/ctech/${var.environment}/ami/alpine/arm64"
}

data "aws_ssm_parameter" "ec2_scripts_alpine_bucket" {
  count = var.os_family == "alpine" ? 1 : 0
  name  = "/ctech/${var.environment}/ec2-scripts-alpine/bucket"
}

data "aws_ssm_parameter" "ec2_scripts_alpine_version" {
  count = var.os_family == "alpine" ? 1 : 0
  name  = "/ctech/${var.environment}/ec2-scripts-alpine/version"
}
```

Extend the `templatefile()` var map (the `bootstrap_sh` local, lines 89-132)
with `ec2_scripts_alpine_bucket`/`ec2_scripts_alpine_version` (empty string
when `os_family != "alpine"`, same ternary pattern lbalancer used), and
branch `bootstrap_sh` itself:

```hcl
bootstrap_sh = var.os_family == "alpine" ? templatefile("${path.module}/../assets/bootstrap-alpine.sh.tftpl", local.bootstrap_vars) : templatefile("${path.module}/../assets/bootstrap.sh.tftpl", local.bootstrap_vars)
```

(Extract the existing inline var map to a `local.bootstrap_vars` so both
branches share it — the map does not need an `os_family` conditional itself,
only the two new alpine-only keys added to it, mirroring lbalancer's
`userdata_template_vars` local.)

`aws_launch_template.this.image_id` and `.block_device_mappings.ebs.volume_size`
branch the same way lbalancer's did:

```hcl
image_id = var.os_family == "alpine" ? data.aws_ssm_parameter.alpine_arm64_ami[0].value : data.aws_ssm_parameter.al2023_arm64_ami.value
# ...
volume_size = var.os_family == "alpine" ? 1 : 4
```

### `terraform/assets/bootstrap-alpine.sh.tftpl` (new)

Alpine port of `terraform/assets/bootstrap.sh.tftpl`. Same substitution
convention as the original (`${...}` from Terraform, `$${...}` literal for
bash). Differences from the AL2023 original, all confirmed against the
already-ported shared scripts:

- `ctech_run` fetches via `ctech-ec2-agent s3-cp` instead of `aws s3 cp`
  (same helper shape as lbalancer's `bootstrap-alpine.sh.tftpl:8`).
- `setup-app-service.sh` call drops the "after units" argument (Alpine
  version takes `<description> <binary-name> [alt-port]`, 3 args — its
  OpenRC unit hardcodes `depend() { need net }` instead, so nothing is
  lost): `ctech_run setup-app-service.sh "CTech Billing API" app ${app_port_alt}`.
- `setup-swap.sh`, `setup-logs.sh` calls unchanged in arguments, resolved
  from the newly-ported alpine scripts (see Part 1).
- SSM agent stop branch (`enable_ssm_agent != true`) uses `rc-update del
  amazon-ssm-agent default` / `rc-service amazon-ssm-agent stop`, matching
  lbalancer's alpine bootstrap — not `systemctl disable --now`.
- **CloudWatch agent block replaced.** No `cwagent.json` / `setup-cloudwatch-agent.sh`
  call. Instead, write two `logs-tail` config files and call the extended
  `setup-ctech-ec2-agent.sh` twice:

  ```bash
  cat > /tmp/ctech-logs-app.json << 'LOGSAPP'
  {
    "logGroup": "${log_group_app}",
    "files": [
      { "path": "/var/log/app/app.log",  "streamPrefix": "app" },
      { "path": "/var/log/app/app2.log", "streamPrefix": "app2" },
      { "path": "/var/log/app/jobs.log", "streamPrefix": "jobs" }
    ]
  }
  LOGSAPP
  ctech_run setup-ctech-ec2-agent.sh /tmp/ctech-logs-app.json app

  cat > /tmp/ctech-logs-nginx.json << 'LOGSNGINX'
  {
    "logGroup": "${log_group_nginx}",
    "files": [
      { "path": "/var/log/nginx/access.log", "streamPrefix": "access" },
      { "path": "/var/log/nginx/error.log",  "streamPrefix": "error" }
    ]
  }
  LOGSNGINX
  ctech_run setup-ctech-ec2-agent.sh /tmp/ctech-logs-nginx.json nginx
  ```

  Note the resulting CloudWatch stream names change shape:
  `logstail.go`'s `streamName()` builds `{prefix}/{instanceID}` (prefix
  first), where the AL2023 config built `{instance_id}/{suffix}`
  (instance first) and left the plain `app.log` stream bare (no suffix).
  Alpine streams are `app/{instanceID}`, `app2/{instanceID}`,
  `jobs/{instanceID}`, `access/{instanceID}`, `error/{instanceID}` — a
  cosmetic, one-time change to any saved CloudWatch Logs Insights query or
  dashboard filter that assumed the old shape. Call this out in the PR
  description; no code needs to handle both shapes.

- **`job.sh` (billing-specific, generated inline, not a shared script)
  ports its leader-election call:**

  AL2023 (`bootstrap.sh.tftpl:146-150`):
  ```bash
  LEADER=$(aws autoscaling describe-auto-scaling-groups \
    --auto-scaling-group-names "ASG_NAME_PLACEHOLDER" \
    --region REGION_PLACEHOLDER \
    --query 'AutoScalingGroups[0].Instances[?LifecycleState==`InService`].InstanceId' \
    --output text | tr '\t' '\n' | sort | head -n1)
  ```

  Alpine:
  ```bash
  LEADER=$(ctech-ec2-agent asg-describe -names "ASG_NAME_PLACEHOLDER" \
    | jq -r '.AutoScalingGroups[0].Instances[] | select(.LifecycleState=="InService") | .InstanceId' \
    | sort | head -n1)
  ```
  (`jq` is already installed by `setup-base.sh`'s alpine port — confirmed
  in `assets/ec2-alpine/setup-base.sh:12`. `-region` is unnecessary:
  `ctech-ec2-agent` always resolves its region from IMDS, per every other
  subcommand's `newXClient` helper.)

- **Timers become OpenRC services (`app`, already covered by the shared
  script) plus busybox cron entries** for the four one-shot jobs. Each job
  keeps its own `job.sh <name>` invocation and its own
  `/bin/test -x /opt/app/current/<name>` guard, now expressed as the first
  lines of the cron-invoked script rather than `ExecStartPre=`:

  Concretely:
  - `billing-sweep` (daily 07:10 UTC) and `billing-dunning` (daily 08:10
    UTC) become two scripts dropped into a root crontab with an exact-minute
    entry (`10 7 * * *` / `10 8 * * *`) — busybox `crond` supports
    minute/hour/day/month/weekday fields natively, so no periodic-directory
    jitter script is needed for these two. `RandomizedDelaySec=300` has no
    equivalent and is dropped (its purpose — avoiding a fleet-wide
    thundering herd — does not apply to a `min_size=1` ASG).
  - `billing-reconcile` (hourly) drops into `/etc/periodic/hourly/`.
  - `billing-deliver` (every 60s) is the one job below cron's one-minute
    floor's *edge* — 60s exactly equals cron's finest granularity, so it
    maps directly to a plain root crontab entry (`* * * * *`), unlike
    lbalancer's 30s reconcile which needed a supervised `while` loop.
    `OnBootSec=90` (skip the first tick right after boot) has no direct
    cron equivalent and is not worth replicating — the app process itself
    is what needs to be up, and it is, by the time cron's own minute tick
    fires post-bootstrap.

  Each cron entry runs `/opt/app/job.sh <name>` directly (unchanged from
  AL2023's oneshot units, since `job.sh` already does its own leader check,
  env sourcing, and binary-exists guard) with output appended to
  `/var/log/app/jobs.log` (`>> /var/log/app/jobs.log 2>&1` in the crontab
  line, replacing systemd's `StandardOutput=append:...`).

  Install via `crontab -l 2>/dev/null; echo "...") | crontab -` for the
  every-minute and daily entries (root's own crontab), and a plain
  executable file under `/etc/periodic/hourly/billing-reconcile` for the
  hourly one (matching lbalancer's `refresh-cloudflare-ips` daily-periodic
  pattern, one level up in frequency).

### IAM (`terraform/billing/iam.tf`) — no changes

Every action the Alpine path needs is already granted with a scope wide
enough to cover it without a new statement:
- `ReadSharedEc2BootstrapScripts` (`s3:GetObject` on
  `arn:aws:s3:::${var.environment}-ctech-ec2-scripts/*`) — the Alpine
  scripts and `ctech-ec2-agent` binary live in the **same** bucket, different
  key prefix (confirmed in ctech-cdk's `lib/ec2-scripts-stack.ts:45,48`), so
  the existing wildcard already covers them.
- `CloudWatchAgent` (`logs:CreateLogStream`, `logs:PutLogEvents`,
  `logs:DescribeLogStreams`, `logs:DescribeLogGroups` on
  `${aws_cloudwatch_log_group.app.arn}:*` and `...nginx.arn}:*`) — this is
  exactly what `ctech-ec2-agent logs-tail` needs too; it is a drop-in
  replacement for the CloudWatch Agent binary, not a different IAM
  consumer.
- `DescribeOwnAutoScalingGroup` (`autoscaling:DescribeAutoScalingGroups`,
  resource `*`) — already covers `ctech-ec2-agent asg-describe`'s call,
  same API action.
- `ReadOwnReleaseArtifacts`, `ListOwnReleasePrefix`, `ArchiveOwnLogs` — S3
  paths unchanged by the OS switch; `setup-deploy.sh` and `setup-logs.sh`'s
  Alpine ports read/write the identical bucket/prefix, just via
  `ctech-ec2-agent s3-cp`/`s3-put`... (billing's archive path is a `PutObject`
  only, matching `s3-put`, already an existing `ctech-ec2-agent` subcommand
  from the lbalancer work).

No `iam.tf` statement is added or modified — this section exists in the plan
to require an explicit "confirmed, no gap" verification task rather than a
silent assumption, the same way lbalancer's plan called out its one real
IAM gap.

## Out of scope

- Replacing `billing-sweep`/`billing-dunning`'s missed-run risk with a
  Lambda + EventBridge Scheduler v2 job (user's stated future direction,
  decision 3 above).
- Enabling the commented-out nightly stop/start `aws_autoscaling_schedule`
  pair (`compute.tf:253-280`) — stays commented out; this migration does
  not touch it, but see decision 3's warning before anyone uncomments it.
- Any change to `ctech-billing`'s application code, DynamoDB schema, or SES
  configuration.

## Verification (no `terraform apply`/`packer build` run by the agent)

- `bash -n` on every new/modified `.sh` file in `ctech-cdk/assets/ec2-alpine`,
  plus the existing `ec2-alpine-scripts.test.ts` Jest suite
  (`ctech-cdk/test/ec2-alpine-scripts.test.ts`), extended with cases for the
  two new scripts and the `setup-ctech-ec2-agent.sh` suffix argument.
- `terraform fmt`/`terraform validate` in `ctech-billing/terraform/billing`.
- `diff` every new `.tftpl`/`.sh` against its AL2023 source and confirm only
  the hunks named above differ — same technique used for every lbalancer
  alpine script.
- `terraform plan -var os_family=alpine` (handed to the user to run, never
  applied by the agent) must show exactly: `data` additions with `count=1`,
  `aws_launch_template.this` updated in place (`image_id`, `user_data`,
  `volume_size` 4→1), `aws_autoscaling_group.this` updated in place — no
  `iam.tf` diff, no destroy.
- Live verification on the prod instance after `terraform apply` (handed to
  the user): both `logs-tail` OpenRC services (`ctech-ec2-agent-logs-app`,
  `ctech-ec2-agent-logs-nginx`) report `started`; confirm log lines actually
  arrive in both CloudWatch log groups; confirm `crontab -l` lists the four
  job entries; confirm `/etc/periodic/hourly/billing-reconcile` is
  executable; trigger one manual `job.sh sweep` run via SSM and confirm it
  exits 0 and appends to `/var/log/app/jobs.log`.
