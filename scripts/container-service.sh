#!/usr/bin/env bash
# container-service.sh - Create systemd service files for an IIAB container
# Usage: ./container-service.sh <edition> <ip_address>
set -euo pipefail

EDITION="${1:?Error: Edition required}"
IP="${2:?Error: IP address required}"

CONTAINER_NAME="iiab-${EDITION}"
IMAGE_PATH="/var/lib/machines/${CONTAINER_NAME}.raw"

if [ ! -f "$IMAGE_PATH" ]; then
    echo "Error: Container image not found at $IMAGE_PATH" >&2
    exit 1
fi

# Create nspawn settings directory
SETTINGS_DIR="/etc/systemd/nspawn"
mkdir -p "$SETTINGS_DIR"

# Create the .nspawn settings file
cat > "${SETTINGS_DIR}/${CONTAINER_NAME}.nspawn" << EOF
[Exec]
Hostname=${CONTAINER_NAME}
Boot=true

[Network]
VirtualEthernet=true
Bridge=iiab-br0
IPAddress=${IP}/24
Gateway=10.0.3.1
DNS=8.8.8.8
DNS=1.1.1.1

[Files]
Uncompressed=yes
EOF

echo "Created ${SETTINGS_DIR}/${CONTAINER_NAME}.nspawn"

# Create a symlink or directory for the container rootfs
# nspawn can boot directly from .raw images
CONTAINER_DIR="/var/lib/machines/${CONTAINER_NAME}"
if [ ! -d "$CONTAINER_DIR" ]; then
    # For raw images, we need to either:
    # 1. Mount the image as a loop device and bind-mount, or
    # 2. Use machinectl import to register it
    # For simplicity, create a directory and use the raw image with machinectl

    # Option: Use systemd-dissect to mount the raw image
    mkdir -p "$CONTAINER_DIR"
    echo "Container directory created at $CONTAINER_DIR"
    echo "Register the raw image with: machinectl import-raw ${IMAGE_PATH} ${CONTAINER_NAME}"
fi

# Create systemd service override for automatic start
SERVICE_OVERRIDE="/etc/systemd/system/systemd-nspawn@${CONTAINER_NAME}.service.d"
mkdir -p "$SERVICE_OVERRIDE"

cat > "${SERVICE_OVERRIDE}/override.conf" << EOF
[Service]
Restart=on-failure
RestartSec=30
EOF

echo "Created ${SERVICE_OVERRIDE}/override.conf"

echo ""
echo "Container service files created for ${CONTAINER_NAME}"
echo ""
echo "To register and start the container:"
echo "  machinectl import-raw ${IMAGE_PATH} ${CONTAINER_NAME}"
echo "  machinectl start ${CONTAINER_NAME}"
echo ""
echo "To check status:"
echo "  machinectl status ${CONTAINER_NAME}"
echo ""
echo "To get a shell inside:"
echo "  machinectl shell ${CONTAINER_NAME}"
