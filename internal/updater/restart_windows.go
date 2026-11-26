//go:build windows

package updater

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	winCreateNoWindow       = 0x08000000
	winCreateNewProcessGrp  = 0x00000200
)

// PrepareRestart spawns a hidden background helper that waits for the current process
// to exit (releasing its network port and executable handle) and then launches the
// updated binary with the same command-line arguments.
func PrepareRestart(skipSpawn bool) error {
	if skipSpawn {
		return nil
	}
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving executable path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}
	return scheduleWindowsRestart(exePath, os.Args[1:], os.Getpid())
}

// CompleteRestart is a no-op on Windows because PrepareRestart already spawned the
// detached relauncher that starts once this process exits.
func CompleteRestart() error {
	return nil
}

func scheduleWindowsRestart(exePath string, args []string, pid int) error {
	dir := filepath.Dir(exePath)

	script := fmt.Sprintf(
		`try { Wait-Process -Id %d -Timeout 15 -ErrorAction SilentlyContinue } catch {}; `+
			`Start-Sleep -Milliseconds 300; `+
			`if ($env:TALLY_MCP_RESTART_ARGS) { `+
			`$a = @(ConvertFrom-Json $env:TALLY_MCP_RESTART_ARGS); `+
			`Start-Process -FilePath $env:TALLY_MCP_RESTART_EXE -ArgumentList $a -WorkingDirectory $env:TALLY_MCP_RESTART_DIR `+
			`} else { `+
			`Start-Process -FilePath $env:TALLY_MCP_RESTART_EXE -WorkingDirectory $env:TALLY_MCP_RESTART_DIR `+
			`}`,
		pid,
	)

	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script)
	cmd.Dir = dir
	env := append(os.Environ(),
		"TALLY_MCP_RESTART_EXE="+exePath,
		"TALLY_MCP_RESTART_DIR="+dir,
	)
	if len(args) > 0 {
		argsJSON, err := json.Marshal(args)
		if err == nil {
			env = append(env, "TALLY_MCP_RESTART_ARGS="+string(argsJSON))
		}
	} else {
		env = append(env, "TALLY_MCP_RESTART_ARGS=")
	}
	cmd.Env = env
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: winCreateNoWindow | winCreateNewProcessGrp,
		HideWindow:    true,
	}

	if err := cmd.Start(); err == nil {
		_ = cmd.Process.Release()
		return nil
	}

	// Fallback to cmd.exe if PowerShell is unavailable
	var quotedArgs []string
	for _, a := range args {
		quotedArgs = append(quotedArgs, `"`+strings.ReplaceAll(a, `"`, `\"`)+`"`)
	}
	argSuffix := ""
	if len(quotedArgs) > 0 {
		argSuffix = " " + strings.Join(quotedArgs, " ")
	}
	cmdLine := fmt.Sprintf(`/c ping 127.0.0.1 -n 3 >nul & start "" "%s"%s`, exePath, argSuffix)
	fallback := exec.Command("cmd.exe")
	fallback.Dir = dir
	fallback.SysProcAttr = &syscall.SysProcAttr{
		CmdLine:       "cmd.exe " + cmdLine,
		CreationFlags: winCreateNoWindow | winCreateNewProcessGrp,
		HideWindow:    true,
	}
	if err := fallback.Start(); err != nil {
		return fmt.Errorf("scheduling Windows restart: %w", err)
	}
	_ = fallback.Process.Release()
	return nil
}
