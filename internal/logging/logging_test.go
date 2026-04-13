package logging_test

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/logging"
)

func TestPlainHandlerOutput(t *testing.T) {
	var buf bytes.Buffer
	h := logging.NewPlainHandler(&buf, slog.LevelInfo)
	logger := slog.New(h)

	logger.Info("Command stderr",
		"command", "nft",
		"args", []string{"delete", "table", "bridge", "iiab"},
	)

	out := buf.String()

	// Verify message is on first line
	if !strings.HasPrefix(out, "Command stderr\n") {
		t.Errorf("expected message on first line, got: %q", out)
	}

	// Verify key=value pairs are indented
	if !strings.Contains(out, "\n    command=nft") {
		t.Errorf("expected indented command key, got: %q", out)
	}

	if !strings.Contains(out, "\n    args=") {
		t.Errorf("expected indented args key, got: %q", out)
	}
}

func TestPlainHandlerLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	h := logging.NewPlainHandler(&buf, slog.LevelWarn)
	logger := slog.New(h)

	logger.Debug("should not appear")
	logger.Info("should not appear")
	logger.Warn("should appear")

	out := buf.String()
	if strings.Contains(out, "should not appear") {
		t.Errorf("debug/info messages should be filtered, got: %q", out)
	}
	if !strings.Contains(out, "should appear") {
		t.Errorf("warn message should appear, got: %q", out)
	}
}

func TestPlainHandlerWithAttrs(t *testing.T) {
	var buf bytes.Buffer
	h := logging.NewPlainHandler(&buf, slog.LevelInfo)
	child := h.WithAttrs([]slog.Attr{slog.String("prefix", "test")})
	logger := slog.New(child)

	logger.Info("test message", "key", "value")

	out := buf.String()
	if !strings.Contains(out, "prefix=test") {
		t.Errorf("expected prefix attr, got: %q", out)
	}
	if !strings.Contains(out, "key=value") {
		t.Errorf("expected key attr, got: %q", out)
	}
}

func TestPlainHandlerWithContext(t *testing.T) {
	var buf bytes.Buffer
	h := logging.NewPlainHandler(&buf, slog.LevelInfo)
	logger := slog.New(h)

	ctx := context.Background()
	logger.InfoContext(ctx, "context test", "foo", "bar")

	out := buf.String()
	if !strings.Contains(out, "context test") {
		t.Errorf("expected context test message, got: %q", out)
	}
}
