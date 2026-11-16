//go:build !windows

package tally

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// findWindowsRegistryTally is a no-op on non-Windows platforms.
func findWindowsRegistryTally() string {
	return ""
}

// getStandardWindowsPaths returns Wine candidate paths on non-Windows platforms.
func getStandardWindowsPaths() []string {
	var paths []string
	home := os.Getenv("HOME")
	if home != "" {
		paths = append(paths,
			filepath.Join(home, ".wine/drive_c/Program Files/TallyPrime/tally.exe"),
			filepath.Join(home, ".wine/drive_c/Program Files (x86)/TallyPrime/tally.exe"),
			filepath.Join(home, ".wine/drive_c/TallyPrime/tally.exe"),
			filepath.Join(home, ".wine/drive_c/Program Files/Tally.ERP9/tally.exe"),
			filepath.Join(home, ".wine/drive_c/Program Files (x86)/Tally.ERP9/tally.exe"),
			filepath.Join(home, ".wine/drive_c/Tally.ERP9/tally.exe"),
		)
	}
	return paths
}

// launchOSProcess launches Tally on non-Windows platforms (using wine if .exe and available).
func launchOSProcess(exePath string) error {
	dir := filepath.Dir(exePath)
	var cmd *exec.Cmd

	if strings.HasSuffix(strings.ToLower(exePath), ".exe") {
		if _, err := exec.LookPath("wine"); err == nil {
			cmd = exec.Command("wine", exePath)
		} else {
			cmd = exec.Command(exePath)
		}
	} else {
		cmd = exec.Command(exePath)
	}

	cmd.Dir = dir
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting tally process %s: %w", exePath, err)
	}

	_ = cmd.Process.Release()
	return nil
}
