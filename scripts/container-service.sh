#!/usr/bin/env bash
# container-service.sh - Create systemd service files for an IIAB container
# Usage:
#   container-service.sh <name> <ip> [--volatile=MODE] [--ram-image]
#
# --volatile=MODE  Controls systemd's Volatile= setting:
#   no     — Standard persistent container (default)
#   yes    — Full overlay: entire rootfs is tmpfs, changes discarded on stop
#   state  — State overlay: only /var is tmpfs, /usr stays read-only from image
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
            if [[ ! "$VOLATILE" =~ ^(no|yes|state)$ ]]; then
                echo "Error: --volatile must be 'no', 'yes', or 'state' (got: $VOLATILE)" >&2
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
PrivateUsers=no

[Network]
VirtualEthernet=true
Bridge=iiab-br0

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
EOF

echo "Created ${SERVICE_OVERRIDE}/override.conf"
