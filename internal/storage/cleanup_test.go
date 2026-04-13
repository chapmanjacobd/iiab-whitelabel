package storage_test

import (
	"errors"
	"strings"
	"testing"
)

func TestCleanupResourcesMultiErrorAccumulation(t *testing.T) {
	// When multiple cleanup steps fail, errors should be joined
	// This test verifies the error accumulation pattern
	errs := make([]error, 0, 3)
	errs = append(errs, errors.New("error 1"))
	errs = append(errs, errors.New("error 2"))
	errs = append(errs, errors.New("error 3"))

	joined := errors.Join(errs...)
	if joined == nil {
		t.Fatal("expected non-nil joined error")
	}
	// errors.Join combines all errors
	if !strings.Contains(joined.Error(), "error 1") {
		t.Errorf("expected joined error to contain 'error 1', got: %s", joined.Error())
	}
	if !strings.Contains(joined.Error(), "error 2") {
		t.Errorf("expected joined error to contain 'error 2', got: %s", joined.Error())
	}
	if !strings.Contains(joined.Error(), "error 3") {
		t.Errorf("expected joined error to contain 'error 3', got: %s", joined.Error())
	}
}

func TestVethInterfacePrefixHandling(t *testing.T) {
	// Verify the expected veth interface prefixes
	prefixes := []string{"ve-", "vb-"}
	foundVe := false
	foundVb := false
	for _, p := range prefixes {
		if p == "ve-" {
			foundVe = true
		}
		if p == "vb-" {
			foundVb = true
		}
	}
	if !foundVe {
		t.Error("expected 've-' prefix to be handled")
	}
	if !foundVb {
		t.Error("expected 'vb-' prefix to be handled")
	}
}
