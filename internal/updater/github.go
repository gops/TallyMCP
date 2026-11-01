package updater

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// GitHubRelease represents release metadata from GitHub API.
type GitHubRelease struct {
	TagName     string        `json:"tag_name"`
	Name        string        `json:"name"`
	Body        string        `json:"body"`
	Prerelease  bool          `json:"prerelease"`
	PublishedAt time.Time     `json:"published_at"`
	Assets      []GitHubAsset `json:"assets"`
}

// GitHubAsset represents a downloadable file attached to a release.
type GitHubAsset struct {
	Name               string `json:"name"`
	Size               int64  `json:"size"`
	BrowserDownloadURL string `json:"browser_download_url"`
	ContentType        string `json:"content_type"`
}

// UpdateInfo contains update check results.
type UpdateInfo struct {
	HasUpdate      bool      `json:"has_update"`
	CurrentVersion string    `json:"current_version"`
	LatestVersion  string    `json:"latest_version"`
	ReleaseName    string    `json:"release_name"`
	ReleaseNotes   string    `json:"release_notes"`
	PublishedAt    time.Time `json:"published_at"`
	AssetURL       string    `json:"asset_url"`
	AssetName      string    `json:"asset_name"`
	AssetSize      int64     `json:"asset_size"`
}

// FetchLatestRelease queries GitHub Releases API for the latest version.
func FetchLatestRelease(ctx context.Context, repo string) (*GitHubRelease, error) {
	repo = strings.TrimSpace(repo)
	if repo == "" || repo == "owner/tally-mcp" {
		return nil, fmt.Errorf("github repository is not configured (current: %q). Please set updater.github_repo in config.yaml", repo)
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating http request: %w", err)
	}

	req.Header.Set("User-Agent", "Tally-MCP-Updater")
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching latest release from GitHub: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("no releases found for repository %s", repo)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github API returned HTTP status %d", resp.StatusCode)
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("decoding release JSON: %w", err)
	}

	return &release, nil
}

// SelectMatchingAsset selects the release asset best matching the current operating system and CPU architecture.
func SelectMatchingAsset(assets []GitHubAsset) (*GitHubAsset, error) {
	goarch := runtime.GOARCH

	// Primary search: Windows asset matching cpu architecture (amd64 or arm64)
	for _, asset := range assets {
		name := strings.ToLower(asset.Name)
		if strings.Contains(name, "windows") && strings.Contains(name, goarch) {
			return &asset, nil
		}
	}

	// Secondary search: any windows asset or .exe
	for _, asset := range assets {
		name := strings.ToLower(asset.Name)
		if strings.Contains(name, "windows") || strings.HasSuffix(name, ".exe") {
			return &asset, nil
		}
	}

	return nil, fmt.Errorf("no compatible Windows asset found in release for Arch=%s (checked %d assets)", goarch, len(assets))
}

// IsNewerVersion returns true if remoteVersion is strictly newer than currentVersion.
func IsNewerVersion(currentVersion, remoteVersion string) bool {
	curr := parseSemVer(currentVersion)
	rem := parseSemVer(remoteVersion)

	for i := 0; i < 3; i++ {
		if rem[i] > curr[i] {
			return true
		}
		if rem[i] < curr[i] {
			return false
		}
	}
	return false
}

// parseSemVer parses "v1.2.3" or "1.2.3" into [1, 2, 3].
func parseSemVer(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	parts := strings.Split(v, ".")
	var res [3]int
	for i := 0; i < len(parts) && i < 3; i++ {
		// Clean off pre-release tags like 1.2.3-beta
		numStr := strings.Split(parts[i], "-")[0]
		n, _ := strconv.Atoi(numStr)
		res[i] = n
	}
	return res
}
