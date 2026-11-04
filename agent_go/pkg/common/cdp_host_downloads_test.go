package common

import "testing"

// The host Downloads folder is granted only on a person's own machine in CDP
// mode: never on a multi-user server, and not on a non-local server unless the
// deployment names the folder explicitly.
func TestCDPHostDownloadsPathOnlyOnAPersonsOwnMachine(t *testing.T) {
	for _, key := range []string{"PI_HOST_DOWNLOADS_PATH", "HOST_DOWNLOADS_PATH"} {
		t.Setenv(key, "")
	}
	t.Setenv("MULTI_USER_MODE", "")
	t.Setenv("LOCAL_MODE", "true")
	if CDPHostDownloadsPath("cdp") != "" {
		t.Fatal("a person's own Downloads must not be opened by default, even in local mode")
	}
	t.Setenv("HOST_DOWNLOADS_PATH", "/data/downloads")
	if CDPHostDownloadsPath("cdp") == "" {
		t.Fatal("an explicitly named folder should be granted in CDP mode")
	}
	t.Setenv("HOST_DOWNLOADS_PATH", "")
	if CDPHostDownloadsPath("headless") != "" {
		t.Fatal("non-CDP mode must not get it")
	}
	t.Setenv("HOST_DOWNLOADS_PATH", "/data/downloads")
	t.Setenv("MULTI_USER_MODE", "true")
	if CDPHostDownloadsPath("cdp") != "" {
		t.Fatal("a multi-user server must never get it")
	}
	t.Setenv("MULTI_USER_MODE", "")
	t.Setenv("LOCAL_MODE", "")
	t.Setenv("HOST_DOWNLOADS_PATH", "")
	if CDPHostDownloadsPath("cdp") != "" {
		t.Fatal("a non-local server without an explicit folder must not get it")
	}
	t.Setenv("HOST_DOWNLOADS_PATH", "/data/downloads")
	if got := CDPHostDownloadsPath("cdp"); got != "/data/downloads" {
		t.Fatalf("an explicitly named folder: got %q", got)
	}
}

// PLAT-856-525: choosing Mac Downloads for captures must not expose it to servers.
func TestLocalCaptureDownloadsIsPersonalAndRevocable(t *testing.T) {
	oldOS := localCaptureHostOS
	oldRemoved := HostDownloadsRemoved.Load()
	t.Cleanup(func() { localCaptureHostOS = oldOS; HostDownloadsRemoved.Store(oldRemoved) })
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NATIVE_WORKSPACE", "true")
	t.Setenv("MULTI_USER_MODE", "false")
	localCaptureHostOS = "darwin"
	HostDownloadsRemoved.Store(false)
	if LocalCaptureDownloadsPath() == "" {
		t.Fatal("personal Mac has no capture destination")
	}
	HostDownloadsRemoved.Store(true)
	if LocalCaptureDownloadsPath() != "" {
		t.Fatal("removed grant was reopened")
	}
	HostDownloadsRemoved.Store(false)
	t.Setenv("MULTI_USER_MODE", "true")
	if LocalCaptureDownloadsPath() != "" {
		t.Fatal("multi-user server received Downloads")
	}
	t.Setenv("MULTI_USER_MODE", "false")
	localCaptureHostOS = "linux"
	if LocalCaptureDownloadsPath() != "" {
		t.Fatal("Linux server received Downloads")
	}
}
