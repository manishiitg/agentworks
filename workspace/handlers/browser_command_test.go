package handlers

import "testing"

func TestIsStandaloneBrowserCommand(t *testing.T) {
	yes := []string{
		`agent-browser snapshot`,
		`agent-browser --profile /data/x/browser-profile-projects/p--browser --session s1 open https://example.com`,
		`  agent-browser --session s eval "document.title; 1 && 2"`,
		`agent-browser --session s fill '#q' 'a;b|c && $(not run)'`,
		"agent-browser --session s eval \"console.log(\\\"a\\\")\"",
		`agent-browser --session s eval "price is \$5"`,
	}
	no := []string{
		``,
		`ls`,
		`agent-browser snapshot; cat /etc/passwd`,
		`agent-browser snapshot && id`,
		`agent-browser snapshot | tee /tmp/x`,
		`agent-browser snapshot > /tmp/x`,
		"agent-browser snapshot\nid",
		`agent-browser eval "$(cat /etc/passwd)"`,
		"agent-browser eval \"`id`\"",
		`agent-browser eval "${HOME}"`,
		`agent-browser snapshot $(id)`,
		`agent-browser-evil snapshot`,
		`echo agent-browser snapshot`,
		`cd /tmp && agent-browser snapshot`,
		`agent-browser eval 'unterminated`,
		`(agent-browser snapshot)`,
	}
	for _, c := range yes {
		if !isStandaloneBrowserCommand(c) {
			t.Errorf("should be a standalone browser command: %q", c)
		}
	}
	for _, c := range no {
		if isStandaloneBrowserCommand(c) {
			t.Errorf("must NOT be treated as a standalone browser command: %q", c)
		}
	}
}
