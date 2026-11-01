//go:build !windows

package tray

// IsFirewallRuleConfigured returns false on non-Windows platforms.
func IsFirewallRuleConfigured() bool {
	return false
}

// ConfigureFirewallRule is a no-op on non-Windows platforms.
func ConfigureFirewallRule(hwnd uintptr, port int) error {
	return nil
}

// RemoveFirewallRule is a no-op on non-Windows platforms.
func RemoveFirewallRule(hwnd uintptr) error {
	return nil
}
