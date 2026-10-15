package server

import "testing"

func TestAccountPhrase(t *testing.T) {
	for _, c := range []struct{ provider, name, want string }{
		{"codex-cli", adminManagedProviderAccountName, "the shared (admin-managed) Codex account"},
		{"claude-code", adminManagedProviderAccountName, "the shared (admin-managed) Claude account"},
		{"codex-cli", "Work key", "the Codex account “Work key”"},
		{"codex-cli", "  ", ""},
		{"some-api", "Team", "the some-api account “Team”"},
	} {
		if got := accountPhrase(c.provider, c.name); got != c.want {
			t.Errorf("accountPhrase(%q,%q) = %q, want %q", c.provider, c.name, got, c.want)
		}
	}
}
