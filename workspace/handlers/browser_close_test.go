package handlers

import "testing"

func TestCommandClosesBrowser(t *testing.T) {
	for command, want := range map[string]bool{
		`agent-browser --session s --profile /p --idle-timeout 0 --args --no-sandbox --json close`: true,
		`agent-browser --session s close`:                                                           true,
		`agent-browser --session s open https://example.com`:                                        false,
		`agent-browser --session s close; echo more`:                                                false,
		`echo close`:                                                                                false,
	} {
		if got := commandClosesBrowser(command); got != want {
			t.Errorf("commandClosesBrowser(%q) = %v, want %v", command, got, want)
		}
	}
}
