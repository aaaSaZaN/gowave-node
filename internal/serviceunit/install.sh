#!/usr/bin/env bash
# Embedded in the binary: used by installers and the first upgraded root service.
set -euo pipefail
install_dir=$1
unit_dir=${2:-/etc/systemd/system}
install_dir=$(realpath -m -- "$install_dir")
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
if [ -n "${3:-}" ] && [ "$3" != "$install_dir/remnanode" ]; then
    install -m 0755 "$3" "$install_dir/remnanode.new"
    mv -f "$install_dir/remnanode.new" "$install_dir/remnanode"
fi
if ! getent group remnanode >/dev/null; then
    groupadd --system remnanode
fi
if ! id remnanode >/dev/null 2>&1; then
    useradd --system --gid remnanode --home-dir "$install_dir" --no-create-home --shell /usr/sbin/nologin remnanode
fi
[ "$(id -u remnanode)" -ne 0 ] || { echo 'Refusing a service account with UID 0.' >&2; exit 1; }
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
chown -hR remnanode:remnanode "$install_dir"
chmod 0750 "$install_dir"
[ ! -L "$install_dir/.env" ] || { echo "Refusing a symlinked .env" >&2; exit 1; }
[ ! -f "$install_dir/.env" ] || chmod 0600 "$install_dir/.env"
mkdir -p "$unit_dir/remnanode.service.d"
policy=$(mktemp "$unit_dir/remnanode.service.d/.hardening.XXXXXX")
trap 'rm -f -- "$policy"' EXIT
cat > "$policy" <<UNIT
[Service]
User=remnanode
Group=remnanode
WorkingDirectory=$install_dir
ExecStart=
ExecStart=$install_dir/remnanode
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
RuntimeDirectory=remnanode
RuntimeDirectoryMode=0700
RuntimeDirectoryPreserve=yes
Restart=always
CapabilityBoundingSet=
CapabilityBoundingSet=CAP_NET_BIND_SERVICE CAP_NET_ADMIN CAP_NET_RAW
AmbientCapabilities=
AmbientCapabilities=CAP_NET_BIND_SERVICE CAP_NET_ADMIN CAP_NET_RAW
UNIT
cat >> "$policy" <<UNIT
Environment=REMNANODE_MANAGED_SERVICE=1
Environment=REMNANODE_RUNTIME_DIR=/run/remnanode
Environment=XRAY_CORE_PATH=$install_dir/rw-core
Environment=XRAY_LOCATION_ASSET=$install_dir
UNIT
# The path units allow only fixed restart/update operations. The node never
# receives permission to manage arbitrary system services or run root code.
cat > "$unit_dir/remnanode-restart.path" <<UNIT
[Unit]
Description=Watch for an unprivileged Remnanode restart request
[Path]
PathExists=/run/remnanode/restart-request
Unit=remnanode-restart.service
[Install]
WantedBy=multi-user.target
UNIT
cat > "$unit_dir/remnanode-restart.service" <<UNIT
[Unit]
Description=Restart only the Remnanode service
[Service]
Type=oneshot
User=remnanode
Group=remnanode
ExecStart=/bin/rm -f /run/remnanode/restart-request
# Only this fixed systemctl command uses the manager's UID, within the sandbox.
ExecStart=!/bin/systemctl restart remnanode.service
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
ReadWritePaths=/run/remnanode
UNIT
cat > "$unit_dir/remnanode-update.path" <<UNIT
[Unit]
Description=Watch for an unprivileged Remnanode update monitor request
[Path]
PathExists=/run/remnanode/update-request.json
Unit=remnanode-update.service
[Install]
WantedBy=multi-user.target
UNIT
cat > "$unit_dir/remnanode-update.service" <<UNIT
[Unit]
Description=Monitor Remnanode update readiness and rollback
[Service]
Type=oneshot
User=remnanode
Group=remnanode
WorkingDirectory=$install_dir
ExecStart=$install_dir/.update-monitor _finish-pending-update
Environment=REMNANODE_MANAGED_SERVICE=1
Environment=REMNANODE_RUNTIME_DIR=/run/remnanode
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
ReadWritePaths=$install_dir /run/remnanode
TimeoutStartSec=300
UNIT
chmod 0644 "$policy"
mv -f "$policy" "$unit_dir/remnanode.service.d/90-unprivileged.conf"
trap - EXIT
systemctl daemon-reload
systemctl enable --now remnanode-restart.path remnanode-update.path
