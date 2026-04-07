#!/usr/bin/env bash
# build-container.sh - Build an IIAB container image with arbitrary config
#
# All builds happen in RAM by default (tmpfs), producing zero disk I/O.
# After shrinking, the image is either kept in RAM (--ram-image) or
# copied to persistent disk. Use --build-on-disk to override.
#
# Usage:
#   build-container.sh --name <name> \
#     --repo <repo> --branch <branch> --size <MB> \
#     --volatile <mode> --ip <ip> [--ram-image] [--build-on-disk] \
#     [--local-vars <path>] [--image-source <path>]
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib-iiab.sh disable=SC1091
source "$SCRIPT_DIR/lib-iiab.sh"

# Defaults
NAME=""
IIAB_REPO="https://github.com/iiab/iiab.git"
IIAB_BRANCH="master"
SIZE_MB=15000
VOLATILE="state"
IP=""
RAM_IMAGE=false
LOCAL_VARS=""
IMAGE_SOURCE=""
BUILD_ON_DISK=false

# Parse arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --name)       NAME="$2"; shift 2 ;;
        --repo)       IIAB_REPO="$2"; shift 2 ;;
        --branch)     IIAB_BRANCH="$2"; shift 2 ;;
        --size)       SIZE_MB="$2"; shift 2 ;;
        --volatile)   VOLATILE="$2"; shift 2 ;;
        --ip)         IP="$2"; shift 2 ;;
        --ram-image)  RAM_IMAGE=true; shift ;;
        --local-vars) LOCAL_VARS="$2"; shift 2 ;;
        --image-source) IMAGE_SOURCE="$2"; shift 2 ;;
        --build-on-disk) BUILD_ON_DISK=true; shift ;;
        *)
            echo "Warning: Unknown option: $1" >&2
            shift
            ;;
    esac
done

# Validate required args
if [ -z "$NAME" ]; then
    echo "Error: --name required" >&2
    exit 1
fi
if [ -z "$IP" ]; then
    echo "Error: --ip required" >&2
    exit 1
fi

echo "=========================================="
echo "Building IIAB container: $NAME"
echo "=========================================="
echo "Branch:       $IIAB_BRANCH"
echo "Repo:         $IIAB_REPO"
echo "Size:         ${SIZE_MB}MB"
echo "Volatile:     $VOLATILE"
echo "IP:           $IP"
echo "RAM image:    $RAM_IMAGE"
echo "Build on disk: $BUILD_ON_DISK"
echo "Local vars:   ${LOCAL_VARS:-(none)}"

###############################################################################
# Resolve base image source
###############################################################################
BASE_IMAGE=""
if [ -n "$IMAGE_SOURCE" ] && [ -f "$IMAGE_SOURCE" ]; then
    BASE_IMAGE="$IMAGE_SOURCE"
elif [ -f "/run/iiab-ramfs/base-image.raw" ]; then
    BASE_IMAGE="/run/iiab-ramfs/base-image.raw"
elif [ -f "/var/lib/iiab-demos/debian-13-generic-amd64.raw" ]; then
    BASE_IMAGE="/var/lib/iiab-demos/debian-13-generic-amd64.raw"
else
    echo "Downloading Debian 13 generic amd64 image..."
    mkdir -p /var/lib/iiab-demos
    curl -fL -o /var/lib/iiab-demos/debian-13-generic-amd64.raw \
        "https://cloud.debian.org/images/cloud/trixie/latest/debian-13-generic-amd64.raw"
    BASE_IMAGE="/var/lib/iiab-demos/debian-13-generic-amd64.raw"
fi

###############################################################################
# Build working directory (tmpfs by default)
###############################################################################
if $BUILD_ON_DISK; then
    BUILD_DIR="/var/lib/iiab-demos/build/${NAME}"
else
    BUILD_DIR="/run/iiab-demos/build/${NAME}"
fi
MOUNT_DIR="${BUILD_DIR}/rootfs"
WORK_IMG="${BUILD_DIR}/work.img"

if ! $BUILD_ON_DISK && ! mountpoint -q "${BUILD_DIR%/*}" 2>/dev/null; then
    mkdir -p "${BUILD_DIR%/*}"
    echo "Mounting tmpfs at ${BUILD_DIR%/*} (${SIZE_MB}MB)..."
    mount -t tmpfs -o "size=${SIZE_MB}M,mode=0755" tmpfs "${BUILD_DIR%/*}"
fi
mkdir -p "$BUILD_DIR" "$MOUNT_DIR"

