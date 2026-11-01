//go:build windows

package tray

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const firewallRuleName = "TallyMCP"

// IsFirewallRuleConfigured checks if an inbound allow rule exists for TallyMCP in Windows Firewall.
func IsFirewallRuleConfigured() bool {
	cmd := exec.Command("netsh", "advfirewall", "firewall", "show", "rule", fmt.Sprintf("name=%s", firewallRuleName))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	s := string(out)
	return strings.Contains(s, firewallRuleName) && (strings.Contains(s, "Allow") || strings.Contains(s, "allow"))
}

// ConfigureFirewallRule configures the inbound firewall allow rule for Tally MCP.
// It first attempts direct netsh execution (succeeds if running as Administrator),
// and falls back to triggering a standard Windows UAC elevation prompt via ShellExecuteW.
func ConfigureFirewallRule(hwnd uintptr, port int) error {
	if port <= 0 {
		port = 8080
	}
	portRange := fmt.Sprintf("%d-%d", port, port+10)

	// 1. Try direct netsh execution in case the process is already elevated
	_ = exec.Command("netsh", "advfirewall", "firewall", "delete", "rule", fmt.Sprintf("name=%s", firewallRuleName)).Run()
	directCmd := exec.Command("netsh", "advfirewall", "firewall", "add", "rule",
		fmt.Sprintf("name=%s", firewallRuleName),
		"dir=in",
		"action=allow",
		"protocol=TCP",
		fmt.Sprintf("localport=%s", portRange),
		"description=Inbound access for Tally MCP Server",
	)
	directCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := directCmd.Run(); err == nil {
		if IsFirewallRuleConfigured() {
			return nil
		}
	}

	// 2. Fall back to UAC elevation via ShellExecuteW ("runas")
	cmdArgs := fmt.Sprintf(`/c netsh advfirewall firewall delete rule name="%s" >nul 2>&1 & netsh advfirewall firewall add rule name="%s" dir=in action=allow protocol=TCP localport=%s description="Inbound access for Tally MCP Server"`,
		firewallRuleName, firewallRuleName, portRange)

	runasPtr, _ := syscall.UTF16PtrFromString("runas")
	cmdPtr, _ := syscall.UTF16PtrFromString("cmd.exe")
	argPtr, _ := syscall.UTF16PtrFromString(cmdArgs)

	ret, _, _ := procShellExecuteW.Call(
		hwnd,
		uintptr(unsafe.Pointer(runasPtr)),
		uintptr(unsafe.Pointer(cmdPtr)),
		uintptr(unsafe.Pointer(argPtr)),
		0,
		0, // SW_HIDE
	)

	// ShellExecute returns an HINSTANCE > 32 on success. Values <= 32 are error codes.
	if ret <= 32 {
		if ret == 5 || ret == 1223 {
			return fmt.Errorf("administrator permission was denied or cancelled")
		}
		return fmt.Errorf("firewall elevation failed (code %d)", ret)
	}

	// Poll briefly for netsh to finish executing in the background
	for i := 0; i < 20; i++ {
		time.Sleep(200 * time.Millisecond)
		if IsFirewallRuleConfigured() {
			return nil
		}
	}

	return nil
}

// RemoveFirewallRule deletes the inbound firewall allow rule for Tally MCP.
func RemoveFirewallRule(hwnd uintptr) error {
	// 1. Try direct netsh execution first
	directCmd := exec.Command("netsh", "advfirewall", "firewall", "delete", "rule", fmt.Sprintf("name=%s", firewallRuleName))
	directCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := directCmd.Run(); err == nil {
		return nil
	}

	// 2. Fall back to UAC elevation via ShellExecuteW ("runas")
	cmdArgs := fmt.Sprintf(`/c netsh advfirewall firewall delete rule name="%s"`, firewallRuleName)

	runasPtr, _ := syscall.UTF16PtrFromString("runas")
	cmdPtr, _ := syscall.UTF16PtrFromString("cmd.exe")
	argPtr, _ := syscall.UTF16PtrFromString(cmdArgs)

	ret, _, _ := procShellExecuteW.Call(
		hwnd,
		uintptr(unsafe.Pointer(runasPtr)),
		uintptr(unsafe.Pointer(cmdPtr)),
		uintptr(unsafe.Pointer(argPtr)),
		0,
		0, // SW_HIDE
	)

	if ret <= 32 {
		if ret == 5 || ret == 1223 {
			return fmt.Errorf("administrator permission was denied or cancelled")
		}
		return fmt.Errorf("firewall elevation failed (code %d)", ret)
	}

	time.Sleep(500 * time.Millisecond)
	return nil
}
