# Repository context

This is Remnanode, a Go implementation of the Remnawave node control plane with
Remnawave Node v3.4.2 as its compatibility baseline. Xray runs as a separate
executable, not as an embedded Go server. Native Linux/systemd is the primary
deployment; existing s6 installations are also supported. The Go module path is
`github.com/remnawave/node-go`, while published node assets come from
`aaaSaZaN/remnanode-go`.

No pre-existing agent, Cursor, Claude, or Copilot rule files were found during
this analysis. See `README.md` for the environment variable table and operator
instructions. Its link to `docs/validation.md` currently points to a missing file;
use the actual CI and tests for validation details.

## Commands and validation

Run from the repository root with the Go version in `go.mod` (currently 1.27.1).

```sh
CGO_ENABLED=0 go build -trimpath -o remnanode ./cmd/remnanode
go test -race ./...
go vet ./...
shellcheck install.sh scripts/*.sh
python3 tests/test_scripts.py
```

`CGO_ENABLED=0` is for the portable production build; leave it off the race-test
command. There is no Makefile or separate Go lint configuration. CI's checks are
defined in `.github/workflows/ci.yml`.

Real-core integration is opt-in; the regular suite skips it without
`REMNANODE_TEST_CORE`. The fetch helper verifies a release digest and supports
the host architectures x86_64 and aarch64:

```sh
python3 scripts/fetch-test-core.py v26.7.28 /tmp/remnanode-core
REMNANODE_TEST_CORE=/tmp/remnanode-core/rw-core go test -race ./internal/xray -run RealCore -v
```

For HTTPS smoke tests, reproduce CI's isolated native and s6 runs:

```sh
python3 scripts/fetch-test-core.py v26.7.28 .smoke
CGO_ENABLED=0 go build -o .smoke/remnanode ./cmd/remnanode
docker build -f tests/smoke/Dockerfile -t remnanode-smoke .
docker run --rm --cap-drop=ALL remnanode-smoke native
docker run --rm --cap-drop=ALL remnanode-smoke s6
```

CI runs both the baseline core above and `latest` on amd64 and arm64. The Docker
image copies `.smoke/`, so fetching the core alone does not rebuild the node
candidate. `.smoke/`, `dist/`, and root build outputs are ignored artifacts.

## Architecture and execution flow

- `cmd/remnanode/main.go` dispatches CLI commands, wires the services, defines
  `newRouter`, and handles shutdown. No command starts the daemon with Xray
  already configured: the panel supplies configuration through `/node/xray/start`.
- `internal/config`, `auth`, and `tlsutil` decode and verify the panel's
  `SECRET_KEY` payload, derive SNI, and enforce TLS 1.3 client certificates and
  RS256 JWT authentication. `/node` uses JWT middleware; `/internal` uses its
  separate query-token authentication.
- `internal/xray/controller.go` orchestrates configuration synchronization:
  synchronize integrations, compare hashes and plugin revision, prepare core and
  assets, generate and validate config, stop the old core, run plugin pre-start,
  clean inbound sockets, seed state, start, and poll gRPC readiness. Activation
  is committed after readiness; failure paths invalidate state/roll back core
  activation. Preserve this ordering when changing lifecycle behavior.
- `internal/xray/process.go` owns native subprocesses or s6 control, private JSON
  configuration, core discovery, and custom-core activation. s6 detection checks
  the service's `supervise/control` path. `s6.go` adapts the recognized stock run
  script and restores it on stop; custom scripts are preserved.
- `internal/xray/client.go` wraps gRPC user and statistics operations.
  `state.go` tracks the configuration snapshot, inbound tags, and user hashes
  used to decide whether a panel synchronization needs a restart.
- `internal/handler` applies user mutations through gRPC and reconciles the local
  hash sets. `internal/stats` translates core RPCs and host/network measurements
  into panel responses; geocheck delegates to an external executable.
- `internal/plugin` owns nftables filters, ASN resolution, connection dropping,
  torrent reports, and pre/post-start hooks. `internal/internalapi` serves config,
  webhook, logs, and readiness over a private endpoint; `internal/cli` discovers
  this endpoint through the runtime discovery file.
- `internal/updater` handles release selection, checksums, archive extraction,
  executable validation, atomic replacement, backup/rollback, and restart
  scheduling. CLI and HTTP update entry points both depend on it.
- `internal/integration` is an intentionally empty provider registry in the
  default wiring, matching the reference version; it is not a missing set of
  implementations.

## Compatibility and concurrency conventions

- Preserve route methods, JSON field spelling, response envelopes, null/empty
  distinctions, and status codes. Several operations report failure inside HTTP
  200 responses. In particular, user mutation errors use
  `response: {success: false, error: ...}` rather than a generic HTTP error.
  Consult `cmd/remnanode/router_test.go` before altering endpoint contracts and
  the service tests for deeper behavior.
- Request types typically implement `Validate() error`; `apiutil.Decode` invokes
  it after decoding and rejects trailing JSON values. `apiutil.Error` produces
  the timestamp/path/message/errorCode envelope, but it does not replace the
  endpoint-specific compatibility envelopes.
- `apiutil.Decompress` supports gzip, zlib-wrapped deflate, Brotli, and Zstandard.
  It limits both the encoded and decoded body to 1000 MiB. This unusually large
  limit is part of the documented compatibility behavior.
- User IDs remain strings, including values with leading zeroes. A username used
  for RPC removal and a UUID used for hash reconciliation are distinct values.
  Successful changes survive partial batch failures; update local hashes only
  for successful mutations (or accepted NotFound removal).
