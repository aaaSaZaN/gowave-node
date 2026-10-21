#!/bin/sh
# Install, update, or migrate a native gowave-node node.
#
#   sudo sh -c "$(curl -fsSL --connect-timeout 15 --max-time 60 https://raw.githubusercontent.com/aaaSaZaN/gowave-node/main/install.sh)"
#
# The same command installs a fresh node, updates an existing native node, or
# migrates a Docker node container, keeping its environment. Docker is
# never stopped before every preflight check passes, and any failure restores
# the previous service. Optional settings: SECRET_KEY, NODE_PORT (used only when
# not already configured), GOWAVE_NODE_VERSION (release tag, default latest), and
# GOWAVE_NODE_INIT (systemd, openrc, or procd; detected by default).
set -eu
umask 077

log() { printf '[gowave-node] %s\n' "$*"; }
fail() { log "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

[ "$(id -u)" = 0 ] || fail 'Run this installer as root.'
[ "$(uname -s)" = Linux ] || fail 'Only Linux is supported.'
for command in awk mktemp od dd; do
    have "$command" || fail "Required command missing: $command"
done
if have curl; then
    fetch() { curl -fsSL --connect-timeout 15 --max-time 180 --retry 2 --retry-max-time 180 -o "$2" "$1" </dev/null; }
elif have wget; then
    # BusyBox wget supports -T but not GNU wget's retry options.
    fetch() { wget -q -T 30 -O "$2" "$1" </dev/null; }
else
    fail 'Required command missing: curl or wget'
fi
if have sha256sum; then
    sha256() { sha256sum "$1" | awk '{print $1}'; }
elif have shasum; then
    sha256() { shasum -a 256 "$1" | awk '{print $1}'; }
elif have openssl; then
    sha256() { openssl dgst -sha256 "$1" | awk '{print $NF}'; }
else
    fail 'Required command missing: sha256sum, shasum, or openssl'
fi

# Byte N of the running shell's ELF header: 4 is the word size (2 = 64-bit),
# 5 is the byte order (1 = little endian). uname alone cannot tell MIPS apart.
elf_byte() { dd if=/bin/sh bs=1 skip="$1" count=1 2>/dev/null | od -b | awk 'NR == 1 && NF > 1 {print $2 + 0}'; }
machine=$(uname -m)
case $machine in
    x86_64|amd64) arch=amd64 ;;
    i386|i486|i586|i686|x86) arch=386 ;;
    aarch64|arm64) arch=arm64 ;;
    armv7*|armv8l|armhf) arch=armv7 ;;
    armv6*) arch=armv6 ;;
    armv5*) arch=armv5 ;;
    riscv64) arch=riscv64 ;;
    mips*)
        arch=mips
        # A 64-bit kernel with a 32-bit userland keeps the 32-bit build.
        case $machine in mips64*) if [ "$(elf_byte 4)" = 2 ]; then arch=mips64; fi ;; esac
        case $(elf_byte 5) in
            1) arch=${arch}le ;;
            2) ;;
            *) case $machine in *el|*le) arch=${arch}le ;; esac ;;
        esac
        ;;
    *) fail "Unsupported CPU architecture: $machine. Published builds: amd64, 386, arm64, armv7, armv6, armv5, riscv64, mips, mipsle, mips64, mips64le." ;;
esac

# Previous installer options remain valid during the rename. New options win.
init=${GOWAVE_NODE_INIT:-${REMNANODE_INIT:-}}
if [ -z "$init" ]; then
    if [ -d /run/systemd/system ] && have systemctl; then init=systemd
    elif have rc-service && have rc-update; then init=openrc
    elif [ -f /etc/rc.common ] && [ -x /sbin/procd ]; then init=procd
    else fail 'No supported service manager found (systemd, OpenRC, or OpenWrt procd).'
    fi
