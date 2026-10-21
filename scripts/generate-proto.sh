#!/usr/bin/env bash
set -euo pipefail
root=$(cd -- "$(dirname -- "$0")/.." && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cd "$root"
# Upstream source: XTLS/Xray-core v26.7.28; keep wire package names unchanged.
curl -fsSL https://raw.githubusercontent.com/XTLS/Xray-core/v26.7.28/app/stats/command/command.proto -o internal/xrayapi/stats.proto
curl -fsSL https://raw.githubusercontent.com/XTLS/Xray-core/v26.7.28/proxy/hysteria/account/config.proto -o internal/xrayapi/hysteria.proto
go build -o "$work/protoc-gen-go" google.golang.org/protobuf/cmd/protoc-gen-go
protoc --plugin="protoc-gen-go=$work/protoc-gen-go" --go_out=. \
  --go_opt=module=github.com/aaaSaZaN/gowave-node \
  --go_opt=Minternal/xrayapi/stats.proto=github.com/aaaSaZaN/gowave-node/internal/xrayapi \
  --go_opt=Minternal/xrayapi/hysteria.proto=github.com/aaaSaZaN/gowave-node/internal/xrayapi/hysteria \
  internal/xrayapi/stats.proto internal/xrayapi/hysteria.proto
python3 scripts/generate-proto-client.py
gofmt -w internal/xrayapi
