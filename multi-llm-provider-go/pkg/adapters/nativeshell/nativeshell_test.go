package nativeshell

import "testing"

func TestEnabledIsOnUnlessSetOff(t *testing.T) {
	for value, want := range map[string]bool{"": true, "on": true, "garbage": true, "off": false, "OFF": false, " 0 ": false, "false": false, "no": false} {
		t.Setenv(EnvVar, value)
		if got := Enabled(); got != want {
			t.Errorf("%s=%q: Enabled()=%v, want %v", EnvVar, value, got, want)
		}
	}
}
