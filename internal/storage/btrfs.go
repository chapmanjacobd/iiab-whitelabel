// Package storage provides btrfs CLI operations for managing container storage.
package storage

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/command"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/state"
	"github.com/chapmanjacobd/iiab-whitelabel/internal/sys"
)

const (
	RAMBtrfsFile  = "/run/iiab-demos/storage.btrfs"
	RAMMount      = "/run/iiab-demos/storage"
	RAMFSRoot     = "/run/iiab-demos"
	DiskBtrfsFile = "/var/iiab-demos/storage.btrfs"
	DiskMount     = "/var/iiab-demos/storage"
	InitialSizeGB = 20

	// StorageHeadroomMB is the headroom for btrfs metadata (2GB).
	StorageHeadroomMB = 2048
	// StorageRoundUpMB is the round-up value to next GB.
	StorageRoundUpMB = 1023
	// StorageMinGB is the minimum storage size.
	StorageMinGB = 20
)

// StorageInfo describes a mounted btrfs storage.
type StorageInfo struct {
	BtrfsFile string // path to the sparse file
	Mount     string // mount point
	OnDisk    bool   // whether it's on disk or tmpfs
}

// StorageType describes the live storage backend.
type StorageType struct {
	Backend   string // "tmpfs" or "disk"
	Mounted   bool
	MountPath string
	BtrfsFile string
	LoopDev   string // loop device if mounted via loop
	FSType    string // filesystem type (btrfs, tmpfs, etc.)
	FileSize  string // size of the backing file
}

// DetectStorageInfo performs a live detection of the actual storage backend.
func DetectStorageInfo(ctx context.Context) StorageType {
	st := StorageType{}

	// Check RAM storage first
	ramFile, ramMount := RAMBtrfsFile, RAMMount
	diskFile, diskMount := DiskBtrfsFile, DiskMount

	// Detect which storage is mounted
	if sys.Mountpoint(ctx, ramMount) {
		st.Backend = "tmpfs"
		st.Mounted = true
		st.MountPath = ramMount
		st.BtrfsFile = ramFile
	} else if sys.Mountpoint(ctx, diskMount) {
		st.Backend = "disk"
		st.Mounted = true
		st.MountPath = diskMount
		st.BtrfsFile = diskFile
	}

	if !st.Mounted {
		return st
	}

	// Get filesystem type
	if out, err := command.Output(ctx, "df", "-T", "--output=target,fstype", st.MountPath); err == nil {
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) >= 2 {
			fields := strings.Fields(lines[1])
			if len(fields) >= 2 {
				st.FSType = fields[1]
			}
		}
	}

	// Detect loop device
	if out, err := command.Output(ctx, "losetup", "-j", st.BtrfsFile); err == nil {
		line := strings.TrimSpace(out)
		if line != "" {
			st.LoopDev = strings.Split(line, ":")[0]
		}
	}

	// Get backing file size
	if info, err := os.Stat(st.BtrfsFile); err == nil {
		sizeBytes := info.Size()
		const (
			mb = 1024 * 1024
			gb = 1024 * 1024 * 1024
		)
		if sizeBytes >= gb {
			st.FileSize = fmt.Sprintf("%.1f GB", float64(sizeBytes)/float64(gb))
		} else {
			st.FileSize = fmt.Sprintf("%.1f MB", float64(sizeBytes)/float64(mb))
		}
	}

	return st
}

