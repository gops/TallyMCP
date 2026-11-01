//go:build !windows

package tray

import (
	"context"
)

// AttachParentConsole is a no-op on non-Windows platforms.
func AttachParentConsole() {}

// Run waits for context cancellation on non-Windows platforms.
func Run(ctx context.Context, cfg Config) error {
	<-ctx.Done()
	if cfg.OnQuit != nil {
		cfg.OnQuit()
	}
	return nil
}
