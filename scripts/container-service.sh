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
PrivateUsers=yes
NoNewPrivileges=yes

[Network]
VirtualEthernet=yes
Bridge=iiab-br0

[Files]
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

# Filesystem restrictions - ProtectSystem=full protects /usr and /boot
# but allows writes to /etc and /var where nspawn needs access
ProtectSystem=full
ProtectHome=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectKernelLogs=yes
ProtectClock=yes
ProtectHostname=yes

# Process visibility - only expose PID info
ProcSubset=pid
ProtectProc=invisible

# Device access - restrict to only what nspawn needs
DevicePolicy=closed
DeviceAllow=char-random rw

# Privilege restrictions
NoNewPrivileges=yes

# Restrict socket families to what's needed for web services + netlink for networking
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK AF_PACKET

# Syscall architecture - only allow native syscalls
SystemCallArchitectures=native

# Memory restrictions
RestrictRealtime=yes
MemoryDenyWriteExecute=yes
RestrictSUIDSGID=yes
EOF

echo "Created ${SERVICE_OVERRIDE}/override.conf"
