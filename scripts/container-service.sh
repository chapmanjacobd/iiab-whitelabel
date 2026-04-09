#!/usr/bin/env bash
# container-service.sh - Create systemd service files for an IIAB container
# Usage:
#   container-service.sh <name> <ip> [--volatile=MODE]
#
# --volatile=MODE  Controls rootfs persistence:
#   no      -- Persistent rootfs. All changes survive restarts.
#   overlay -- Overlayfs with tmpfs upper. Changes discarded on stop.
#   state   -- Volatile /etc and /usr, persistent /var.
#   yes     -- Full volatile rootfs. Everything resets on boot.
#
# Default (from democtl): overlay
#
# The container rootfs is a btrfs subvolume at /var/lib/machines/<name>.
# systemd-nspawn's --volatile= is used directly -- it works with directory
# rootfs the same way it works with image files.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib-iiab.sh disable=SC1091
source "$SCRIPT_DIR/lib-iiab.sh"

NAME="${1:?Error: container name required}"
IP="${2:?Error: IP address required}"
shift 2 || true

VOLATILE="overlay"

while [[ $# -gt 0 ]]; do
    case "$1" in
        --volatile=*)
            VOLATILE="${1#--volatile=}"
            if [[ ! "$VOLATILE" =~ ^(no|overlay|state|yes)$ ]]; then
                echo "Error: --volatile must be 'no', 'overlay', 'state', or 'yes' (got: $VOLATILE)" >&2
                exit 1
            fi
            ;;
        *)
            echo "Warning: Unknown option: $1" >&2
            ;;
    esac
    shift
done

# Validate the rootfs exists
ROOTFS="/var/lib/machines/$NAME"
if [ ! -d "$ROOTFS" ]; then
    echo "Error: Container rootfs not found: $ROOTFS" >&2
    echo "  Build it first: democtl build $NAME" >&2
    exit 1
fi

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
PrivateUsers=yes
NoNewPrivileges=yes

[Network]
VirtualEthernet=yes
Bridge=${IIAB_BRIDGE}

[Files]
${FILES_SECTION}
EOF

echo "Created ${SETTINGS_DIR}/${NAME}.nspawn"
echo "  Rootfs:    $ROOTFS"
echo "  IP:        ${IP}"
echo "  Volatile:  ${VOLATILE}"

# Create systemd service override
SERVICE_OVERRIDE="/etc/systemd/system/systemd-nspawn@${NAME}.service.d"
mkdir -p "$SERVICE_OVERRIDE"

cat > "${SERVICE_OVERRIDE}/override.conf" << EOF
[Service]
Restart=on-failure
RestartSec=30

# Filesystem restrictions
ProtectSystem=full
ProtectHome=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectKernelLogs=yes
ProtectClock=yes
ProtectHostname=yes

# Process visibility
ProcSubset=pid
ProtectProc=invisible

# Device access
DevicePolicy=closed
DeviceAllow=char-random rw

# Privilege restrictions
NoNewPrivileges=yes

# Restrict socket families
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK AF_PACKET

# Syscall architecture
SystemCallArchitectures=native

# Memory restrictions
RestrictRealtime=yes
MemoryDenyWriteExecute=yes
RestrictSUIDSGID=yes
EOF

echo "Created ${SERVICE_OVERRIDE}/override.conf"
