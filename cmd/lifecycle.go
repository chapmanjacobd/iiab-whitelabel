package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/config"
)

// StartCmd starts stopped demo(s).
type StartCmd struct {
	Names []string `help:"Demo name(s) to start"   arg:"" optional:""`
	All   bool     `help:"Start all stopped demos"                    default:"false"`
}

// Run executes the start command.
func (c *StartCmd) Run(ctx context.Context, globals *GlobalOptions) error {
	if err := ensureRoot(); err != nil {
		return err
	}

	names := c.Names
	if c.All {
		var err error
		names, err = config.List(globals.StateDir)
		if err != nil {
			return err
		}
	}

	if len(names) == 0 {
		return errors.New("no demos specified")
	}

	for _, name := range names {
		if err := startDemo(ctx, globals, name); err != nil {
			slog.ErrorContext(ctx, "Start failed", "demo", name, "error", err)
		}
	}
	return nil
}

// StopCmd stops running demo(s).
type StopCmd struct {
	Names []string `help:"Demo name(s) to stop"   arg:"" optional:""`
	All   bool     `help:"Stop all running demos"                    default:"false"`
}

// Run executes the stop command.
func (c *StopCmd) Run(ctx context.Context, globals *GlobalOptions) error {
	if err := ensureRoot(); err != nil {
		return err
	}

	names := c.Names
	if c.All {
		var err error
		names, err = config.List(globals.StateDir)
		if err != nil {
			return err
		}
	}

	if len(names) == 0 {
		return errors.New("no demos specified")
	}

	for _, name := range names {
		if err := stopDemo(ctx, globals, name); err != nil {
			slog.ErrorContext(ctx, "Stop failed", "demo", name, "error", err)
		}
	}
	return nil
}

// RestartCmd restarts running demo(s).
type RestartCmd struct {
	Names []string `help:"Demo name(s) to restart"   arg:"" optional:""`
	All   bool     `help:"Restart all running demos"                    default:"false"`
}

// Run executes the restart command.
func (c *RestartCmd) Run(ctx context.Context, globals *GlobalOptions) error {
	if err := ensureRoot(); err != nil {
		return err
	}

	names := c.Names
	if c.All {
		var err error
		names, err = config.List(globals.StateDir)
		if err != nil {
			return err
		}
	}

	if len(names) == 0 {
		return errors.New("no demos specified")
	}

	for _, name := range names {
		if err := stopDemo(ctx, globals, name); err != nil {
			slog.ErrorContext(ctx, "Stop failed during restart", "demo", name, "error", err)
		}
		if err := startDemo(ctx, globals, name); err != nil {
			slog.ErrorContext(ctx, "Start failed during restart", "demo", name, "error", err)
		}
	}
	return nil
}

// SettleCmd waits until all demos reach a settled state.
type SettleCmd struct {
	Timeout int `help:"Timeout in seconds" default:"300"`
}

// Run executes the settle command.
func (c *SettleCmd) Run(ctx context.Context, globals *GlobalOptions) error {
	if err := ensureRoot(); err != nil {
		return err
	}

	deadline := time.Now().Add(time.Duration(c.Timeout) * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		names, err := config.List(globals.StateDir)
		if err != nil {
			return err
		}

		if len(names) == 0 {
			fmt.Println("No demos to settle")
			return nil
		}

		allSettled := true
		for _, name := range names {
			status, _ := config.GetDemoStatus(globals.StateDir, name)
			if status == "pending" || status == "building" || status == "starting" || status == "stopping" {
				allSettled = false
				slog.InfoContext(ctx, "Waiting for demo to settle", "name", name, "status", status)
				break
			}
		}

		if allSettled {
			fmt.Println("All demos settled")
			return nil
		}
		time.Sleep(2 * time.Second)
	}

	return errors.New("timeout waiting for demos to settle")
}
