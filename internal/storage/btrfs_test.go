package storage_test

import (
	"strings"
	"testing"
	"time"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/storage"
)

func TestParseSizeToMBGiB(t *testing.T) {
	tests := []struct {
		input    string
		expected int
		hasError bool
	}{
		{"10GiB", 10240, false},
		{"1GiB", 1024, false},
		{"0.5GiB", 512, false},
		{"2.5GiB", 2560, false},
		{"100GiB", 102400, false},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result, err := storage.ParseSizeToMB(tc.input)
			if tc.hasError {
				if err == nil {
					t.Error("expected error")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if result != tc.expected {
					t.Errorf("expected %d, got %d", tc.expected, result)
				}
			}
		})
	}
}

func TestParseSizeToMBMiB(t *testing.T) {
	tests := []struct {
		input    string
		expected int
		hasError bool
	}{
		{"512MiB", 512, false},
		{"1024MiB", 1024, false},
		{"2048MiB", 2048, false},
		{"100MiB", 100, false},
		{"0MiB", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result, err := storage.ParseSizeToMB(tc.input)
			if tc.hasError {
				if err == nil {
					t.Error("expected error")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if result != tc.expected {
					t.Errorf("expected %d, got %d", tc.expected, result)
				}
			}
		})
	}
}

func TestParseSizeToMBBytes(t *testing.T) {
	tests := []struct {
		input    string
		expected int
		hasError bool
	}{
		{"1048576B", 1, false},       // 1 MiB
		{"104857600B", 100, false},   // 100 MiB
		{"1073741824B", 1024, false}, // 1 GiB
		{"0B", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result, err := storage.ParseSizeToMB(tc.input)
			if tc.hasError {
				if err == nil {
					t.Error("expected error")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if result != tc.expected {
					t.Errorf("expected %d, got %d", tc.expected, result)
				}
			}
		})
	}
}

func TestParseSizeToMBPlainNumber(t *testing.T) {
	tests := []struct {
		input    string
		expected int
		hasError bool
	}{
		{"1024", 1024, false},
		{"512", 512, false},
		{"0", 0, false},
		{"100", 100, false},
		{"invalid", 0, true},
		{"", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result, err := storage.ParseSizeToMB(tc.input)
			if tc.hasError {
				if err == nil {
					t.Error("expected error")
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if result != tc.expected {
					t.Errorf("expected %d, got %d", tc.expected, result)
				}
			}
		})
	}
}

func TestParseSizeToMBWhitespaceHandling(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{" 10GiB ", 10240},
		{"\t512MiB\n", 512},
		{"  1024  ", 1024},
	}
	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			result, err := storage.ParseSizeToMB(tc.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result != tc.expected {
				t.Errorf("expected %d, got %d", tc.expected, result)
			}
		})
	}
}

func TestStorageConstantsConsistency(t *testing.T) {
	if storage.StorageHeadroomMB != 2048 {
		t.Errorf("expected StorageHeadroomMB 2048, got %d", storage.StorageHeadroomMB)
	}
	if storage.StorageRoundUpMB != 1023 {
		t.Errorf("expected StorageRoundUpMB 1023, got %d", storage.StorageRoundUpMB)
	}
	if storage.StorageMinGB != 20 {
		t.Errorf("expected StorageMinGB 20, got %d", storage.StorageMinGB)
	}
	if storage.InitialSizeGB != 20 {
		t.Errorf("expected InitialSizeGB 20, got %d", storage.InitialSizeGB)
	}
}

func TestGetAlternateStoragePathOnDisk(t *testing.T) {
	altStorage, altMount := storage.GetAlternateStoragePath(true)
	if altStorage != storage.RAMBtrfsFile {
		t.Errorf("expected alt storage %q, got %q", storage.RAMBtrfsFile, altStorage)
	}
	if !strings.Contains(altMount, "alt-ram-storage") {
		t.Errorf("expected alt mount to contain 'alt-ram-storage', got %q", altMount)
	}
}

func TestGetAlternateStoragePathOnRAM(t *testing.T) {
	altStorage, altMount := storage.GetAlternateStoragePath(false)
	if altStorage != storage.DiskBtrfsFile {
		t.Errorf("expected alt storage %q, got %q", storage.DiskBtrfsFile, altStorage)
	}
	if !strings.Contains(altMount, "alt-disk-storage") {
		t.Errorf("expected alt mount to contain 'alt-disk-storage', got %q", altMount)
	}
}

func TestFindStorageRootsEmpty(t *testing.T) {
	// When no storage is mounted, should return empty slice
	// Note: This test may return non-empty on systems with mounted storage
	roots := storage.FindStorageRoots(t.Context())
	// Just verify it doesn't crash and returns a valid slice
	t.Logf("Found %d storage roots: %v", len(roots), roots)
}

func TestSubvolumeDeleteRetriesConstant(t *testing.T) {
	if storage.SubvolumeDeleteRetries != 3 {
		t.Errorf("expected SubvolumeDeleteRetries 3, got %d", storage.SubvolumeDeleteRetries)
	}
}

func TestSubvolumeDeleteRetryDelayDefault(t *testing.T) {
	if storage.SubvolumeDeleteRetryDelay != 2*time.Second {
		t.Errorf("expected SubvolumeDeleteRetryDelay 2s, got %v", storage.SubvolumeDeleteRetryDelay)
	}
}
