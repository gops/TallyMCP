//go:build windows

package tray

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	advapi32 = syscall.NewLazyDLL("advapi32.dll")

	procRegSetValueExW   = advapi32.NewProc("RegSetValueExW")
	procRegDeleteValueW  = advapi32.NewProc("RegDeleteValueW")
	procRegQueryValueExW = advapi32.NewProc("RegQueryValueExW")
)

const (
	runRegistryKey = `Software\Microsoft\Windows\CurrentVersion\Run`
	appName        = "TallyMCP"
)

// IsAutoStartEnabled checks if TallyMCP is configured to start with Windows.
func IsAutoStartEnabled() bool {
	subKeyPtr, err := syscall.UTF16PtrFromString(runRegistryKey)
	if err != nil {
		return false
	}

	var hKey syscall.Handle
	err = syscall.RegOpenKeyEx(syscall.HKEY_CURRENT_USER, subKeyPtr, 0, syscall.KEY_QUERY_VALUE, &hKey)
	if err != nil {
		return false
	}
	defer syscall.RegCloseKey(hKey)

	valNamePtr, err := syscall.UTF16PtrFromString(appName)
	if err != nil {
		return false
	}

	ret, _, _ := procRegQueryValueExW.Call(
		uintptr(hKey),
		uintptr(unsafe.Pointer(valNamePtr)),
		0,
		0,
		0,
		0,
	)

	return ret == 0 // ERROR_SUCCESS
}

// SetAutoStart enables or disables starting TallyMCP when the user logs into Windows.
func SetAutoStart(enable bool) error {
	subKeyPtr, err := syscall.UTF16PtrFromString(runRegistryKey)
	if err != nil {
		return err
	}

	var hKey syscall.Handle
	access := uint32(syscall.KEY_SET_VALUE)
	err = syscall.RegOpenKeyEx(syscall.HKEY_CURRENT_USER, subKeyPtr, 0, access, &hKey)
	if err != nil {
		return fmt.Errorf("opening registry key: %w", err)
	}
	defer syscall.RegCloseKey(hKey)

	valNamePtr, err := syscall.UTF16PtrFromString(appName)
	if err != nil {
		return err
	}

	if enable {
		exePath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolving executable path: %w", err)
		}
		exePath, err = filepath.EvalSymlinks(exePath)
		if err != nil {
			return fmt.Errorf("resolving symlinks: %w", err)
		}

		cmdVal := fmt.Sprintf("\"%s\"", exePath)
		valData, err := syscall.UTF16FromString(cmdVal)
		if err != nil {
			return err
		}

		ret, _, _ := procRegSetValueExW.Call(
			uintptr(hKey),
			uintptr(unsafe.Pointer(valNamePtr)),
			0,
			1, // REG_SZ
			uintptr(unsafe.Pointer(&valData[0])),
			uintptr(len(valData)*2),
		)
		if ret != 0 {
			return fmt.Errorf("setting registry value failed with code: %d", ret)
		}
	} else {
		ret, _, _ := procRegDeleteValueW.Call(
			uintptr(hKey),
			uintptr(unsafe.Pointer(valNamePtr)),
		)
		// Return success if deleted or if already didn't exist (ERROR_FILE_NOT_FOUND = 2)
		if ret != 0 && ret != 2 {
			return fmt.Errorf("deleting registry value failed with code: %d", ret)
		}
	}

	return nil
}
