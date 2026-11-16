package tally_test

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"tally-mcp/internal/config"
	"tally-mcp/internal/tally"
)

func TestLauncherHelperFunctions(t *testing.T) {
	t.Run("FindTallyExecutable Custom Existing", func(t *testing.T) {
		tmpDir := t.TempDir()
		fakeExe := filepath.Join(tmpDir, "tally.exe")
		if err := os.WriteFile(fakeExe, []byte("fake binary"), 0755); err != nil {
			t.Fatalf("failed to create fake exe: %v", err)
		}

		found, err := tally.FindTallyExecutable(fakeExe)
		if err != nil {
			t.Fatalf("expected to find fake exe: %v", err)
		}
		if found != fakeExe {
			t.Errorf("expected %s, got %s", fakeExe, found)
		}
	})

	t.Run("FindTallyExecutable Custom Non-Existent", func(t *testing.T) {
		_, err := tally.FindTallyExecutable("/non/existent/path/tally.exe")
		if err == nil {
			t.Errorf("expected error for non-existent path")
		}
	})
}

func TestClientStartTallyAlreadyRunning(t *testing.T) {
	mockTallyResponse := `<ENVELOPE>
  <BODY>
    <DATA>
      <COLLECTION>
        <COMPANY NAME="Acme Corporation Ltd">
          <FORMALNAME>Acme Corp</FORMALNAME>
        </COMPANY>
      </COLLECTION>
    </DATA>
  </BODY>
</ENVELOPE>`

	cfg := config.TallyConfig{
		URL:            "http://localhost:9000",
		TimeoutSeconds: 5,
	}
	client := tally.NewClient(cfg)
	client.SetTransport(roundTripFunc(func(req *http.Request) *http.Response {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(mockTallyResponse)),
			Header:     make(http.Header),
		}
	}))

	ctx := context.Background()
	if !client.IsRunning(ctx) {
		t.Fatalf("expected client.IsRunning to return true with mock response")
	}

	res, err := client.StartTally(ctx, "", 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Status != "already_running" {
		t.Errorf("expected status 'already_running', got '%s'", res.Status)
	}
	if !res.Connected {
		t.Errorf("expected connected true")
	}
	if res.CompaniesCount != 1 {
		t.Errorf("expected 1 company, got %d", res.CompaniesCount)
	}
}

func TestClientStartTallyRemoteRejected(t *testing.T) {
	cfg := config.TallyConfig{
		URL:            "http://192.168.100.250:9000",
		TimeoutSeconds: 5,
	}
	client := tally.NewClient(cfg)

	ctx := context.Background()
	res, err := client.StartTally(ctx, "", 5*time.Second)
	if err == nil {
		t.Fatalf("expected error when trying to start remote Tally")
	}
	if res == nil || res.Status != "failed" {
		t.Errorf("expected status 'failed', got: %v", res)
	}
	if !strings.Contains(res.Message, "remote") {
		t.Errorf("expected message mentioning remote, got: %s", res.Message)
	}
}

func TestClientAutoStartOnQuery(t *testing.T) {
	tmpDir := t.TempDir()
	fakeExe := filepath.Join(tmpDir, "tally.exe")
	if err := os.WriteFile(fakeExe, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("failed to create fake exe: %v", err)
	}

	cfg := config.TallyConfig{
		URL:            "http://localhost:9000",
		TimeoutSeconds: 5,
		AutoStart:      true,
		ExePath:        fakeExe,
	}
	client := tally.NewClient(cfg)

	calls := 0
	client.SetTransport(roundTripFunc(func(req *http.Request) *http.Response {
		calls++
		if calls == 1 {
			// First call simulates down server
			return nil
		}
		// Subsequent calls succeed
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(strings.NewReader(`<ENVELOPE><BODY><DATA><COLLECTION>
				<COMPANY><NAME>Started Corp</NAME></COMPANY>
			</COLLECTION></DATA></BODY></ENVELOPE>`)),
			Header: make(http.Header),
		}
	}))

	// Verify properties
	if client.ExePath() != fakeExe {
		t.Errorf("expected ExePath %s, got %s", fakeExe, client.ExePath())
	}
	if !client.AutoStartEnabled() {
		t.Errorf("expected AutoStart true")
	}

	client.SetAutoStart(false)
	if client.AutoStartEnabled() {
		t.Errorf("expected AutoStart false after SetAutoStart")
	}
	client.SetAutoStart(true)

	client.SetExePath("/tmp/other.exe")
	if client.ExePath() != "/tmp/other.exe" {
		t.Errorf("expected updated ExePath")
	}
	client.SetExePath(fakeExe)
}
