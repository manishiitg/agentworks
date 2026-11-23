package picli

import (
	"reflect"
	"testing"
)

// A resolved API key never reached the pi subprocess's environment on the
// structured path (only the interactive/tmux path converted it to env vars),
// so a correctly-resolved workspace-scoped Gemini key still produced "pi run
// returned no text output" -- pi ran with no credential at all. This pins the
// fix: override wins over any conflicting ambient value, and leaves
// unrelated ambient vars untouched.
func TestPiOverrideEnvReplacesConflictingKeysOnly(t *testing.T) {
	base := []string{
		"PATH=/usr/bin",
		"GEMINI_API_KEY=stale_ambient_key",
		"HOME=/home/test",
	}
	overrides := piAPIKeyEnv("google", "resolved_workspace_key")

	got := piOverrideEnv(base, overrides)

	byKey := map[string]string{}
	for _, entry := range got {
		if idx := indexOfEquals(entry); idx >= 0 {
			byKey[entry[:idx]] = entry[idx+1:]
		}
	}

	if byKey["GEMINI_API_KEY"] != "resolved_workspace_key" {
		t.Fatalf("GEMINI_API_KEY = %q, want the resolved key to win over the stale ambient one", byKey["GEMINI_API_KEY"])
	}
	if byKey["GOOGLE_API_KEY"] != "resolved_workspace_key" || byKey["PI_API_KEY"] != "resolved_workspace_key" {
		t.Fatalf("google provider's other env aliases missing/wrong: %#v", byKey)
	}
	if byKey["PATH"] != "/usr/bin" || byKey["HOME"] != "/home/test" {
		t.Fatalf("unrelated ambient vars were disturbed: %#v", byKey)
	}

	// Exactly one GEMINI_API_KEY entry -- not the stale one still sitting
	// alongside the new one relying on "last wins" to be correct.
	count := 0
	for _, entry := range got {
		if len(entry) >= len("GEMINI_API_KEY=") && entry[:len("GEMINI_API_KEY=")] == "GEMINI_API_KEY=" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("GEMINI_API_KEY appears %d times in the resulting env, want exactly 1: %#v", count, got)
	}
}

func TestPiOverrideEnvNoOverridesReturnsBaseUnchanged(t *testing.T) {
	base := []string{"PATH=/usr/bin"}
	got := piOverrideEnv(base, nil)
	if !reflect.DeepEqual(got, base) {
		t.Fatalf("piOverrideEnv(base, nil) = %#v, want unchanged base %#v", got, base)
	}
}

func indexOfEquals(s string) int {
	for i, r := range s {
		if r == '=' {
			return i
		}
	}
	return -1
}
