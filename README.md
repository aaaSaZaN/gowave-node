# Remnanode (Go)

[![Release](https://github.com/aaaSaZaN/remnanode-go/actions/workflows/release.yml/badge.svg)](https://github.com/aaaSaZaN/remnanode-go/actions/workflows/release.yml)
[![GitHub release](https://img.shields.io/github/v/release/aaaSaZaN/remnanode-go?include_prereleases&style=flat-square)](https://github.com/aaaSaZaN/remnanode-go/releases)
[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL_3.0-blue.svg?style=flat-square)](LICENSE)

A Go implementation of the Remnawave node control plane. Xray runs as a separate
process. The compatibility baseline is Remnawave Node v3.4.2, with native Linux
systemd deployment as the primary target and the original s6 Xray execution path
retained.

The runtime needs no Node.js, Python, CGO, or LMDB library. Native installation
and release-asset conversion use separate build/installation tools.

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
on amd64 and arm64. ARMv7 and MIPS binaries are cross-built; router deployment is
not certified by the server smoke tests.

## Install, update, or migrate

One command installs a fresh node, updates a native node, or migrates a Docker
`remnawave/node` container:

```sh
curl -fsSL https://raw.githubusercontent.com/aaaSaZaN/remnanode-go/main/install.sh | sudo sh
```

Without curl, use `wget -qO- <same URL> | sudo sh`. Releases also publish the
script as the `install.sh` asset, listed in `checksums.txt`.

`install.sh` is POSIX sh (bash, dash, and BusyBox ash). It needs curl or wget and
sha256sum, shasum, or openssl; no Python or bash is required. It detects:

- the architecture: amd64, 386, arm64, armv7, mips, mipsle, mips64, or mips64le.
  MIPS byte order and 32-bit userlands come from the ELF header of `/bin/sh`;
- the service manager: systemd, OpenRC (Alpine), or OpenWrt procd. Override it
  with `REMNANODE_INIT=systemd|openrc|procd`;
- a Docker container running `remnawave/node`. Its environment becomes the native
  `.env`.

A fresh install asks for `SECRET_KEY` and `NODE_PORT` (default `3000`) on the
terminal, even through `curl | sh`. For unattended runs, pass them:
`curl -fsSL ... | sudo SECRET_KEY=... NODE_PORT=2222 sh`. An existing `.env` or
container keeps its values.

The installer verifies published checksums for the node, the portable ASN index
(when the release has one), and geocheck (amd64/arm64). `remnanode core install`
then installs the latest stable Xray and verifies its release checksum. All
downloads and checks happen before services change.

For a migration, the installer checks that TLS files referenced by the running
configuration exist on the host before it stops Docker. Referenced container
paths must exist at the same locations on the host. It waits up to 120 seconds
for the panel to send configuration and Xray to become ready. Failure restores
the previous native service or Docker container, including its restart policy.
The old container is retained. Migration interrupts existing connections;
downtime depends on panel synchronization and is measured rather than assumed. A
fresh install that the panel has not configured yet stays installed and reports
that it is waiting for the panel.

To canary a published candidate, run
`curl -fsSL ... | sudo REMNANODE_VERSION=v1.0.6-rc.1 sh`. An unavailable explicitly
selected tag fails without switching services.

Candidate assets must include `checksums.txt`, `asn-prefixes.zip`, and the matching
`remnanode-linux-<architecture>` binary. The Candidate artifacts workflow builds
these files without publishing a release. Publish only after the Validation
workflow and a canary migration pass.

## Configuration

Place `.env` beside the executable or in `/opt/remnanode/.env`; explicitly set
process environment values take precedence.

| Variable | Default | Purpose |
| --- | --- | --- |
| `SECRET_KEY` | required | Panel-generated base64 certificate/JWT payload |
| `NODE_PORT` | `3000` | TLS 1.3 management port; mTLS and RS256 JWT required |
| `REMNANODE_RUNTIME_DIR` | `/run/remnanode` | Private configuration and CLI discovery directory |
| `INTERNAL_REST_TOKEN` | random per process | Internal HTTP authentication; normally leave unset |
| `INTERNAL_SOCKET_PATH` | random abstract name | Internal configuration/webhook socket |
| `XTLS_API_SOCKET_PATH` | `/run/remnanode/xray.sock` | Xray gRPC endpoint |
| `XRAY_CORE_PATH` | detected adjacent/system core | Explicit core executable |
| `S6_COMMAND_DIR` | `/command` or PATH | Existing s6 command directory |
| `XRAY_S6_SERVICE_DIR` | `/run/service/xray` | Existing supervised Xray service |
| `XRAY_LOCATION_ASSET` | detected core asset directory | Geodata directory |
| `ASN_INDEX_PATH` | `/usr/local/share/asn/asn-prefixes.zip` | Portable ASN index |
| `SNI_VERIFICATION` | `false` | Require the panel-derived SNI, including on otherwise valid mTLS connections |
| `NFTABLES_LOGGING` | `true` | Kernel firewall logging |
| `NFTABLES_ACCEPT_REPLY_TRAFFIC` | `false` | Exempt connection reply traffic from ingress blocks |
| `DISABLE_HASHED_SET_CHECK` | `false` | Force configuration synchronization to restart the core |

Use a `0700` runtime directory owned by the service user. Configuration and CLI
discovery files use `0600`. Existing explicit abstract Unix names remain
supported. Explicit TCP management endpoints must use a loopback IP. Runtime
configuration is never copied to `/etc/xray/config.json`.

For a manual native systemd service:

```ini
[Unit]
Description=Remnanode (Go)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory=/opt/remnanode
ExecStart=/opt/remnanode/remnanode
RuntimeDirectory=remnanode
RuntimeDirectoryMode=0700
UMask=0077
Restart=on-failure
RestartSec=3
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
```

Firewall enforcement requires `nft`, `ss` from iproute2, and CAP_NET_ADMIN.
Unavailable or failed enforcement is reported to the panel. Geocheck requires
`/usr/local/bin/geocheck`; unsupported/missing capabilities return errors.

## CLI and updates

```sh
remnanode status
remnanode status --wait-ready
remnanode logs -n 100 -f
remnanode xlogs -n 100 -f
remnanode restart
remnanode update -y
remnanode core install -y
remnanode core install --version v26.7.28 -y
remnanode core reset
```

CLI logs and status use the private discovery file, so run them as the service
user. Add PATH symlinks explicitly or use those installed by `install.sh`;
executing the binary does not overwrite system binaries or links.

Node updates default to `aaaSaZaN/remnanode-go`. Core installs default to the
latest stable `XTLS/Xray-core` release. Explicit version requests never fall back
to another version. Downloaded executables require a published SHA-256 digest
or checksum manifest and are validated before replacement.

Updates retain the preceding executable. Native HTTP updates use a separate
systemd transient unit to monitor restart/readiness and restore the preceding
executable on failure. CLI updates wait synchronously for restart. HTTP updates
on an s6-only host require an external restart supervisor; without one, the
endpoint rejects the update and restores the executable. Existing s6 Xray
start/stop/configuration functionality is independent of that restriction. The
recognized stock s6 run script is temporarily adapted to read the private JSON
file, supporting cores without the Unix-HTTP loader; its original is restored on
stop. Custom s6 scripts are preserved and should read that private file when
using a core without Unix-HTTP configuration support.

Custom core configuration uses the original `xrayConfig.geodata.core` URL and
SHA-256 fields. `geodata.assets` prepares named external data files. A custom
core is staged and configuration-validated before activation. Removing custom
core configuration or running `core reset` restores the managed stock core.

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
and Xray separately; there is no fixed 10–15 MB guarantee. See `docs/validation.md`
for reproducible checks and release limitations.

## ASN asset generation

`scripts/build-asn.sh output.zip` downloads the upstream LMDB index, exports it
with the build-only `lmdb@3.5.6` module, and creates a deterministic portable ZIP.
The runtime loads individual ASN entries on demand and caches at most 64 entries.
Build requirements are Node.js/npm, Python 3, curl, and zstd. They are not needed
on an installed node.

The original projects are [Remnawave Node](https://github.com/remnawave/node) and
[Xray-core](https://github.com/XTLS/Xray-core).