- `StateManager.Operation` is the shared lifecycle gate: start/stop, plugin sync,
  and HTTP updates take its write lock; user mutations take its read lock.
  Handler user locks are striped, deduplicated, sorted before acquisition, and
  acquired before the operation read lock. Preserve lock ordering and the
  distinction between this gate and each component's internal mutex.
- Hashes are a panel compatibility mechanism, not arbitrary checksums.
  `internal/hashedset` XORs dual signed int32 accumulators and formats signed hex;
  negative signs in the output are intentional (see `TestHashedSetParity`).
  Do not substitute unsigned formatting or a different hash algorithm.
- Config maps use decoded JSON shapes (`map[string]interface{}` and
  `[]interface{}`). `GenerateApiConfig` and state accessors clone configuration;
  do not mutate shared input maps. Generated config injects API routing, stats,
  policy, and torrent hooks. The Unix API tunnel's nominal port 1 and explicit
  Unix network support older cores.
- Use `gofmt` for changed Go sources. Handler methods follow `Handle...` naming;
  JSON tags follow the panel contract rather than Go field spelling. Existing
  logs use component prefixes such as `[XRAY]`, `[STATE]`, and `[PLUGIN]`.

## Runtime and deployment gotchas

- `.env` lookup order is working directory, executable directory,
  `/opt/remnanode/.env`, then `/etc/remnanode/.env`; values already present in the
  process environment win, and earlier files win over later ones.
- `SECRET_KEY` is a base64 JSON certificate/key payload, not a plain shared
  password. Starting the daemon also performs core/geodata discovery and can
  download missing assets. Prefer the isolated smoke harness for full startup
  testing.
- The default runtime directory is `/run/remnanode`; override it with
  `REMNANODE_RUNTIME_DIR` in isolated environments. Private directories must be
  owned by the current user and cannot be symlinks. Config/discovery writes are
  staged privately and renamed. Runtime config is `xray.json` there, not
  `/etc/xray/config.json`.
- `connection.json` holds the internal endpoint, token, and PID. CLI logs/status
  need the same runtime directory and service user. The internal HTTP socket and
  Xray gRPC socket are separate endpoints. Endpoint parsing supports absolute
  Unix paths, abstract names with or without `@`, and explicit loopback-IP TCP;
  hostnames/non-loopback TCP endpoints are rejected.
- Readiness requires configuration, a running core, and a successful gRPC stats
  call. A listening management server or cached health response alone is not
  equivalent to readiness.
- Firewall enforcement needs `nft`, iproute2 `ss`, and CAP_NET_ADMIN; geocheck
  needs `/usr/local/bin/geocheck`. Missing capabilities are error cases. Smoke
  containers drop all capabilities and do not certify real firewall enforcement.
- `install.sh` is the single root installer/updater/migrator for systemd, OpenRC,
  and OpenWrt procd. It is POSIX sh and must keep working under BusyBox ash and
  dash with only curl/wget and a SHA-256 tool. It can stop Docker and change
  host services. Its tests mock host commands and redirect install paths into
  temporary directories; use `python3 tests/test_scripts.py` for installer changes.
- Explicit release versions must never silently fall back to another tag.
  Preserve checksum verification, executable validation, target locking, backups,
  and readiness rollback. Native HTTP updates schedule a separate systemd
  transient monitor; s6-only HTTP updates require an external restart supervisor.
  Custom-core removal/reset restores the managed stock core.

## Tests and generated assets

Go tests live beside source, using standard `testing`, `httptest`, temporary
directories, and environment overrides. `internal/testutil.XrayServer` provides
a real Unix-socket gRPC transport with deterministic RPC callbacks; reuse it for
protocol and error-path coverage. Handler tests also use the small `XrayClient`
interface. The router contract test uses a real signed JWT and checks the original
controller routes. The Python suite covers shell syntax, portable ASN packing,
and mocked installer success/rollback. HTTPS smoke tests generate their own
certificates/JWT and exercise compressed configuration, readiness, user changes,
stats, and shutdown against a real core.

`internal/xrayapi` contains generated stats and Hysteria bindings copied from
Xray v26.7.28. Regenerate with:

```sh
bash scripts/generate-proto.sh
```

This requires protoc, Go, curl, and Python 3 and fetches pinned upstream schemas.
Edit generation inputs/scripts rather than generated `.pb.go` files or
`internal/xrayapi/client.go`. Preserve protobuf package names and field numbers.
The older `github.com/xtls/xray-core` dependency supplies HandlerService/account
types; the local stats bindings replace its old stats package. Importing both
stats packages can register conflicting protobuf names. Details are in
`internal/xrayapi/README.md`.

`bash scripts/build-asn.sh output.zip` needs Node/npm, Python 3, curl, and zstd.
It downloads LMDB, exports with build-only `lmdb@3.5.6`, then packs a deterministic
ZIP of `<asn>.json` entries. Runtime uses the portable ZIP and caches at most 64
ASN entries; Node, Python, CGO, and LMDB are not runtime requirements.

## Release workflow discrepancies

Check the workflow relevant to a release instead of assuming the two builders
produce identical assets:

- `artifacts.yml` produces candidate binaries, `asn-prefixes.zip`, and
  `checksums.txt`; it embeds the input version through `main.AppVersion`.
- `release.yml` also builds the ASN ZIP and publishes `install.sh`,
  `checksums.txt`, and the legacy `sha256sums.txt` copy. `install.sh` accepts
  either manifest and treats a missing ASN ZIP as optional.
- Both workflows set `GOMIPS=softfloat` for mips/mipsle, build `386`, and label
  GOARCH=arm builds as `armv7` with GOARM=7. CI server smoke tests cover
  amd64/arm64, not router deployment.
