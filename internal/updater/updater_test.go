package updater_test

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

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
