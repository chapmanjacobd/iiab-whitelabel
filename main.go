package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/chapmanjacobd/iiab-whitelabel/cmd"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	exitCode := cmd.Run(ctx)
	stop()
	os.Exit(exitCode)
}