// SetupStorage creates and mounts a btrfs filesystem if not already done.
func SetupStorage(ctx context.Context, onDisk bool) (*StorageInfo, error) {
	btrfsFile, mount := DiskBtrfsFile, DiskMount
	if !onDisk {
		if err := ensureRAMFS(ctx, InitialSizeGB*1024); err != nil {
			return nil, fmt.Errorf("cannot setup ramfs: %w", err)
		}
		btrfsFile, mount = RAMBtrfsFile, RAMMount
	}

	info := &StorageInfo{BtrfsFile: btrfsFile, Mount: mount, OnDisk: onDisk}

	// Already mounted?
	if sys.Mountpoint(ctx, mount) {
		return info, nil
	}

	// Create the btrfs file if it doesn't exist
	if !state.FileExists(btrfsFile) {
		if err := os.MkdirAll(filepath.Dir(btrfsFile), 0o755); err != nil {
			return nil, err
		}
		if onDisk {
			// Pre-allocate on disk
			if err := command.Run(ctx, "fallocate", "-l", fmt.Sprintf("%dG", InitialSizeGB), btrfsFile); err != nil {
				return nil, err
			}
		} else {
			// Sparse file in tmpfs
			if err := command.Run(ctx, "truncate", "-s", fmt.Sprintf("%dG", InitialSizeGB), btrfsFile); err != nil {
				return nil, err
			}
		}

		// Create btrfs filesystem
		if err := command.Run(ctx, "mkfs.btrfs", "-f", btrfsFile); err != nil {
			return nil, err
		}
	}

	// Mount
	if err := os.MkdirAll(mount, 0o755); err != nil {
		return nil, err
	}

	mountOpts := "loop,noatime"
	if onDisk {
		mountOpts = "loop,compress-force=zstd:1,noatime,discard=async"
	}
	if err := command.Run(ctx, "mount", "-o", mountOpts, btrfsFile, mount); err != nil {
		slog.WarnContext(ctx, "Mount failed, attempting to re-format", "file", btrfsFile, "error", err)
		// Force re-format
		if err := command.Run(ctx, "mkfs.btrfs", "-f", btrfsFile); err != nil {
			return nil, err
		}
		if err := command.Run(ctx, "mount", "-o", mountOpts, btrfsFile, mount); err != nil {
			return nil, err
		}
	}

	return info, nil
}

// GrowStorage dynamically grows the btrfs file to accommodate a build.
func GrowStorage(ctx context.Context, onDisk bool, neededMB int) error {
	btrfsFile, mount := DiskBtrfsFile, DiskMount
	if !onDisk {
		// Calculate total target size including overhead
		btrfsUsed := getBtrfsUsedMB(ctx, RAMMount)
		targetMB := max(
			(btrfsUsed + neededMB + StorageHeadroomMB + StorageRoundUpMB), InitialSizeGB*1024)

		if err := ensureRAMFS(ctx, targetMB); err != nil {
			return fmt.Errorf("cannot resize ramfs: %w", err)
		}
		btrfsFile, mount = RAMBtrfsFile, RAMMount
	}

	if !sys.Mountpoint(ctx, mount) {
		return fmt.Errorf("storage not mounted at %s", mount)
	}

	// Current size
	currentSize := int64(0)
	if info, err := os.Stat(btrfsFile); err == nil {
		currentSize = info.Size()
	}
	currentGB := int(currentSize / (1024 * 1024 * 1024))

	// Get btrfs used space
	btrfsUsed := getBtrfsUsedMB(ctx, mount)
	targetGB := max(
		// headroom, round up
		(btrfsUsed+neededMB+StorageHeadroomMB+StorageRoundUpMB)/1024, StorageMinGB)

	if targetGB <= currentGB {
		slog.InfoContext(ctx, "Storage sufficient", "current_gb", currentGB, "needed_mb", neededMB)
		return nil
	}

	slog.InfoContext(ctx, "Growing storage online", "file", btrfsFile, "from_gb", currentGB, "to_gb", targetGB)

	// 1. Expand the underlying file
	if err := command.Run(ctx, "truncate", "-s", fmt.Sprintf("%dG", targetGB), btrfsFile); err != nil {
		return fmt.Errorf("truncate failed: %w", err)
	}

	// 2. Identify and update the loop device capacity
	// losetup -j returns: /dev/loopX: [device]:inode (file)
	out, err := exec.CommandContext(ctx, "losetup", "-j", btrfsFile).Output()
	if err != nil {
		return fmt.Errorf("failed to find loop device for %s: %w", btrfsFile, err)
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return fmt.Errorf("no loop device found for %s", btrfsFile)
	}
	loopDev := strings.Split(line, ":")[0]

	slog.DebugContext(ctx, "Updating loop device capacity", "dev", loopDev)
	if err := command.Run(ctx, "losetup", "-c", loopDev); err != nil {
		return fmt.Errorf("losetup -c failed for %s: %w", loopDev, err)
	}

	// 3. Expand the btrfs filesystem online
	slog.DebugContext(ctx, "Resizing btrfs filesystem", "mount", mount)
	if err := command.Run(ctx, "btrfs", "filesystem", "resize", "max", mount); err != nil {
		// Attempt to revert truncate on failure (best effort)
		_ = command.Run(ctx, "truncate", "-s", fmt.Sprintf("%dG", currentGB), btrfsFile)
		_ = command.Run(ctx, "losetup", "-c", loopDev)
		return fmt.Errorf("btrfs resize failed: %w", err)
	}

	slog.InfoContext(ctx, "Storage grown online", "size_gb", targetGB)
	return nil
}

