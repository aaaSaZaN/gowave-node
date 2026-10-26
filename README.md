# gowave-node

[![Release](https://github.com/aaaSaZaN/gowave-node/actions/workflows/release.yml/badge.svg)](https://github.com/aaaSaZaN/gowave-node/actions/workflows/release.yml)
[![GitHub release](https://img.shields.io/github/v/release/aaaSaZaN/gowave-node?include_prereleases&style=flat-square)](https://github.com/aaaSaZaN/gowave-node/releases)
[![License: AGPL-3.0](https://img.shields.io/badge/License-AGPL_3.0-blue.svg?style=flat-square)](LICENSE)

The Go node control plane for [GoWave](https://github.com/aaaSaZaN/gowave).
Xray runs as a separate process. The panel API preserves the v3.4.2 node protocol.

The runtime needs no Node.js, Python, CGO, or LMDB library.

**Documentation: [aaasazan.github.io/gowave-docs](https://aaasazan.github.io/gowave-docs/en/install/node/install/)** · [Русский](https://aaasazan.github.io/gowave-docs/install/node/install/)

## Install, update, or migrate

One command installs a fresh node, updates a native node, or migrates a Docker
node container:

```sh
sudo sh -c "$(curl -fsSL --connect-timeout 15 --max-time 60 https://raw.githubusercontent.com/aaaSaZaN/gowave-node/main/install.sh)"
```

A fresh install asks for the `SECRET_KEY` from the node settings in the panel.
`NODE_PORT` defaults to `3000`; export it to select another port. The installer
returns once the management daemon responds (up to 30 seconds), so the panel
can be configured afterwards. Updates and migrations wait up to two minutes
for Xray readiness and restore the previous service on failure. The script is
POSIX sh and supports systemd, OpenRC, OpenWrt procd, and Entware init.d
(Keenetic and other opkg systems) on amd64, 386, arm64, ARMv7/v6/v5, RISC-V,
and MIPS.

On Entware the installer writes `/opt/etc/init.d/S99gowave-node`, which
`rc.unslung` starts at boot and which restarts the node 3 seconds after it
exits. Files stay under `/opt` (commands in `/opt/bin`) and runtime state lives
in `/tmp/gowave-node`. Node output does not go to the system log; read it with
`gowave-node logs`. Panel-triggered updates need systemd, so update with
`gowave-node update`. Open `NODE_PORT` and the Xray inbound ports in the
router firewall for the WAN interface.

The installer automatically migrates previous native installations to the
`gowave-node` service. It retains their installation directory so certificate
paths and custom cores keep working. It
preserves the secret and port and translates saved environment options. After a
successful switch it removes the old binaries and CLI aliases; use `gowave-node`.
The previous service and its update watchers
are disabled only after the release is verified and provisioned; a failed switch
restores their prior state and the original `.env`.

Previous `REMNANODE_` installer options remain accepted; explicit `GOWAVE_NODE_`
options take precedence. For a custom previous installation directory, set
`GOWAVE_NODE_LEGACY_INSTALL_DIR` (systemd also discovers its working directory).
Do not change the installation directory during this migration. Fresh installs
use `/opt/gowave-node` and the `gowave-node` account. Custom native unit overrides
and s6 supervisor definitions should be reviewed separately when changing names.

Publish `gowave-node-linux-<architecture>` assets in `aaaSaZaN/gowave-node`
before using this installer or the updater.

| Topic | Page |
| :--- | :--- |
| Installation, unattended runs, canary versions | [Install](https://aaasazan.github.io/gowave-docs/en/install/node/install/) |
| Environment variables, firewall, custom core | [Configuration](https://aaasazan.github.io/gowave-docs/en/install/node/configuration/) |
| `gowave-node` commands, node and Xray updates | [CLI](https://aaasazan.github.io/gowave-docs/en/install/node/cli/) |
| Moving from Docker | [Migration](https://aaasazan.github.io/gowave-docs/en/migration/docker-node/) |

## Build and test

Use the Go version declared in `go.mod` (currently 1.27.1).

```sh
CGO_ENABLED=0 go build -trimpath -o gowave-node ./cmd/gowave-node
go test -race ./...
go vet ./...
python3 tests/test_scripts.py
```

To exercise a real core, download and verify it into a temporary directory:

```sh
python3 scripts/fetch-test-core.py v26.7.28 /tmp/gowave-node-core
GOWAVE_NODE_TEST_CORE=/tmp/gowave-node-core/rw-core go test -race ./internal/xray -run RealCore -v
```

CI runs baseline/latest-stable core tests and isolated native/s6 HTTPS smoke tests
on amd64 and arm64. 386, ARMv7/v6/v5, RISC-V, and MIPS binaries are cross-built;
router deployment is not certified by the server smoke tests.

## Releases

To canary a published candidate, run
`sudo GOWAVE_NODE_VERSION=v1.0.6-rc.1 sh -c "$(curl -fsSL ...)"`. An unavailable explicitly
selected tag fails without switching services.

Candidate assets must include `checksums.txt`, `asn-prefixes.zip`, and the matching
`gowave-node-linux-<architecture>` binary. The Candidate artifacts workflow builds
these files without publishing a release. Publish only after the Validation
workflow and a canary migration pass.

## Configuration

Place `.env` beside the executable or in `/opt/gowave-node/.env`; explicitly set
process environment values take precedence.

| Variable | Default | Purpose |
| --- | --- | --- |
| `SECRET_KEY` | required | Panel-generated base64 certificate/JWT payload |
| `NODE_PORT` | `3000` | TLS 1.3 management port; mTLS and RS256 JWT required |
| `GOWAVE_NODE_RUNTIME_DIR` | `/run/gowave-node` | Private configuration and CLI discovery directory |
| `INTERNAL_REST_TOKEN` | random per process | Internal HTTP authentication; normally leave unset |
| `INTERNAL_SOCKET_PATH` | random abstract name | Internal configuration/webhook socket |
| `XTLS_API_SOCKET_PATH` | `/run/gowave-node/xray.sock` | Xray gRPC endpoint |
| `XRAY_CORE_PATH` | detected adjacent/system core | Explicit core executable |
| `S6_COMMAND_DIR` | `/command` or PATH | Existing s6 command directory |
| `XRAY_S6_SERVICE_DIR` | `/run/service/xray` | Existing supervised Xray service |
| `XRAY_LOCATION_ASSET` | detected core asset directory | Geodata directory |
| `ASN_INDEX_PATH` | `/usr/local/share/asn/asn-prefixes.zip` | Portable ASN index |
| `SNI_VERIFICATION` | `false` | Require the panel-derived SNI, including on otherwise valid mTLS connections |
| `NFTABLES_LOGGING` | `true` | Kernel firewall logging |
| `NFTABLES_ACCEPT_REPLY_TRAFFIC` | `false` | Exempt connection reply traffic from ingress blocks |
| `DISABLE_HASHED_SET_CHECK` | `false` | Force configuration synchronization to restart the core |

The runtime directory uses `0700`; configuration and CLI discovery files use
`0600`. Existing explicit abstract Unix names remain supported. Explicit TCP
management endpoints must use a loopback IP. Runtime configuration is never
copied to `/etc/xray/config.json`.

The installed service runs as root, like the Docker node: Xray must be able to
use whatever the panel configuration references, such as Unix sockets shared
with nginx under `/dev/shm`, certificates of other services, and low ports.
Administrators who want isolation can add their own systemd override.

For a manual native systemd service:

```ini
[Unit]
Description=gowave-node
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
WorkingDirectory=/opt/gowave-node
ExecStart=/opt/gowave-node/gowave-node
RuntimeDirectory=gowave-node
RuntimeDirectoryMode=0700
UMask=0077
Restart=always
RestartSec=3
LimitNOFILE=1048576

[Install]
WantedBy=multi-user.target
```

```sh
sudo systemctl enable gowave-node
sudo systemctl restart gowave-node
```

Firewall enforcement requires `nft`, `ss` from iproute2, and CAP_NET_ADMIN.
Unavailable or failed enforcement is reported to the panel. Geocheck requires
`/usr/local/bin/geocheck`; unsupported/missing capabilities return errors.

## CLI and updates

```sh
gowave-node status
gowave-node status --wait-running
gowave-node status --wait-ready
gowave-node logs -n 100 -f
gowave-node xlogs -n 100 -f
gowave-node restart
gowave-node update -y
gowave-node core install -y
gowave-node core install --version v26.7.28 -y
gowave-node core reset
```

CLI logs and status use the private discovery file, so run them as root. Add PATH symlinks
explicitly or use those installed by `install.sh`;
executing the binary does not overwrite system binaries or links.

Node updates default to `aaaSaZaN/gowave-node`. Core installs default to the
latest stable `XTLS/Xray-core` release. Explicit version requests never fall back
to another version. Downloaded executables require a published SHA-256 digest
or checksum manifest and are validated before replacement.

Updates retain the preceding executable. Native HTTP updates start a transient
`systemd-run` unit that monitors restart/readiness and restores the preceding
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
and Xray separately; there is no fixed 10 to 15 MB guarantee. See `docs/validation.md`
for reproducible checks and release limitations.

## ASN asset generation

`scripts/build-asn.sh output.zip` downloads the upstream LMDB index, exports it
with the build-only `lmdb@3.5.6` module, and creates a deterministic portable ZIP.
The runtime loads individual ASN entries on demand and caches at most 64 entries.
Build requirements are Node.js/npm, Python 3, curl, and zstd. They are not needed
on an installed node.

Xray is provided by [Xray-core](https://github.com/XTLS/Xray-core).
