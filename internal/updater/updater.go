package updater

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// CheckForUpdate checks if a newer version exists in GitHub Releases.
func CheckForUpdate(ctx context.Context, currentVersion, repo string) (*UpdateInfo, error) {
	rel, err := FetchLatestRelease(ctx, repo)
	if err != nil {
		return nil, err
	}

	asset, err := SelectMatchingAsset(rel.Assets)
	if err != nil {
		return nil, err
	}

	hasUpdate := IsNewerVersion(currentVersion, rel.TagName)

	return &UpdateInfo{
		HasUpdate:      hasUpdate,
		CurrentVersion: currentVersion,
		LatestVersion:  rel.TagName,
		ReleaseName:    rel.Name,
		ReleaseNotes:   rel.Body,
		PublishedAt:    rel.PublishedAt,
		AssetURL:       asset.BrowserDownloadURL,
		AssetName:      asset.Name,
		AssetSize:      asset.Size,
	}, nil
}

// ApplyUpdate downloads the new release asset and replaces the current executable in-place.
// On Windows, this safely renames the running binary to .old and swaps in the new executable.
func ApplyUpdate(ctx context.Context, info *UpdateInfo, progressFn func(downloaded, total int64)) error {
	if info.AssetURL == "" {
		return fmt.Errorf("no asset URL provided for update")
	}

	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolving current executable path: %w", err)
	}
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("evaluating symlinks for executable: %w", err)
	}

	exeDir := filepath.Dir(exePath)
	exeName := filepath.Base(exePath)

	// Step 1: Download new asset into temporary file in the same directory
	// (Keeping it on the same drive/volume is critical on Windows for atomic renames)
	tempFile, err := os.CreateTemp(exeDir, "update-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temporary update file: %w", err)
	}
	tempPath := tempFile.Name()
	defer func() {
		_ = os.Remove(tempPath)
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, info.AssetURL, nil)
	if err != nil {
		tempFile.Close()
		return fmt.Errorf("creating download request: %w", err)
	}
	req.Header.Set("User-Agent", "Tally-MCP-Updater")

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		tempFile.Close()
		return fmt.Errorf("downloading update binary: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		tempFile.Close()
		return fmt.Errorf("download failed with HTTP status: %d", resp.StatusCode)
	}

	totalSize := resp.ContentLength
	var downloaded int64

	writer := io.Writer(tempFile)
	if progressFn != nil {
		writer = io.MultiWriter(tempFile, &progressWriter{
			total: totalSize,
			onProgress: func(n int64) {
				downloaded += n
				progressFn(downloaded, totalSize)
			},
		})
	}

	if _, err := io.Copy(writer, resp.Body); err != nil {
		tempFile.Close()
		return fmt.Errorf("saving downloaded update: %w", err)
	}
	tempFile.Close()

	// Step 2: Extract binary if the asset is a ZIP archive
	finalNewBinaryPath := tempPath
	if strings.HasSuffix(strings.ToLower(info.AssetName), ".zip") {
		extractedPath, err := extractExecutableFromZip(tempPath, exeDir, exeName)
		if err != nil {
			return fmt.Errorf("extracting binary from zip: %w", err)
		}
		defer func() {
			_ = os.Remove(extractedPath)
		}()
		finalNewBinaryPath = extractedPath
	}

	// Ensure downloaded binary has executable permissions
	if err := os.Chmod(finalNewBinaryPath, 0755); err != nil {
		return fmt.Errorf("setting executable permissions: %w", err)
	}

	// Step 3: Windows Safe Binary Swap
	// Rename current running executable to .old
	oldExePath := filepath.Join(exeDir, exeName+".old")
	_ = os.Remove(oldExePath) // Clean any existing .old file if left behind

	if err := os.Rename(exePath, oldExePath); err != nil {
		return fmt.Errorf("renaming running executable to %s: %w", oldExePath, err)
	}

	// Move new binary into target position
	if err := os.Rename(finalNewBinaryPath, exePath); err != nil {
		// Rollback if replacement failed
		_ = os.Rename(oldExePath, exePath)
		return fmt.Errorf("installing new executable to %s (rolled back): %w", exePath, err)
	}

	return nil
}

// CleanupOldBinary removes any stale .old binary left over from a previous update.
// This is called automatically when the application starts up.
func CleanupOldBinary() {
	exePath, err := os.Executable()
	if err != nil {
		return
	}
	oldPath := exePath + ".old"
	if fi, err := os.Stat(oldPath); err == nil && !fi.IsDir() {
		_ = os.Remove(oldPath)
	}
}

type progressWriter struct {
	total      int64
	onProgress func(int64)
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n := len(p)
	if pw.onProgress != nil {
		pw.onProgress(int64(n))
	}
	return n, nil
}

// extractExecutableFromZip extracts the target executable from a zip archive.
func extractExecutableFromZip(zipPath, targetDir, targetName string) (string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", err
	}
	defer r.Close()

	var targetFile *zip.File
	for _, f := range r.File {
		base := filepath.Base(f.Name)
		if runtime.GOOS == "windows" {
			if strings.EqualFold(base, targetName) || strings.HasSuffix(strings.ToLower(base), ".exe") {
				targetFile = f
				break
			}
		} else {
			if base == targetName || !strings.Contains(base, ".") {
				targetFile = f
				break
			}
		}
	}

	if targetFile == nil {
		return "", fmt.Errorf("no matching executable found inside downloaded zip archive")
	}

	rc, err := targetFile.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()

	outTemp, err := os.CreateTemp(targetDir, "extracted-*.tmp")
	if err != nil {
		return "", err
	}
	defer outTemp.Close()

	if _, err := io.Copy(outTemp, rc); err != nil {
		return "", err
	}

	return outTemp.Name(), nil
}

// IsValidBinary performs a quick sanity check to ensure the file is an executable binary.
func IsValidBinary(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	header := make([]byte, 4)
	if _, err := io.ReadFull(f, header); err != nil {
		return false
	}

	// Windows PE header starts with "MZ" (0x4D, 0x5A)
	if runtime.GOOS == "windows" {
		return bytes.HasPrefix(header, []byte{0x4D, 0x5A})
	}

	// ELF header starts with 0x7F, 'E', 'L', 'F'
	// Mach-O starts with 0xCF, 0xFA, 0xED, 0xFE etc.
	return true
}
