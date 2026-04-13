// Package logging provides a compact plain-text slog handler.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
)

// NewPlainHandler returns an [slog.Handler] that renders key=value pairs
// indented on separate lines beneath the message for readability.
func NewPlainHandler(out io.Writer, level slog.Level) slog.Handler {
	return &plainHandler{level: level, out: out}
}

// plainHandler implements [slog.Handler] with plain output.
type plainHandler struct {
	level slog.Level
	out   io.Writer
	attrs []slog.Attr
}

func (h *plainHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.level
}

func (h *plainHandler) Handle(_ context.Context, r slog.Record) error {
	var msg strings.Builder
	msg.WriteString(r.Message)
	for _, a := range h.attrs {
		fmt.Fprintf(&msg, "\n    %s=%v", a.Key, a.Value.Any())
	}
	r.Attrs(func(a slog.Attr) bool {
		fmt.Fprintf(&msg, "\n    %s=%v", a.Key, a.Value.Any())
		return true
	})
	_, err := fmt.Fprintln(h.out, msg.String())
	return err
}

func (h *plainHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &plainHandler{
		level: h.level,
		out:   h.out,
		attrs: append(h.attrs, attrs...),
	}
}

func (h *plainHandler) WithGroup(_ string) slog.Handler {
	return h
}
