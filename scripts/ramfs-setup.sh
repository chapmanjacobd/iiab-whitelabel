#!/usr/bin/env bash
# ramfs-setup.sh - Manage tmpfs mounts for container images in RAM
# Usage: ./ramfs-setup.sh <action> [edition]
#   action: load, unload, status, cleanup
#
# When ram_image is enabled, the .raw image is copied to a tmpfs mount
# so the container boots entirely from RAM — no disk I/O after initial load.
set -euo pipefail

ACTION="${1:?Error: Action required (load, unload, status, cleanup)}"
NAME="${2:-}"
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

load_image() {
    local name="$1"
    local src="/var/lib/machines/${name}.raw"
    local dest="${RAMFS_ROOT}/${name}.raw"

    if [ ! -f "$src" ]; then
        echo "Error: Source image not found: $src" >&2
        exit 1
    fi

    acquire_ramfs_lock

    # Create RAMFS root if needed
    if ! mountpoint -q "$RAMFS_ROOT" 2>/dev/null; then
        mkdir -p "$RAMFS_ROOT"
        local img_mb
        img_mb=$(du -m "$src" | cut -f1)
        # Use exact image size for the initial mount
        echo "Mounting tmpfs at $RAMFS_ROOT (${img_mb}MB)..."
        mount -t tmpfs -o "size=${img_mb}M,mode=0755" tmpfs "$RAMFS_ROOT"
    fi

    if [ -f "$dest" ]; then
        echo "Image already loaded: $dest"
        return 0
    fi

    local img_size_mb
    img_size_mb=$(du -m "$src" | cut -f1)
    echo "Loading ${name}.raw into RAM (${img_size_mb}MB)..."

    # Check available RAM
    local avail_mb
    avail_mb=$(awk '/MemAvailable/ {print int($2/1024)}' /proc/meminfo)
    # Keeping the 512MB RAM safety buffer as requested
    if [ "$img_size_mb" -gt "$((avail_mb - 512))" ]; then
        echo "Error: Not enough RAM available (${avail_mb}MB, need ~${img_size_mb}MB)" >&2
        exit 1
    fi

    # If already mounted but too small, remount to grow
    local current_size
    current_size=$(df -m "$RAMFS_ROOT" | awk 'NR==2 {print $2}')
    local current_used
    current_used=$(df -m "$RAMFS_ROOT" | awk 'NR==2 {print $3}')
    local needed=$(( current_used + img_size_mb ))
    if [ "$needed" -gt "$current_size" ]; then
        mount -o "remount,size=${needed}M" "$RAMFS_ROOT"
    fi

    cp --reflink=auto "$src" "$dest"
    chmod 0644 "$dest"
    echo "Loaded: $dest"
}

unload_image() {
    local name="$1"
    local dest="${RAMFS_ROOT}/${name}.raw"

    acquire_ramfs_lock

    # Safety: don't unload if container is still running from this image
    if systemctl is-active --quiet "systemd-nspawn@${name}.service"; then
        echo "Error: Demo '$name' is still running. Stop it first:" >&2
        echo "  systemctl stop systemd-nspawn@${name}.service" >&2
        echo "  or: democtl remove $name" >&2
        return 1
    fi

    if [ -f "$dest" ]; then
        echo "Removing $dest from RAM..."
        rm -f "$dest"
        echo "Unloaded: ${name}"
    else
        echo "Image not in RAM: ${name}"
    fi

    # If no images remain, unmount the tmpfs and clean up symlinks
    local remaining
    remaining=$(find "$RAMFS_ROOT" -name '*.raw' -type f 2>/dev/null | wc -l)
    if [ "$remaining" -eq 0 ] && mountpoint -q "$RAMFS_ROOT" 2>/dev/null; then
        echo "No images in RAM, unmounting tmpfs..."
        umount "$RAMFS_ROOT"
        rmdir "$RAMFS_ROOT"

        # Clean up symlinks in /var/lib/machines that pointed to RAM images
        echo "Cleaning up dangling symlinks in /var/lib/machines/..."
        for link in /var/lib/machines/*.raw; do
            [ -L "$link" ] || continue
            local target
            target=$(readlink -f "$link" 2>/dev/null || echo "")
            if [[ "$target" == /run/iiab-ramfs/* ]] && [ ! -f "$target" ]; then
                echo "  Removing dangling symlink: $link"
                rm -f "$link"
            fi
        done
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

        # Clean up symlinks in /var/lib/machines that pointed to RAM images
        echo "Cleaning up dangling symlinks in /var/lib/machines/..."
        for link in /var/lib/machines/*.raw; do
            [ -L "$link" ] || continue
            local target
            target=$(readlink -f "$link" 2>/dev/null || echo "")
            if [[ "$target" == /run/iiab-ramfs/* ]] && [ ! -f "$target" ]; then
                echo "  Removing dangling symlink: $link"
                rm -f "$link"
            fi
        done
        echo "Cleanup complete"
    else
        echo "RAMFS not mounted, nothing to clean"
    fi
}

case "$ACTION" in
    load)
        if [ -z "$NAME" ]; then
            echo "Error: demo name required for load" >&2
            exit 1
        fi
        load_image "$NAME"
        ;;
    unload)
        if [ -z "$NAME" ]; then
            echo "Unloading all images from RAM..."
            find "$RAMFS_ROOT" -name '*.raw' -type f 2>/dev/null | while read -r img; do
                n=$(basename "$img" .raw)
                unload_image "$n"
            done
        else
            unload_image "$NAME"
        fi
        ;;
    status)
        status
        ;;
    cleanup)
        cleanup
        ;;
    *)
        echo "Usage: $0 {load|unload|status|cleanup} [name]" >&2
        exit 1
        ;;
esac