# Track whether we need to unmount tmpfs at end
TMPFS_MOUNTED=false
if ! $BUILD_ON_DISK && mountpoint -q "${BUILD_DIR%/*}" 2>/dev/null; then
    TMPFS_MOUNTED=true
fi

# Cleanup on exit or failure
LOOPDEV=""
cleanup() {
    if mountpoint -q "$MOUNT_DIR" 2>/dev/null; then
        echo "Warning: Mount still active at $MOUNT_DIR — cleaning up" >&2
        umount -l "$MOUNT_DIR" 2>/dev/null || true
    fi
    if [ -n "$LOOPDEV" ] && losetup "$LOOPDEV" &>/dev/null; then
        echo "Warning: Loop device $LOOPDEV still attached — detaching" >&2
        losetup --detach "$LOOPDEV" 2>/dev/null || true
    fi
    # For non-RAM builds from RAM, clean up the tmpfs entirely
    if $TMPFS_MOUNTED && ! $RAM_IMAGE && mountpoint -q "${BUILD_DIR%/*}" 2>/dev/null; then
        echo "Cleaning up build tmpfs..."
        umount "${BUILD_DIR%/*}" 2>/dev/null || true
    fi
}
trap cleanup EXIT

###############################################################################
# Step 1: Prepare Debian base image
###############################################################################
echo ""
echo "=== Step 1: Preparing Debian base image ==="

# Copy base image to working file (never modify the source in-place)
echo "Copying base image to working file..."
cp --reflink=auto "$BASE_IMAGE" "$WORK_IMG"

# Grow image to target size
CURRENT_BYTES=$(stat -c %s "$WORK_IMG")
CURRENT_MB=$(( CURRENT_BYTES / 1024 / 1024 ))
ADDITIONAL_MB=$(( SIZE_MB - CURRENT_MB ))
if [ "$ADDITIONAL_MB" -gt 0 ]; then
    echo "Growing image from ${CURRENT_MB}MB to ${SIZE_MB}MB..."
    truncate -s "${SIZE_MB}M" "$WORK_IMG"
fi

# Create loop device with partition scanning
LOOPDEV=$(losetup --find "$WORK_IMG" --nooverlap --show --partscan)
echo "Loop device: $LOOPDEV"

# Fix GPT backup header (Debian images are GPT)
if command -v sgdisk &>/dev/null; then
    sgdisk -e "$LOOPDEV" 2>/dev/null || true
fi

# Wait for partition device
for _ in $(seq 1 30); do
    [ -b "${LOOPDEV}p1" ] && break
    sleep 1
done
if [ ! -b "${LOOPDEV}p1" ]; then
    echo "Error: Partition ${LOOPDEV}p1 not found after 30s" >&2
    exit 1
fi

# Resize partition to fill the image
echo "Resizing partition to fill available space..."
parted --script "$LOOPDEV" resizepart 1 100%
sync
partprobe "$LOOPDEV" 2>/dev/null || true
udevadm settle 2>/dev/null || sleep 2

# Resize filesystem
PARTDEV="${LOOPDEV}p1"
echo "Checking and resizing filesystem..."
e2fsck -p -f "$PARTDEV"
resize2fs "$PARTDEV"
e2fsck -p -f "$PARTDEV"

# Mount
echo "Mounting root filesystem at $MOUNT_DIR..."
mount "$PARTDEV" "$MOUNT_DIR"

echo "Base image ready at $MOUNT_DIR ($(du -sh "$MOUNT_DIR" | cut -f1))"

###############################################################################
# Step 2: Prepare the container rootfs
###############################################################################
echo ""
echo "=== Step 2: Preparing container rootfs ==="

