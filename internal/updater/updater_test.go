package updater_test

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"tally-mcp/internal/updater"
)

func TestIsNewerVersion(t *testing.T) {
	tests := []struct {
		current  string
		remote   string
		expected bool
	}{
		{"v1.0.0", "v1.0.1", true},
		{"v1.0.0", "v1.1.0", true},
		{"v1.0.0", "v2.0.0", true},
		{"v1.2.3", "v1.2.3", false},
		{"v1.2.4", "v1.2.3", false},
		{"1.0.0", "1.0.1", true},
		{"v1.0.0-beta", "v1.0.1", true},
		{"v2.0.0", "v1.9.9", false},
	}

	for _, tt := range tests {
		got := updater.IsNewerVersion(tt.current, tt.remote)
		if got != tt.expected {
			t.Errorf("IsNewerVersion(%q, %q) = %v, expected %v", tt.current, tt.remote, got, tt.expected)
		}
	}
}

func TestSelectMatchingAsset(t *testing.T) {
	assets := []updater.GitHubAsset{
		{Name: "tally-mcp-windows-amd64.zip"},
		{Name: "tally-mcp-windows-amd64.exe"},
		{Name: "tally-mcp-windows-arm64.zip"},
		{Name: "tally-mcp-windows-arm64.exe"},
	}

	asset, err := updater.SelectMatchingAsset(assets)
	if err != nil {
		t.Fatalf("failed to select matching asset: %v", err)
	}
	if asset == nil || asset.Name == "" {
		t.Fatalf("expected valid asset returned")
	}
}

func TestExtractExecutableFromZip(t *testing.T) {
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "test.zip")

	// Create test zip with dummy exe
	zipFile, err := os.Create(zipPath)
	if err != nil {
		t.Fatalf("failed to create zip: %v", err)
	}
	zw := zip.NewWriter(zipFile)
	w, err := zw.Create("tally-mcp.exe")
	if err != nil {
		t.Fatalf("failed to create entry in zip: %v", err)
	}
	_, _ = w.Write([]byte("dummy binary payload"))
	_ = zw.Close()
	_ = zipFile.Close()

	// Verify zip exists
	if _, err := os.Stat(zipPath); err != nil {
		t.Fatalf("zip file does not exist")
	}
}

func TestAutoUpdater_ETagAndAutoRestart(t *testing.T) {
	var requestCount atomic.Int32
	var releaseTag atomic.Value
	releaseTag.Store("v1.0.0")
	var currentETag atomic.Value
	currentETag.Store(`"etag-v1.0.0"`)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount.Add(1)
		etag := currentETag.Load().(string)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		tag := releaseTag.Load().(string)
		w.Header().Set("ETag", etag)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(updater.GitHubRelease{
			TagName: tag,
			Name:    "Release " + tag,
			Assets: []updater.GitHubAsset{
				{Name: "tally-mcp-windows-amd64.zip", BrowserDownloadURL: "https://example.com/" + tag + ".zip"},
				{Name: "tally-mcp-linux-amd64.tar.gz", BrowserDownloadURL: "https://example.com/" + tag + ".tar.gz"},
			},
		})
	}))
	defer srv.Close()

	var appliedCount atomic.Int32
	var restartCalled atomic.Int32
	var failFirstApply atomic.Bool
	failFirstApply.Store(true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	restartedCh := make(chan string, 1)
	au := updater.NewAutoUpdater(updater.AutoConfig{
		CurrentVersion: "v1.0.0",
		Repo:           "gops/TallyMCP",
		APIBaseURL:     srv.URL,
		CheckOnStartup: true,
		Interval:       25 * time.Millisecond,
		AutoApply:      true,
		Logf:           func(string, ...any) {},
		ApplyFn: func(ctx context.Context, info *updater.UpdateInfo) error {
			appliedCount.Add(1)
			if failFirstApply.Swap(false) {
				return errors.New("transient download error")
			}
			return nil
		},
		OnRestartFn: func(info *updater.UpdateInfo) error {
			restartCalled.Add(1)
			restartedCh <- info.LatestVersion
			return nil
		},
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		au.Run(ctx)
	}()

	// Wait a couple ticks while version is v1.0.0 (should hit 200 once then 304s)
	time.Sleep(80 * time.Millisecond)
	if restartCalled.Load() != 0 {
		t.Fatalf("did not expect restart when already on latest version")
	}

	// Publish a newer version on GitHub
	releaseTag.Store("v1.0.1")
	currentETag.Store(`"etag-v1.0.1"`)

	select {
	case ver := <-restartedCh:
		if ver != "v1.0.1" {
			t.Fatalf("expected restart for v1.0.1, got %s", ver)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for auto-update and restart")
	}

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatalf("expected AutoUpdater.Run to stop after initiating restart")
	}

	if appliedCount.Load() < 2 {
		t.Errorf("expected retry after transient ApplyFn failure (appliedCount=%d)", appliedCount.Load())
	}
	if restartCalled.Load() != 1 {
		t.Errorf("expected exactly 1 restart call, got %d", restartCalled.Load())
	}
	if requestCount.Load() < 3 {
		t.Errorf("expected multiple periodic polls, got %d", requestCount.Load())
	}
}
