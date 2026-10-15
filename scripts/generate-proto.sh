#!/usr/bin/env bash
# Regenerates the Go and TypeScript code for proto/realtime.proto. The output is
# committed; CI runs this and fails on any difference. Versions are pinned in
# docs/realtime/protocol.md and .github/workflows/proto.yml.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
command -v protoc >/dev/null || { echo "protoc 36.1 is required (see docs/realtime/protocol.md)"; exit 1; }
command -v protoc-gen-go >/dev/null || { echo "go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.12"; exit 1; }
test -x "$ROOT/ui/node_modules/.bin/protoc-gen-ts_proto" || { echo "run: npm ci --prefix ui"; exit 1; }

mkdir -p "$ROOT/api/internal/realtime/wire" "$ROOT/ui/src/lib/realtime/wire"

protoc -I "$ROOT/proto" \
  --go_out="$ROOT/api/internal/realtime/wire" \
  --go_opt=paths=source_relative \
  realtime.proto

protoc -I "$ROOT/proto" \
  --plugin="protoc-gen-ts_proto=$ROOT/ui/node_modules/.bin/protoc-gen-ts_proto" \
  --ts_proto_out="$ROOT/ui/src/lib/realtime/wire" \
  --ts_proto_opt=snakeToCamel=false,outputServices=none,esModuleInterop=true,useOptionals=messages,oneof=unions-value \
  realtime.proto
