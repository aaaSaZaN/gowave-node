# Remnanode (Go)

[![Release](https://github.com/aaaSaZaN/remnanode-go/actions/workflows/release.yml/badge.svg)](https://github.com/aaaSaZaN/remnanode-go/actions/workflows/release.yml)
[![GitHub release](https://img.shields.io/github/v/release/aaaSaZaN/remnanode-go?include_prereleases&style=flat-square)](https://github.com/aaaSaZaN/remnanode-go/releases)
[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL_3.0-blue.svg?style=flat-square)](LICENSE)

A Go implementation of the Remnawave node control plane. Xray runs as a separate
process. The compatibility baseline is Remnawave Node v3.4.2, so the node works
with both [GoWave](https://github.com/aaaSaZaN/gowave) and Remnawave panels.

The runtime needs no Node.js, Python, CGO, or LMDB library.

**Documentation: [aaasazan.github.io/gowave-docs](https://aaasazan.github.io/gowave-docs/en/install/node/install/)** · [Русский](https://aaasazan.github.io/gowave-docs/install/node/install/)

## Install, update, or migrate

One command installs a fresh node, updates a native node, or migrates a Docker
`remnawave/node` container:

```sh
curl -fsSL https://raw.githubusercontent.com/aaaSaZaN/remnanode-go/main/install.sh | sudo sh
```

A fresh install asks for the `SECRET_KEY` from the node settings in the panel and
for `NODE_PORT`. The script is POSIX sh and supports systemd, OpenRC, and OpenWrt
procd on amd64, 386, arm64, ARMv7/v6/v5, RISC-V, and MIPS.

| Topic | Page |
| :--- | :--- |
| Installation, unattended runs, canary versions | [Install](https://aaasazan.github.io/gowave-docs/en/install/node/install/) |
| Environment variables, firewall, custom core | [Configuration](https://aaasazan.github.io/gowave-docs/en/install/node/configuration/) |
| `remnanode` commands, node and Xray updates | [CLI](https://aaasazan.github.io/gowave-docs/en/install/node/cli/) |
| Moving from the Docker `remnawave/node` | [Migration](https://aaasazan.github.io/gowave-docs/en/migration/docker-node/) |

## Build and test

Use the Go version declared in `go.mod` (currently 1.27.1).

```sh
CGO_ENABLED=0 go build -trimpath -o remnanode ./cmd/remnanode
go test -race ./...
go vet ./...
python3 tests/test_scripts.py
```

To exercise a real core, download and verify it into a temporary directory:

```sh
python3 scripts/fetch-test-core.py v26.7.28 /tmp/remnanode-core
REMNANODE_TEST_CORE=/tmp/remnanode-core/rw-core go test -race ./internal/xray -run RealCore -v
```

CI runs baseline/latest-stable core tests and isolated native/s6 HTTPS smoke tests
on amd64 and arm64. 386, ARMv7/v6/v5, RISC-V, and MIPS binaries are cross-built;
router deployment is not certified by the server smoke tests.

## Releases

To canary a published candidate, run
`curl -fsSL ... | sudo REMNANODE_VERSION=v1.0.6-rc.1 sh`. An unavailable explicitly
selected tag fails without switching services.

Candidate assets must include `checksums.txt`, `asn-prefixes.zip`, and the matching
`remnanode-linux-<architecture>` binary. The Candidate artifacts workflow builds
these files without publishing a release. Publish only after the Validation
workflow and a canary migration pass.

## Compatibility and diagnostics

All 25 original controller routes have contract fixtures. Compressed JSON
requests support gzip, deflate, Brotli, and Zstandard, with the original 1000 MiB
decoded-body limit. Traffic RPC failures retain the original error envelopes;
user mutations return `success: false` if any requested mutation fails, while
preserving successful changes for reconciliation. User IDs remain strings.

Plugins include ingress/egress filters, ASN shared lists, torrent detection and
reports, exclusion-aware connection destruction, socket-only pre-start cleanup,
and post-start webhooks with metadata. The optional integration registry is
empty, matching the concrete providers in the reference snapshot.

Memory depends on user count, configuration, and workload. Measure the Go daemon
and Xray separately; there is no fixed 10 to 15 MB guarantee. See `docs/validation.md`
for reproducible checks and release limitations.

## ASN asset generation

`scripts/build-asn.sh output.zip` downloads the upstream LMDB index, exports it
with the build-only `lmdb@3.5.6` module, and creates a deterministic portable ZIP.
The runtime loads individual ASN entries on demand and caches at most 64 entries.
Build requirements are Node.js/npm, Python 3, curl, and zstd. They are not needed
on an installed node.

The original projects are [Remnawave Node](https://github.com/remnawave/node) and
[Xray-core](https://github.com/XTLS/Xray-core).
