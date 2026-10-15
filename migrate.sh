#!/usr/bin/env bash
# Install/migrate a native node. Never stops Docker before all preflight checks pass.
set -euo pipefail
umask 077

log() { printf '[remnanode] %s\n' "$*"; }
fail() { log "$*" >&2; exit 1; }
[[ $(id -u) == 0 ]] || fail 'Run this installer as root.'
for command in systemctl curl sha256sum python3 tar; do
    command -v "$command" >/dev/null || fail "Required command missing: $command"
done
case $(uname -m) in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    armv7*|armhf) arch=armv7 ;;
    mipsle|mips|mips64le|mips64) arch=$(uname -m) ;;
    *) fail 'Unsupported CPU architecture.' ;;
esac
install_dir=${REMNANODE_INSTALL_DIR:-/opt/remnanode}
prefix=${REMNANODE_PREFIX:-/usr/local}
unit_dir=${REMNANODE_SYSTEMD_DIR:-/etc/systemd/system}
work=$(mktemp -d)
container=''
was_running=false
switched=false
modified=false
complete=false
native_running=false
previous_enabled=disabled
restart_policy=no
rollback() {
    code=$?
    trap - EXIT INT TERM
    if [[ $complete != true && $modified == true ]]; then
        log 'Readiness failed; restoring previous service.'
        if [[ $switched == true ]]; then systemctl stop remnanode || true; fi
        if [[ -f "$work/previous-node" ]]; then
            install -m 0755 "$work/previous-node" "$install_dir/remnanode.rollback"
            mv -f "$install_dir/remnanode.rollback" "$install_dir/remnanode"
        else
            rm -f -- "$install_dir/remnanode"
        fi
        if [[ -f "$work/previous-env" ]]; then cp "$work/previous-env" "$install_dir/.env"; else rm -f -- "$install_dir/.env"; fi
        if [[ -f "$work/previous-unit" ]]; then cp "$work/previous-unit" "$unit_dir/remnanode.service"; else rm -f -- "$unit_dir/remnanode.service"; fi
        systemctl daemon-reload || true
        if [[ $previous_enabled != enabled ]]; then systemctl disable remnanode || true; fi
        if [[ $switched == true && $was_running == true ]]; then
            docker update --restart="$restart_policy" "$container" || true
            docker start "$container" || true
        elif [[ $switched == true && $native_running == true ]]; then
            systemctl restart remnanode || true
        fi
        code=1
    fi
    rm -rf -- "$work"
    exit "$code"
}
trap rollback EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

if command -v docker >/dev/null && docker info >/dev/null 2>&1; then
    container=$(docker ps -a --format '{{.Names}}\t{{.Image}}' | awk '$2 ~ /(^|\/)remnawave\/node(:|@|$)|remnanode/ {print $1; exit}')
fi
mkdir -p "$install_dir" "$prefix/bin" "$unit_dir"
if [[ -f "$unit_dir/remnanode.service" ]]; then cp "$unit_dir/remnanode.service" "$work/previous-unit"; fi
previous_enabled=$(systemctl is-enabled remnanode 2>/dev/null || true)
if systemctl is-active --quiet remnanode; then native_running=true; fi
if [[ -f "$install_dir/.env" ]]; then cp "$install_dir/.env" "$work/previous-env"; fi
if [[ -x "$install_dir/remnanode" ]]; then cp "$install_dir/remnanode" "$work/previous-node"; fi
if [[ -n $container ]]; then
    docker inspect "$container" > "$work/container.json"
    python3 - "$work/container.json" "$work/env" <<'PY'
import json, sys
config = json.load(open(sys.argv[1]))[0]
with open(sys.argv[2], 'w') as output:
    for entry in config['Config'].get('Env', []):
        key, _, value = entry.partition('=')
        if key in {'PATH', 'HOSTNAME', 'HOME', 'NODE_OPTIONS', 'NODE_ENV', 'INTERNAL_REST_TOKEN', 'INTERNAL_SOCKET_PATH', 'XTLS_API_SOCKET_PATH'}:
            continue
        if '\n' in value or '\r' in value or not key.replace('_', '').isalnum():
            raise ValueError('Unsupported environment value')
        output.write(key + '=' + value + '\n')
PY
    was_running=$(docker inspect -f '{{.State.Running}}' "$container")
    restart_policy=$(python3 - "$work/container.json" <<'PY'
import json, sys
p = json.load(open(sys.argv[1]))[0]['HostConfig']['RestartPolicy']
name = p['Name'] or 'no'
if name == 'on-failure' and p.get('MaximumRetryCount', 0):
    name += ':' + str(p['MaximumRetryCount'])
print(name)
PY
)
    if [[ $was_running == true ]]; then
        # Read only file references from the current configuration; no credentials
        # are emitted or reused as a potentially stale user snapshot.
        docker exec "$container" node -e '
