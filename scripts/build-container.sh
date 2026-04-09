#!/usr/bin/env bash
# build-container.sh - Build an IIAB container image with arbitrary config
#
# Builds use btrfs CoW snapshots within a single storage.btrfs file.
# The storage file lives in /run/iiab-demos/ (tmpfs, default) or
# /var/iiab-demos/ (disk, --build-on-disk).
#
# Output: a read-only subvolume at <storage>/builds/<name>, symlinked
#         from /var/lib/machines/<name> for systemd-nspawn discovery.
#
# Usage:
#   build-container.sh --name <name> \
#     --repo <repo> --branch <branch> --size <MB> \
#     --volatile <mode> --ip <ip> [--build-on-disk] \
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
VOLATILE_MODE="overlay"
IP=""
LOCAL_VARS=""
BUILD_ON_DISK=false
SKIP_INSTALL=false
CONFIG_PATH=""
BASE_NAME=""

# Btrfs storage sizing
INITIAL_SIZE_GB=20    # Start with 20GB, grow on demand

# Parse arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --name)       NAME="$2"; shift 2 ;;
        --repo)       IIAB_REPO="$2"; shift 2 ;;
        --branch)     IIAB_BRANCH="$2"; shift 2 ;;
        --size)       SIZE_MB="$2"; shift 2 ;;
        --volatile)   VOLATILE_MODE="$2"; shift 2 ;;
        --ip)         IP="$2"; shift 2 ;;
        --local-vars) LOCAL_VARS="$2"; shift 2 ;;
        --build-on-disk) BUILD_ON_DISK=true; shift ;;
        --skip-install) SKIP_INSTALL=true; shift ;;
        --config)     CONFIG_PATH="$2"; shift 2 ;;
        --base)       BASE_NAME="$2"; shift 2 ;;
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
echo "Size:         ${SIZE_MB}MB (capacity)"
echo "Volatile:     $VOLATILE_MODE"
echo "IP:           $IP"
echo "Build on disk: $BUILD_ON_DISK"
echo "Local vars:   ${LOCAL_VARS:-(none)}"

###############################################################################
# Storage setup: single btrfs file with CoW snapshots
###############################################################################
if $BUILD_ON_DISK; then
    DEMO_BASE_DIR="/var/iiab-demos"
else
    DEMO_BASE_DIR="/run/iiab-demos"
fi
STORAGE_BTRFS="$DEMO_BASE_DIR/storage.btrfs"
STORAGE_ROOT="$DEMO_BASE_DIR/storage"
BUILDS_DIR="$STORAGE_ROOT/builds"

# Build mount options based on storage backend
if $BUILD_ON_DISK; then
    MOUNT_OPTS="loop,compress-force=zstd:1,noatime,discard=async"
else
    MOUNT_OPTS="loop,noatime"
fi

# Grow the btrfs file if needed to accommodate the requested size
grow_storage_file() {
    local needed_mb="$1"
    local current_size_gb=0
    if [ -f "$STORAGE_BTRFS" ]; then
        # Get current file size in GB (stat reports virtual size, not disk usage)
        local size_bytes
        size_bytes=$(stat -c%s "$STORAGE_BTRFS" 2>/dev/null || echo 0)
        current_size_gb=$(( size_bytes / 1073741824 ))  # bytes -> GB
    fi

    # Calculate needed size: current usage + needed_mb + 2GB headroom
    local needed_total_mb
    if mountpoint -q "$STORAGE_ROOT" 2>/dev/null; then
        local btrfs_used
        btrfs_used=$(btrfs filesystem df "$STORAGE_ROOT" 2>/dev/null | \
            awk '/Data, single/ {match($0, /used=([0-9.]+)([A-Za-z]+)/, a); if (a[2]=="GiB") printf "%d", a[1]*1024; else if (a[2]=="MiB") printf "%d", a[1]; else print 0}' || echo 0)
        needed_total_mb=$(( ${btrfs_used:-0} + needed_mb + 2048 ))  # 2GB headroom
    else
        needed_total_mb=$(( needed_mb + 2048 ))
    fi

    local target_gb=$(( (needed_total_mb + 1023) / 1024 ))  # Round up to GB

    # Enforce minimum initial size
    if [ "$target_gb" -lt "$INITIAL_SIZE_GB" ]; then
        target_gb=$INITIAL_SIZE_GB
    fi

    # Grow if needed
    if [ "$target_gb" -gt "$current_size_gb" ]; then
        echo "Growing storage.btrfs from ${current_size_gb}G to ${target_gb}G..."
        umount -l "$STORAGE_ROOT" 2>/dev/null || true
        truncate -s "${target_gb}G" "$STORAGE_BTRFS"
        # Remount will happen below in ensure_storage
    fi
}

