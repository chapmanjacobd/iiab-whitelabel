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
VOLATILE="overlay"
IP=""
LOCAL_VARS=""
BUILD_ON_DISK=false
SKIP_INSTALL=false
CONFIG_PATH=""
BASE_NAME=""

# Parse arguments
while [[ $# -gt 0 ]]; do
    case "$1" in
        --name)       NAME="$2"; shift 2 ;;
        --repo)       IIAB_REPO="$2"; shift 2 ;;
        --branch)     IIAB_BRANCH="$2"; shift 2 ;;
        --size)       SIZE_MB="$2"; shift 2 ;;
        --volatile)   VOLATILE="$2"; shift 2 ;;
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
echo "Volatile:     $VOLATILE"
echo "IP:           $IP"
echo "Build on disk: $BUILD_ON_DISK"
echo "Local vars:   ${LOCAL_VARS:-(none)}"

###############################################################################
# Storage setup: single btrfs file with CoW snapshots
###############################################################################
if $BUILD_ON_DISK; then
    STORAGE_DIR="/var/iiab-demos"
else
    STORAGE_DIR="/run/iiab-demos"
fi
STORAGE_BTRFS="$STORAGE_DIR/storage.btrfs"
STORAGE_MOUNT="$STORAGE_DIR/storage"
BUILDS_DIR="$STORAGE_MOUNT/builds"

# Ensure storage.btrfs exists and is mounted
ensure_storage() {
    if mountpoint -q "$STORAGE_MOUNT" 2>/dev/null; then
        return 0
    fi

    mkdir -p "$STORAGE_DIR"

    if [ ! -f "$STORAGE_BTRFS" ]; then
        echo "Creating storage.btrfs at $STORAGE_BTRFS..."
        if $BUILD_ON_DISK; then
            # Disk storage: pre-allocate for performance
            fallocate -l 50G "$STORAGE_BTRFS"
            chattr +C "$STORAGE_BTRFS" 2>/dev/null || true
        else
            # RAM storage: sparse file in tmpfs (only allocates as written)
            truncate -s 50G "$STORAGE_BTRFS"
        fi
        mkfs.btrfs -f -L iiab-demos "$STORAGE_BTRFS" >/dev/null 2>&1
    fi

    mount -o loop "$STORAGE_BTRFS" "$STORAGE_MOUNT"
    mkdir -p "$BUILDS_DIR"
}

# Cleanup on exit or failure
cleanup() {
    # Remove build snapshot if build didn't complete
    if btrfs subvolume show "$BUILDS_DIR/$NAME" >/dev/null 2>&1; then
        btrfs subvolume delete "$BUILDS_DIR/$NAME" >/dev/null 2>&1 || true
    fi
    # Unmount storage if we mounted it
    if [ "${DID_MOUNT:-false}" = "true" ] && mountpoint -q "$STORAGE_MOUNT" 2>/dev/null; then
        umount -l "$STORAGE_MOUNT" 2>/dev/null || true
    fi
}
trap cleanup EXIT

# Mount storage if not already mounted
ensure_storage
if ! mountpoint -q "$STORAGE_MOUNT" 2>/dev/null; then
    mount -o loop "$STORAGE_BTRFS" "$STORAGE_MOUNT"
    DID_MOUNT=true
fi
mkdir -p "$BUILDS_DIR"

