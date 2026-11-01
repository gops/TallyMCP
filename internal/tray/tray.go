package tray

import (
	"context"
)

// Config holds configuration parameters and callbacks for the system tray application.
type Config struct {
	Version       string
	ListenURL     string
	TallyURL      string
	ConfigPath    string
	GitHubRepo    string
	OnCheckUpdate func(ctx context.Context) (hasUpdate bool, newVersion string, releaseNotes string, err error)
	OnApplyUpdate func(ctx context.Context, progressFn func(downloaded, total int64)) error
	OnQuit        func()
}
