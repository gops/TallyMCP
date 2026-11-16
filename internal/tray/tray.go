package tray

import (
	"context"
)

// Config holds configuration parameters and callbacks for the system tray application.
type Config struct {
	Version       string
	Port          int
	PortChanged   bool
	OriginalPort  int
	ListenURL     string
	TallyURL      string
	ConfigPath    string
	GitHubRepo    string
	OnStartTally  func(ctx context.Context) (status, msg string, err error)
	OnCheckUpdate func(ctx context.Context) (hasUpdate bool, newVersion string, releaseNotes string, err error)
	OnApplyUpdate func(ctx context.Context, progressFn func(downloaded, total int64)) error
	OnQuit        func()
}
