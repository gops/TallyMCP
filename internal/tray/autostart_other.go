//go:build !windows

package tray

// IsAutoStartEnabled returns false on non-Windows platforms.
func IsAutoStartEnabled() bool {
	return false
}

// SetAutoStart is a no-op on non-Windows platforms.
func SetAutoStart(enable bool) error {
	return nil
}
