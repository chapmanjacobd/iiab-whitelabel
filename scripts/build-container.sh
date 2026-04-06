#!/usr/bin/env bash
# build-container.sh - Build an IIAB container image
# Usage: ./build-container.sh <edition> [image_path]
#   edition: small, medium, or large
#   image_path: optional path to Debian base image (defaults to download)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"
NSPAWN_DIR="${PROJECT_DIR}/../nspawn-loop"

EDITION="${1:?Error: Edition required (small, medium, or large)}"
IMAGE_SOURCE="${2:-}"

# Load container vars
CONTAINERS_YML="${PROJECT_DIR}/vars/containers.yml"

echo "=========================================="
echo "Building IIAB ${EDITION} container"
echo "=========================================="

# Validate edition
if [[ ! "$EDITION" =~ ^(small|medium|large)$ ]]; then
    echo "Error: Edition must be 'small', 'medium', or 'large'" >&2
    exit 1
fi

# Check for nspawn-loop
if [ ! -d "$NSPAWN_DIR" ]; then
    echo "Error: nspawn-loop directory not found at ${NSPAWN_DIR}" >&2
    echo "Clone it: git clone https://github.com/chapmanjacobd/nspawn-loop.git" >&2
    exit 1
fi

# Set image size based on edition
case "$EDITION" in
    small)  TARGET_MB=12000 ;;
    medium) TARGET_MB=20000 ;;
    large)  TARGET_MB=30000 ;;
esac

LOCAL_VARS="${PROJECT_DIR}/vars/local_vars_${EDITION}.yml"
if [ ! -f "$LOCAL_VARS" ]; then
    echo "Error: local_vars file not found: $LOCAL_VARS" >&2
    exit 1
fi

echo "Edition: $EDITION"
echo "Target size: ${TARGET_MB}MB"
echo "Local vars: $LOCAL_VARS"

# Step 1: Mount Debian image
echo ""
echo "=== Step 1: Preparing Debian base image ==="
if [ -n "$IMAGE_SOURCE" ] && [ -f "$IMAGE_SOURCE" ]; then
    sudo "${NSPAWN_DIR}/mount.sh" "$IMAGE_SOURCE" "$TARGET_MB"
elif [ -f "${NSPAWN_DIR}/debian-13-generic-amd64.img" ]; then
    sudo "${NSPAWN_DIR}/mount.sh" "${NSPAWN_DIR}/debian-13-generic-amd64.img" "$TARGET_MB"
else
    echo "Downloading Debian 13 generic amd64 image..."
    sudo "${NSPAWN_DIR}/mount.sh" \
        "https://cloud.debian.org/images/cloud/trixie/latest/debian-13-generic-amd64.raw" \
        "$TARGET_MB"
fi

# Find the state file
STATE_FILE=$(find "$NSPAWN_DIR" -maxdepth 1 -name "*.state" | head -n 1)
if [ -z "$STATE_FILE" ]; then
    echo "Error: No .state file found after mount" >&2
    exit 1
fi

source "$STATE_FILE"
echo "Mount directory: $MOUNT_DIR"

# Step 2: Prepare the container rootfs
echo ""
echo "=== Step 2: Preparing container rootfs ==="

# Install IIAB code
mkdir -p "$MOUNT_DIR/opt/iiab"
if [ -d "${PROJECT_DIR}/../iiab" ]; then
    echo "Copying IIAB from local checkout..."
    cp -R --preserve=mode,timestamps,links "${PROJECT_DIR}/../iiab" "$MOUNT_DIR/opt/iiab/"
else
    echo "Cloning IIAB from GitHub..."
    git clone --depth 1 https://github.com/iiab/iiab.git "$MOUNT_DIR/opt/iiab/iiab"
fi

# Install IIAB configuration
mkdir -p "$MOUNT_DIR/etc/iiab"
cp --preserve=mode,timestamps "$LOCAL_VARS" "$MOUNT_DIR/etc/iiab/local_vars.yml"

# Disable RPi-specific settings
sed -i '/rpi_image: True/d' "$MOUNT_DIR/etc/iiab/local_vars.yml"
sed -i 's/^iiab_admin_user_install: True/iiab_admin_user_install: False/' "$MOUNT_DIR/etc/iiab/local_vars.yml"

# Set hostname for the container
echo "iiab-${EDITION}" > "$MOUNT_DIR/etc/hostname"

# Disable WiFi/hostapd (not needed in containers)
cat >> "$MOUNT_DIR/etc/iiab/local_vars.yml" << 'EOF'
# Disabled for container deployment
hostapd_install: False
hostapd_enabled: False
captiveportal_install: False
captiveportal_enabled: False
EOF

