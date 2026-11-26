package updater

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

var staleUpdateTempRegex = regexp.MustCompile(`^(update|extracted)-[0-9]+\.tmp$`)

// CleanupOldBinary safely moves leftover files from a previous update (such as
// <exe>.old and stale update-*.tmp / extracted-*.tmp files) to the OS Trash /
// Recycle Bin. It never permanently deletes files and strictly validates filenames
// and file types so unrelated files in the directory are never touched.
func CleanupOldBinary() {
	exePath, err := os.Executable()
	if err != nil {
		return
	}
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}
	_ = CleanupOldBinaryAt(exePath)
}

// CleanupOldBinaryAt safely moves leftover update files associated with exePath
// to the OS Trash / Recycle Bin.
func CleanupOldBinaryAt(exePath string) error {
	return cleanupOldBinaryInDir(exePath, MoveToTrashWithRetry)
}

// MoveToTrashWithRetry attempts MoveToTrash with brief retries on transient lock errors
// (e.g. right after a previous process exits on Windows during auto-restart).
func MoveToTrashWithRetry(path string) error {
	var err error
	for attempt := 0; attempt < 4; attempt++ {
		if _, statErr := os.Lstat(path); os.IsNotExist(statErr) {
			return nil
		}
		err = MoveToTrash(path)
		if err == nil {
			return nil
		}
		if attempt < 3 {
			time.Sleep(250 * time.Millisecond)
		}
	}
	return err
}

func cleanupOldBinaryInDir(exePath string, trashFn func(string) error) error {
	if exePath == "" || trashFn == nil {
		return nil
	}
	exeDir := filepath.Dir(exePath)
	exeName := filepath.Base(exePath)
	if exeDir == "" || exeName == "" || exeName == "." {
		return nil
	}

	entries, err := os.ReadDir(exeDir)
	if err != nil {
		return err
	}

	expectedOldName := exeName + ".old"
	var firstErr error

	for _, entry := range entries {
		name := entry.Name()
		candidate := filepath.Join(exeDir, name)

		// Reject symlinks and directories immediately via Lstat
		fi, lerr := os.Lstat(candidate)
		if lerr != nil || fi.IsDir() || !fi.Mode().IsRegular() || fi.Mode()&os.ModeSymlink != 0 {
			continue
		}

		isOldExe := namesMatch(name, expectedOldName)
		isStaleTemp := staleUpdateTempRegex.MatchString(name)

		if !isOldExe && !isStaleTemp {
			continue
		}

		// Extra safety: ensure <exe>.old is actually an executable binary for this OS
		if isOldExe && !IsValidBinary(candidate) {
			log.Printf("[WARN] Skipping cleanup of %s: file is not a valid executable binary", candidate)
			continue
		}

		if err := trashFn(candidate); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("moving %s to trash: %w", candidate, err)
			}
			log.Printf("[WARN] Could not move old update file %s to trash: %v", candidate, err)
		} else if isOldExe {
			log.Printf("[INFO] Moved previous executable %s to trash", candidate)
		}
	}

	return firstErr
}

func namesMatch(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
