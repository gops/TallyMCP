//go:build windows

package tally

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var (
	advapi32             = syscall.NewLazyDLL("advapi32.dll")
	shell32              = syscall.NewLazyDLL("shell32.dll")
	procRegQueryValueExW = advapi32.NewProc("RegQueryValueExW")
	procShellExecuteW    = shell32.NewProc("ShellExecuteW")
)

const (
	regAppPathsTally = `Software\Microsoft\Windows\CurrentVersion\App Paths\tally.exe`
)

// findWindowsRegistryTally queries HKLM and HKCU for App Paths\tally.exe.
func findWindowsRegistryTally() string {
	for _, root := range []syscall.Handle{syscall.HKEY_LOCAL_MACHINE, syscall.HKEY_CURRENT_USER} {
		if path := queryRegistryAppPath(root, regAppPathsTally); path != "" {
			clean := strings.Trim(strings.TrimSpace(path), "\"")
			if fi, err := os.Stat(clean); err == nil && !fi.IsDir() {
				return clean
			}
		}
	}
	return ""
}

func queryRegistryAppPath(root syscall.Handle, subKey string) string {
	subKeyPtr, err := syscall.UTF16PtrFromString(subKey)
	if err != nil {
		return ""
	}
	var hKey syscall.Handle
	err = syscall.RegOpenKeyEx(root, subKeyPtr, 0, syscall.KEY_QUERY_VALUE, &hKey)
	if err != nil {
		return ""
	}
	defer syscall.RegCloseKey(hKey)

	var buf [512]uint16
	var bufLen uint32 = uint32(len(buf) * 2)
	ret, _, _ := procRegQueryValueExW.Call(
		uintptr(hKey),
		0, // default value
		0,
		0,
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&bufLen)),
	)
	if ret == 0 {
		return syscall.UTF16ToString(buf[:])
	}
	return ""
}

// getStandardWindowsPaths returns common installation locations for Tally on Windows.
func getStandardWindowsPaths() []string {
	var paths []string

	progFiles := os.Getenv("ProgramFiles")
	if progFiles == "" {
		progFiles = `C:\Program Files`
	}
	progFiles86 := os.Getenv("ProgramFiles(x86)")
	if progFiles86 == "" {
		progFiles86 = `C:\Program Files (x86)`
	}
	localAppData := os.Getenv("LOCALAPPDATA")
	sysDrive := os.Getenv("SystemDrive")
	if sysDrive == "" {
		sysDrive = "C:"
	}

	paths = append(paths,
		filepath.Join(progFiles, "TallyPrime", "tally.exe"),
		filepath.Join(progFiles, "TallyPrimeEditLog", "tally.exe"),
		filepath.Join(progFiles, "Tally", "tally.exe"),
		filepath.Join(progFiles, "Tally.ERP9", "tally.exe"),
		filepath.Join(progFiles86, "TallyPrime", "tally.exe"),
		filepath.Join(progFiles86, "TallyPrimeEditLog", "tally.exe"),
		filepath.Join(progFiles86, "Tally", "tally.exe"),
		filepath.Join(progFiles86, "Tally.ERP9", "tally.exe"),
		filepath.Join(sysDrive+`\`, "TallyPrime", "tally.exe"),
		filepath.Join(sysDrive+`\`, "TallyPrimeEditLog", "tally.exe"),
		filepath.Join(sysDrive+`\`, "Tally.ERP9", "tally.exe"),
		filepath.Join(sysDrive+`\`, "Tally", "tally.exe"),
		`C:\Program Files\TallyPrime\tally.exe`,
		`C:\Program Files (x86)\TallyPrime\tally.exe`,
		`C:\Program Files\Tally.ERP9\tally.exe`,
		`C:\Program Files (x86)\Tally.ERP9\tally.exe`,
		`C:\Program Files\Tally\tally.exe`,
		`C:\Program Files (x86)\Tally\tally.exe`,
		`C:\TallyPrime\tally.exe`,
		`C:\Tally.ERP9\tally.exe`,
		`C:\Tally\tally.exe`,
		`D:\TallyPrime\tally.exe`,
		`D:\Tally.ERP9\tally.exe`,
		`D:\Tally\tally.exe`,
	)

	if localAppData != "" {
		paths = append(paths, filepath.Join(localAppData, "Programs", "TallyPrime", "tally.exe"))
	}

	return paths
}

// launchOSProcess launches Tally on Windows using ShellExecuteW (mimics Explorer launch)
// with fallback to exec.Command.
func launchOSProcess(exePath string) error {
	dir := filepath.Dir(exePath)
	exePtr, err := syscall.UTF16PtrFromString(exePath)
	if err != nil {
		return err
	}
	dirPtr, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return err
	}
	openPtr, err := syscall.UTF16PtrFromString("open")
	if err != nil {
		return err
	}

	// SW_SHOWNORMAL = 1
	ret, _, _ := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(openPtr)),
		uintptr(unsafe.Pointer(exePtr)),
		0,
		uintptr(unsafe.Pointer(dirPtr)),
		1,
	)

	// ShellExecute returns an HINSTANCE > 32 on success.
	if ret <= 32 {
		// Fallback to exec.Command
		cmd := exec.Command(exePath)
		cmd.Dir = dir
		cmd.SysProcAttr = &syscall.SysProcAttr{
			CreationFlags: 0x00000200, // CREATE_NEW_PROCESS_GROUP
		}
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("launching %s (ShellExecute error %d): %w", exePath, ret, err)
		}
		_ = cmd.Process.Release()
	}

	return nil
}
