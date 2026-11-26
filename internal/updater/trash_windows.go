//go:build windows

package updater

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"unicode/utf16"
	"unsafe"
)

var (
	shell32DLL           = syscall.NewLazyDLL("shell32.dll")
	procSHFileOperationW = shell32DLL.NewProc("SHFileOperationW")
)

const (
	foDelete          = 0x0003
	fofSilent         = 0x0004
	fofNoConfirmation = 0x0010
	fofAllowUndo      = 0x0040
	fofNoErrorUI      = 0x0400
)

type shFileOpStructW struct {
	hwnd                  uintptr
	wFunc                 uint32
	pFrom                 *uint16
	pTo                   *uint16
	fFlags                uint16
	fAnyOperationsAborted int32
	hNameMappings         uintptr
	lpszProgressTitle     *uint16
}

// MoveToTrash moves a regular file to the Windows Recycle Bin instead of
// permanently deleting it.
func MoveToTrash(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("resolving path %s: %w", path, err)
	}

	fi, err := os.Lstat(absPath)
	if err != nil {
		return err
	}
	if fi.IsDir() || !fi.Mode().IsRegular() || fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to trash non-regular file %s", absPath)
	}

	u16 := utf16.Encode([]rune(absPath))
	u16 = append(u16, 0, 0) // SHFileOperationW requires double-null termination

	op := shFileOpStructW{
		wFunc:  foDelete,
		pFrom:  &u16[0],
		fFlags: fofAllowUndo | fofNoConfirmation | fofNoErrorUI | fofSilent,
	}

	ret, _, callErr := procSHFileOperationW.Call(uintptr(unsafe.Pointer(&op)))
	runtime.KeepAlive(u16)

	if ret != 0 || op.fAnyOperationsAborted != 0 {
		return fmt.Errorf("moving %s to Recycle Bin failed (ret=%d, aborted=%d, err=%v)", absPath, ret, op.fAnyOperationsAborted, callErr)
	}
	return nil
}
