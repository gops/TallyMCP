//go:build !windows

package updater

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

// MoveToTrash moves a regular file to the user's OS Trash directory
// (FreeDesktop.org Trash on Linux/BSD, ~/.Trash on macOS) instead of
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

	if runtime.GOOS == "darwin" {
		return moveToDarwinTrash(absPath)
	}
	return moveToFreeDesktopTrash(absPath)
}

func moveToDarwinTrash(absPath string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("finding home directory: %w", err)
	}
	trashDir := filepath.Join(home, ".Trash")
	if err := os.MkdirAll(trashDir, 0700); err != nil {
		return fmt.Errorf("creating ~/.Trash: %w", err)
	}

	base := filepath.Base(absPath)
	for i := 0; i < 1000; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s.%d", base, i)
		}
		dest := filepath.Join(trashDir, name)
		if _, err := os.Lstat(dest); os.IsNotExist(err) {
			return moveFileAcrossDevices(absPath, dest)
		}
	}
	return fmt.Errorf("could not allocate unique trash filename for %s", base)
}

func moveToFreeDesktopTrash(absPath string) error {
	dataHome := os.Getenv("XDG_DATA_HOME")
	if dataHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("finding home directory: %w", err)
		}
		dataHome = filepath.Join(home, ".local", "share")
	}

	trashDir := filepath.Join(dataHome, "Trash")
	filesDir := filepath.Join(trashDir, "files")
	infoDir := filepath.Join(trashDir, "info")

	if err := os.MkdirAll(filesDir, 0700); err != nil {
		return fmt.Errorf("creating trash files dir: %w", err)
	}
	if err := os.MkdirAll(infoDir, 0700); err != nil {
		return fmt.Errorf("creating trash info dir: %w", err)
	}

	base := filepath.Base(absPath)
	var (
		trashName string
		infoPath  string
		infoFile  *os.File
	)

	for i := 0; i < 1000; i++ {
		candidate := base
		if i > 0 {
			candidate = fmt.Sprintf("%s.%d", base, i)
		}
		destFile := filepath.Join(filesDir, candidate)
		if _, err := os.Lstat(destFile); !os.IsNotExist(err) {
			continue
		}
		candidateInfo := filepath.Join(infoDir, candidate+".trashinfo")
		f, err := os.OpenFile(candidateInfo, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			trashName = candidate
			infoPath = candidateInfo
			infoFile = f
			break
		}
		if !os.IsExist(err) {
			return fmt.Errorf("creating trashinfo %s: %w", candidateInfo, err)
		}
	}

	if infoFile == nil {
		return fmt.Errorf("could not allocate unique trash slot for %s", base)
	}

	escapedPath := (&url.URL{Path: absPath}).EscapedPath()
	deletionDate := time.Now().Format("2006-01-02T15:04:05")
	meta := fmt.Sprintf("[Trash Info]\nPath=%s\nDeletionDate=%s\n", escapedPath, deletionDate)
	if _, err := infoFile.WriteString(meta); err != nil {
		_ = infoFile.Close()
		_ = os.Remove(infoPath)
		return fmt.Errorf("writing trashinfo: %w", err)
	}
	if err := infoFile.Close(); err != nil {
		_ = os.Remove(infoPath)
		return fmt.Errorf("closing trashinfo: %w", err)
	}

	destFile := filepath.Join(filesDir, trashName)
	if err := moveFileAcrossDevices(absPath, destFile); err != nil {
		_ = os.Remove(infoPath)
		return err
	}
	return nil
}

func moveFileAcrossDevices(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	} else {
		var linkErr *os.LinkError
		if !errors.As(err, &linkErr) || !errors.Is(linkErr.Err, syscall.EXDEV) {
			return err
		}
	}

	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	fi, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dst)
		return err
	}
	return os.Remove(src)
}
