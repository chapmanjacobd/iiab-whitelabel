package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/chapmanjacobd/iiab-whitelabel/internal/tls"
)

// CertsCmd manages TLS certificates for demos.
type CertsCmd struct {
	Setup bool `help:"Setup/renew certificates for all active demos"`
}

// Run executes the certs command.
func (c *CertsCmd) Run(ctx context.Context, globals *GlobalOptions) error {
	if err := ensureRoot(); err != nil {
		return err
	}

	if c.Setup {
		slog.InfoContext(ctx, "Setting up TLS certificates for active demos")
		if err := tls.SetupCerts(ctx, globals.StateDir); err != nil {
			return fmt.Errorf("certificate setup failed: %w", err)
		}
		slog.InfoContext(ctx, "TLS certificates setup complete")
		return nil
	}

	return errors.New("no action specified. Use --setup to obtain/renew certificates")
}