fi
legacy_dir=${GOWAVE_NODE_LEGACY_INSTALL_DIR:-${REMNANODE_INSTALL_DIR:-/opt/remnanode}}
install_dir=${GOWAVE_NODE_INSTALL_DIR:-}
prefix=${GOWAVE_NODE_PREFIX:-${REMNANODE_PREFIX:-/usr/local}}
case $init in
    systemd)
        service_dir=${GOWAVE_NODE_SYSTEMD_DIR:-${REMNANODE_SYSTEMD_DIR:-/etc/systemd/system}}
        service_file=$service_dir/gowave-node.service
        legacy_service_file=$service_dir/remnanode.service
        service_mode=0644
        # The manager knows custom WorkingDirectory overrides as well.
        if [ -z "${GOWAVE_NODE_LEGACY_INSTALL_DIR:-${REMNANODE_INSTALL_DIR:-}}" ] && [ -f "$legacy_service_file" ]; then
            detected_dir=$(systemctl show -p WorkingDirectory --value remnanode 2>/dev/null || true)
            case $detected_dir in /*) legacy_dir=$detected_dir ;; esac
        fi
        if [ -z "$install_dir" ] && [ -f "$service_file" ]; then
            detected_dir=$(systemctl show -p WorkingDirectory --value gowave-node 2>/dev/null || true)
            case $detected_dir in /*) install_dir=$detected_dir ;; esac
        fi
        ;;
    openrc|procd)
        service_dir=${GOWAVE_NODE_INITD_DIR:-${REMNANODE_INITD_DIR:-/etc/init.d}}
        service_file=$service_dir/gowave-node
        legacy_service_file=$service_dir/remnanode
        service_mode=0755
        ;;
    *) fail "Unsupported GOWAVE_NODE_INIT value: $init" ;;
esac
if [ -z "$install_dir" ]; then
    if [ -x /opt/gowave-node/gowave-node ]; then install_dir=/opt/gowave-node
    elif [ -x "$legacy_dir/remnanode" ] || [ -x "$legacy_dir/gowave-node" ]; then install_dir=$legacy_dir
    else install_dir=/opt/gowave-node
    fi
fi
runtime_dir=$(printf '%s' "${GOWAVE_NODE_RUNTIME_DIR:-${REMNANODE_RUNTIME_DIR:-/run/gowave-node}}" | sed 's#//*#/#g; s#/$##')
validate_runtime() {
    case "$1" in
        /*) ;;
        *) fail 'GOWAVE_NODE_RUNTIME_DIR must be an absolute dedicated directory.' ;;
    esac
    case "$1" in
        /|/run|/var|/var/run|/var/lib|/var/log|/tmp|/opt|/etc|/usr|/usr/local|/dev|/proc|/sys|/home|/root|*/..|*/../*|*/.|*/./*|*[!a-zA-Z0-9_./-]*)
            fail 'GOWAVE_NODE_RUNTIME_DIR must be a dedicated directory with a simple path.' ;;
    esac
}
validate_runtime "$runtime_dir"
config_dir=${GOWAVE_NODE_CONFIG_DIR:-${REMNANODE_CONFIG_DIR:-/etc/gowave-node}}
capabilities_file=$config_dir/capabilities.json
if [ "$init" = procd ]; then
    have ujail || fail 'OpenWrt requires procd-ujail for unprivileged network capabilities. Install it before running the installer.'
    ujail=$(command -v ujail)
fi
repo=aaaSaZaN/gowave-node
version=${GOWAVE_NODE_VERSION:-${REMNANODE_VERSION:-latest}}
case $version in *[!a-zA-Z0-9.+-]*|'') fail 'Invalid release version.' ;; esac
log "Architecture: $arch; service manager: $init; release: $version."

service_action() {
    service_name=$1
    action=$2
    action_file=$3
    case $init:$action in
        systemd:reload) systemctl daemon-reload ;;
        systemd:enabled) [ "$(systemctl is-enabled "$service_name" 2>/dev/null)" = enabled ] ;;
        systemd:active) systemctl is-active --quiet "$service_name" ;;
        systemd:*) systemctl "$action" "$service_name" ;;
        openrc:reload|procd:reload) : ;;
        openrc:enabled) rc-update show default 2>/dev/null | awk -v name="$service_name" '$1 == name {found = 1} END {exit !found}' ;;
        openrc:active) rc-service "$service_name" status >/dev/null 2>&1 ;;
        openrc:enable) rc-update add "$service_name" default ;;
        openrc:disable) rc-update del "$service_name" default ;;
        openrc:*) rc-service "$service_name" "$action" ;;
        procd:enabled) [ -x "$action_file" ] && "$action_file" enabled ;;
        procd:active) [ -x "$action_file" ] && "$action_file" running >/dev/null 2>&1 ;;
        procd:*) "$action_file" "$action" ;;
    esac
}
svc() { service_action gowave-node "$1" "$service_file"; }
legacy_svc() { service_action remnanode "$1" "$legacy_service_file"; }
# Copy with a mode through a temporary sibling so the target is replaced atomically.
put() { cp "$2" "$3.new" && chmod "$1" "$3.new" && mv -f "$3.new" "$3"; }
# Unlike new release files, rollback files must retain their original ownership.
restore() { cp -p "$1" "$2.new" && mv -f "$2.new" "$2"; }
verify() {
    expected=$(awk -v file="$2" '$2 == file || $2 == "*" file {print $1; exit}' "$1")
    [ -n "$expected" ] || fail "No published checksum for $2."
    [ "$(sha256 "$work/$2")" = "$expected" ] || fail "Checksum mismatch for $2."
}
ask() {
    if (: </dev/tty) 2>/dev/null; then
        printf '%s' "$1" >/dev/tty
        IFS= read -r answer </dev/tty || answer=''
        printf '%s' "$answer"
    fi
}
env_get() { awk -v key="$1" 'index($0, key "=") == 1 {value = substr($0, length(key) + 2)} END {print value}' "$work/env"; }
env_set() {
    awk -v key="$1" 'index($0, key "=") != 1' "$work/env" > "$work/env.next"
    printf '%s=%s\n' "$1" "$2" >> "$work/env.next"
    mv -f "$work/env.next" "$work/env"
}
# port_busy reports a TCP listener on $1; without ss or netstat it assumes free.
port_busy() {
    if have ss; then
        ss -Hltn "sport = :$1" 2>/dev/null | grep -q .
    elif have netstat; then
        netstat -ltn 2>/dev/null | awk -v p=":$1" 'substr($4, length($4) - length(p) + 1) == p {f = 1} END {exit !f}'
    else
        return 1
    fi
}
unquote() { printf '%s' "$1" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' -e "s/^$2=//" -e 's/^"\(.*\)"$/\1/' -e "s/^'\\(.*\\)'\$/\\1/"; }

work=$(mktemp -d)
container=''
was_running=false
restart_policy=no
native_running=false
legacy_running=false
legacy_enabled=false
legacy_migration=false
legacy_switched=false
legacy_paths_enabled=''
legacy_paths_active=''
previous_enabled=false
modified=false
switched=false
complete=false
rollback() {
    code=$?
    set +e
    trap - EXIT INT TERM
    if [ "$complete" != true ] && [ "$modified" = true ]; then
        log 'Installation failed; restoring the previous service.'
        if [ "$switched" = true ]; then svc stop; fi
        if [ "$previous_enabled" != true ]; then svc disable 2>/dev/null; fi
        if [ -f "$work/previous-node" ]; then restore "$work/previous-node" "$install_dir/gowave-node"; else rm -f -- "$install_dir/gowave-node"; fi
        if [ -f "$work/previous-env" ]; then restore "$work/previous-env" "$install_dir/.env"; else rm -f -- "$install_dir/.env"; fi
        if [ -f "$work/previous-service" ]; then restore "$work/previous-service" "$service_file"; else rm -f -- "$service_file"; fi
        if [ "$init" = systemd ]; then
            systemctl disable --now gowave-node-restart.path gowave-node-update.path 2>/dev/null || true
            for policy in gowave-node.service.d/90-unprivileged.conf gowave-node-restart.path gowave-node-restart.service gowave-node-update.path gowave-node-update.service; do
                rm -f -- "$(dirname "$service_file")/$policy"
                if [ -f "$work/policy/$policy" ]; then
                    mkdir -p "$(dirname "$(dirname "$service_file")/$policy")"
                    cp -p "$work/policy/$policy" "$(dirname "$service_file")/$policy"
                fi
            done
        fi
        if [ "$init" = procd ]; then
            if [ -f "$work/previous-capabilities" ]; then restore "$work/previous-capabilities" "$capabilities_file"
            else rm -f -- "$capabilities_file"
            fi
        fi
        svc reload
        if [ "$legacy_switched" = true ]; then
            if [ "$legacy_enabled" = true ]; then legacy_svc enable; else legacy_svc disable; fi
            if [ "$legacy_running" = true ]; then legacy_svc restart; fi
            if [ "$init" = systemd ]; then
                for unit in $legacy_paths_enabled; do systemctl enable "$unit"; done
                for unit in $legacy_paths_active; do systemctl start "$unit"; done
            fi
        fi
        if [ "$init" = systemd ] && [ "$previous_paths" = true ]; then
            systemctl enable --now gowave-node-restart.path gowave-node-update.path || true
        fi
        if [ "$switched" = true ] && [ "$was_running" = true ]; then
            docker update --restart="$restart_policy" "$container"
            docker start "$container"
        elif [ "$switched" = true ] && [ "$native_running" = true ]; then
            svc restart
        fi
        code=1
    fi
    rm -rf -- "$work"
    exit "$code"
}
trap rollback EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

mkdir -p "$install_dir" "$prefix/bin" "$(dirname "$service_file")"
if [ -f "$service_file" ]; then cp -p "$service_file" "$work/previous-service"; fi
previous_paths=false
if [ "$init" = systemd ]; then
    if [ -f "$(dirname "$service_file")/gowave-node-restart.path" ] && [ -f "$(dirname "$service_file")/gowave-node-update.path" ]; then previous_paths=true; fi
    for policy in gowave-node.service.d/90-unprivileged.conf gowave-node-restart.path gowave-node-restart.service gowave-node-update.path gowave-node-update.service; do
        if [ -f "$(dirname "$service_file")/$policy" ]; then
            mkdir -p "$work/policy/$(dirname "$policy")"
            cp -p "$(dirname "$service_file")/$policy" "$work/policy/$policy"
        fi
    done
fi
if [ "$init" = procd ] && [ -f "$capabilities_file" ]; then
    cp -p "$capabilities_file" "$work/previous-capabilities"
fi
if svc enabled; then previous_enabled=true; fi
if svc active; then native_running=true; fi
if [ -f "$legacy_service_file" ]; then
    if legacy_svc active; then legacy_running=true; fi
    if legacy_svc enabled; then legacy_enabled=true; fi
    if [ "$legacy_running" = true ] || [ "$legacy_enabled" = true ] || [ ! -f "$service_file" ]; then
        legacy_migration=true
        [ "$native_running" != true ] || fail 'Both native services exist and the new service is running. Stop the unused service before migrating.'
        [ "$install_dir" = "$legacy_dir" ] || fail "Keep the existing installation directory during migration: GOWAVE_NODE_INSTALL_DIR=$legacy_dir"
        [ -x "$install_dir/remnanode" ] || fail 'The previous node executable is missing; set GOWAVE_NODE_LEGACY_INSTALL_DIR to its directory.'
        log "Migrating the previous native service in $install_dir."
        if [ "$init" = systemd ]; then
            if systemctl is-active --quiet remnanode-update.service; then
                fail 'An update monitor is running for the previous service. Wait for that update to finish before migrating.'
            fi
            for unit in remnanode-restart.path remnanode-update.path; do
                if [ "$(systemctl is-enabled "$unit" 2>/dev/null)" = enabled ]; then legacy_paths_enabled="$legacy_paths_enabled $unit"; fi
                if systemctl is-active --quiet "$unit"; then legacy_paths_active="$legacy_paths_active $unit"; fi
            done
        fi
    fi
fi
if [ -f "$install_dir/.env" ]; then cp -p "$install_dir/.env" "$work/previous-env"; fi
legacy_config_dir=${GOWAVE_NODE_LEGACY_CONFIG_DIR:-${REMNANODE_CONFIG_DIR:-/etc/remnanode}}
if [ "$legacy_migration" = true ] && [ ! -f "$install_dir/.env" ] && [ -f "$legacy_config_dir/.env" ]; then
    cp -p "$legacy_config_dir/.env" "$work/legacy-env"
fi
if [ -x "$install_dir/gowave-node" ]; then cp -p "$install_dir/gowave-node" "$work/previous-node"; fi

if have docker && docker info >/dev/null 2>&1; then
    container=$(docker ps -a --format '{{.Names}}\t{{.Image}}' | awk '$2 ~ /(^|\/)remnawave\/node(:|@|$)|(^|\/)gowave-node(:|@|$)/ {print $1; exit}')
fi
if [ "$legacy_migration" = true ] && [ -n "$container" ]; then
    fail 'Both a previous native service and a Docker node were found. Select the installation to migrate before continuing.'
fi
if [ -n "$container" ]; then
    log "Found Docker node container: $container."
    was_running=$(docker inspect -f '{{.State.Running}}' "$container")
    policy=$(docker inspect -f '{{.HostConfig.RestartPolicy.Name}} {{.HostConfig.RestartPolicy.MaximumRetryCount}}' "$container")
    restart_policy=${policy%% *}
    [ -n "$restart_policy" ] || restart_policy=no
    if [ "$restart_policy" = on-failure ] && [ "${policy##* }" != 0 ]; then restart_policy=on-failure:${policy##* }; fi
    docker inspect -f '{{range .Config.Env}}{{println .}}{{end}}' "$container" > "$work/docker-env"
    [ "$(docker inspect -f '{{len .Config.Env}}' "$container")" = "$(awk 'length($0) > 0 {n++} END {print n + 0}' "$work/docker-env")" ] \
        || fail 'The container environment has multi-line values; configure the node .env manually.'
    # docker inspect ends its output with an empty line.
    awk -F= 'length($0) == 0 {next} $1 !~ /^[A-Za-z_][A-Za-z0-9_]*$/ {exit 1}
        $1 !~ /^(PATH|HOSTNAME|HOME|NODE_OPTIONS|NODE_ENV|INTERNAL_REST_TOKEN|INTERNAL_SOCKET_PATH|XTLS_API_SOCKET_PATH)$/' \
        "$work/docker-env" > "$work/docker-env.filtered" || fail 'Unsupported container environment entry.'
    if [ "$was_running" = true ] && docker exec "$container" node --version >/dev/null 2>&1; then
        # Read only file references from the current configuration; no credentials
        # are emitted or reused as a potentially stale user snapshot.
        docker exec "$container" node -e '
const fs=require("fs"),http=require("http");
const env=n=>fs.readFileSync("/run/s6/container_environment/"+n,"utf8").trim();
const req=http.get({socketPath:"\0"+env("INTERNAL_SOCKET_PATH"),path:"/internal/get-config?token="+encodeURIComponent(env("INTERNAL_REST_TOKEN"))},res=>{
 if(res.statusCode!==200)process.exit(1);let data="";res.on("data",b=>data+=b);res.on("end",()=>{
 const files=new Set();function walk(o){if(!o||typeof o!=="object")return;for(const[k,v]of Object.entries(o)){if(["certificateFile","keyFile","dhFile"].includes(k)&&typeof v==="string"&&v.startsWith("/"))files.add(v);else walk(v);}}walk(JSON.parse(data));for(const p of files)console.log(p);
 });});req.setTimeout(10000,()=>{req.destroy();process.exit(1)});req.on("error",()=>process.exit(1));
' > "$work/references" || fail 'Could not read the running container configuration. Docker has not been stopped.'
        while IFS= read -r referenced; do
            [ -r "$referenced" ] || fail "Required file is unavailable on the host: $referenced. Copy/mount it before migrating. Docker has not been stopped."
        done < "$work/references"
    elif [ "$was_running" = true ]; then
        log 'The container has no Node.js runtime; skipping the TLS file reference check.'
    fi
fi

# A running container holds the live settings; otherwise keep the native ones.
if [ "$was_running" = true ]; then cp "$work/docker-env.filtered" "$work/env"
elif [ -f "$work/previous-env" ]; then cp "$work/previous-env" "$work/env"
elif [ -f "$work/legacy-env" ]; then cp "$work/legacy-env" "$work/env"
elif [ -n "$container" ]; then cp "$work/docker-env.filtered" "$work/env"
else : > "$work/env"
fi
# Translate saved options without evaluating shell input. Explicit new names
# take precedence regardless of their ordering in the file.
awk -F= 'NR == FNR {if ($1 ~ /^GOWAVE_NODE_/) present[$1]=1; next}
    /^REMNANODE_[A-Za-z0-9_]*=/ {
        key=$1; sub(/^REMNANODE_/, "GOWAVE_NODE_", key)
        if (!(key in present)) print key substr($0, length($1)+1)
        next
    }
    {print}' "$work/env" "$work/env" > "$work/env.next"
mv -f "$work/env.next" "$work/env"
if [ -z "${GOWAVE_NODE_RUNTIME_DIR:-${REMNANODE_RUNTIME_DIR:-}}" ]; then
    saved_runtime=$(env_get GOWAVE_NODE_RUNTIME_DIR)
    if [ -n "$saved_runtime" ]; then runtime_dir=$(unquote "$saved_runtime" GOWAVE_NODE_RUNTIME_DIR); fi
fi
validate_runtime "$runtime_dir"
service_user=$(env_get GOWAVE_NODE_SERVICE_USER)
service_user=${GOWAVE_NODE_SERVICE_USER:-$service_user}
if [ -z "$service_user" ]; then
    service_user=gowave-node
    # Retain the account that can read existing private keys and custom cores.
    if [ "$legacy_migration" = true ] && id remnanode >/dev/null 2>&1; then service_user=remnanode; fi
fi
service_user=$(unquote "$service_user" GOWAVE_NODE_SERVICE_USER)
case $service_user in ''|-*|*[!a-zA-Z0-9_-]*|root) fail 'The service account must be an unprivileged account name.' ;; esac
if [ "$legacy_migration" = true ]; then
    env_set GOWAVE_NODE_SERVICE_USER "$service_user"
    env_set GOWAVE_NODE_RUNTIME_DIR "$runtime_dir"
fi
# The same runtime must be used by the installed policy and the CLI checks.
export GOWAVE_NODE_RUNTIME_DIR="$runtime_dir" GOWAVE_NODE_SERVICE_USER="$service_user"
secret=$(env_get SECRET_KEY)
[ -n "$secret" ] || secret=${SECRET_KEY:-}
[ -n "$secret" ] || secret=$(ask 'Paste SECRET_KEY from the panel node settings: ')
secret=$(unquote "$secret" SECRET_KEY)
[ -n "$secret" ] || fail 'SECRET_KEY is required: run the installer in a terminal or export SECRET_KEY.'
case $secret in *[!A-Za-z0-9+/=_-]*) fail 'SECRET_KEY must be the base64 value shown by the panel.' ;; esac
port=$(env_get NODE_PORT)
[ -n "$port" ] || port=${NODE_PORT:-}
port=$(unquote "${port:-3000}" NODE_PORT)
case $port in ''|*[!0-9]*) fail "Invalid NODE_PORT: $port" ;; esac
if [ "$port" -lt 1 ] || [ "$port" -gt 65535 ]; then fail "Invalid NODE_PORT: $port"; fi
# Our own node or container may hold the port; anything else would make the
# new node crash-loop while the panel reaches a stranger on that port.
if [ "$native_running" != true ] && [ "$legacy_running" != true ] && [ "$was_running" != true ] && port_busy "$port"; then
    fail "NODE_PORT $port is already in use by another program (see: ss -ltnp 'sport = :$port'). Pick a free port and use the same one for the node in the panel."
fi
env_set SECRET_KEY "$secret"
env_set NODE_PORT "$port"

base=https://github.com/$repo/releases/latest/download
if [ "$version" != latest ]; then base=https://github.com/$repo/releases/download/$version; fi
asset=gowave-node-linux-$arch
log "Downloading $asset..."
if ! fetch "$base/checksums.txt" "$work/checksums.txt" 2>/dev/null; then
    fetch "$base/sha256sums.txt" "$work/checksums.txt" || fail "Release $version is unavailable or has no checksum manifest."
fi
fetch "$base/$asset" "$work/$asset" || fail "Release $version has no $asset build."
verify "$work/checksums.txt" "$asset"
chmod 0755 "$work/$asset"
"$work/$asset" version 2>/dev/null | grep -q '^gowave-node v' || fail "The $asset build does not run on this host."

# Provision optional assets before changing traffic-serving services.
if fetch "$base/asn-prefixes.zip" "$work/asn-prefixes.zip" 2>/dev/null; then
    verify "$work/checksums.txt" asn-prefixes.zip
    mkdir -p "$prefix/share/asn"
    put 0644 "$work/asn-prefixes.zip" "$prefix/share/asn/asn-prefixes.zip"
else
    log 'This release has no ASN index; ASN-based plugin lists stay unavailable.'
fi

if [ "$init" = systemd ]; then
    cat > "$work/service" <<UNIT
[Unit]
Description=gowave-node
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
User=$service_user
Group=$service_user
WorkingDirectory=$install_dir
ExecStart=$install_dir/gowave-node
RuntimeDirectory=gowave-node
RuntimeDirectoryMode=0700
UMask=0077
Restart=always
RestartSec=3
LimitNOFILE=1048576
[Install]
WantedBy=multi-user.target
UNIT
elif [ "$init" = openrc ]; then
    cat > "$work/service" <<RC
#!/sbin/openrc-run
description="gowave-node"
command="$install_dir/gowave-node"
directory="$install_dir"
command_user="$service_user:$service_user"
capabilities="^cap_net_bind_service,^cap_net_admin,^cap_net_raw"
no_new_privs=true
export GOWAVE_NODE_RUNTIME_DIR="$runtime_dir"
export XRAY_CORE_PATH="$install_dir/rw-core"
export XRAY_LOCATION_ASSET="$install_dir"
supervisor=supervise-daemon
respawn_delay=3
respawn_max=0
umask=077
rc_ulimit="-n 1048576"

start_pre() {
    checkpath -d -m 0700 -o $service_user:$service_user "$runtime_dir"
}

depend() {
	need net
	after firewall
}
RC
else
    cat > "$work/service" <<PROCD
#!/bin/sh /etc/rc.common
START=99
STOP=10
USE_PROCD=1

start_service() {
    [ ! -L "$runtime_dir" ] || return 1
    mkdir -p "$runtime_dir" || return 1
    chown $service_user:$service_user "$runtime_dir" || return 1
    chmod 0700 "$runtime_dir" || return 1
	procd_open_instance
    # ujail drops UID/GID and sets ambient capabilities before executing the node.
    procd_set_param command "$ujail" -U $service_user -G $service_user -C "$capabilities_file" -c -- "$install_dir/gowave-node"
    procd_set_param env GOWAVE_NODE_RUNTIME_DIR="$runtime_dir" XRAY_CORE_PATH="$install_dir/rw-core" XRAY_LOCATION_ASSET="$install_dir"
	procd_set_param respawn 3600 3 0
	procd_set_param limits nofile="1048576 1048576"
	procd_set_param stdout 1
	procd_set_param stderr 1
	procd_close_instance
}
PROCD
fi

modified=true
put 0600 "$work/env" "$install_dir/.env"
put 0755 "$work/$asset" "$install_dir/gowave-node"
# The CLI selects latest stable Xray and verifies the release checksum.
if [ ! -x "$install_dir/rw-core" ] && [ ! -x "$prefix/bin/rw-core" ]; then
    "$install_dir/gowave-node" core install -y
fi
put "$service_mode" "$work/service" "$service_file"
# Share persistent ownership and core/asset setup across all service managers.
GOWAVE_NODE_DEFER_PATHS=1 "$install_dir/gowave-node" service install "$install_dir" "$(dirname "$service_file")" "$init"
if [ "$init" = procd ]; then
    mkdir -p "$config_dir"
    config_path=$(cd "$config_dir" && pwd -P)
    data_path=$(cd "$install_dir" && pwd -P)
    case "$config_path" in
        "$data_path"|"$data_path"/*) fail 'Capability policy must be outside the node-writable installation directory.' ;;
    esac
    chown root:root "$config_dir"
    chmod 0755 "$config_dir"
    cat > "$work/capabilities.json" <<CAPS
{
    "bounding": ["CAP_NET_BIND_SERVICE", "CAP_NET_ADMIN", "CAP_NET_RAW"],
    "effective": ["CAP_NET_BIND_SERVICE", "CAP_NET_ADMIN", "CAP_NET_RAW"],
    "inheritable": ["CAP_NET_BIND_SERVICE", "CAP_NET_ADMIN", "CAP_NET_RAW"],
    "permitted": ["CAP_NET_BIND_SERVICE", "CAP_NET_ADMIN", "CAP_NET_RAW"],
    "ambient": ["CAP_NET_BIND_SERVICE", "CAP_NET_ADMIN", "CAP_NET_RAW"]
}
CAPS
    put 0644 "$work/capabilities.json" "$capabilities_file"
fi
svc reload
svc enable
started=$(date +%s)
switched=true
if [ "$legacy_migration" = true ]; then
    # Do not retire the old service or its update watchers before provisioning
    # and checksum verification finish. Rollback restores each prior state.
    legacy_switched=true
    if [ "$init" = systemd ]; then
        for unit in remnanode-restart.path remnanode-update.path; do
            if [ -f "$service_dir/$unit" ]; then systemctl disable --now "$unit"; fi
        done
    fi
    legacy_svc disable
    legacy_svc stop
fi
if [ "$was_running" = true ]; then
    docker update --restart=no "$container"
    docker stop -t 10 "$container"
fi
svc restart
if [ "$init" = systemd ]; then systemctl enable --now gowave-node-restart.path gowave-node-update.path; fi
if [ "$was_running" = true ] || [ "$native_running" = true ] || [ "$legacy_running" = true ]; then
    log 'Waiting up to 2 minutes for the node to become ready...'
    "$install_dir/gowave-node" status --wait-ready || fail 'The node did not become ready.'
    log "Node is ready; measured switch-to-Xray-readiness time: $(( $(date +%s) - started )) seconds."
else
    # The daemon starts without Xray configuration. Do not hold the installer
    # open waiting for the panel to configure it for the first time.
    log 'Waiting up to 30 seconds for the management daemon to start...'
    "$install_dir/gowave-node" status --wait-running || fail 'The management daemon did not start. Check: gowave-node status'
    log "Installed and running. Add the node in the panel with port $port to start Xray."
fi
complete=true
for name in gowave-node xlogs; do
    destination=$prefix/bin/$name
    if [ ! -e "$destination" ] || [ -L "$destination" ]; then ln -sfn "$install_dir/gowave-node" "$destination"; fi
done
if [ "$legacy_migration" = true ] || [ -x "$install_dir/remnanode" ]; then
    for name in remnanode remnanode-go; do
        destination=$prefix/bin/$name
        if [ ! -e "$destination" ] || [ -L "$destination" ]; then ln -sfn "$install_dir/gowave-node" "$destination"; fi
    done
fi
if [ -n "$container" ]; then log "The previous container $container is retained for manual recovery."; fi
