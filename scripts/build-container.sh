#!/usr/bin/env bash
# build-container.sh - Build an IIAB container image with arbitrary config
# Usage:
#   build-container.sh --name <name> --edition <edition> \
#     --repo <repo> --branch <branch> --size <MB> \
#     --volatile <mode> --ip <ip> [--ram-image] [--local-vars <path>]
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

# Defaults
NAME=""
EDITION=""
IIAB_REPO="https://github.com/iiab/iiab.git"
IIAB_BRANCH="master"
SIZE_MB=15000
VOLATILE="state"
IP=""
RAM_IMAGE=false
LOCAL_VARS=""
NSPAWN_DIR=""
IMAGE_SOURCE=""

# Parse arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --name)       NAME="$2"; shift 2 ;;
        --edition)    EDITION="$2"; shift 2 ;;
        --repo)       IIAB_REPO="$2"; shift 2 ;;
        --branch)     IIAB_BRANCH="$2"; shift 2 ;;
        --size)       SIZE_MB="$2"; shift 2 ;;
        --volatile)   VOLATILE="$2"; shift 2 ;;
        --ip)         IP="$2"; shift 2 ;;
        --ram-image)  RAM_IMAGE=true; shift ;;
        --local-vars) LOCAL_VARS="$2"; shift 2 ;;
        --nspawn-dir) NSPAWN_DIR="$2"; shift 2 ;;
        --image-source) IMAGE_SOURCE="$2"; shift 2 ;;
        *)
            echo "Warning: Unknown option: $1" >&2
            shift
            ;;
    esac
done

# Resolve NSPAWN_DIR
if [ -z "$NSPAWN_DIR" ]; then
    NSPAWN_DIR="${PROJECT_DIR}/nspawn-loop"
fi

if [ ! -d "$NSPAWN_DIR" ]; then
    echo "Error: nspawn-loop directory not found: $NSPAWN_DIR" >&2
    exit 1
fi

# Validate required args
if [ -z "$NAME" ]; then
    echo "Error: --name required" >&2
    exit 1
fi
if [ -z "$EDITION" ]; then
    EDITION="$NAME"
fi
if [ -z "$IP" ]; then
    echo "Error: --ip required" >&2
    exit 1
fi

echo "=========================================="
echo "Building IIAB container: $NAME"
echo "=========================================="
echo "Edition:  $EDITION"
echo "Branch:   $IIAB_BRANCH"
echo "Repo:     $IIAB_REPO"
echo "Size:     ${SIZE_MB}MB"
echo "Volatile: $VOLATILE"
echo "IP:       $IP"
echo "RAM image: $RAM_IMAGE"
echo "Local vars: ${LOCAL_VARS:-(none)}"

