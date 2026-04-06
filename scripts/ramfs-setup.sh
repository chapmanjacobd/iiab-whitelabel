#!/usr/bin/env bash
# ramfs-setup.sh - Manage tmpfs mounts for container images in RAM
# Usage: ./ramfs-setup.sh <action> [edition]
#   action: load, unload, status, cleanup
#
# When ram_image is enabled, the .raw image is copied to a tmpfs mount
# so the container boots entirely from RAM — no disk I/O after initial load.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

ACTION="${1:?Error: Action required (load, unload, status, cleanup)}"
EDITION="${2:-}"
RAMFS_ROOT="/run/iiab-ramfs"

# Flock for concurrent safety on tmpfs operations
LOCK_FILE="/var/lib/iiab-demos/.ramfs.lock"
LOCK_FD=201

acquire_ramfs_lock() {
    mkdir -p "$(dirname "$LOCK_FILE")"
    eval "exec $LOCK_FD>\"$LOCK_FILE\""
    flock -w 10 "$LOCK_FD" || {
        echo "Error: another ramfs operation is in progress" >&2
        exit 1
    }
}

release_ramfs_lock() {
    flock -u "$LOCK_FD" 2>/dev/null || true
}

trap release_ramfs_lock EXIT

# Default image sizes for estimating tmpfs requirements
declare -A IMAGE_SIZES=( [small]=12000 [medium]=20000 [large]=30000 )

load_image() {
    local edition="$1"
    local src="/var/lib/machines/iiab-${edition}.raw"
    local dest="${RAMFS_ROOT}/iiab-${edition}.raw"

    if [ ! -f "$src" ]; then
        echo "Error: Source image not found: $src" >&2
        exit 1
    fi

    acquire_ramfs_lock

    # Create RAMFS root if needed
    if ! mountpoint -q "$RAMFS_ROOT" 2>/dev/null; then
        mkdir -p "$RAMFS_ROOT"
        # Calculate total size needed for all images
        local total_mb=0
        for e in small medium large; do
            if [ -f "/var/lib/machines/iiab-${e}.raw" ]; then
                local img_mb
                img_mb=$(du -m "/var/lib/machines/iiab-${e}.raw" | cut -f1)
                total_mb=$((total_mb + img_mb))
            fi
        done
        # Add 20% headroom
        local size_mb=$(( (total_mb * 12) / 10 ))
        echo "Mounting tmpfs at $RAMFS_ROOT (${size_mb}MB)..."
        mount -t tmpfs -o "size=${size_mb}M,mode=0755" tmpfs "$RAMFS_ROOT"
    fi

    if [ -f "$dest" ]; then
        echo "Image already loaded: $dest"
        return 0
    fi

    local img_size_mb
    img_size_mb=$(du -m "$src" | cut -f1)
    echo "Loading iiab-${edition}.raw into RAM (${img_size_mb}MB)..."

    # Check available RAM
    local avail_mb
    avail_mb=$(awk '/MemAvailable/ {print int($2/1024)}' /proc/meminfo)
    if [ "$img_size_mb" -gt "$((avail_mb - 512))" ]; then
        echo "Error: Not enough RAM available (${avail_mb}MB, need ~${img_size_mb}MB)" >&2
        exit 1
    fi

    cp --reflink=auto "$src" "$dest"
    chmod 0644 "$dest"
    echo "Loaded: $dest"
}

unload_image() {
    local edition="$1"
    local dest="${RAMFS_ROOT}/iiab-${edition}.raw"

    acquire_ramfs_lock

    if [ -f "$dest" ]; then
        echo "Removing $dest from RAM..."
        rm -f "$dest"
        echo "Unloaded: iiab-${edition}"
    else
        echo "Image not in RAM: iiab-${edition}"
    fi

    # If no images remain, unmount the tmpfs
    local remaining
    remaining=$(find "$RAMFS_ROOT" -name '*.raw' 2>/dev/null | wc -l)
    if [ "$remaining" -eq 0 ] && mountpoint -q "$RAMFS_ROOT" 2>/dev/null; then
        echo "No images in RAM, unmounting tmpfs..."
        umount "$RAMFS_ROOT"
        rmdir "$RAMFS_ROOT"
    fi
}

status() {
    if mountpoint -q "$RAMFS_ROOT" 2>/dev/null; then
        echo "=== RAMFS Status ==="
        echo "Mounted: yes"
        df -h "$RAMFS_ROOT"
        echo ""
        echo "Images loaded:"
        ls -lh "$RAMFS_ROOT"/*.raw 2>/dev/null || echo "  (none)"
        echo ""
        # Show tmpfs usage
        local used total
        used=$(du -sh "$RAMFS_ROOT" 2>/dev/null | cut -f1)
        total=$(df -h "$RAMFS_ROOT" | awk 'NR==2 {print $2}')
        echo "RAM usage: ${used} / ${total}"
    else
        echo "RAMFS not mounted"
    fi
}

cleanup() {
    acquire_ramfs_lock
    if mountpoint -q "$RAMFS_ROOT" 2>/dev/null; then
        echo "Unmounting RAMFS and removing all images..."
        umount "$RAMFS_ROOT"
        rmdir "$RAMFS_ROOT"
        echo "Cleanup complete"
    else
        echo "RAMFS not mounted, nothing to clean"
    fi
}

case "$ACTION" in
    load)
        if [ -z "$EDITION" ]; then
            echo "Loading all available images into RAM..."
            for e in small medium large; do
                if [ -f "/var/lib/machines/iiab-${e}.raw" ]; then
                    load_image "$e"
                fi
            done
        else
            load_image "$EDITION"
        fi
        ;;
    unload)
        if [ -z "$EDITION" ]; then
            echo "Unloading all images from RAM..."
            for e in small medium large; do
                unload_image "$e"
            done
        else
            unload_image "$EDITION"
        fi
        ;;
    status)
        status
        ;;
    cleanup)
        cleanup
        ;;
    *)
        echo "Usage: $0 {load|unload|status|cleanup} [edition]" >&2
        exit 1
        ;;
esac
