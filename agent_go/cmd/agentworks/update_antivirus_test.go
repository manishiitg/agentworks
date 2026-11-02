package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// PLAT-856-222: an antivirus launch block must give recovery steps and retain
// the verified final file/old backup, rather than claim the old version ran.
func TestUpdateAntivirusBlockKeepsDownloadAndExplainsRecovery(t *testing.T) {
	payload := []byte("verified update fixture")
	var compressed bytes.Buffer
	w := gzip.NewWriter(&compressed)
	_, _ = w.Write(payload)
	_ = w.Close()
	sum := sha256.Sum256(payload)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".gz") {
			_, _ = w.Write(compressed.Bytes())
		} else {
			_, _ = fmt.Fprintf(w, "%x  agentworks.exe\n", sum)
		}
	}))
	defer server.Close()
	previous := updateVersionOutput
	t.Cleanup(func() { updateVersionOutput = previous })
	for _, code := range []syscall.Errno{225, 226} {
		target := filepath.Join(t.TempDir(), "agentworks.exe")
		if err := os.WriteFile(target, []byte("previous"), 0700); err != nil {
			t.Fatal(err)
		}
		updateVersionOutput = func(path string) ([]byte, error) {
			return nil, &os.PathError{Op: "fork/exec", Path: path, Err: code}
		}
		_, err := installUpdate(server.URL, "agentworks.exe", target, cliRelease{Version: "test"}, false, nil)
		if err == nil || !strings.Contains(err.Error(), "Protection history") || !strings.Contains(err.Error(), "agentworks setup") {
			t.Fatalf("antivirus block lost its next step: %v", err)
		}
		if got, err := os.ReadFile(target); err != nil || !bytes.Equal(got, payload) {
			t.Fatalf("blocked final download was replaced: %q, %v", got, err)
		}
		if got, err := os.ReadFile(target + ".bak"); err != nil || string(got) != "previous" {
			t.Fatalf("previous executable was lost: %q, %v", got, err)
		}
	}
	if windowsAntivirusBlocked(&os.PathError{Err: syscall.Errno(5)}) {
		t.Fatal("ordinary access denial was misdiagnosed as antivirus")
	}
}