// CreateSubvolume creates a btrfs subvolume at the given path.
func CreateSubvolume(ctx context.Context, mount, name string) error {
	path := filepath.Join(mount, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return command.Run(ctx, "btrfs", "subvolume", "create", path)
}

// Snapshot creates a read-write btrfs snapshot.
func Snapshot(ctx context.Context, source, dest string) error {
	return command.Run(ctx, "btrfs", "subvolume", "snapshot", source, dest)
}

// DeleteSubvolume deletes a btrfs subvolume.
func DeleteSubvolume(ctx context.Context, path string) error {
	return command.Run(ctx, "btrfs", "subvolume", "delete", "--commit-each", path)
}

// DeleteSubvolumeWithRetry attempts to delete a btrfs subvolume with retries.
func DeleteSubvolumeWithRetry(ctx context.Context, path string) error {
	if !state.FileExists(path) {
		return nil
	}

	var lastErr error
	for range SubvolumeDeleteRetries {
		lastErr = DeleteSubvolume(ctx, path)
		if lastErr == nil {
			return nil
		}
		if !state.FileExists(path) {
			return nil
		}
		time.Sleep(SubvolumeDeleteRetryDelay)
	}

	if lastErr != nil {
		slog.WarnContext(ctx, "Failed to delete btrfs subvolume after retries", "subvol", path, "error", lastErr)
		return fmt.Errorf("failed to delete btrfs subvolume %s after retries: %w", path, lastErr)
	}
	return nil
}

// SubvolumeDeleteRetries is the number of retries for subvolume deletion.
const SubvolumeDeleteRetries = 3

// SubvolumeDeleteRetryDelay is the delay between subvolume deletion retries.
var SubvolumeDeleteRetryDelay = 2 * time.Second //nolint:gochecknoglobals // intentionally mutable for test override

// SubvolumeExists checks if a subvolume exists.
func SubvolumeExists(ctx context.Context, mount, name string) bool {
	path := filepath.Join(mount, name)
	return command.Run(ctx, "btrfs", "subvolume", "show", path) == nil
}

// CopySubvolumeFromAlternate copies a subvolume from an alternate storage backend.
// This is used when chaining builds across storage backends (RAM <-> disk).
func CopySubvolumeFromAlternate(ctx context.Context, subvolName, altStorage, altMount, currentMount string) error {
	// Check if alternate storage is already mounted at its primary location
	primaryMount := RAMMount
	if altStorage == DiskBtrfsFile {
		primaryMount = DiskMount
	}

	sourceMount := altMount
	if sys.Mountpoint(ctx, primaryMount) {
		sourceMount = primaryMount
	} else if !sys.Mountpoint(ctx, altMount) {
		// Mount alternate storage if needed
		if err := os.MkdirAll(altMount, 0o755); err != nil {
			return fmt.Errorf("cannot create alternate mount point: %w", err)
		}
		if err := command.Run(ctx, "mount", "-o", "loop,noatime", altStorage, altMount); err != nil {
			return fmt.Errorf("cannot mount alternate storage: %w", err)
		}
	}

	// Check if subvolume exists in alternate storage (either root or builds/)
	sourcePath := filepath.Join(sourceMount, subvolName)
	if !SubvolumeExists(ctx, sourceMount, subvolName) {
		// Try builds/subvolName
		sourcePath = filepath.Join(sourceMount, "builds", subvolName)
		if !SubvolumeExists(ctx, sourceMount, "builds/"+subvolName) {
			return fmt.Errorf(
				"subvolume '%s' not found in alternate storage %s (tried root and builds/)",
				subvolName,
				sourceMount,
			)
		}
	}

	slog.InfoContext(ctx, "Copying subvolume from alternate storage", "source", sourcePath)

	// Destination should be where we want the subvolume to end up.
	// If it's a "base-debian" it usually goes into root.
	// If it was in builds/ on source, we probably want it in builds/ on dest.
	destPath := filepath.Join(currentMount, subvolName)
	if strings.Contains(sourcePath, "/builds/") {
		destPath = filepath.Join(currentMount, "builds", subvolName)
	}
	destDir := filepath.Dir(destPath)

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("cannot create destination directory: %w", err)
	}

	if err := SendReceive(ctx, sourcePath, destDir); err == nil {
		slog.InfoContext(ctx, "Subvolume copied via btrfs send/receive", "subvolume", subvolName)
		// Mark read-only to match source
		if err := SetReadOnly(ctx, destPath); err != nil {
			slog.WarnContext(ctx, "Could not set read-only on copied subvolume", "subvolume", subvolName, "error", err)
		}
		return nil
	}

	// Fallback: cp -a --reflink=auto
	slog.InfoContext(ctx, "Falling back to cp --reflink=auto", "subvolume", subvolName)
	if !SubvolumeExists(ctx, currentMount, subvolName) {
		if err := CreateSubvolume(ctx, currentMount, subvolName); err != nil {
			return fmt.Errorf("failed to create destination subvolume: %w", err)
		}
	}

	if err := command.Run(ctx, "cp", "-a", "--reflink=auto", sourcePath+"/.", destPath+"/"); err != nil {
		return fmt.Errorf("cp --reflink=auto failed: %w", err)
	}

	if err := SetReadOnly(ctx, destPath); err != nil {
		slog.WarnContext(ctx, "Could not set read-only on copied subvolume", "subvolume", subvolName, "error", err)
	}
	slog.InfoContext(ctx, "Subvolume copied via cp --reflink=auto", "subvolume", subvolName)
	return nil
}

