#!/usr/bin/env bash
set -euo pipefail
script_dir=$(cd -- "$(dirname -- "$0")" && pwd)
output=${1:?Usage: build-asn.sh output.zip}
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
curl -fsSL https://github.com/remnawave/asn-index/releases/latest/download/asn-prefixes.lmdb.zst -o "$work/index.zst"
zstd -d "$work/index.zst" -o "$work/index.lmdb"
npm install --prefix "$work/tools" --ignore-scripts=false --no-audit --no-fund lmdb@3.5.6
node "$script_dir/export-asn.mjs" "$work/tools/node_modules/lmdb/index.js" "$work/index.lmdb" | python3 "$script_dir/pack-asn.py" "$output"
sha256sum "$output"
