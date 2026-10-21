#!/bin/sh
# Embedded in the binary: used by installers and the first upgraded root service.
set -eu
install_dir=$1
unit_dir=${2:-/etc/systemd/system}
manager=${4:-systemd}
case "$manager" in systemd|openrc|procd) ;; *) echo 'Unsupported service manager.' >&2; exit 1 ;; esac
have() { command -v "$1" >/dev/null 2>&1; }
[ "$(id -u)" -eq 0 ] || { echo 'Service installation requires root.' >&2; exit 1; }
case "$install_dir" in
    /*) ;;
    *) echo 'Installation path must be absolute.' >&2; exit 1 ;;
esac
# systemd paths have their own escaping rules. Refuse ambiguous paths.
case "$install_dir" in *[!a-zA-Z0-9_./-]*) echo 'Unsupported installation path.' >&2; exit 1 ;; esac
case "$install_dir" in
    /|/bin|/sbin|/usr|/usr/bin|/usr/sbin|/usr/local|/usr/local/bin|/etc|/var|/var/lib|/tmp|/opt|/srv|/home|/home/*|/root|/root/*)
        echo 'Use a dedicated installation directory outside home directories.' >&2; exit 1 ;;
esac
mkdir -p "$install_dir"
install_dir=$(cd "$install_dir" && pwd -P)
case "$install_dir" in
    /|/bin|/sbin|/usr|/usr/bin|/usr/sbin|/usr/local|/usr/local/bin|/etc|/var|/var/lib|/tmp|/opt|/srv|/home|/home/*|/root|/root/*)
        echo 'Use a dedicated installation directory outside home directories.' >&2; exit 1 ;;
esac
if [ -n "${3:-}" ] && [ "$3" != "$install_dir/gowave-node" ]; then
    cp "$3" "$install_dir/gowave-node.new"
    chmod 0755 "$install_dir/gowave-node.new"
    mv -f "$install_dir/gowave-node.new" "$install_dir/gowave-node"
fi
group_exists() {
    if have getent; then getent group gowave-node >/dev/null
    else awk -F: '$1 == "gowave-node" { found=1 } END { exit !found }' /etc/group
    fi
}
if ! group_exists; then
    if have groupadd; then groupadd --system gowave-node
    elif have addgroup; then addgroup -S gowave-node
    else echo 'Install groupadd or BusyBox addgroup before installing the node.' >&2; exit 1
    fi
fi
if ! id gowave-node >/dev/null 2>&1; then
    if have useradd; then
        useradd --system --gid gowave-node --home-dir "$install_dir" --no-create-home --shell /bin/false gowave-node
    elif have adduser; then
        adduser -S -D -H -h "$install_dir" -s /bin/false -G gowave-node gowave-node
    else echo 'Install useradd or BusyBox adduser before installing the node.' >&2; exit 1
    fi
fi
[ "$(id -u gowave-node)" -ne 0 ] || { echo 'Refusing a service account with UID 0.' >&2; exit 1; }
# Put the managed core and mutable geodata inside the writable installation.
if [ ! -f "$install_dir/rw-core" ]; then
    for core in "${XRAY_CORE_PATH:-}" "$install_dir/xray" /usr/local/bin/rw-core /usr/local/bin/xray; do
        if [ -n "$core" ] && [ -f "$core" ]; then
            cp "$core" "$install_dir/rw-core"
            chmod 0755 "$install_dir/rw-core"
            for suffix in .stock .stock.previous .stock.pending .previous; do
                [ ! -f "$core$suffix" ] || cp "$core$suffix" "$install_dir/rw-core$suffix"
            done
            break
        fi
    done
fi
for asset in geoip.dat geosite.dat; do
    if [ ! -f "$install_dir/$asset" ]; then
        for dir in "${XRAY_LOCATION_ASSET:-}" /usr/local/share/xray /usr/share/xray; do
            if [ -n "$dir" ] && [ -f "$dir/$asset" ]; then
                cp "$dir/$asset" "$install_dir/$asset"
                break
            fi
        done
    fi
done
# Persistent ownership is required for self-updates and private database/runtime files.
chown -hR gowave-node:gowave-node "$install_dir"
chmod 0750 "$install_dir"
[ ! -L "$install_dir/.env" ] || { echo "Refusing a symlinked .env" >&2; exit 1; }
[ ! -f "$install_dir/.env" ] || chmod 0600 "$install_dir/.env"
# OpenRC/procd provide the runtime directory and capability policy at startup.
[ "$manager" = systemd ] || exit 0
mkdir -p "$unit_dir/gowave-node.service.d"
policy=$(mktemp "$unit_dir/gowave-node.service.d/.hardening.XXXXXX")
trap 'rm -f -- "$policy"' EXIT
cat > "$policy" <<UNIT
[Service]
User=gowave-node
Group=gowave-node
WorkingDirectory=$install_dir
ExecStart=
ExecStart=$install_dir/gowave-node
DynamicUser=false
ProtectSystem=strict
ProtectHome=true
PrivateDevices=true
PrivateTmp=true
NoNewPrivileges=true
MemoryDenyWriteExecute=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true
UMask=0077
ReadWritePaths=$install_dir
RuntimeDirectory=gowave-node
RuntimeDirectoryMode=0700
RuntimeDirectoryPreserve=yes
Restart=always
CapabilityBoundingSet=
CapabilityBoundingSet=CAP_NET_BIND_SERVICE CAP_NET_ADMIN CAP_NET_RAW
AmbientCapabilities=
AmbientCapabilities=CAP_NET_BIND_SERVICE CAP_NET_ADMIN CAP_NET_RAW
UNIT
cat >> "$policy" <<UNIT
Environment=GOWAVE_NODE_MANAGED_SERVICE=1
Environment=GOWAVE_NODE_RUNTIME_DIR=/run/gowave-node
Environment=XRAY_CORE_PATH=$install_dir/rw-core
Environment=XRAY_LOCATION_ASSET=$install_dir
UNIT
# The path units allow only fixed restart/update operations. The node never
# receives permission to manage arbitrary system services or run root code.
cat > "$unit_dir/gowave-node-restart.path" <<UNIT
[Unit]
Description=Watch for an unprivileged gowave-node restart request
[Path]
PathExists=/run/gowave-node/restart-request
Unit=gowave-node-restart.service
[Install]
WantedBy=multi-user.target
UNIT
cat > "$unit_dir/gowave-node-restart.service" <<UNIT
[Unit]
Description=Restart only the gowave-node service
[Service]
Type=oneshot
User=gowave-node
Group=gowave-node
ExecStart=/bin/rm -f /run/gowave-node/restart-request
# Only this fixed systemctl command uses the manager's UID, within the sandbox.
ExecStart=!/bin/systemctl restart gowave-node.service
ProtectSystem=strict
ProtectHome=true
PrivateDevices=true
PrivateTmp=true
NoNewPrivileges=true
MemoryDenyWriteExecute=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true
UMask=0077
CapabilityBoundingSet=
ReadWritePaths=/run/gowave-node
UNIT
cat > "$unit_dir/gowave-node-update.path" <<UNIT
[Unit]
Description=Watch for an unprivileged gowave-node update monitor request
[Path]
PathExists=/run/gowave-node/update-request.json
Unit=gowave-node-update.service
[Install]
WantedBy=multi-user.target
UNIT
cat > "$unit_dir/gowave-node-update.service" <<UNIT
[Unit]
Description=Monitor gowave-node update readiness and rollback
[Service]
Type=oneshot
User=gowave-node
Group=gowave-node
WorkingDirectory=$install_dir
ExecStart=$install_dir/.update-monitor _finish-pending-update
Environment=GOWAVE_NODE_MANAGED_SERVICE=1
Environment=GOWAVE_NODE_RUNTIME_DIR=/run/gowave-node
ProtectSystem=strict
ProtectHome=true
PrivateDevices=true
PrivateTmp=true
NoNewPrivileges=true
MemoryDenyWriteExecute=true
ProtectKernelTunables=true
ProtectKernelModules=true
ProtectControlGroups=true
RestrictSUIDSGID=true
LockPersonality=true
UMask=0077
CapabilityBoundingSet=
ReadWritePaths=$install_dir /run/gowave-node
TimeoutStartSec=300
UNIT
chmod 0644 "$policy"
mv -f "$policy" "$unit_dir/gowave-node.service.d/90-unprivileged.conf"
trap - EXIT
systemctl daemon-reload
systemctl enable --now gowave-node-restart.path gowave-node-update.path