# Ensure storage.btrfs exists and is mounted
ensure_storage() {
    if mountpoint -q "$STORAGE_ROOT" 2>/dev/null; then
        return 0
    fi

    mkdir -p "$DEMO_BASE_DIR"
    mkdir -p "$STORAGE_ROOT"

    if [ ! -f "$STORAGE_BTRFS" ]; then
        echo "Creating storage.btrfs at $STORAGE_BTRFS (${INITIAL_SIZE_GB}G initial)..."
        if $BUILD_ON_DISK; then
            # Disk storage: pre-allocate for performance
            fallocate -l "${INITIAL_SIZE_GB}G" "$STORAGE_BTRFS"
            chattr +C "$STORAGE_BTRFS" 2>/dev/null || true
        else
            # RAM storage: sparse file in tmpfs (only allocates as written)
            truncate -s "${INITIAL_SIZE_GB}G" "$STORAGE_BTRFS"
        fi
        mkfs.btrfs -f -L iiab-demos "$STORAGE_BTRFS"
    fi

    mount -o "$MOUNT_OPTS" "$STORAGE_BTRFS" "$STORAGE_ROOT"
    mkdir -p "$BUILDS_DIR"
}

# Cleanup on exit or failure
cleanup() {
    # Remove build snapshot only if build failed
    if [ "${BUILD_SUCCESS:-false}" = "true" ]; then
        return 0
    fi
    if btrfs subvolume show "$BUILDS_DIR/$NAME" >/dev/null 2>&1; then
        echo "Cleanup: removing failed build snapshot $NAME"
        btrfs subvolume delete "$BUILDS_DIR/$NAME" >/dev/null 2>&1 || true
    fi
    # Note: storage mount is intentionally left mounted here -- it may be shared
    # across builds. The OS cleans up tmpfs mounts on process exit.
}
trap cleanup EXIT

# Ensure storage is mounted (ensure_storage is idempotent; returns immediately if already mounted)
ensure_storage
mkdir -p "$BUILDS_DIR"

# Grow storage if needed (after mount, so btrfs filesystem df works)
grow_storage_file "$SIZE_MB"

