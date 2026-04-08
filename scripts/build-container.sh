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
VOLATILE="overlay"
IP=""
RAM_IMAGE=false
LOCAL_VARS=""
IMAGE_SOURCE=""
BUILD_ON_DISK=false
SKIP_INSTALL=false
CONFIG_PATH=""

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
        --skip-install) SKIP_INSTALL=true; shift ;;
        --config)     CONFIG_PATH="$2"; shift 2 ;;
        *)
            echo "Warning: Unknown option: $1" >&2
            shift
            ;;
    esac
done

# Normalize repo URL: add https:// if missing
if [[ "$IIAB_REPO" == github.com/* ]]; then
    IIAB_REPO="https://$IIAB_REPO"
fi

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
        echo "Warning: Mount still active at $MOUNT_DIR -- cleaning up" >&2
        umount -l "$MOUNT_DIR" 2>/dev/null || true
    fi
    if [ -n "$LOOPDEV" ] && losetup "$LOOPDEV" &>/dev/null; then
        echo "Warning: Loop device $LOOPDEV still attached -- detaching" >&2
        losetup --detach "$LOOPDEV" 2>/dev/null || true
    fi
    # For non-RAM builds from RAM, clean up only our own build tmpfs
    if $TMPFS_MOUNTED && ! $RAM_IMAGE && mountpoint -q "$BUILD_DIR" 2>/dev/null; then
        echo "Cleaning up build tmpfs for $NAME..."
        umount "$BUILD_DIR" 2>/dev/null || true
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
    if ! git clone --depth 1 --branch "$IIAB_BRANCH" "$IIAB_REPO" "$MOUNT_DIR/opt/iiab/iiab"; then
        echo "Error: Failed to clone IIAB repo branch '$IIAB_BRANCH' from $IIAB_REPO" >&2
        exit 1
    fi
fi

# Resolve local_vars path
# Priority: (1) relative path from host cwd → copy into image, (2) path inside IIAB repo
if [ -n "$LOCAL_VARS" ]; then
    if [[ "$LOCAL_VARS" != /* ]] && [ -f "$LOCAL_VARS" ]; then
        # Relative path exists on host -- copy into the IIAB repo inside the image
        RELATIVE_VARS_DIR=$(dirname "$LOCAL_VARS")
        mkdir -p "$MOUNT_DIR/opt/iiab/iiab/$RELATIVE_VARS_DIR"
        cp --preserve=mode,timestamps "$LOCAL_VARS" "$MOUNT_DIR/opt/iiab/iiab/$LOCAL_VARS"
        IIAB_VARS_PATH="$LOCAL_VARS"
        echo "Copied local_vars from host: $LOCAL_VARS → IIAB repo in image"
    elif [[ "$LOCAL_VARS" != /* ]]; then
        # Relative path but file not on host -- assume it's relative to IIAB repo root
        IIAB_VARS_PATH="$LOCAL_VARS"
    elif [[ "$LOCAL_VARS" == /* ]]; then
        # Absolute path -- handled later by host-path fallback
        IIAB_VARS_PATH=""
    fi
elif [ -z "$LOCAL_VARS" ]; then
    IIAB_VARS_PATH="vars/local_vars_${NAME}.yml"
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

# Set image-building flag so services know they're building an image (not running live)
# This prevents service restarts during build and lets them use 'stopped' instead

# Set hostname
echo "$NAME" > "$MOUNT_DIR/etc/hostname"

# Set default target to multi-user (no GUI needed in containers)
ln -sf /usr/lib/systemd/system/multi-user.target "$MOUNT_DIR/etc/systemd/system/default.target"

# Configure container IP via systemd-networkd inside the image
# With --network-bridge, nspawn creates a veth pair:
#   host side: vb-<machine> bridged to iiab-br0
#   container side: host0
mkdir -p "$MOUNT_DIR/etc/systemd/network"
cat > "$MOUNT_DIR/etc/systemd/network/ve-default.network" << EOF
[Match]
Name=host0 host-* eth0 ve-*

[Network]
Address=$IP/24
Gateway=$IIAB_GW
DNS=8.8.8.8
DNS=1.1.1.1
EOF

# Ensure systemd-networkd is enabled in the rootfs
ln -sf /usr/lib/systemd/system/systemd-networkd.service "$MOUNT_DIR/etc/systemd/system/multi-user.target.wants/systemd-networkd.service"
# Disable systemd-resolved so it doesn't overwrite resolv.conf
rm -f "$MOUNT_DIR/etc/systemd/system/sysinit.target.wants/systemd-resolved.service" 2>/dev/null || true
rm -f "$MOUNT_DIR/etc/systemd/system/multi-user.target.wants/systemd-resolved.service" 2>/dev/null || true

# Container and hardware-specific overrides
cat >> "$MOUNT_DIR/etc/iiab/local_vars.yml" << 'EOF'
is_container: True
iiab_admin_user_install: False
sshd_install: False
sshd_enabled: False
tailscale_install: False
tailscale_enabled: False
remoteit_install: False
remoteit_enabled: False
transmission_install: False
transmission_enabled: False
EOF

###############################################################################
# Step 3: Run IIAB installer inside nspawn
###############################################################################
if $SKIP_INSTALL; then
    echo ""
    echo "=== Step 3: SKIPPED (--skip-install) ==="
    # Still need basic container setup that the install step would have done
    systemd-firstboot --root="$MOUNT_DIR" --delete-root-password --force

    # Quick boot test to generate SSH keys and lock root
    setup_bridge
    export MOUNT_DIR IIAB_BRIDGE IIAB_GW IIAB_IP=$IP
    expect << 'EXPECT_EOF'
set timeout 60

spawn systemd-nspawn -q --network-bridge=$env(IIAB_BRIDGE) --resolv-conf=replace-static -D $env(MOUNT_DIR) -M box --boot

expect "login: " { send "root\r" }
expect -re {#\s?$} { send "ssh-keygen -A\r" }
expect -re {#\s?$} { send "usermod --lock --expiredate=1 root\r" }
expect -re {#\s?$} { send "shutdown now\r" }
expect eof
EXPECT_EOF
else
    echo ""
    echo "=== Step 3: Running IIAB installer (this takes 30-60 minutes) ==="

    # Network setup
    setup_bridge
    sysctl -w net.ipv4.ip_forward=1

    # Set up NAT/masquerade and isolation rules
    EXT_IF=$(ip route | grep default | awk '{print $5}' | head -n1)
    if [ -n "$EXT_IF" ]; then
        setup_nftables_nat "$EXT_IF"
        add_container_isolation
    fi

    systemd-firstboot --root="$MOUNT_DIR" --delete-root-password --force

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

    export MOUNT_DIR IIAB_BRIDGE IIAB_GW IIAB_IP=$IP
    expect << 'EXPECT_EOF'
set timeout 7200

spawn systemd-nspawn -q --network-bridge=$env(IIAB_BRIDGE) --resolv-conf=off -D $env(MOUNT_DIR) -M box --boot

expect "login: " { send "root\r" }
expect -re {#\s?$} { send "export PAGER=cat SYSTEMD_PAGER=cat\r" }

# Configure network manually (systemd-networkd may not be running in cloud image)
expect -re {#\s?$} { send "ip addr add $env(IIAB_IP)/24 dev host0\r" }
expect -re {#\s?$} { send "ip link set host0 up\r" }
expect -re {#\s?$} { send "ip route add default via $env(IIAB_GW)\r" }
expect -re {#\s?$} { send "rm -f /etc/resolv.conf && printf 'nameserver 8.8.8.8\\nnameserver 1.1.1.1\\n' > /etc/resolv.conf\r" }

# Debian cloud image prep: generate SSH host keys (Ansible starts SSH later; needed on Debian)
expect -re {#\s?$} { send "ssh-keygen -A\r" }

expect -re {#\s?$} { send "/root/run_build.sh; echo \"BUILD_EXIT_CODE:\$?\"\r" }

expect {
    timeout { puts "\nTimed out waiting for IIAB install"; exit 1 }
    -re "BUILD_EXIT_CODE:(\[0-9\]+)" {
        set exit_code $expect_out(1,string)
        if { $exit_code != 0 } {
            puts "\nIIAB build script failed with exit code: $exit_code"
            exit 1
        }
    }
    "photographed" {
        send "\r"
        exp_continue
    }
}

expect "login: " { send "root\r" }

expect -re {#\s?$} { send "usermod --lock --expiredate=1 root\r" }
expect -re {#\s?$} { send "shutdown now\r" }
expect eof
EXPECT_EOF

    echo ""
    echo "=== IIAB install complete ==="
fi

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

# Calculate minimal partition size from filesystem metadata
ROOTFS_BLOCKSIZE=$(tune2fs -l "$PARTDEV" | grep "^Block size" | awk '{print $NF}')
ROOTFS_BLOCKCOUNT=$(tune2fs -l "$PARTDEV" | grep "^Block count" | awk '{print $NF}')
ROOTFS_PARTSIZE=$((ROOTFS_BLOCKCOUNT * ROOTFS_BLOCKSIZE))
# 50MB buffer covers ext4 journal (~128MB reserved, typically <32MB active) + alignment slack.
# resize2fs -M already produces the exact minimum, so this is pure safety margin.
BUFFER_SIZE_MB=50
BUFFER_SIZE=$((BUFFER_SIZE_MB * 1024 * 1024))
TARGET_PARTITION_SIZE=$((ROOTFS_PARTSIZE + BUFFER_SIZE))

# Align to 1 MiB boundary for safety
ALIGN=$((1024 * 1024))
TARGET_PARTITION_SIZE=$(( ((TARGET_PARTITION_SIZE + ALIGN - 1) / ALIGN) * ALIGN ))

# Get device sector size
SECTOR_SIZE=$(blockdev --getss "$LOOPDEV" 2>/dev/null || echo 512)
echo "Sector size: $SECTOR_SIZE"

echo "Filesystem: $(( ROOTFS_PARTSIZE / 1024 / 1024 ))MB, target partition: $(( TARGET_PARTITION_SIZE / 1024 / 1024 ))MB"

# Detach loop device -- we'll work on the image file directly from here
losetup --detach "$LOOPDEV"
LOOPDEV=""

# Recreate partition 1 at exact target size using sgdisk on the image file
# (avoids kernel loop-device caching and GPT size-mismatch issues)
SGDISK_INFO=$(sgdisk -i 1 "$WORK_IMG" 2>/dev/null || true)
PART_START_SECTOR=$(echo "$SGDISK_INFO" | awk '/^First sector:/ {print $3}')
# GUID is field 4: "Partition GUID code: UUID (description)"
PART_TYPE_CODE=$(echo "$SGDISK_INFO" | awk '/^Partition GUID code:/ {print $4}')

TARGET_SECTORS=$((TARGET_PARTITION_SIZE / SECTOR_SIZE))
PART_END_SECTOR=$((PART_START_SECTOR + TARGET_SECTORS - 1))

echo "Recreating partition 1 on image: sector ${PART_START_SECTOR} to ${PART_END_SECTOR}"

# Delete and recreate with exact bounds directly on the image file
sgdisk -d 1 "$WORK_IMG" >/dev/null 2>&1
sgdisk -n "1:${PART_START_SECTOR}:${PART_END_SECTOR}" \
    -t "1:${PART_TYPE_CODE:-1}" \
    "$WORK_IMG" >/dev/null 2>&1

# Truncate image: partition end + 1 sector + GPT backup (~34 sectors), aligned
GPT_BACKUP_SECTORS=34
TRUNCATE_SECTORS=$((PART_END_SECTOR + 1 + GPT_BACKUP_SECTORS))
NEW_SIZE=$((TRUNCATE_SECTORS * SECTOR_SIZE))
# Align to 1 MiB boundary
NEW_SIZE=$(( ((NEW_SIZE + ALIGN - 1) / ALIGN) * ALIGN ))

echo "Truncating image to $(( NEW_SIZE / 1024 / 1024 ))MB..."
truncate -s "$NEW_SIZE" "$WORK_IMG"

# Fix GPT backup header for the new (smaller) disk size
sgdisk -e "$WORK_IMG" >/dev/null 2>&1

# Reattach loop device with partition scanning so we can expand the filesystem
LOOPDEV=$(losetup --find "$WORK_IMG" --nooverlap --show --partscan)
PARTDEV="${LOOPDEV}p1"
for _ in $(seq 1 30); do
    [ -b "${LOOPDEV}p1" ] && break
    sleep 1
done
if [ ! -b "${LOOPDEV}p1" ]; then
    echo "Error: Partition ${LOOPDEV}p1 not found after reattach" >&2
    exit 1
fi

# Expand filesystem to fill the (now smaller) partition
echo "Expanding filesystem to fill resized partition..."
e2fsck -p -f "$PARTDEV" || true
if ! resize2fs "$PARTDEV" 2>&1; then
    echo "Error: resize2fs failed, filesystem does not match partition size" >&2
    exit 1
fi
tune2fs -m 1 "$PARTDEV" >/dev/null 2>&1

# Final detach
losetup --detach "$LOOPDEV"
LOOPDEV=""

# Update config with actual image size (the --size value was a max, not the real usage)
ACTUAL_SIZE_MB=$(( ($(stat -c %s "$WORK_IMG") + 1048575) / 1048576 ))  # round up to MB
if [ -n "$CONFIG_PATH" ] && [ -f "$CONFIG_PATH" ]; then
    sed -i "s/^IMAGE_SIZE_MB=.*/IMAGE_SIZE_MB=$ACTUAL_SIZE_MB/" "$CONFIG_PATH"
    echo "Updated config IMAGE_SIZE_MB: $SIZE_MB -> $ACTUAL_SIZE_MB"
fi

echo "Image shrunk successfully (${ACTUAL_SIZE_MB}MB)"

###############################################################################
# Step 5: Register the image
###############################################################################
echo ""
echo "=== Step 5: Registering container image ==="

mkdir -p /var/lib/machines

if $RAM_IMAGE; then
    # Keep image in RAM -- mount /run/iiab-ramfs if needed.
    # The tmpfs holds all demos' images, so we cap it at ~90% of host RAM.
    # Per-image capacity checks are handled by ramfs-setup.sh.
    if ! mountpoint -q "/run/iiab-ramfs" 2>/dev/null; then
        mkdir -p "/run/iiab-ramfs"
        local_host_ram=$(( $(awk '/MemTotal/ {print int($2/1024)}' /proc/meminfo) * 90 / 100 ))
        echo "Mounting tmpfs at /run/iiab-ramfs (${local_host_ram}MB)..."
        mount -t tmpfs -o "size=${local_host_ram}M,mode=0755" tmpfs "/run/iiab-ramfs"
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