# Step 3: Run IIAB installer inside nspawn
echo ""
echo "=== Step 3: Running IIAB installer (this takes 30-60 minutes) ==="

# Ensure host networking is ready
systemctl is-active --quiet systemd-networkd || systemctl start systemd-networkd
systemctl is-active --quiet systemd-resolved || systemctl start systemd-resolved
sysctl -w net.ipv4.ip_forward=1

# Setup NAT if not already done
EXT_IF=$(ip route | grep default | awk '{print $5}' | head -n1)
iptables -t nat -C POSTROUTING -o "$EXT_IF" -j MASQUERADE 2>/dev/null || \
    iptables -t nat -A POSTROUTING -o "$EXT_IF" -j MASQUERADE
iptables -C FORWARD -i ve-+ -o "$EXT_IF" -j ACCEPT 2>/dev/null || {
    iptables -A FORWARD -i ve-+ -o "$EXT_IF" -j ACCEPT
    iptables -A FORWARD -i "$EXT_IF" -o ve-+ -m state --state RELATED,ESTABLISHED -j ACCEPT
}

systemd-firstboot --root="$MOUNT_DIR" --delete-root-password --force
echo "nameserver 8.8.8.8" > "$MOUNT_DIR/etc/resolv.conf"

export MOUNT_DIR
expect << 'EXPECT_EOF'
set timeout 7200

spawn systemd-nspawn -q --network-veth --resolv-conf=off -D $env(MOUNT_DIR) -M box --boot

expect "login: " { send "root\r" }

expect -re {#\s?$} { send "apt update\r" }
expect -re {#\s?$} { send "DEBIAN_FRONTEND=noninteractive apt upgrade -y\r" }

expect -re {#\s?$} { send "curl -fLo /usr/sbin/iiab https://raw.githubusercontent.com/iiab/iiab-factory/master/iiab\r" }
expect -re {#\s?$} { send "chmod 0755 /usr/sbin/iiab\r" }
expect -re {#\s?$} { send "/usr/sbin/iiab --risky\r" }

expect {
    timeout { puts "\nTimed out waiting for IIAB install"; exit 1 }
    "photographed" { send "\r" }
}

# Wait for reboot prompt
expect "login: " { send "root\r" }

expect -re {#\s?$} { send "usermod --lock --expiredate=1 root\r" }
expect -re {#\s?$} { send "shutdown now\r" }
expect eof
EXPECT_EOF

echo ""
echo "=== IIAB install complete ==="

# Step 4: Shrink the image
echo ""
echo "=== Step 4: Shrinking image ==="

# Add metadata
echo "iiab-${EDITION}" > "$MOUNT_DIR/.iiab-image"
echo "Build date: $(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$MOUNT_DIR/.iiab-image"
echo "Edition: $EDITION" >> "$MOUNT_DIR/.iiab-image"

# Clean up
echo uninitialized > "$MOUNT_DIR/etc/machine-id"
rm -f "$MOUNT_DIR/etc/iiab/uuid"
rm -f "$MOUNT_DIR/var/swap"
touch "$MOUNT_DIR/.resize-rootfs"

sudo "${NSPAWN_DIR}/shrink.sh" "$STATE_FILE" 200

# Step 5: Register the image
echo ""
echo "=== Step 5: Registering container image ==="

IMG_FILE=$(basename "$STATE_FILE" .state)
DEST="/var/lib/machines/iiab-${EDITION}.raw"

# The shrink script outputs the image file path
# Find the resulting image
if [ -f "${NSPAWN_DIR}/${IMG_FILE}" ]; then
    sudo mv "${NSPAWN_DIR}/${IMG_FILE}" "$DEST"
    echo "Image registered at: $DEST"
elif [ -f "${NSPAWN_DIR}/${IMG_FILE}.img" ]; then
    sudo mv "${NSPAWN_DIR}/${IMG_FILE}.img" "$DEST"
    echo "Image registered at: $DEST"
else
    echo "Warning: Could not find resulting image file" >&2
    echo "Look in ${NSPAWN_DIR}/ for *.img files" >&2
fi

echo ""
echo "=========================================="
echo "Build complete!"
echo "Image: $DEST"
echo "=========================================="
echo ""
echo "To start the container:"
echo "  machinectl start iiab-${EDITION}"
echo "  systemd-nspawn -b -D /var/lib/machines/iiab-${EDITION}.raw -M iiab-${EDITION}"
echo ""
echo "To register as a service:"
echo "  sudo cp /var/lib/machines/iiab-${EDITION}.raw /var/lib/machines/iiab-${EDITION}/"
echo "  sudo systemd-nspawn --register=yes -M iiab-${EDITION}"