// GetAlternateStoragePath returns the path to the alternate storage.btrfs file.
func GetAlternateStoragePath(onDisk bool) (altStorage, altMount string) {
	if onDisk {
		// We're on disk; alternate is RAM
		return RAMBtrfsFile, "/run/iiab-demos/alt-ram-storage"
	}
	// We're in RAM; alternate is disk
	return DiskBtrfsFile, "/var/iiab-demos/alt-disk-storage"
}

func SendReceive(ctx context.Context, source, dest string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	r, w, err := os.Pipe()
	if err != nil {
		return err
	}
	defer w.Close()
	defer r.Close()

	// Fork btrfs send -> pipe
	// --compressed-data avoids decompressing zstd extents
	sendCmd := exec.CommandContext(ctx, "btrfs", "send", "--compressed-data", source)
	var sendStderr bytes.Buffer
	sendCmd.Stdout = w
	sendCmd.Stderr = &sendStderr

	if err := sendCmd.Start(); err != nil {
		return fmt.Errorf("failed to start btrfs send: %w", err)
	}
	w.Close() // close writer in parent so receive gets EOF

	// Fork btrfs receive <- pipe
	receiveCmd := exec.CommandContext(ctx, "btrfs", "receive", dest)
	var receiveStderr bytes.Buffer
	receiveCmd.Stdin = r
	receiveCmd.Stderr = &receiveStderr

	receiveErr := receiveCmd.Run()
	sendErr := sendCmd.Wait()

	if receiveErr != nil || sendErr != nil {
		if receiveErr != nil {
			slog.ErrorContext(ctx, "btrfs receive failed", "stderr", receiveStderr.String(), "error", receiveErr)
		}
		if sendErr != nil {
			slog.ErrorContext(ctx, "btrfs send failed", "stderr", sendStderr.String(), "error", sendErr)
		}
		if receiveErr != nil {
			return receiveErr
		}
		return sendErr
	}

	return nil
}

// SetReadOnly marks a subvolume as read-only.
func SetReadOnly(ctx context.Context, path string) error {
	return command.Run(ctx, "btrfs", "property", "set", "-ts", path, "ro", "true")
}

// -- internal helpers --

func getBtrfsUsedMB(ctx context.Context, mount string) int {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "btrfs", "filesystem", "df", mount).Output()
	if err != nil {
		return 0
	}

	for line := range strings.SplitSeq(string(out), "\n") {
		if strings.Contains(line, "Data, single") {
			// Parse "Data, single: total=XXGiB, used=YYGiB"
			parts := strings.Split(line, "used=")
			if len(parts) < 2 {
				return 0
			}
			sizePart := strings.Fields(parts[1])[0]
			mb, _ := ParseSizeToMB(sizePart)
			return mb
		}
	}
	return 0
}