const fs=require("fs"),http=require("http");
const env=n=>fs.readFileSync("/run/s6/container_environment/"+n,"utf8").trim();
const req=http.get({socketPath:"\0"+env("INTERNAL_SOCKET_PATH"),path:"/internal/get-config?token="+encodeURIComponent(env("INTERNAL_REST_TOKEN"))},res=>{
 if(res.statusCode!==200)process.exit(1);let data="";res.on("data",b=>data+=b);res.on("end",()=>{
 const files=new Set();function walk(o){if(!o||typeof o!=="object")return;for(const[k,v]of Object.entries(o)){if(["certificateFile","keyFile","dhFile"].includes(k)&&typeof v==="string"&&v.startsWith("/"))files.add(v);else walk(v);}}walk(JSON.parse(data));for(const p of files)console.log(p);
 });});req.setTimeout(10000,()=>{req.destroy();process.exit(1)});req.on("error",()=>process.exit(1));
' > "$work/references"
        while IFS= read -r referenced; do
            [[ -r $referenced ]] || fail "Required file is unavailable on the host: $referenced. Copy/mount it before migrating. Docker has not been stopped."
        done < "$work/references"
    fi
elif [[ -f "$work/previous-env" ]]; then
    cp "$work/previous-env" "$work/env"
else
    : > "$work/env"
fi
# Preserve inherited settings without sourcing an environment file as shell code.
python3 - "$work/env" <<'PY'
import os, sys
path = sys.argv[1]
lines = open(path).read().splitlines()
values = dict(line.split('=', 1) for line in lines if '=' in line and not line.lstrip().startswith('#'))
for key in ('SECRET_KEY', 'NODE_PORT'):
    if not values.get(key) and os.environ.get(key): values[key] = os.environ[key]
if not values.get('SECRET_KEY'): raise SystemExit('SECRET_KEY is required; export it or configure the existing node.')
values.setdefault('NODE_PORT', '3000')
with open(path, 'w') as output:
    for key, value in values.items(): output.write(key + '=' + value + '\n')
PY
repo=aaaSaZaN/remnanode-go
version=${REMNANODE_VERSION:-latest}
case "$version" in *[!a-zA-Z0-9.+-]*|'') fail "Invalid release version." ;; esac
base=https://github.com/$repo/releases/latest/download
if [[ $version != latest ]]; then base=https://github.com/$repo/releases/download/$version; fi
asset=remnanode-linux-$arch
curl -fsSL "$base/checksums.txt" -o "$work/checksums.txt"
curl -fsSL "$base/$asset" -o "$work/$asset"
(cd "$work" && awk -v file="$asset" '$2 == file {print}' checksums.txt | sha256sum --check --strict -)
chmod 0755 "$work/$asset"
"$work/$asset" version | awk '/^Remnawave Node \(Go\) v/ {valid=1} END {exit !valid}'

# Provision optional assets before changing traffic-serving services.
curl -fsSL "$base/asn-prefixes.zip" -o "$work/asn-prefixes.zip"
(cd "$work" && awk '$2 == "asn-prefixes.zip" {print}' checksums.txt | sha256sum --check --strict -)
mkdir -p "$prefix/share/asn"
install -m 0644 "$work/asn-prefixes.zip" "$prefix/share/asn/asn-prefixes.zip"
if [[ $arch == amd64 || $arch == arm64 ]]; then
    geo_asset=geocheck_linux_${arch}.tar.gz
    geo_base=https://github.com/remnawave/geocheck/releases/download/v0.3.0
    curl -fsSL "$geo_base/checksums.txt" -o "$work/geo-checksums.txt"
    curl -fsSL "$geo_base/$geo_asset" -o "$work/$geo_asset"
    (cd "$work" && awk -v file="$geo_asset" '$2 == file {print}' geo-checksums.txt | sha256sum --check --strict -)
    tar -xzf "$work/$geo_asset" -C "$work" geocheck
    install -m 0755 "$work/geocheck" "$prefix/bin/geocheck"
fi
modified=true
install -m 0600 "$work/env" "$install_dir/.env"
install -m 0755 "$work/$asset" "$install_dir/remnanode.new"
mv -f "$install_dir/remnanode.new" "$install_dir/remnanode"
# The CLI selects latest stable Xray and verifies the release checksum.
if [[ ! -x "$install_dir/rw-core" && ! -x "$prefix/bin/rw-core" ]]; then
    "$install_dir/remnanode" core install -y
fi
cat > "$work/remnanode.service" <<UNIT
[Unit]
Description=Remnanode (Go)
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
User=root
WorkingDirectory=$install_dir
ExecStart=$install_dir/remnanode
RuntimeDirectory=remnanode
RuntimeDirectoryMode=0700
UMask=0077
Restart=on-failure
RestartSec=3
LimitNOFILE=1048576
[Install]
WantedBy=multi-user.target
UNIT
install -m 0644 "$work/remnanode.service" "$unit_dir/remnanode.service"
systemctl daemon-reload
systemctl enable remnanode
started=$(date +%s)
switched=true
if [[ $was_running == true ]]; then
    docker update --restart=no "$container"
    docker stop -t 10 "$container"
fi
systemctl restart remnanode
"$install_dir/remnanode" status --wait-ready
complete=true
log "Migration is ready; measured switch-to-Xray-readiness time: $(( $(date +%s) - started )) seconds."
for name in remnanode remnanode-go xlogs; do
    destination=$prefix/bin/$name
    if [[ ! -e $destination || -L $destination ]]; then ln -sfn "$install_dir/remnanode" "$destination"; fi
done
log 'The previous container is retained for manual recovery.'
