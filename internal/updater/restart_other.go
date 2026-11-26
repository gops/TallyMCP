//go:build !windows

package updater

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// PrepareRestart is a no-op on Unix platforms because CompleteRestart replaces
// the current process in-place via execve after graceful shutdown releases ports.
func PrepareRestart(_ bool) error {
	return nil
}

// CompleteRestart replaces the current process in-place with the newly installed
// executable, preserving arguments and environment.
func CompleteRestart() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving executable path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}
	if err := syscall.Exec(exePath, os.Args, os.Environ()); err != nil {
		return fmt.Errorf("exec %s: %w", exePath, err)
	}
	return nil
}
