# Realtime runbook

How the live screens work, how to see whether they are well, how to turn them off.
Spec: `docs/specs/2026-10-14-realtime.md`. Wire: `docs/realtime/protocol.md`. Events: `docs/realtime/events.md`.

There are **no CloudWatch alarms and no custom metrics** (cost). Health is read from the
gateway's own log line with `scripts/realtime-check.sh`, by a person, at rollout time and when
somebody reports a problem.

## How it works in one page {#how-it-works}

- A mutation commits its change in the same DynamoDB transaction as the state (`RT#{scope}` items in the webhook table, 48 h TTL), then publishes it through the ws registry (Valkey in production). Scopes: `tenant:{org}:{mode}`, `space:{key}`, `user:{sub}`, `invoice:{org}:{mode}:{id}`.
- `GET /v1.0/ws` authenticates on its first frame, replays from the client's cursor, then streams live. A change carries ids and state words only; the screen refetches through the authorized REST API.
- If the socket cannot be had, the UI polls `GET /v1.0/realtime/changes` every 10 s; if that is also gone the screens refetch on focus, as before.
- One line a minute: `realtime stats stats="k=v ..."` in `/ctech-billing/prod/app`.

## Reading it {#reading}

```
scripts/realtime-check.sh prod 60     # the last 60 minutes
```

It prints peak connections, gaps, slow-consumer closes, publish errors, auth failures,
fallback polls and the worst write latency, and exits 2 when a rollback trigger is hit:
publish errors above zero; gaps above 3 % of connections an hour; write latency above 1.5 s;
any slow-consumer close under normal load. "No stats lines" means the flag is off or nobody is connected.

## Gap rate is up {#gaps}

`gap.replay_limit`: clients reconnecting more than 3 times a minute, or a replay too long, or the change log unreadable (a flapping network or edge: look at nginx and HAProxy logs; or DynamoDB trouble). `gap.slow_consumer`: a client that cannot keep up (one browser; ignore unless widespread). `gap.cursor_too_old`: a client that slept past the log's 48 h (normal).

## Publish errors {#publish-errors}

Valkey is unreachable or the registry cannot publish. Clients are told to poll (Hint) and recover on their own when it returns. Check the Valkey instance and the security group first.

## Clients stuck polling {#polling}

`fallback_polls` high with few connections: the edge refuses upgrades. Check, in order: the nginx `location = /v1.0/ws` block (`terraform/assets/bootstrap*.tftpl`), HAProxy `timeout tunnel`, Cloudflare WebSockets, and the browser's CSP (`NEXT_PUBLIC_WS_URL` in `.github/workflows/frontend.yml` must name the `wss://` host: the match is scheme-exact).

## "My screen did not update" {#not-updating}

1. Flag: `NEXT_PUBLIC_REALTIME` and `NEXT_PUBLIC_REALTIME_SURFACES` in the deployed build, `REALTIME_ENABLED` on the API. The indicator ("Ao vivo") shows on the checkout.
2. Edge: does the socket upgrade (101) in the browser's network tab; a CSP violation in the console means `NEXT_PUBLIC_WS_URL` is missing.
3. Cursor and scope: the first frame's selectors (org, mode, space) must match the screen's.
4. Change log: `aws dynamodb query` on the webhook table, partition `RT#{scope}`, newest first. No row means the mutation did not emit it (a missing emission site: `docs/realtime/events.md` and `api/internal/repositories/realtime_hooks.go`).
5. If the row is there and the screen is still stale, the screen's query key is not in `effectsOf` (`ui/src/lib/realtime/catalogue.ts`).

## Turning it off {#off}

- One surface: remove it from `NEXT_PUBLIC_REALTIME_SURFACES` in the `build-env-prod` of `.github/workflows/frontend.yml` and deploy the frontend. The surface mounts nothing on the next load.
- Everything in the UI: `NEXT_PUBLIC_REALTIME` off, same file.
- The server: `realtime_enabled = false` in Terraform and deploy. The instance restarts, the sockets close, the routes disappear. A UI still carrying the flag gets a 404 from the cursor route and stops by itself (status off) until reload; the screens carry on as before.

## Draining {#drain}

A deploy sends `Bye{draining}` to every client with a retry-after and jitter; they reconnect and resume from their cursor. Nothing to do.

## Replay for one user {#replay}

`GET /v1.0/realtime/changes?cursor=<ulid>` with the user's token returns what they would be sent.

## Rollout {#rollout}

One surface at a time, in this order, each with its soak, running `scripts/realtime-check.sh prod` before and after: checkout (2 days), console (3), portal (3), Finanças (3). Step: add `NEXT_PUBLIC_REALTIME=true` and `NEXT_PUBLIC_REALTIME_SURFACES=<list>` to `build-env-prod`, set `realtime_enabled = true` on the API first (nothing is published to anyone until a client connects). Roll back at the first trigger the check prints.
