package relay

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestValidateServerAndWSURL(t *testing.T) {
	validHTTP := []string{
		"https://1erp.work",
		"https://1erp.work/",
		"http://localhost:3000",
		"http://127.0.0.1:8080",
	}
	for _, u := range validHTTP {
		if _, err := ValidateServerURL(u); err != nil {
			t.Errorf("expected valid server URL %q, got error: %v", u, err)
		}
	}

	invalidHTTP := []string{
		"http://1erp.work",
		"ftp://1erp.work",
		"http://example.com/api",
	}
	for _, u := range invalidHTTP {
		if _, err := ValidateServerURL(u); err == nil {
			t.Errorf("expected error for insecure/invalid server URL %q", u)
		}
	}

	if _, err := ValidateWSURL("wss://1erp.work/api/tally-relay/ws"); err != nil {
		t.Errorf("expected valid wss URL, got %v", err)
	}
	if _, err := ValidateWSURL("ws://1erp.work/api/tally-relay/ws"); err == nil {
		t.Errorf("expected error for non-TLS ws:// on remote host")
	}
	if _, err := ValidateWSURL("ws://localhost:3000/ws"); err != nil {
		t.Errorf("expected localhost ws:// to be allowed, got %v", err)
	}
}

func TestCredentialsSaveLoadDelete(t *testing.T) {
	tmpDir := t.TempDir()
	credsPath := filepath.Join(tmpDir, "sub", "1erp-credentials.json")

	want := &Credentials{
		AccessToken:  "tok-secret-123",
		RefreshToken: "ref-secret-456",
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().UTC().Add(1 * time.Hour).Truncate(time.Second),
		TenantID:     "tenant-1",
		UserEmail:    "owner@acme.com",
		DeviceID:     "dev-abc",
		DeviceName:   "OFFICE-PC",
		RelayWSURL:   "wss://1erp.work/api/tally-relay/ws",
		ServerURL:    "https://1erp.work",
	}

	if err := SaveCredentials(credsPath, want); err != nil {
		t.Fatalf("SaveCredentials failed: %v", err)
	}

	if runtime.GOOS != "windows" {
		fi, err := os.Stat(credsPath)
		if err != nil {
			t.Fatalf("stat credentials: %v", err)
		}
		if fi.Mode().Perm() != 0600 {
			t.Errorf("expected 0600 file permissions, got %o", fi.Mode().Perm())
		}
	}

	got, err := LoadCredentials(credsPath)
	if err != nil {
		t.Fatalf("LoadCredentials failed: %v", err)
	}
	if got.AccessToken != want.AccessToken || got.UserEmail != want.UserEmail || got.TenantID != want.TenantID {
		t.Errorf("loaded credentials mismatch: got %+v, want %+v", got, want)
	}

	if err := DeleteCredentials(credsPath); err != nil {
		t.Fatalf("DeleteCredentials failed: %v", err)
	}
	if _, err := LoadCredentials(credsPath); err != ErrNotLoggedIn {
		t.Errorf("expected ErrNotLoggedIn after delete, got %v", err)
	}
}

func TestClientRefusesWhenNotLoggedIn(t *testing.T) {
	tmpDir := t.TempDir()
	credsPath := filepath.Join(tmpDir, "missing.json")
	c := NewClient(credsPath, false, "v1.0.0", nil)

	if err := c.Run(context.Background()); err != nil {
		t.Fatalf("expected nil return when not logged in, got %v", err)
	}
	st := c.Status()
	if st.State != StateAuthRequired || st.LoggedIn {
		t.Errorf("expected StateAuthRequired and LoggedIn=false, got %+v", st)
	}
}

func TestReadSafePDFFileBlocksTraversal(t *testing.T) {
	tmpDir := t.TempDir()
	pdfDir := filepath.Join(tmpDir, "pdfs")
	_ = os.MkdirAll(pdfDir, 0755)
	_ = os.WriteFile(filepath.Join(tmpDir, "secret.pdf"), []byte("secret"), 0600)
	_ = os.WriteFile(filepath.Join(pdfDir, "notes.txt"), []byte("text"), 0600)
	_ = os.WriteFile(filepath.Join(pdfDir, "invoice.pdf"), []byte("%PDF-1.4"), 0600)

	badNames := []string{
		"",
		"../secret.pdf",
		"..\\secret.pdf",
		"sub/invoice.pdf",
		"notes.txt",
		"missing.pdf",
	}
	for _, name := range badNames {
		if _, err := ReadSafePDFFile(pdfDir, name); err == nil {
			t.Errorf("expected ReadSafePDFFile(%q) to fail", name)
		}
	}

	data, err := ReadSafePDFFile(pdfDir, "invoice.pdf")
	if err != nil {
		t.Fatalf("expected invoice.pdf to succeed, got %v", err)
	}
	if string(data) != "%PDF-1.4" {
		t.Errorf("unexpected PDF bytes: %q", string(data))
	}
}

func TestWSConnFrameRoundTrip(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()

	ws1 := WrapWSConn(c1)
	ws2 := WrapWSConn(c2)

	errCh := make(chan error, 1)
	go func() {
		errCh <- ws1.WriteText([]byte(`{"type":"ping","id":"1"}`))
	}()

	msg, err := ws2.ReadMessage(context.Background())
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("WriteText failed: %v", err)
	}
	if !strings.Contains(string(msg), `"type":"ping"`) {
		t.Errorf("unexpected message: %s", string(msg))
	}
}