# Clone IIAB
echo "Cloning IIAB from $IIAB_REPO (branch: $IIAB_BRANCH)..."
mkdir -p "$MOUNT_DIR/opt/iiab"
if [[ "$IIAB_BRANCH" == refs/pull/* ]]; then
    git clone --depth 1 "$IIAB_REPO" "$MOUNT_DIR/opt/iiab/iiab"
    (cd "$MOUNT_DIR/opt/iiab/iiab" && \
        git fetch --depth 1 "$IIAB_REPO" "$IIAB_BRANCH" && \
        git checkout FETCH_HEAD)
else
    git clone --depth 1 --branch "$IIAB_BRANCH" "$IIAB_REPO" "$MOUNT_DIR/opt/iiab/iiab" 2>/dev/null || \
        git clone --depth 1 "$IIAB_REPO" "$MOUNT_DIR/opt/iiab/iiab"
fi

# Resolve local_vars path
if [[ "$LOCAL_VARS" != /* ]] && [ -n "$LOCAL_VARS" ]; then
    IIAB_VARS_PATH="$LOCAL_VARS"
elif [ -z "$LOCAL_VARS" ]; then
    IIAB_VARS_PATH="vars/local_vars_${NAME}.yml"
else
    IIAB_VARS_PATH=""
fi

# Install IIAB configuration
mkdir -p "$MOUNT_DIR/etc/iiab"

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
        exit 1
    fi
fi

if ! $VARS_COPIED && [ -n "$LOCAL_VARS" ] && [[ "$LOCAL_VARS" == /* ]] && [ -f "$LOCAL_VARS" ]; then
    echo "Using local_vars from host: $LOCAL_VARS"
    cp --preserve=mode,timestamps "$LOCAL_VARS" "$MOUNT_DIR/etc/iiab/local_vars.yml"
    VARS_COPIED=true
fi

if ! $VARS_COPIED; then
    echo "Error: No valid local_vars file found" >&2
    exit 1
fi

# Disable RPi-specific settings
sed -i '/rpi_image: True/d' "$MOUNT_DIR/etc/iiab/local_vars.yml"
sed -i 's/^iiab_admin_user_install: True/iiab_admin_user_install: False/' "$MOUNT_DIR/etc/iiab/local_vars.yml"

# Set hostname
echo "$NAME" > "$MOUNT_DIR/etc/hostname"

# Disable WiFi/hostapd
cat >> "$MOUNT_DIR/etc/iiab/local_vars.yml" << 'EOF'
# Disabled for container deployment
hostapd_install: False
hostapd_enabled: False
captiveportal_install: False
captiveportal_enabled: False
EOF

###############################################################################
# Step 3: Run IIAB installer inside nspawn
###############################################################################
echo ""
echo "=== Step 3: Running IIAB installer (this takes 30-60 minutes) ==="

# Network setup
systemctl is-active --quiet systemd-networkd || systemctl start systemd-networkd
systemctl is-active --quiet systemd-resolved || systemctl start systemd-resolved
sysctl -w net.ipv4.ip_forward=1

EXT_IF=$(ip route | grep default | awk '{print $5}' | head -n1)
setup_iptables_nat "$EXT_IF"

systemd-firstboot --root="$MOUNT_DIR" --delete-root-password --force
rm -f "$MOUNT_DIR/etc/resolv.conf"
echo "nameserver 8.8.8.8" > "$MOUNT_DIR/etc/resolv.conf"

# Build script to run inside container
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

expect "login: " { send "root\r" }

expect -re {#\s?$} { send "usermod --lock --expiredate=1 root\r" }
expect -re {#\s?$} { send "shutdown now\r" }
expect eof
EXPECT_EOF

echo ""
echo "=== IIAB install complete ==="

###############################################################################
# Step 4: Shrink the image
###############################################################################
echo ""
echo "=== Step 4: Shrinking image ==="

# Add metadata before cleanup
{
    echo "$NAME"
    echo "Build date: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
    echo "Branch: $IIAB_BRANCH"
    echo "Repo: $IIAB_REPO"
} >> "$MOUNT_DIR/.iiab-image"

# Clean up
echo uninitialized > "$MOUNT_DIR/etc/machine-id"
rm -f "$MOUNT_DIR/etc/iiab/uuid"
rm -f "$MOUNT_DIR/var/swap"
touch "$MOUNT_DIR/.resize-rootfs"

# Clean rootfs via nspawn
systemd-nspawn -q -D "$MOUNT_DIR" --pipe /bin/bash -eux << 'CLEANEOF'
apt clean
rm -rf /var/cache/apt/archives/*.deb /var/lib/apt/lists/*
rm -rf /var/cache/man/*
rm -rf /var/cache/fontconfig/*
rm -f /var/log/*log /var/log/*gz
rm -f /etc/ssh/ssh_host_*
rm -f /var/lib/NetworkManager/*.lease
rm -f /var/log/nginx/*.log
rm -rf /root/.cache/*
rm -f /root/.bash_history
journalctl --vacuum-time=1s
CLEANEOF

systemd-firstboot --root="$MOUNT_DIR" --timezone=UTC --force

# Zero-fill free space for better compression
echo "Zero-filling unused blocks..."
(sh -c "cat /dev/zero > '$MOUNT_DIR/zero.fill'" 2>/dev/null || true)
sync
rm -f "$MOUNT_DIR/zero.fill"

# Unmount
echo "Unmounting..."
umount "$MOUNT_DIR"
rmdir "$MOUNT_DIR"
sync

# Shrink filesystem
echo "Shrinking filesystem to minimal size..."
e2fsck -p -f "$PARTDEV"
resize2fs -M "$PARTDEV"

# Calculate partition size with buffer
ROOTFS_BLOCKSIZE=$(tune2fs -l "$PARTDEV" | grep "^Block size" | awk '{print $NF}')
ROOTFS_BLOCKCOUNT=$(tune2fs -l "$PARTDEV" | grep "^Block count" | awk '{print $NF}')
ROOTFS_PARTSIZE=$((ROOTFS_BLOCKCOUNT * ROOTFS_BLOCKSIZE))
BUFFER_SIZE_MB=200
BUFFER_SIZE=$((BUFFER_SIZE_MB * 1024 * 1024))
TARGET_USER_SPACE=$((ROOTFS_PARTSIZE + BUFFER_SIZE))
TOTAL_REQUIRED_SIZE=$(( (TARGET_USER_SPACE * 1011) / 1000 ))  # /0.99 ≈ *1.011

PART_INFO=$(parted -m --script "$LOOPDEV" unit B print | grep "^1:")
ROOTFS_PARTSTART=$(echo "$PART_INFO" | awk -F ":" '{print $2}' | tr -d 'B')
ROOTFS_PARTNEWEND=$((ROOTFS_PARTSTART + TOTAL_REQUIRED_SIZE - 1))

echo "Resizing partition to ${ROOTFS_PARTNEWEND}..."
(yes Yes | parted ---pretend-input-tty "$LOOPDEV" unit b resizepart 1 "$ROOTFS_PARTNEWEND" || true) >/dev/null 2>&1
sync
partprobe "$LOOPDEV" 2>/dev/null || true
udevadm settle 2>/dev/null || sleep 2

# Expand filesystem to fill new partition
echo "Expanding filesystem to fill partition..."
e2fsck -p -f "$PARTDEV" || true
resize2fs "$PARTDEV" >/dev/null 2>&1
tune2fs -m 1 "$PARTDEV" >/dev/null 2>&1

# Detach loop device before truncation
losetup --detach "$LOOPDEV"
LOOPDEV=""

# Get free space after partition and truncate
PART_TYPE=$(blkid -o value -s PTTYPE "$WORK_IMG")
FREE_SPACE=$(parted -m --script "$WORK_IMG" unit B print free | tail -1)

if [[ "$FREE_SPACE" =~ "free" ]]; then
    NEW_SIZE=$(echo "$FREE_SPACE" | awk -F ":" '{print $2}' | tr -d 'B')
    if [[ "$PART_TYPE" == "gpt" ]]; then
        NEW_SIZE=$((NEW_SIZE + 1048576))  # GPT backup header
    fi

    echo "Truncating image to $(( NEW_SIZE / 1024 / 1024 ))MB..."
    truncate -s "$NEW_SIZE" "$WORK_IMG"

    if [[ "$PART_TYPE" == "gpt" ]]; then
        sgdisk -e "$WORK_IMG" >/dev/null 2>&1
    fi
fi

echo "Image shrunk successfully ($(du -sm "$WORK_IMG" | cut -f1)MB)"

###############################################################################
# Step 5: Register the image
###############################################################################
echo ""
echo "=== Step 5: Registering container image ==="

mkdir -p /var/lib/machines

if $RAM_IMAGE; then
    # Keep image in RAM — mount /run/iiab-ramfs if needed
    if ! mountpoint -q "/run/iiab-ramfs" 2>/dev/null; then
        mkdir -p "/run/iiab-ramfs"
        ram_size=$(( SIZE_MB * 11 / 10 ))
        echo "Mounting tmpfs at /run/iiab-ramfs (${ram_size}MB)..."
        mount -t tmpfs -o "size=${ram_size}M,mode=0755" tmpfs "/run/iiab-ramfs"
    fi

    # Move from build location to RAM image store
    DEST="/run/iiab-ramfs/${NAME}.raw"
    mv "$WORK_IMG" "$DEST"

    # Symlink so systemd-nspawn can find it
    ln -sf "$DEST" "/var/lib/machines/${NAME}.raw"
    echo ""
    echo "=========================================="
    echo "Build complete (RAM image)!"
    echo "Image: $DEST (symlinked to /var/lib/machines/${NAME}.raw)"
    echo "=========================================="
else
    # Copy final image to persistent disk
    DEST="/var/lib/machines/${NAME}.raw"
    cp --reflink=auto "$WORK_IMG" "$DEST"
    echo ""
    echo "=========================================="
    echo "Build complete!"
    echo "Image: $DEST"
    echo "=========================================="
fi

# Clean up build directory
rm -rf "$BUILD_DIR"