# local_vars path is relative to the IIAB repo (will be resolved after clone/copy)
# If absolute path provided, use as-is
if [[ "$LOCAL_VARS" != /* ]] && [ -n "$LOCAL_VARS" ]; then
    # Relative path - will be resolved relative to IIAB repo in container
    IIAB_VARS_PATH="$LOCAL_VARS"
elif [ -z "$LOCAL_VARS" ]; then
    # Default to edition-based path in IIAB repo
    IIAB_VARS_PATH="vars/local_vars_${EDITION}.yml"
else
    # Absolute path - use directly
    IIAB_VARS_PATH=""
fi

# Step 1: Mount Debian image
echo ""
echo "=== Step 1: Preparing Debian base image ==="

if [ -n "$IMAGE_SOURCE" ] && [ -f "$IMAGE_SOURCE" ]; then
    sudo "${NSPAWN_DIR}/mount.sh" "$IMAGE_SOURCE" "$SIZE_MB"
elif [ -f "${NSPAWN_DIR}/debian-13-generic-amd64.img" ]; then
    sudo "${NSPAWN_DIR}/mount.sh" "${NSPAWN_DIR}/debian-13-generic-amd64.img" "$SIZE_MB"
else
    echo "Downloading Debian 13 generic amd64 image..."
    sudo "${NSPAWN_DIR}/mount.sh" \
        "https://cloud.debian.org/images/cloud/trixie/latest/debian-13-generic-amd64.raw" \
        "$SIZE_MB"
fi

# Find the state file
STATE_FILE=$(find "$NSPAWN_DIR" -maxdepth 1 -name "*.state" | head -n 1)
if [ -z "$STATE_FILE" ]; then
    echo "Error: No .state file found after mount" >&2
    exit 1
fi

# shellcheck source=/dev/null
source "$STATE_FILE"
echo "Mount directory: $MOUNT_DIR"

# Step 2: Prepare the container rootfs
echo ""
echo "=== Step 2: Preparing container rootfs ==="

# Install IIAB code
mkdir -p "$MOUNT_DIR/opt/iiab"

# Determine IIAB source: clone from repository
echo "Cloning IIAB from $IIAB_REPO (branch: $IIAB_BRANCH)..."
# Handle refspec for PRs
if [[ "$IIAB_BRANCH" == refs/pull/* ]]; then
    git clone --depth 1 "$IIAB_REPO" "$MOUNT_DIR/opt/iiab/iiab"
    # Fetch the specific PR ref
    (cd "$MOUNT_DIR/opt/iiab/iiab" && \
        git fetch --depth 1 "$IIAB_REPO" "$IIAB_BRANCH" && \
        git checkout FETCH_HEAD)
else
    git clone --depth 1 --branch "$IIAB_BRANCH" "$IIAB_REPO" "$MOUNT_DIR/opt/iiab/iiab" 2>/dev/null || \
        git clone --depth 1 "$IIAB_REPO" "$MOUNT_DIR/opt/iiab/iiab"
fi

# Install IIAB configuration
mkdir -p "$MOUNT_DIR/etc/iiab"

# Copy local_vars from the cloned IIAB repo in the container
VARS_COPIED=false
if [ -n "$IIAB_VARS_PATH" ]; then
    CONTAINER_VARS_FILE="$MOUNT_DIR/opt/iiab/iiab/$IIAB_VARS_PATH"
    if [ -f "$CONTAINER_VARS_FILE" ]; then
        echo "Copying local_vars from IIAB repo: $IIAB_VARS_PATH"
        cp --preserve=mode,timestamps "$CONTAINER_VARS_FILE" "$MOUNT_DIR/etc/iiab/local_vars.yml"
        VARS_COPIED=true
    else
        echo "Error: local_vars not found at $IIAB_VARS_PATH in IIAB repo" >&2
        echo "  Expected: $CONTAINER_VARS_FILE" >&2
        echo "  Check that the file exists in this branch/ref of $IIAB_REPO" >&2
        exit 1
    fi
fi

if ! $VARS_COPIED && [ -n "$LOCAL_VARS" ] && [[ "$LOCAL_VARS" == /* ]] && [ -f "$LOCAL_VARS" ]; then
    # Fallback: use absolute path from host if provided and exists
    echo "Using local_vars from host: $LOCAL_VARS"
    cp --preserve=mode,timestamps "$LOCAL_VARS" "$MOUNT_DIR/etc/iiab/local_vars.yml"
    VARS_COPIED=true
fi

if ! $VARS_COPIED; then
    echo "Error: No valid local_vars file found" >&2
    echo "  Tried: $IIAB_VARS_PATH (in IIAB repo)" >&2
    echo "  Specify --local-vars with a path relative to the IIAB repo" >&2
    exit 1
fi

# Disable RPi-specific settings
sed -i '/rpi_image: True/d' "$MOUNT_DIR/etc/iiab/local_vars.yml"
sed -i 's/^iiab_admin_user_install: True/iiab_admin_user_install: False/' "$MOUNT_DIR/etc/iiab/local_vars.yml"

# Set hostname
echo "$NAME" > "$MOUNT_DIR/etc/hostname"

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

# Create a robust build script to run inside the container
cat > "$MOUNT_DIR/root/run_build.sh" << 'EOF_SCRIPT'
#!/bin/bash
set -euo pipefail
apt update
DEBIAN_FRONTEND=noninteractive apt upgrade -y
curl -fLo /usr/sbin/iiab https://raw.githubusercontent.com/iiab/iiab-factory/master/iiab
chmod 0755 /usr/sbin/iiab
/usr/sbin/iiab --risky
EOF_SCRIPT
chmod +x "$MOUNT_DIR/root/run_build.sh"

export MOUNT_DIR
expect << 'EXPECT_EOF'
set timeout 7200

spawn systemd-nspawn -q --network-veth --resolv-conf=off -D $env(MOUNT_DIR) -M box --boot

expect "login: " { send "root\r" }

expect -re {#\s?$} { send "/root/run_build.sh || echo 'IIAB_BUILD_FAILED'\r" }

expect {
    timeout { puts "\nTimed out waiting for IIAB install"; exit 1 }
    "IIAB_BUILD_FAILED" { puts "\nIIAB build script failed"; exit 1 }
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
echo "$NAME" > "$MOUNT_DIR/.iiab-image"
echo "Build date: $(date -u +%Y-%m-%dT%H:%M:%SZ)" >> "$MOUNT_DIR/.iiab-image"
echo "Edition: $EDITION" >> "$MOUNT_DIR/.iiab-image"
echo "Branch: $IIAB_BRANCH" >> "$MOUNT_DIR/.iiab-image"
echo "Repo: $IIAB_REPO" >> "$MOUNT_DIR/.iiab-image"

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
DEST="/var/lib/machines/${NAME}.raw"

# Find the resulting image
if [ -f "${NSPAWN_DIR}/${IMG_FILE}" ]; then
    sudo mv "${NSPAWN_DIR}/${IMG_FILE}" "$DEST"
elif [ -f "${NSPAWN_DIR}/${IMG_FILE}.img" ]; then
    sudo mv "${NSPAWN_DIR}/${IMG_FILE}.img" "$DEST"
else
    echo "Warning: Could not find resulting image file" >&2
    echo "Look in ${NSPAWN_DIR}/ for *.img files" >&2
    exit 1
fi

echo ""
echo "=========================================="
echo "Build complete!"
echo "Image: $DEST"
echo "=========================================="
