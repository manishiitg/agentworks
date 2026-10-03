package handlers

import "testing"

// A terminal without the sandbox is only for a person's own native, single-user machine that opted in. It must never be possible on a
// server: not without the switch, not outside native mode, not where per-user accounts are on.
func TestInteractiveShellUnconfinedIsLocalOnly(t *testing.T) {
	cases := []struct {
		name                          string
		terminalSwitch, native, slots string
		want                          bool
	}{
		{"local machine that opted in", "on", "true", "", true},
		{"a server that never set the switch", "", "true", "", false},
		{"switch off", "off", "true", "", false},
		{"switch on but not native mode", "on", "false", "", false},
		{"switch on, native, per-user accounts on", "on", "true", "on", false},
		{"switch on, native, per-user accounts opt-in", "on", "true", "optin", false},
	}
	for _, c := range cases {
		t.Setenv("AGENTWORKS_TERMINAL_UNCONFINED", c.terminalSwitch)
		t.Setenv("NATIVE_WORKSPACE", c.native)
		t.Setenv("AGENTWORKS_SLOTS", c.slots)
		if got := interactiveShellUnconfinedAllowed(); got != c.want {
			t.Errorf("%s: allowed = %v, want %v", c.name, got, c.want)
		}
	}
}
