#!/usr/bin/env bash
# build-container.sh - Build an IIAB container image with arbitrary config
#
# All builds happen in RAM by default (tmpfs), producing zero disk I/O.
# Uses btrfs CoW snapshots to share a common Debian base across builds.
# After shrinking, the image is either kept in RAM (--ram-image) or
# copied to persistent disk. Use --build-on-disk to override.
#
# Usage:
#   build-container.sh --name <name> \
#     --repo <repo> --branch <branch> --size <MB> \
#     --volatile <mode> --ip <ip> [--ram-image] [--build-on-disk] \
#     [--local-vars <path>] [--config <path>]
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
# Shared btrfs base image
###############################################################################
BASE_DIR="/var/lib/iiab-demos"
BASE_BTRFS="$BASE_DIR/base-debian.btrfs"
BASE_MOUNT="$BASE_DIR/base-debian"

# Prepare the shared Debian base btrfs image (runs once, then reused)
prepare_base_image() {
    if [ -f "$BASE_BTRFS" ]; then
        echo "Base image already exists: $BASE_BTRFS"
        return 0
    fi

    mkdir -p "$BASE_DIR"

    # Download Debian cloud image
    local raw_file="$BASE_DIR/debian-13-generic-amd64.raw"
    if [ ! -f "$raw_file" ]; then
        echo "Downloading Debian 13 generic amd64 image..."
        curl -fL -o "$raw_file" \
            "https://cloud.debian.org/images/cloud/trixie/latest/debian-13-generic-amd64.raw"
    fi

    # Create backing file for the btrfs base
    echo "Creating base btrfs image: $BASE_BTRFS (5GB)..."
    fallocate -l 5G "$BASE_BTRFS"
    # Prevent nested CoW if host filesystem is btrfs
    chattr +C "$BASE_BTRFS" 2>/dev/null || true

    mkfs.btrfs -f -L debian-base "$BASE_BTRFS" >/dev/null 2>&1
    mkdir -p "$BASE_MOUNT"
    mount -o loop "$BASE_BTRFS" "$BASE_MOUNT"

    # Extract rootfs from Debian .raw into btrfs using systemd-dissect
    local extract_dir
    extract_dir=$(mktemp -d /tmp/debian-extract.XXXXXX)
    echo "Extracting Debian rootfs from .raw image..."
    systemd-dissect -M "$raw_file" "$extract_dir" 2>/dev/null || {
        echo "Error: systemd-dissect failed to mount $raw_file" >&2
        umount "$BASE_MOUNT" 2>/dev/null || true
        rm -rf "$BASE_MOUNT" "$BASE_BTRFS" "$extract_dir"
        exit 1
    }

    echo "Copying rootfs into btrfs image..."
    cp -a "$extract_dir/"* "$BASE_MOUNT/" 2>/dev/null || true
    cp -a "$extract_dir/."[^.]* "$BASE_MOUNT/" 2>/dev/null || true

    # Clean up
    umount "$extract_dir" 2>/dev/null || true
    rm -rf "$extract_dir"

    # Clean up extracted image metadata
    rm -f "$BASE_MOUNT"/etc/machine-id "$BASE_MOUNT"/etc/hostname

    echo "Base image ready: $BASE_BTRFS ($(du -sh "$BASE_MOUNT" | cut -f1))"
}

###############################################################################
# Build working directory (tmpfs by default)
###############################################################################
if $BUILD_ON_DISK; then
    BUILD_DIR="/var/lib/iiab-demos/build/${NAME}"
else
    BUILD_DIR="/run/iiab-demos/build/${NAME}"
fi
MOUNT_DIR="${BUILD_DIR}/rootfs"
# WORK_BTRFS is the btrfs image file for this build
WORK_BTRFS="${BUILD_DIR}/work.btrfs"

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
        umount -l "$MOUNT_DIR" 2>/dev/null || true
    fi
    if [ -n "$LOOPDEV" ] && losetup "$LOOPDEV" &>/dev/null; then
        losetup --detach "$LOOPDEV" 2>/dev/null || true
    fi
    # For non-RAM builds from RAM, clean up only our own build tmpfs
    if $TMPFS_MOUNTED && ! $RAM_IMAGE && mountpoint -q "$BUILD_DIR" 2>/dev/null; then
        umount "$BUILD_DIR" 2>/dev/null || true
    fi
}
trap cleanup EXIT

###############################################################################
# Step 1: Snapshot Debian base into independent btrfs image
###############################################################################
echo ""
echo "=== Step 1: Preparing Debian base from shared btrfs image ==="

prepare_base_image

# Mount the base btrfs if not already mounted
if ! mountpoint -q "$BASE_MOUNT" 2>/dev/null; then
    mkdir -p "$BASE_MOUNT"
    mount -o loop "$BASE_BTRFS" "$BASE_MOUNT"
fi

# Create an independent btrfs file for this build (starts small, grows on demand)
# The backing file is sparse -- actual space used matches written data
echo "Creating build btrfs image: $WORK_BTRFS (${SIZE_MB}MB capacity)..."
fallocate -l 0 "$WORK_BTRFS"
truncate -s "${SIZE_MB}M" "$WORK_BTRFS"
mkfs.btrfs -f -L "$NAME" "$WORK_BTRFS" >/dev/null 2>&1

# Mount the build image
mkdir -p "$MOUNT_DIR"
mount -o loop "$WORK_BTRFS" "$MOUNT_DIR"

