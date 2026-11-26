package updater

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// AutoConfig configures the background automatic update checker and applier.
type AutoConfig struct {
	CurrentVersion string
	Repo           string
	APIBaseURL     string
	CheckOnStartup bool
	Interval       time.Duration
	AutoApply      bool

	ApplyFn       func(ctx context.Context, info *UpdateInfo) error
	OnAvailableFn func(info *UpdateInfo)
	OnRestartFn   func(info *UpdateInfo) error
	Logf          func(format string, args ...any)
}

// AutoUpdater periodically polls GitHub Releases for new versions using conditional
// ETag requests and, when AutoApply is enabled, downloads, installs, and restarts
// the application automatically.
type AutoUpdater struct {
	cfg           AutoConfig
	mu            sync.Mutex
	updating      bool
	lastETag      string
	notifiedTag   string
	lastErrMsg    string
	lastErrLogged time.Time
}

// NewAutoUpdater constructs a new AutoUpdater with sensible defaults.
func NewAutoUpdater(cfg AutoConfig) *AutoUpdater {
	if cfg.Logf == nil {
		cfg.Logf = log.Printf
	}
	if cfg.ApplyFn == nil {
		cfg.ApplyFn = func(ctx context.Context, info *UpdateInfo) error {
			return ApplyUpdate(ctx, info, nil)
		}
	}
	return &AutoUpdater{cfg: cfg}
}

// Run starts the periodic update check loop and blocks until ctx is canceled
// or an automatic update triggers a restart.
func (u *AutoUpdater) Run(ctx context.Context) {
	repo := strings.TrimSpace(u.cfg.Repo)
	if repo == "" || repo == "owner/tally-mcp" {
		return
	}

	if u.cfg.CheckOnStartup {
		if u.CheckOnce(ctx) {
			return
		}
	}

	if u.cfg.Interval <= 0 {
		return
	}

	ticker := time.NewTicker(u.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if u.CheckOnce(ctx) {
				return
			}
		}
	}
}

// CheckOnce performs a single update check (and auto-apply + restart if configured).
// It returns true if a restart was initiated and polling should stop.
func (u *AutoUpdater) CheckOnce(ctx context.Context) bool {
	if ctx.Err() != nil {
		return false
	}

	u.mu.Lock()
	if u.updating {
		u.mu.Unlock()
		return false
	}
	u.updating = true
	etag := u.lastETag
	u.mu.Unlock()

	defer func() {
		u.mu.Lock()
		u.updating = false
		u.mu.Unlock()
	}()

	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	rel, newETag, notModified, err := fetchLatestReleaseWithETag(checkCtx, u.cfg.APIBaseURL, u.cfg.Repo, etag)
	cancel()

	if err != nil {
		u.logThrottledError(fmt.Sprintf("Auto-update check failed: %v", err))
		return false
	}

	if notModified || rel == nil {
		u.clearError()
		return false
	}

	asset, err := SelectMatchingAsset(rel.Assets)
	if err != nil {
		u.logThrottledError(fmt.Sprintf("Auto-update asset selection failed: %v", err))
		return false
	}

	u.clearError()

	var checksumsURL string
	for _, a := range rel.Assets {
		if strings.EqualFold(a.Name, "checksums.txt") {
			checksumsURL = a.BrowserDownloadURL
			break
		}
	}

	info := &UpdateInfo{
		HasUpdate:      IsNewerVersion(u.cfg.CurrentVersion, rel.TagName),
		CurrentVersion: u.cfg.CurrentVersion,
		LatestVersion:  rel.TagName,
		ReleaseName:    rel.Name,
		ReleaseNotes:   rel.Body,
		PublishedAt:    rel.PublishedAt,
		AssetURL:       asset.BrowserDownloadURL,
		AssetName:      asset.Name,
		AssetSize:      asset.Size,
		ChecksumsURL:   checksumsURL,
	}

	if !info.HasUpdate {
		u.mu.Lock()
		u.lastETag = newETag
		u.mu.Unlock()
		return false
	}

	if !u.cfg.AutoApply {
		u.mu.Lock()
		u.lastETag = newETag
		alreadyNotified := u.notifiedTag == info.LatestVersion
		if !alreadyNotified {
			u.notifiedTag = info.LatestVersion
		}
		u.mu.Unlock()

		if !alreadyNotified && u.cfg.OnAvailableFn != nil {
			u.cfg.OnAvailableFn(info)
		}
		return false
	}

	u.cfg.Logf("[INFO] New version %s found (current: %s). Downloading and installing update...", info.LatestVersion, u.cfg.CurrentVersion)

	applyCtx, applyCancel := context.WithTimeout(ctx, 10*time.Minute)
	err = u.cfg.ApplyFn(applyCtx, info)
	applyCancel()

	if err != nil {
		// Do not advance ETag on failure so the next poll retries cleanly.
		u.cfg.Logf("[WARN] Auto-update to %s failed: %v", info.LatestVersion, err)
		return false
	}

	u.mu.Lock()
	u.lastETag = newETag
	u.mu.Unlock()

	u.cfg.Logf("[INFO] Successfully installed update %s. Restarting automatically...", info.LatestVersion)

	if u.cfg.OnRestartFn != nil {
		if err := u.cfg.OnRestartFn(info); err != nil {
			u.cfg.Logf("[ERROR] Automatic restart after update to %s failed: %v", info.LatestVersion, err)
			return false
		}
		return true
	}

	return false
}

func (u *AutoUpdater) logThrottledError(msg string) {
	u.mu.Lock()
	shouldLog := msg != u.lastErrMsg || time.Since(u.lastErrLogged) >= 5*time.Minute
	if shouldLog {
		u.lastErrMsg = msg
		u.lastErrLogged = time.Now()
	}
	u.mu.Unlock()

	if shouldLog {
		u.cfg.Logf("[WARN] %s", msg)
	}
}

func (u *AutoUpdater) clearError() {
	u.mu.Lock()
	u.lastErrMsg = ""
	u.mu.Unlock()
}
