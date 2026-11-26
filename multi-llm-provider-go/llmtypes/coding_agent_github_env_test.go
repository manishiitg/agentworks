package llmtypes

import (
	"slices"
	"testing"
)

func TestScopedCodingAgentEnvironmentPreservesGitHubCLIAndScrubsAmbient(t *testing.T) {
	opts := &CallOptions{}
	WithCodingAgentSecretEnvironment(map[string]string{
		"SECRET_GITHUB_TOKEN": "ghp_project_a",
		"GH_TOKEN":            "ghp_project_a",
		"GIT_CONFIG_COUNT":    "2",
		"GIT_CONFIG_KEY_0":    "credential.https://github.com.helper",
		"GIT_CONFIG_VALUE_0":  "",
		"GIT_CONFIG_KEY_1":    "credential.https://github.com.helper",
		"GIT_CONFIG_VALUE_1":  "!helper",
		"GIT_CONFIG_KEY_BAD":  "must-not-pass",
		"GIT_CONFIG_VALUE_X":  "must-not-pass",
		"SECRET_EMPTY":        "",
	})(opts)

	base := []string{
		"PATH=/usr/bin",
		"GH_TOKEN=ghp_ambient_other_account",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=credential.https://github.com.helper",
		"GIT_CONFIG_VALUE_0=!ambient_helper",
	}
	merged := MergeCodingAgentSecretEnvironment(base, opts)

	for _, want := range []string{
		"PATH=/usr/bin",
		"SECRET_GITHUB_TOKEN=ghp_project_a",
		"GH_TOKEN=ghp_project_a",
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=credential.https://github.com.helper",
		"GIT_CONFIG_VALUE_0=",
		"GIT_CONFIG_KEY_1=credential.https://github.com.helper",
		"GIT_CONFIG_VALUE_1=!helper",
	} {
		if !slices.Contains(merged, want) {
			t.Fatalf("merged env missing %q: %v", want, merged)
		}
	}
	for _, forbidden := range []string{
		"GH_TOKEN=ghp_ambient_other_account",
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_VALUE_0=!ambient_helper",
		"GIT_CONFIG_KEY_BAD=must-not-pass",
		"GIT_CONFIG_VALUE_X=must-not-pass",
		"SECRET_EMPTY=",
	} {
		if slices.Contains(merged, forbidden) {
			t.Fatalf("merged env unexpectedly kept %q: %v", forbidden, merged)
		}
	}

	// A project with no GitHub connection must scrub ambient GH_TOKEN and GIT_CONFIG_*.
	emptyOpts := &CallOptions{}
	WithCodingAgentSecretEnvironment(map[string]string{})(emptyOpts)
	scrubbed := MergeCodingAgentSecretEnvironment(base, emptyOpts)
	for _, entry := range scrubbed {
		if entry != "PATH=/usr/bin" {
			t.Fatalf("empty scope leaked ambient GitHub/git entry %q: %v", entry, scrubbed)
		}
	}

	// Interactive/tmux plan must export empty GIT_CONFIG_VALUE_0 and unset ambient GH_TOKEN/GIT_CONFIG_* when absent.
	exports, unsets := ScopedCodingAgentEnvironmentPlan(base, nil, emptyOpts)
	if len(exports) != 0 {
		t.Fatalf("expected no exports for empty scope, got %v", exports)
	}
	for _, wantUnset := range []string{"GH_TOKEN", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_0", "GIT_CONFIG_VALUE_0"} {
		if !slices.Contains(unsets, wantUnset) {
			t.Fatalf("expected %q in unset list, got %v", wantUnset, unsets)
		}
	}
	exports, _ = ScopedCodingAgentEnvironmentPlan(base, nil, opts)
	if !slices.Contains(exports, "GH_TOKEN=ghp_project_a") || !slices.Contains(exports, "GIT_CONFIG_VALUE_0=") {
		t.Fatalf("expected GH_TOKEN and empty GIT_CONFIG_VALUE_0 in exports, got %v", exports)
	}
}