# Receive the base into our build image via btrfs send (creates independent copy)
# The base data is transferred efficiently; the build image is now standalone
echo "Receiving base snapshot into build image..."
btrfs subvolume snapshot -r "$BASE_MOUNT" "$BASE_MOUNT/.iiab-send-snap" >/dev/null
btrfs send "$BASE_MOUNT/.iiab-send-snap" | btrfs receive -d "$MOUNT_DIR" rootfs >/dev/null 2>&1
btrfs subvolume delete "$BASE_MOUNT/.iiab-send-snap" >/dev/null 2>&1 || true

# Remount so $MOUNT_DIR is the rootfs directly (no subvolume path needed in Steps 2-3)
umount "$MOUNT_DIR"
mount -o loop,subvol=rootfs "$WORK_BTRFS" "$MOUNT_DIR"

echo "Base image snapshot ready at $MOUNT_DIR ($(du -sh "$MOUNT_DIR" | cut -f1))"

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

# Configure container networking via systemd one-shot service
# (More reliable than systemd-networkd in cloud images)
# With --network-bridge, nspawn creates a veth pair:
#   host side: vb-<machine> bridged to iiab-br0
#   container side: host0
cat > "$MOUNT_DIR/etc/systemd/system/iiab-network-setup.service" << EOF
[Unit]
Description=Configure IIAB container network
Before=network-online.target
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=/usr/sbin/ip addr add $IP/24 dev host0
ExecStart=/usr/sbin/ip link set host0 up
ExecStart=/usr/sbin/ip route add default via $IIAB_GW
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
EOF
ln -sf /etc/systemd/system/iiab-network-setup.service "$MOUNT_DIR/etc/systemd/system/multi-user.target.wants/iiab-network-setup.service"

# Write resolv.conf directly
rm -f "$MOUNT_DIR/etc/resolv.conf"
cat > "$MOUNT_DIR/etc/resolv.conf" << EOF
nameserver 8.8.8.8
nameserver 1.1.1.1
EOF

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

spawn systemd-nspawn -q --network-bridge=$env(IIAB_BRIDGE) --resolv-conf=off -D $env(MOUNT_DIR) -M box --boot

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

echo "=== Network state ==="
ip addr show 2>&1 || true
ip route show 2>&1 || true
cat /etc/resolv.conf 2>&1 || true

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
# Step 4: Shrink the btrfs image
###############################################################################
echo ""
echo "=== Step 4: Shrinking btrfs image ==="

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

# Zero-fill free space so btrfs balance can reclaim it
echo "Zero-filling unused blocks..."
(sh -c "cat /dev/zero > '$MOUNT_DIR/zero.fill'" 2>/dev/null || true)
sync
rm -f "$MOUNT_DIR/zero.fill"

# Unmount before shrinking
echo "Unmounting..."
umount "$MOUNT_DIR"
rmdir "$MOUNT_DIR"
sync

# Re-mount to run btrfs operations
mkdir -p "$MOUNT_DIR"
mount -o loop "$WORK_BTRFS" "$MOUNT_DIR"

# Balance to compact all data to the beginning of the filesystem
echo "Balancing btrfs to compact data..."
btrfs balance start -m -d -v "$MOUNT_DIR" 2>&1 | tail -1

# Get actual used space and calculate target size
USED_KB=$(btrfs filesystem usage --raw "$MOUNT_DIR" 2>/dev/null | \
    awk '/Device size:/ {print $NF}' || \
    btrfs filesystem df "$MOUNT_DIR" 2>/dev/null | awk '/Data/ {used+=$4} END {print used*1024}')
# Fallback: just use du
if [ -z "$USED_KB" ] || [ "$USED_KB" = "0" ]; then
    USED_KB=$(du -sk "$MOUNT_DIR" | cut -f1)
fi

# Add 100MB buffer for btrfs metadata and safety
BUFFER_KB=$((100 * 1024))
TARGET_KB=$((USED_KB + BUFFER_KB))
# Round up to nearest MiB
TARGET_MB=$(( (TARGET_KB + 1023) / 1024 ))

echo "Used: $(( USED_KB / 1024 ))MB, target: ${TARGET_MB}MB"

# Shrink the filesystem
btrfs filesystem resize "${TARGET_MB}M" "$MOUNT_DIR" >/dev/null 2>&1 || true

# Unmount and truncate the backing file
umount "$MOUNT_DIR"
rmdir "$MOUNT_DIR"

# Add small safety margin and align to 1 MiB
NEW_SIZE=$(( (TARGET_MB + 10) * 1024 * 1024 ))
ALIGN=$((1024 * 1024))
NEW_SIZE=$(( ((NEW_SIZE + ALIGN - 1) / ALIGN) * ALIGN ))

echo "Truncating image to $(( NEW_SIZE / 1024 / 1024 ))MB..."
truncate -s "$NEW_SIZE" "$WORK_BTRFS"

# Update config with actual image size (the --size value was a max, not the real usage)
ACTUAL_SIZE_MB=$(( NEW_SIZE / 1024 / 1024 ))
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
    mv "$WORK_BTRFS" "$DEST"

    # Symlink so systemd-nspawn can find it (systemd-dissect auto-detects btrfs)
    ln -sf "$DEST" "/var/lib/machines/${NAME}.raw"
    echo ""
    echo "=========================================="
    echo "Build complete (RAM image)!"
    echo "Image: $DEST (symlinked to /var/lib/machines/${NAME}.raw)"
    echo "=========================================="
else
    # Copy final image to persistent disk
    DEST="/var/lib/machines/${NAME}.raw"
    cp --reflink=auto "$WORK_BTRFS" "$DEST"
    echo ""
    echo "=========================================="
    echo "Build complete!"
    echo "Image: $DEST"
    echo "=========================================="
fi

# Clean up build directory
rm -rf "$BUILD_DIR"