# Resolve base subvolume
if [ -n "$BASE_NAME" ]; then
    if [[ "$BASE_NAME" == /* ]]; then
        BASE_BTRFS="$BASE_NAME"
        BASE_MOUNT="$STORAGE_DIR/$(basename "$BASE_NAME" .btrfs)"
        BASE_SUBVOL="rootfs"
    else
        BASE_SUBVOL="$BASE_NAME"
        BASE_BTRFS="$STORAGE_BTRFS"
        BASE_MOUNT="$STORAGE_MOUNT"
    fi
else
    BASE_SUBVOL="base-debian"
    BASE_BTRFS="$STORAGE_BTRFS"
    BASE_MOUNT="$STORAGE_MOUNT"
fi

# Mount external base if needed
if [ -n "${BASE_BTRFS:-}" ] && [[ "$BASE_BTRFS" != "$STORAGE_BTRFS" ]]; then
    if ! mountpoint -q "$BASE_MOUNT" 2>/dev/null; then
        mkdir -p "$BASE_MOUNT"
        mount -o loop "$BASE_BTRFS" "$BASE_MOUNT"
    fi
fi

###############################################################################
# Step 1: Prepare base (CoW snapshot)
###############################################################################
echo ""
echo "=== Step 1: Preparing base ==="

# Prepare the Debian base subvolume if needed
if [ -z "$BASE_NAME" ]; then
    if ! btrfs subvolume show "$STORAGE_MOUNT/base-debian" >/dev/null 2>&1; then
        # Download and extract Debian into a temp dir, then copy into subvolume
        tmpdir=$(mktemp -d "$STORAGE_DIR/debian-base.XXXXXX")
        tar_url="https://cloud.debian.org/images/cloud/trixie/latest/debian-13-nocloud-amd64.tar.xz"
        echo "Downloading and extracting Debian 13 nocloud rootfs..."
        curl -fL "$tar_url" | tar -xJ -C "$tmpdir" 2>/dev/null || {
            echo "Error: Failed to download/extract Debian rootfs" >&2
            rm -rf "$tmpdir"
            exit 1
        }

        echo "Creating base subvolume..."
        btrfs subvolume create "$STORAGE_MOUNT/base-debian"
        cp -a --reflink=auto "$tmpdir"/. "$STORAGE_MOUNT/base-debian/"
        rm -rf "$tmpdir"

        rm -f "$STORAGE_MOUNT/base-debian"/etc/machine-id "$STORAGE_MOUNT/base-debian/etc/hostname"
        btrfs property set "$STORAGE_MOUNT/base-debian" ro true
        echo "Base subvolume ready: $STORAGE_MOUNT/base-debian ($(du -sh "$STORAGE_MOUNT/base-debian" | cut -f1))"
    else
        echo "Base subvolume already exists: $STORAGE_MOUNT/base-debian"
    fi
else
    if ! btrfs subvolume show "$BASE_MOUNT/$BASE_SUBVOL" >/dev/null 2>&1; then
        echo "Error: Base subvolume not found: $BASE_SUBVOL" >&2
        exit 1
    fi
fi

# Create CoW snapshot for this build
MOUNT_DIR="$BUILDS_DIR/$NAME"
echo "Creating CoW snapshot of $BASE_SUBVOL..."
btrfs subvolume snapshot "$BASE_MOUNT/$BASE_SUBVOL" "$MOUNT_DIR" >/dev/null
echo "Build rootfs: $MOUNT_DIR ($(du -sh "$MOUNT_DIR" | cut -f1))"

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
if [ -n "$LOCAL_VARS" ]; then
    if [[ "$LOCAL_VARS" != /* ]] && [ -f "$LOCAL_VARS" ]; then
        RELATIVE_VARS_DIR=$(dirname "$LOCAL_VARS")
        mkdir -p "$MOUNT_DIR/opt/iiab/iiab/$RELATIVE_VARS_DIR"
        cp --preserve=mode,timestamps "$LOCAL_VARS" "$MOUNT_DIR/opt/iiab/iiab/$LOCAL_VARS"
        IIAB_VARS_PATH="$LOCAL_VARS"
        echo "Copied local_vars from host: $LOCAL_VARS → IIAB repo in image"
    elif [[ "$LOCAL_VARS" != /* ]]; then
        IIAB_VARS_PATH="$LOCAL_VARS"
    elif [[ "$LOCAL_VARS" == /* ]]; then
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

# Set hostname
echo "$NAME" > "$MOUNT_DIR/etc/hostname"

# Set default target to multi-user
ln -sf /usr/lib/systemd/system/multi-user.target "$MOUNT_DIR/etc/systemd/system/default.target"

# Configure container networking via systemd one-shot service
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

# Write resolv.conf
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
    systemd-firstboot --root="$MOUNT_DIR" --delete-root-password --force

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

    setup_bridge
    sysctl -w net.ipv4.ip_forward=1

    EXT_IF=$(ip route | grep default | awk '{print $5}' | head -n1)
    if [ -n "$EXT_IF" ]; then
        setup_nftables_nat "$EXT_IF"
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
    echo "Volatile: $VOLATILE"
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

# Mark as read-only to signal build is complete and prevent accidental writes
btrfs property set "$MOUNT_DIR" ro true

# Measure final size
USED_MB=$(du -sm "$MOUNT_DIR" | cut -f1)
echo "Final image size: ${USED_MB}MB"

# Update config with actual size
if [ -n "$CONFIG_PATH" ] && [ -f "$CONFIG_PATH" ]; then
    sed -i "s/^IMAGE_SIZE_MB=.*/IMAGE_SIZE_MB=$USED_MB/" "$CONFIG_PATH"
    echo "Updated config IMAGE_SIZE_MB: $SIZE_MB -> $USED_MB"
fi

# Prevent cleanup from deleting our successful build
# (cleanup trap deletes the snapshot on failure)

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
ln -sf "$MOUNT_DIR" "$SYMLINK"

echo ""
echo "=========================================="
echo "Build complete: $NAME"
echo "Subvolume: $MOUNT_DIR"
echo "Symlink:   $SYMLINK → $MOUNT_DIR"
echo "Size:      ${USED_MB}MB"
echo "Volatile:  $VOLATILE"
echo "=========================================="