# Resolve base subvolume
if [ -n "$BASE_NAME" ]; then
    if [[ "$BASE_NAME" == /* ]]; then
        BASE_BTRFS="$BASE_NAME"
        BASE_MOUNT="$DEMO_BASE_DIR/$(basename "$BASE_NAME" .btrfs)"
        BASE_SUBVOL="rootfs"
    else
        BASE_SUBVOL="$BASE_NAME"
        BASE_BTRFS="$STORAGE_BTRFS"
        BASE_MOUNT="$STORAGE_ROOT"
    fi
else
    BASE_SUBVOL="base-debian"
    BASE_BTRFS="$STORAGE_BTRFS"
    BASE_MOUNT="$STORAGE_ROOT"
fi

# Mount external base if needed
if [ -n "${BASE_BTRFS:-}" ] && [[ "$BASE_BTRFS" != "$STORAGE_BTRFS" ]]; then
    if ! mountpoint -q "$BASE_MOUNT" 2>/dev/null; then
        mkdir -p "$BASE_MOUNT"
        mount -o "$MOUNT_OPTS" "$BASE_BTRFS" "$BASE_MOUNT"
    fi
fi

###############################################################################
# Step 1: Prepare base (CoW snapshot)
###############################################################################
echo ""
echo "=== Step 1: Preparing base ==="

# Prepare the Debian base subvolume if needed
if [ -z "$BASE_NAME" ]; then
    if ! btrfs subvolume show "$STORAGE_ROOT/base-debian" >/dev/null; then
        # Download and extract Debian into a temp dir, then copy into subvolume
        tmpdir=$(mktemp -d "$DEMO_BASE_DIR/debian-base.XXXXXX")
        tar_url="https://cloud.debian.org/images/cloud/trixie/latest/debian-13-nocloud-amd64.tar.xz"
        echo "Downloading and extracting Debian 13 nocloud raw image..."
        curl -fL "$tar_url" | tar -xJ -C "$tmpdir" || {
            echo "Error: Failed to download/extract Debian rootfs" >&2
            rm -rf "$tmpdir"
            exit 1
        }

        # Find the .raw disk image
        raw_image=$(find "$tmpdir" -name "*.raw" -type f | head -1)
        if [ -z "$raw_image" ]; then
            # Fallback: try .qcow2
            raw_image=$(find "$tmpdir" -name "*.qcow2" -type f | head -1)
            if [ -z "$raw_image" ]; then
                echo "Error: No .raw or .qcow2 image found in tarball" >&2
                echo "Tar contents:" >&2
                find "$tmpdir" -type f | head -20 >&2
                rm -rf "$tmpdir"
                exit 1
            fi
        fi
        echo "Found disk image: $raw_image"

        # Use systemd-dissect to mount and extract the root filesystem
        echo "Extracting root filesystem using systemd-dissect --mount..."
        extract_dir=$(mktemp -d "$DEMO_BASE_DIR/debian-extract.XXXXXX")
        systemd-dissect --mount --mkdir "$raw_image" "$extract_dir" || {
            echo "Error: systemd-dissect mount failed" >&2
            rm -rf "$tmpdir" "$extract_dir"
            exit 1
        }

        echo "Creating base subvolume..."
        btrfs subvolume create "$STORAGE_ROOT/base-debian"
        cp -a --reflink=auto "$extract_dir"/. "$STORAGE_ROOT/base-debian/"

        # Unmount and cleanup
        systemd-dissect --umount "$extract_dir" || true
        rmdir "$extract_dir" 2>/dev/null || true
        rm -rf "$tmpdir"

        rm -f "$STORAGE_ROOT/base-debian"/etc/machine-id "$STORAGE_ROOT/base-debian/etc/hostname"
        btrfs property set "$STORAGE_ROOT/base-debian" ro true
        echo "Base subvolume ready: $STORAGE_ROOT/base-debian ($(du -sh "$STORAGE_ROOT/base-debian" | cut -f1))"
    else
        echo "Base subvolume already exists: $STORAGE_ROOT/base-debian"
    fi
else
    if ! btrfs subvolume show "$BASE_MOUNT/$BASE_SUBVOL" >/dev/null; then
        echo "Error: Base subvolume not found: $BASE_SUBVOL" >&2
        exit 1
    fi
fi

# Create CoW snapshot for this build
BUILD_SUBVOL="$BUILDS_DIR/$NAME"
echo "Creating CoW snapshot of $BASE_SUBVOL..."
btrfs subvolume snapshot "$BASE_MOUNT/$BASE_SUBVOL" "$BUILD_SUBVOL"
echo "Build rootfs: $BUILD_SUBVOL ($(du -sh "$BUILD_SUBVOL" | cut -f1))"

# Verify the snapshot has expected root structure
echo "Verifying base subvolume structure..."
REQUIRED_FILES="/etc/os-release /usr /bin /sbin /lib"
for f in $REQUIRED_FILES; do
    if [ ! -e "$BUILD_SUBVOL$f" ]; then
        echo "Error: Expected path $f not found in snapshot!" >&2
        echo "Snapshot contents (top-level):" >&2
        ls -la "$BUILD_SUBVOL" >&2
        echo "Error: /etc/os-release missing - this usually means tar extracted to a subdirectory" >&2
        echo "  Check the 'Tar extraction contents' output above to confirm" >&2
        exit 1
    fi
done
echo "Base subvolume structure verified (os-release found)"

###############################################################################
# Step 2: Prepare the container rootfs
###############################################################################
echo ""
echo "=== Step 2: Preparing container rootfs ==="

# Clone IIAB
echo "Cloning IIAB from $IIAB_REPO (branch: $IIAB_BRANCH)..."
mkdir -p "$BUILD_SUBVOL/opt/iiab"
if [[ "$IIAB_BRANCH" == refs/pull/* ]]; then
    git clone --depth 1 "$IIAB_REPO" "$BUILD_SUBVOL/opt/iiab/iiab"
    (cd "$BUILD_SUBVOL/opt/iiab/iiab" && \
        git fetch --depth 1 "$IIAB_REPO" "$IIAB_BRANCH" && \
        git checkout FETCH_HEAD)
else
    if ! git clone --depth 1 --branch "$IIAB_BRANCH" "$IIAB_REPO" "$BUILD_SUBVOL/opt/iiab/iiab"; then
        echo "Error: Failed to clone IIAB repo branch '$IIAB_BRANCH' from $IIAB_REPO" >&2
        exit 1
    fi
fi

# Resolve local_vars path: three sources are checked in order
#   1. Absolute host path (--local-vars /full/path.yml) -- copied directly
#   2. Relative host path (--local-vars vars/foo.yml) -- copied into container repo, then used
#   3. Path inside the IIAB repo (relative path not found on host, or default)
resolve_local_vars() {
    if [ -z "$LOCAL_VARS" ]; then
        # Default: vars/local_vars_<name>.yml inside the IIAB repo
        IIAB_VARS_PATH="vars/local_vars_${NAME}.yml"
        return 0
    fi

    if [[ "$LOCAL_VARS" == /* ]]; then
        # Absolute host path -- will be copied directly later
        if [ ! -f "$LOCAL_VARS" ]; then
            echo "Error: local-vars file not found: $LOCAL_VARS" >&2
            return 1
        fi
        echo "Using local_vars from host absolute path: $LOCAL_VARS"
        cp --preserve=mode,timestamps "$LOCAL_VARS" "$BUILD_SUBVOL/etc/iiab/local_vars.yml"
        return 0
    fi

    # Relative path: check if it exists on the host first
    if [ -f "$LOCAL_VARS" ]; then
        RELATIVE_VARS_DIR=$(dirname "$LOCAL_VARS")
        mkdir -p "$BUILD_SUBVOL/opt/iiab/iiab/$RELATIVE_VARS_DIR"
        cp --preserve=mode,timestamps "$LOCAL_VARS" "$BUILD_SUBVOL/opt/iiab/iiab/$LOCAL_VARS"
        echo "Copied local_vars from host relative path: $LOCAL_VARS"
        IIAB_VARS_PATH="$LOCAL_VARS"
    else
        # Not on host -- will look inside the IIAB repo
        IIAB_VARS_PATH="$LOCAL_VARS"
    fi
}

resolve_local_vars

# Ensure the target directory exists
mkdir -p "$BUILD_SUBVOL/etc/iiab"

# If IIAB_VARS_PATH is set, try to copy from the IIAB repo inside the container
if [ -n "${IIAB_VARS_PATH:-}" ]; then
    IIAB_VARS_CONTAINER_PATH="$BUILD_SUBVOL/opt/iiab/iiab/$IIAB_VARS_PATH"
    if [ -f "$IIAB_VARS_CONTAINER_PATH" ]; then
        echo "Copying local_vars from IIAB repo: $IIAB_VARS_PATH"
        cp --preserve=mode,timestamps "$IIAB_VARS_CONTAINER_PATH" "$BUILD_SUBVOL/etc/iiab/local_vars.yml"
    else
        echo "Error: local_vars not found at $IIAB_VARS_PATH in IIAB repo" >&2
        echo "  Expected: $IIAB_VARS_CONTAINER_PATH" >&2
        exit 1
    fi
fi

# Set hostname
echo "$NAME" > "$BUILD_SUBVOL/etc/hostname"

# Set default target to multi-user
mkdir -p "$BUILD_SUBVOL/etc/systemd/system"
ln -sf /usr/lib/systemd/system/multi-user.target "$BUILD_SUBVOL/etc/systemd/system/default.target"

# Configure container networking via systemd one-shot service
cat > "$BUILD_SUBVOL/etc/systemd/system/iiab-network-setup.service" << EOF
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
mkdir -p "$BUILD_SUBVOL/etc/systemd/system/multi-user.target.wants"
ln -sf /etc/systemd/system/iiab-network-setup.service "$BUILD_SUBVOL/etc/systemd/system/multi-user.target.wants/iiab-network-setup.service"

# Write resolv.conf
rm -f "$BUILD_SUBVOL/etc/resolv.conf"
cat > "$BUILD_SUBVOL/etc/resolv.conf" << EOF
nameserver 8.8.8.8
nameserver 1.1.1.1
EOF

# Container and hardware-specific overrides
cat >> "$BUILD_SUBVOL/etc/iiab/local_vars.yml" << 'EOF'
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
    systemd-firstboot --root="$BUILD_SUBVOL" --delete-root-password --force

    setup_bridge
    export BUILD_SUBVOL IIAB_BRIDGE IIAB_GW IIAB_IP=$IP
    expect << 'EXPECT_EOF'
set timeout 60

spawn systemd-nspawn -q --network-bridge=$env(IIAB_BRIDGE) --resolv-conf=off -D $env(BUILD_SUBVOL) -M box --boot

expect "login: " { send "root\r" }
expect -re {#\s?$} { send "ssh-keygen -A\r" }
expect -re {#\s?$} { send "usermod --lock --expiredate=1 root\r" }
expect -re {#\s?$} { send "shutdown now\r" }
expect eof
EXPECT_EOF
else
    echo ""
    echo "=== Step 3: Running IIAB installer (this takes 30-60 minutes) ==="

    setup_bridge
    sysctl -w net.ipv4.ip_forward=1

    EXT_IF=$(ip route | grep default | awk '{print $5}' | head -n1)
    if [ -n "$EXT_IF" ]; then
        setup_nftables_nat "$EXT_IF"
    fi

    systemd-firstboot --root="$BUILD_SUBVOL" --delete-root-password --force

    # Ensure /root directory exists (Debian nocloud images may not have it)
    mkdir -p "$BUILD_SUBVOL/root"

    cat > "$BUILD_SUBVOL/root/run_build.sh" << 'EOF_SCRIPT'
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
    chmod +x "$BUILD_SUBVOL/root/run_build.sh"

    # Verify the script was written correctly
    if [ ! -f "$BUILD_SUBVOL/root/run_build.sh" ]; then
        echo "Error: Failed to write run_build.sh to container rootfs" >&2
        exit 1
    fi

    export BUILD_SUBVOL IIAB_BRIDGE IIAB_GW IIAB_IP=$IP
    expect << 'EXPECT_EOF'
set timeout 7200

spawn systemd-nspawn -q --network-bridge=$env(IIAB_BRIDGE) --resolv-conf=off -D $env(BUILD_SUBVOL) -M box --boot

expect "login: " { send "root\r" }
expect -re {#\s?$} { send "export PAGER=cat SYSTEMD_PAGER=cat\r" }
expect -re {#\s?$} { send "ssh-keygen -A\r" }
expect -re {#\s?$} { send "test -f /root/run_build.sh && echo 'BUILD_SCRIPT_FOUND' || echo 'BUILD_SCRIPT_MISSING'\r" }
expect {
    "BUILD_SCRIPT_FOUND" {
        puts "Build script found in container"
    }
    "BUILD_SCRIPT_MISSING" {
        puts "\nError: /root/run_build.sh not found in container!"
        exit 1
    }
    timeout {
        puts "\nTimed out waiting for build script check"
        exit 1
    }
}
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
# Step 4: Clean up and finalize
###############################################################################
echo ""
echo "=== Step 4: Cleaning and finalizing ==="

# Add metadata
{
    echo "$NAME"
    echo "Build date: $(date -u +%Y-%m-%dT%H:%M:%SZ)"
    echo "Branch: $IIAB_BRANCH"
    echo "Repo: $IIAB_REPO"
    echo "Volatile: $VOLATILE_MODE"
} >> "$BUILD_SUBVOL/.iiab-image"

# Clean up
echo uninitialized > "$BUILD_SUBVOL/etc/machine-id"
rm -f "$BUILD_SUBVOL/etc/iiab/uuid"
rm -f "$BUILD_SUBVOL/var/swap"

# Clean rootfs via nspawn
systemd-nspawn -q -D "$BUILD_SUBVOL" --pipe /bin/bash -eux << 'CLEANEOF'
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

systemd-firstboot --root="$BUILD_SUBVOL" --timezone=UTC --force

# Mark as read-only to signal build is complete and prevent accidental writes
btrfs property set "$BUILD_SUBVOL" ro true

# Measure final size
USED_MB=$(du -sm "$BUILD_SUBVOL" | cut -f1)
echo "Final image size: ${USED_MB}MB"

# Update config with actual size
if [ -n "$CONFIG_PATH" ] && [ -f "$CONFIG_PATH" ]; then
    sed -i "s/^IMAGE_SIZE_MB=.*/IMAGE_SIZE_MB=$USED_MB/" "$CONFIG_PATH"
    echo "Updated config IMAGE_SIZE_MB: $SIZE_MB -> $USED_MB"
fi

# Prevent cleanup from deleting our successful build
# (cleanup trap deletes the snapshot on failure)
BUILD_SUCCESS=true

###############################################################################
# Step 5: Register the image
###############################################################################
echo ""
echo "=== Step 5: Registering container ==="

mkdir -p /var/lib/machines

# Symlink so systemd-nspawn discovers it automatically
# systemd-nspawn@.service uses RootImage= or -D with /var/lib/machines/<name>
SYMLINK="/var/lib/machines/$NAME"
rm -f "$SYMLINK"
ln -sf "$BUILD_SUBVOL" "$SYMLINK"

echo ""
echo "=========================================="
echo "Build complete: $NAME"
echo "Subvolume: $BUILD_SUBVOL"
echo "Symlink:   $SYMLINK → $BUILD_SUBVOL"
echo "Size:      ${USED_MB}MB"
echo "Volatile:  $VOLATILE_MODE"
echo "=========================================="
