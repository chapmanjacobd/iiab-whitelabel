#!/usr/bin/env bash
# container-service.sh - Create systemd service files for an IIAB container
# Usage:
#   container-service.sh <name> <ip> [--volatile=MODE] [--ram-image]
#
# --volatile=MODE  Controls systemd's Volatile= setting:
#   no      — Persistent rootfs. All changes survive restarts.
#   overlay — Overlayfs with tmpfs upper. Changes discarded on stop. Works with any rootfs.
#   state   — Volatile /etc and /usr, persistent /var. Requires bootable /usr-only system.
#   yes     — Full volatile rootfs. Everything resets on boot. Requires bootable /usr-only system.
#
# --ram-image  Image is loaded into host tmpfs. Container boots from RAM,
#              never reads from disk after initial copy.
set -euo pipefail

NAME="${1:?Error: container name required}"
IP="${2:?Error: IP address required}"
shift 2 || true

VOLATILE="no"
RAM_IMAGE=false

while [[ $# -gt 0 ]]; do
    case "$1" in
        --volatile=*)
            VOLATILE="${1#--volatile=}"
            if [[ ! "$VOLATILE" =~ ^(no|overlay|state|yes)$ ]]; then
                echo "Error: --volatile must be 'no', 'overlay', 'state', or 'yes' (got: $VOLATILE)" >&2
                exit 1
            fi
            ;;
        --ram-image)
            RAM_IMAGE=true
            ;;
        *)
            echo "Warning: Unknown option: $1" >&2
            ;;
    esac
    shift
done

# Create nspawn settings directory
SETTINGS_DIR="/etc/systemd/nspawn"
mkdir -p "$SETTINGS_DIR"

# Build the [Files] section
FILES_SECTION=""
if [[ "$VOLATILE" != "no" ]]; then
    FILES_SECTION="Volatile=${VOLATILE}"
fi

cat > "${SETTINGS_DIR}/${NAME}.nspawn" << EOF
[Exec]
Hostname=${NAME}
Boot=true
PrivateUsers=pick
NoNewPrivileges=yes
Capability=CAP_NET_BIND_SERVICE
SystemCallFilter=@system-service
SystemCallFilter=~@privileged @resources @reboot @swap @mount @debug @clock @module @raw-io @reboot

[Network]
VirtualEthernet=yes
Bridge=iiab-br0
PrivateNetworking=yes

${FILES_SECTION}
EOF

echo "Created ${SETTINGS_DIR}/${NAME}.nspawn"
echo "  IP:        ${IP}"
echo "  Volatile:  ${VOLATILE}"
echo "  RAM image: ${RAM_IMAGE}"

# Create systemd service override
SERVICE_OVERRIDE="/etc/systemd/system/systemd-nspawn@${NAME}.service.d"
mkdir -p "$SERVICE_OVERRIDE"

if [[ "$VOLATILE" != "no" ]] || $RAM_IMAGE; then
    RESTART_POLICY="always"
else
    RESTART_POLICY="on-failure"
fi

cat > "${SERVICE_OVERRIDE}/override.conf" << EOF
[Service]
Restart=${RESTART_POLICY}
RestartSec=30

# Filesystem restrictions
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/var/lib/machines/${NAME}

# Device access restrictions
DevicePolicy=closed
DeviceAllow=char-random rw

# Privilege restrictions
NoNewPrivileges=yes

# Additional hardening
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictRealtime=yes
RestrictNamespaces=yes
MemoryDenyWriteExecute=yes
LockPersonality=yes
EOF

echo "Created ${SERVICE_OVERRIDE}/override.conf"
