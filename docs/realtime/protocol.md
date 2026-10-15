# Realtime protocol

The wire contract between Billing's realtime endpoint and its clients (the
checkout, the console, the portal and Finanças). Defined in
`proto/realtime.proto`; the design is `docs/specs/2026-10-14-realtime.md` (§ 4.5).

## Versions

`Ready.protocol_version` tells a client what the server it reached understands.

| protocol_version | added | introduced in |
|---|---|---|
| 1 | the first protocol: Auth, Reauth, Ping, Ready, Change, Gap, Pong, Tick, Bye, Hint | R1 |

A version is added only when a client must **not** use a feature against an older
server. A change that old readers can ignore needs no new version: write the row
as "no version change" in the pull request instead.

## Rules

Enforced by CI (`.github/workflows/proto.yml`):

- A field number is never reused. A removed field is `reserved` (number and name),
  so nothing can claim it later. `buf breaking` fails the build otherwise.
- Additions are optional and old readers ignore them. Both decoders skip an
  unknown field instead of rejecting the frame (pinned by the golden fixtures).
- A feature a client must not use against an older server is gated on
  `Ready.protocol_version`, and the version is written in the table above.
- There is no minimum client: an old client keeps working, with the new
  features dark.
- `Change` carries ids and state only (spec D3). A new field on it is a decision,
  made in `api/internal/realtime/wire/change_fields_test.go`.
- Errors are not a frame type: an auth failure is `Bye` with a reason and a close
  code.

## Generating

The generated Go (`api/internal/realtime/wire/realtime.pb.go`) and TypeScript
(`ui/src/lib/realtime/wire/realtime.ts`) are committed and never edited by hand.
Regenerate with `./scripts/generate-proto.sh`. It needs the pinned versions, the
same ones CI installs:

- `protoc` 36.1 (protobuf releases)
- `protoc-gen-go` v1.36.12 (`go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12`)
- `npm ci --prefix ui` (`ts-proto` and `@bufbuild/protobuf` are pinned in `ui/package-lock.json`)
- `buf` for `buf lint` and `buf breaking`

A different `protoc-gen-go` version writes a different header and shows up as drift
in CI, so put the pinned one first in `PATH`.

## Adding a message or a field

1. Edit `proto/realtime.proto` (a new field gets the next unused number).
2. `./scripts/generate-proto.sh`, and commit the generated files.
3. Add a golden fixture for it (`proto/testdata/`, written by
   `api/internal/realtime/wire/cmd_gen`) and make sure both the Go and the
   TypeScript tests cover it.
4. Add a row to the table above only if a client must gate a feature on it.

## Retiring a field

Replace it with `reserved <number>;` and `reserved "<name>";`. Never reuse either.
Keep reading it on the server until no supported client sends it.
