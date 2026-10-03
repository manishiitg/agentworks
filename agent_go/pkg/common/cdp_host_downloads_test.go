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
	if CDPHostDownloadsPath("cdp") == "" {
		t.Fatal("local single-user CDP should get the host Downloads folder")
	}
	if CDPHostDownloadsPath("headless") != "" {
		t.Fatal("non-CDP mode must not get it")
	}
	t.Setenv("MULTI_USER_MODE", "true")
	if CDPHostDownloadsPath("cdp") != "" {
		t.Fatal("a multi-user server must never get it")
	}
	t.Setenv("MULTI_USER_MODE", "")
	t.Setenv("LOCAL_MODE", "")
	if CDPHostDownloadsPath("cdp") != "" {
		t.Fatal("a non-local server without an explicit folder must not get it")
	}
	t.Setenv("HOST_DOWNLOADS_PATH", "/data/downloads")
	if got := CDPHostDownloadsPath("cdp"); got != "/data/downloads" {
		t.Fatalf("an explicitly named folder: got %q", got)
	}
}