// ParseSizeToMB parses a size string like "10GiB", "512MiB", "1048576B", or "1024" to MB.
func ParseSizeToMB(s string) (int, error) {
	s = strings.TrimSpace(s)
	if before, ok := strings.CutSuffix(s, "GiB"); ok {
		val, err := strconv.ParseFloat(before, 64)
		if err != nil {
			return 0, err
		}
		return int(val * 1024), nil
	}
	if before, ok := strings.CutSuffix(s, "MiB"); ok {
		val, err := strconv.ParseFloat(before, 64)
		if err != nil {
			return 0, err
		}
		return int(val), nil
	}
	if before, ok := strings.CutSuffix(s, "B"); ok {
		val, err := strconv.ParseFloat(before, 64)
		if err != nil {
			return 0, err
		}
		return int(val / (1024 * 1024)), nil
	}
	val, err := strconv.Atoi(s)
	return val, err
}

// TeardownStorage unmounts and deletes all btrfs storage files and mounts.
func TeardownStorage(ctx context.Context) error {
	roots := FindStorageRoots(ctx)
	for _, root := range roots {
		slog.InfoContext(ctx, "Unmounting storage", "root", root)
		if err := command.Run(ctx, "umount", "-l", root); err != nil {
			slog.WarnContext(ctx, "Failed to unmount storage", "root", root, "error", err)
		}
	}

	// Wait for lazy unmounts
	time.Sleep(1 * time.Second)

	// Unmount RAMFSRoot if it's a mountpoint
	if sys.Mountpoint(ctx, RAMFSRoot) {
		slog.InfoContext(ctx, "Unmounting RAM storage root", "root", RAMFSRoot)
		if err := command.Run(ctx, "umount", "-l", RAMFSRoot); err != nil {
			slog.WarnContext(ctx, "Failed to unmount RAM storage root", "root", RAMFSRoot, "error", err)
		}
	}

	storageFiles := []string{
		RAMBtrfsFile,
		DiskBtrfsFile,
	}
	for _, f := range storageFiles {
		if state.FileExists(f) {
			slog.InfoContext(ctx, "Removing storage file", "file", f)
			if err := os.Remove(f); err != nil {
				slog.WarnContext(ctx, "Failed to remove storage file", "file", f, "error", err)
			}
		}
	}

	for _, root := range roots {
		_ = os.Remove(root)
	}

	return nil
}

// FindStorageRoots returns all mounted btrfs storage roots.
func FindStorageRoots(ctx context.Context) []string {
	var roots []string
	candidates := []string{
		"/run/iiab-demos/storage",
		"/var/iiab-demos/storage",
		"/run/iiab-demos/alt-ram-storage",
		"/var/iiab-demos/alt-disk-storage",
	}
	for _, c := range candidates {
		if sys.Mountpoint(ctx, c) {
			roots = append(roots, c)
		}
	}
	return roots
}

// ensureRAMFS ensures /run/iiab-demos is a tmpfs of at least the given size.
func ensureRAMFS(ctx context.Context, sizeMB int) error {
	// 1. Check if it's already a mountpoint
	if sys.Mountpoint(ctx, RAMFSRoot) {
		// Get current size to see if it needs growing
		cmd := exec.CommandContext(ctx, "df", "-m", "--output=size", RAMFSRoot)
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("failed to get tmpfs size: %w", err)
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) >= 2 {
			var currentSizeMB int
			if _, err := fmt.Sscanf(lines[1], "%d", &currentSizeMB); err == nil {
				if currentSizeMB >= sizeMB {
					return nil
				}
			}
		}

		slog.InfoContext(ctx, "Growing RAM storage (tmpfs)", "size_mb", sizeMB)
		return command.Run(ctx, "mount", "-o", fmt.Sprintf("remount,size=%dM", sizeMB), RAMFSRoot)
	}

	// 2. Mount fresh tmpfs
	if err := os.MkdirAll(RAMFSRoot, 0o755); err != nil {
		return err
	}

	slog.InfoContext(ctx, "Mounting RAM storage (tmpfs)", "size_mb", sizeMB)
	return command.Run(ctx, "mount", "-t", "tmpfs", "-o", fmt.Sprintf("size=%dM,mode=0755", sizeMB), "tmpfs", RAMFSRoot)
}
