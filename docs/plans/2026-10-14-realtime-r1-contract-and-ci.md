# Realtime R1 — The contract and its CI — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `proto/realtime.proto` exists with generated Go and TypeScript, and a CI workflow that fails on a breaking change, on generated-code drift, on a Go/TypeScript disagreement about the bytes, and on a free-text field added to `Change`.

**Architecture:** The Poker pipeline (pinned `protoc` + `protoc-gen-go` + `ts-proto`, regenerate, `git diff --exit-code`) plus `buf lint` and `buf breaking`, committed golden `.bin` fixtures decoded and re-encoded by both languages, and a field-set test. Nothing runs at build time: the generated files are committed.

**Tech Stack:** protobuf 3, `protoc` 36.1, `protoc-gen-go` 1.36.12, `ts-proto` 2.x with `@bufbuild/protobuf` 2.x (versions as Poker pins them), `buf`, Go test, vitest, GitHub Actions.

**Spec:** `docs/specs/2026-10-14-realtime.md` (§4.5, §5, §7). **Depends on:** nothing. **Blocks:** R2, R3.

Repos: `P_BILL=/home/artur-revgas/Documents/Projects/Ctech/ctech-billing` (api in `P_BILL/api`, ui in `P_BILL/ui`). Pattern source: `/home/artur-revgas/Documents/Projects/Ctech/ctech-poker/scripts/generate-proto.sh` and `.github/workflows/proto.yml`.

## Global Constraints

- The contract is Billing's alone; no shared proto repository (spec D2).
- A field number is never reused; a removed field is `reserved`. Additions are optional and old readers ignore them.
- `Change` carries ids and state only (spec D3). The field-set test is the guard.
- Generated files are committed and never edited by hand. Generator versions are pinned in the workflow and in `ui/package-lock.json`.
- Routes and proto names in English; no attribution trailers; docs under `docs/specs|plans/`.
- Nothing in this phase is wired into the running API or UI.

## Review Focus

- Renumbering or reusing a field passes `protoc` but must fail `buf breaking`. Pinned in Task 4.
- A Go-encoded frame and a TypeScript-encoded frame of the same content must be byte-identical (proto3 is canonical for these scalar/oneof shapes, but ordering of oneof is exactly the thing that drifts). Pinned in Task 3.
- A `Change` field named like a person, an amount or a document must not pass the field-set test. Pinned in Task 5.
- An unknown future field in a frame must be skipped by both decoders, not rejected. Pinned in Task 3.
- Forgetting to regenerate must fail CI with a message that says what to run. Pinned in Task 4.

---

### Task 1: The proto file

**Files:**
- Create: `proto/realtime.proto`, `buf.yaml`, `docs/realtime/protocol.md`

- [ ] **Step 1:** Create `proto/realtime.proto` exactly as in the spec § 4.5 (`package ctech.billing.realtime.v1;`, `option go_package = "gopkg.aoctech.app/billing/api/internal/realtime/wire;wire";`). Messages: `ClientFrame{Auth,Reauth,Ping}`, `ServerFrame{Ready,Change,Gap,Pong,Tick,Bye,Hint}`, with every field as listed there.
- [ ] **Step 2:** Create `buf.yaml` at the repo root:

```yaml
version: v2
modules:
  - path: proto
lint:
  use: [STANDARD]
  except: [PACKAGE_VERSION_SUFFIX]   # the version is in the package already: v1
breaking:
  use: [FILE]
```

- [ ] **Step 3:** Create `docs/realtime/protocol.md` with a table `protocol_version | added | introduced in`. Row 1: `1 | the first protocol: Auth, Reauth, Ping, Ready, Change, Gap, Pong, Tick, Bye, Hint | R1`. Add the rule text from spec § 4.5 (numbers never reused, additive, feature gates, no minimum client).
- [ ] **Step 4:** `buf lint` locally (install per Step 1 of Task 2 if missing). Expected: no findings.
- [ ] **Step 5: Commit** `feat(realtime): the wire contract (proto v1)`.

### Task 2: Generation

