package handlers

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestBrowserLivePortValidatesMetadata(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENT_BROWSER_SOCKET_DIR", dir)
	for _, input := range []string{"../escape", "", "a/b", "--cdp"} {
		// --cdp is a valid filename but must still have runtime metadata.
		if _, err := browserLivePort(input); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
	for _, value := range []string{"0", "65536", "http://example.com", "123\n456"} {
		os.WriteFile(filepath.Join(dir, "live-test.stream"), []byte(value), 0600)
		if _, err := browserLivePort("live-test"); err == nil {
			t.Fatalf("accepted port %q", value)
		}
	}
	os.WriteFile(filepath.Join(dir, "live-test.stream"), []byte("12345\n"), 0600)
	if port, err := browserLivePort("live-test"); err != nil || port != 12345 {
		t.Fatalf("port=%d err=%v", port, err)
	}
}

func TestBrowserLiveUnavailableDoesNotStartBrowser(t *testing.T) {
	t.Setenv("AGENT_BROWSER_CDP_ENABLED", "false")
	router := gin.New()
	router.GET("/stream/:session", BrowserLiveStream)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/stream/nonexistent-live-test", nil))
	if response.Code != http.StatusNotFound {
		t.Fatal(response.Code)
	}
}

func TestBrowserLivePortUsesSystemdRuntimeDirectory(t *testing.T) {
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	t.Setenv("AGENT_BROWSER_SOCKET_DIR", "")
	dir := filepath.Join(runtimeDir, "agent-browser")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "systemd-live-test.stream"), []byte("12346"), 0600); err != nil {
		t.Fatal(err)
	}
	if port, err := browserLivePort("systemd-live-test"); err != nil || port != 12346 {
		t.Fatalf("port=%d err=%v", port, err)
	}
}

func TestBrowserLiveEndpointFindsNamespacedMetadataBeforeLegacy(t *testing.T) {
	for _, key := range []string{"AGENT_BROWSER_SOCKET_DIR", "XDG_RUNTIME_DIR"} {
		t.Run(key, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("AGENT_BROWSER_SOCKET_DIR", "")
			t.Setenv("XDG_RUNTIME_DIR", "")
			t.Setenv(key, root)
			t.Setenv("AGENT_BROWSER_NAMESPACE", "example-app-test")
			base := root
			if key == "XDG_RUNTIME_DIR" {
				base = filepath.Join(root, "agent-browser")
			}
			dir := filepath.Join(base, "namespaces", "example-app-test", "run")
			if err := os.MkdirAll(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(base, "namespace-live-test.stream"), []byte("12345"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "namespace-live-test.stream"), []byte("12346"), 0600); err != nil {
				t.Fatal(err)
			}
			port, foundDir, err := browserLiveEndpoint("namespace-live-test")
			if err != nil || port != 12346 || foundDir != dir {
				t.Fatalf("port=%d dir=%q err=%v", port, foundDir, err)
			}
		})
	}
}
