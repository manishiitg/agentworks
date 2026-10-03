package handlers

import "testing"

// A terminal without the sandbox is only for a person's own Mac in native mode. It must never be possible on a server: not on Linux,
// not outside native mode, not where per-user accounts are on.
func TestInteractiveShellUnconfinedIsLocalOnly(t *testing.T) {
	orig := interactiveShellHostOS
	t.Cleanup(func() { interactiveShellHostOS = orig })
	cases := []struct {
		name, hostOS, native, slots string
		want                        bool
	}{
		{"a person's own Mac", "darwin", "true", "", true},
		{"a Linux server, even in native mode", "linux", "true", "", false},
		{"a Mac not in native mode", "darwin", "false", "", false},
		{"a Mac with per-user accounts on", "darwin", "true", "on", false},
		{"a Mac with per-user accounts opt-in", "darwin", "true", "optin", false},
		{"a Linux server with per-user accounts", "linux", "true", "on", false},
	}
	for _, c := range cases {
		interactiveShellHostOS = c.hostOS
		t.Setenv("NATIVE_WORKSPACE", c.native)
		t.Setenv("AGENTWORKS_SLOTS", c.slots)
		if got := interactiveShellUnconfinedAllowed(); got != c.want {
			t.Errorf("%s: allowed = %v, want %v", c.name, got, c.want)
		}
	}
}