**Files:**
- Create: `scripts/generate-proto.sh`, `api/internal/realtime/wire/realtime.pb.go`, `ui/src/lib/realtime/wire/realtime.ts`
- Modify: `ui/package.json` (dev deps `ts-proto`, runtime `@bufbuild/protobuf`; same majors as Poker's `ui/package.json:26,56`)

- [ ] **Step 1:** Install what the script needs, once, locally: `protoc` 36.1 from the protobuf releases, `go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12`, `buf` from its release page. `cd ui && npm install --save @bufbuild/protobuf && npm install --save-dev ts-proto`.
- [ ] **Step 2:** Create `scripts/generate-proto.sh` (executable), modelled on Poker's:

```bash
#!/usr/bin/env bash
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
command -v protoc >/dev/null || { echo "protoc 36.1 is required (see docs/realtime/protocol.md)"; exit 1; }
command -v protoc-gen-go >/dev/null || { echo "go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12"; exit 1; }
test -x "$ROOT/ui/node_modules/.bin/protoc-gen-ts_proto" || { echo "run: npm ci --prefix ui"; exit 1; }

mkdir -p "$ROOT/api/internal/realtime/wire" "$ROOT/ui/src/lib/realtime/wire"
protoc -I "$ROOT/proto" \
  --go_out="$ROOT/api/internal/realtime/wire" --go_opt=paths=source_relative \
  realtime.proto
protoc -I "$ROOT/proto" \
  --plugin="protoc-gen-ts_proto=$ROOT/ui/node_modules/.bin/protoc-gen-ts_proto" \
  --ts_proto_out="$ROOT/ui/src/lib/realtime/wire" \
  --ts_proto_opt=snakeToCamel=false,outputServices=none,esModuleInterop=true,useOptionals=messages,oneof=unions-value \
  realtime.proto
```

  (`oneof=unions-value` gives `{ $case: "change", value: Change }`-style unions in TypeScript, which narrows cleanly in a `switch`.)
- [ ] **Step 3:** Run it. Expected: `realtime.pb.go` and `realtime.ts` appear. `cd api && go build ./...` and `cd ui && npx tsc --noEmit` pass.
- [ ] **Step 4: Commit** `feat(realtime): generated Go and TypeScript for the contract`.

### Task 3: Golden bytes, in both languages

**Files:**
- Create: `proto/testdata/*.bin` (+ `README.md`), `api/internal/realtime/wire/golden_test.go`, `ui/src/lib/realtime/wire/golden.test.ts`, `api/internal/realtime/wire/cmd_gen/main.go` (writes the fixtures)

- [ ] **Step 1: Define the fixture set** (one file each, named for what it holds): `auth_token`, `auth_pay_link`, `reauth`, `ping`, `ready`, `change_invoice_paid`, `change_minimal` (only `id` and `type`), `gap_cursor_too_old`, `pong`, `tick`, `bye_draining`, `hint`, and `change_with_unknown_field` (a `Change` followed by an unknown field number 99, to prove it is skipped).
- [ ] **Step 2: Write the failing Go test** (`golden_test.go`):

```go
func TestEveryGoldenFileDecodesAndReEncodesToTheSameBytes(t *testing.T) {
	files, _ := filepath.Glob("../../../../proto/testdata/*.bin")
	if len(files) < 13 {
		t.Fatalf("expected the whole fixture set, found %d", len(files))
	}
	for _, f := range files {
		raw, _ := os.ReadFile(f)
		name := filepath.Base(f)
		var msg proto.Message = &ServerFrame{}
		if strings.HasPrefix(name, "auth") || strings.HasPrefix(name, "reauth") || name == "ping.bin" {
			msg = &ClientFrame{}
		}
		if err := proto.Unmarshal(raw, msg); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		again, err := proto.MarshalOptions{Deterministic: true}.Marshal(msg)
		if err != nil || (name != "change_with_unknown_field.bin" && !bytes.Equal(raw, again)) {
			t.Errorf("%s re-encodes differently: %x vs %x", name, raw, again)
		}
	}
}

func TestAnUnknownFieldIsSkippedNotRejected(t *testing.T) {
	raw, _ := os.ReadFile("../../../../proto/testdata/change_with_unknown_field.bin")
	var f ServerFrame
	if err := proto.Unmarshal(raw, &f); err != nil || f.GetChange().GetId() == "" {
		t.Fatalf("%v %+v", err, f.GetChange())
	}
}
```

- [ ] **Step 3: Write the generator** `cmd_gen/main.go` building each fixture from literal values (fixed ids like `01JABCDEFGHJKMNPQRSTVWXYZ0`, fixed timestamps) with deterministic marshalling, writing `proto/testdata/<name>.bin`; for `change_with_unknown_field` append the raw bytes of field 99 (`0xFA 0x06 …`) after the encoded frame. Run it once and commit the `.bin` files with a `proto/testdata/README.md` saying **they are the contract's reference bytes; regenerate only with an intentional contract change, in the same PR**.
- [ ] **Step 4: Write the failing TypeScript test** (`golden.test.ts`) with the same loop over `proto/testdata/*.bin` using `fs`, decoding with `ServerFrame.decode`/`ClientFrame.decode`, re-encoding, and comparing bytes with `Buffer.equals` (skip the byte comparison for the unknown-field fixture, assert it decodes and the `change.id` is set).
- [ ] **Step 5: Run** `cd api && go test ./internal/realtime/wire/ -v` and `cd ui && npx vitest run src/lib/realtime/wire` — expect PASS in both once the fixtures exist (they were red before Step 3).
- [ ] **Step 6: Commit** `test(realtime): golden bytes decoded by Go and TypeScript`.

### Task 4: The workflow

**Files:**
- Create: `.github/workflows/proto.yml`

- [ ] **Step 1:** Create `proto.yml`, triggered on pull requests and pushes to `main`, `staging`, `dev` touching `proto/**`, `buf.yaml`, `scripts/generate-proto.sh`, the generated files, and the workflow itself. Jobs (one job, ordered steps, cheapest first):

```yaml
name: Protobuf contract
on:
  pull_request:
    paths: ['proto/**', 'buf.yaml', 'scripts/generate-proto.sh', 'api/internal/realtime/wire/**', 'ui/src/lib/realtime/wire/**', '.github/workflows/proto.yml']
  push:
    branches: [main, staging, dev]
    paths: ['proto/**', 'buf.yaml', 'scripts/generate-proto.sh', '.github/workflows/proto.yml']
permissions:
  contents: read
jobs:
  contract:
    runs-on: ubuntu-26.04-arm
    steps:
      - uses: actions/checkout@v7
        with: { fetch-depth: 0 }          # buf breaking needs the base branch
      - uses: bufbuild/buf-action@v1       # pinned by SHA when this file is committed
        with:
          lint: true
          breaking: true
          breaking_against: 'https://github.com/${{ github.repository }}.git#branch=main'
      - uses: actions/setup-go@v7
        with: { go-version-file: api/go.mod }
      - uses: actions/setup-node@v7
        with: { node-version: '24', cache: npm, cache-dependency-path: ui/package-lock.json }
      - name: Install protoc 36.1
        run: |
          curl -sSL -o /tmp/protoc.zip https://github.com/protocolbuffers/protobuf/releases/download/v36.1/protoc-36.1-linux-aarch_64.zip
          sudo unzip -o /tmp/protoc.zip -d /usr/local && rm /tmp/protoc.zip
      - name: Install pinned generators
        run: |
          go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12
          npm ci --prefix ui
      - name: Regenerate the contract
        run: ./scripts/generate-proto.sh
      - name: Generated files are committed
        run: git diff --exit-code -- api/internal/realtime/wire ui/src/lib/realtime/wire || { echo "::error::Run ./scripts/generate-proto.sh and commit the result"; exit 1; }
      - name: Go and TypeScript agree on the bytes
        run: |
          (cd api && go test ./internal/realtime/wire/...)
          (cd ui && npx vitest run src/lib/realtime/wire)
```

- [ ] **Step 2: Prove it fails when it should** (do each on a throwaway branch, push, read the check, delete the branch): (a) change a field number in `realtime.proto` → `buf breaking` fails; (b) change a field name without regenerating → the drift step fails with the message above; (c) edit a `.bin` byte → the golden step fails.
- [ ] **Step 3: Commit** `ci(realtime): contract workflow (lint, breaking, drift, golden bytes)`. Record the three failure runs' URLs in the PR description.

### Task 5: The field-set guard

**Files:**
- Create: `api/internal/realtime/wire/change_fields_test.go`

- [ ] **Step 1: Write the failing test:**

```go
// Change is a hint to read, not data (spec D3). Every field it has is on this list
// on purpose; adding one is a decision, and this test is where it gets made.
func TestChangeCarriesIdsAndStateOnly(t *testing.T) {
	allowed := map[string]bool{"id": true, "type": true, "entity": true, "entity_id": true, "status": true, "revision": true, "occurred_at_ms": true}
	fields := (&Change{}).ProtoReflect().Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		if name := string(fields.Get(i).Name()); !allowed[name] {
			t.Errorf("Change.%s is not an id or a state word; the event must not carry data. Read spec D3 before adding it.", name)
		}
	}
	if fields.Len() != len(allowed) {
		t.Errorf("Change has %d fields, the allowed set has %d", fields.Len(), len(allowed))
	}
}
```

- [ ] **Step 2:** Run — expect PASS now (the proto already matches); temporarily add `string customer_name = 8;` to prove it FAILS, then revert.
- [ ] **Step 3: Commit** `test(realtime): Change may carry ids and state only`.

### Task 6: Pull-request template and docs

**Files:**
- Modify/Create: `.github/pull_request_template.md` (or the existing one), `docs/realtime/protocol.md`

- [ ] **Step 1:** Add a checklist item to the PR template: `Changes proto/**? Updated docs/realtime/protocol.md (new protocol_version or "no version change") and ran ./scripts/generate-proto.sh`.
- [ ] **Step 2:** Add to `protocol.md` how to add a message (steps: edit proto, regenerate, add a golden file + both tests, bump version only if a client must gate a feature) and how to retire a field (`reserved`).
- [ ] **Step 3: Commit** and open the PR for R1. All checks green; `buf breaking` has nothing to compare against on the first PR (the base has no `proto/`), which is expected: note it in the PR.

## Self-review (against the spec)

| Spec requirement | Task |
|---|---|
| § 4.5 the protocol | 1 |
| § 7.1 buf lint and breaking | 1, 4 |
| § 7.2 regenerate and fail on drift | 2, 4 |
| § 7.3 golden bytes in both languages | 3 |
| § 7.4 `Change` field set | 5 |
| § 7.5 generated files committed | 2, 4 |
| § 7.7 protocol.md and the PR checklist | 1, 6 |
| § 7.6 deploy order and `protocol_version` | documented in 1 and 6 (enforced by use in R2/R3) |
