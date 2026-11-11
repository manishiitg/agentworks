package browserrelay

import "testing"

// A person renames or removes only their own saved browsers; removing one clears the Crew choice that pointed at it.
func TestRenameAndRemoveBrowserAreOwnerScoped(t *testing.T) {
	m := New()
	id := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	m.browserRecords[key("owner", id)] = BrowserConnection{ID: id, Name: "Chrome on Mac", Browser: "Chrome", Enabled: true}
	m.selectedBrowsers[key("owner", "crew-a")] = id
	if err := m.RenameBrowser("someone-else", id, "Mine now"); err == nil {
		t.Fatal("another person renamed this browser")
	}
	if err := m.RemoveBrowser("someone-else", id); err == nil {
		t.Fatal("another person removed this browser")
	}
	if err := m.RenameBrowser("owner", id, "  Work profile "); err != nil || m.browserRecords[key("owner", id)].Name != "Work profile" {
		t.Fatalf("own rename failed: %v", err)
	}
	if err := m.RemoveBrowser("owner", id); err != nil {
		t.Fatal(err)
	}
	if _, kept := m.browserRecords[key("owner", id)]; kept || m.selectedBrowsers[key("owner", "crew-a")] != "" {
		t.Fatal("removed browser or its Crew choice was kept")
	}
	if browserPlatform("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 Chrome/130") != "Mac" || browserPlatform("Mozilla/5.0 (X11; Linux x86_64)") != "Linux" {
		t.Fatal("platform not read from the user agent")
	}
}
