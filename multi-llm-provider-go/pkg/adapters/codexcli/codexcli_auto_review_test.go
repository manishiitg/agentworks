package codexcli

import (
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// #4602: an Auto-review Codex thread runs under Codex's own sandbox with the
// reach ours gives: AgentWorks' protected data closed except where the chat
// has a grant, writes only in its folders, no network, and the reviewer on.
// A sandbox mode would make Codex ignore the profile, so none is sent.
func TestCodexAutoReviewThreadKeepsOurSandboxLinesUnderCodexReviewer(t *testing.T) {
	policy := &llmtypes.CLISecurityPolicy{ProtectedRoots: []string{"/data/agentworks"}, WorkspaceWritePaths: []string{"/data/agentworks/projects/p1"}, PrivateHome: "/data/agentworks/private/home"}
	opts := &llmtypes.CallOptions{CLISecurity: policy, Metadata: &llmtypes.Metadata{Custom: map[string]interface{}{metadataKeyCodexAutoReview: true}}}
	params := map[string]any{"sandbox": "danger-full-access", "approvalPolicy": "never"}
	config := map[string]any{}
	codexAutoReviewThreadParams(params, config, opts, "/work/repo")
	if _, has := params["sandbox"]; has || params["approvalsReviewer"] != "auto_review" || params["approvalPolicy"] != "on-request" || params["permissions"] != codexAutoReviewProfile {
		t.Fatalf("thread params %+v", params)
	}
	profile := config["permissions"].(map[string]any)[codexAutoReviewProfile].(map[string]any)
	fs := profile["filesystem"].(map[string]any)
	for path, want := range map[string]string{":root": "read", "/data/agentworks": "deny", "/data/agentworks/projects/p1": "write", "/data/agentworks/private/home": "write", "/work/repo": "write"} {
		if fs[path] != want {
			t.Errorf("%s = %v, want %s", path, fs[path], want)
		}
	}
	if profile["network"].(map[string]any)["enabled"] != false {
		t.Fatal("network must go through the reviewer")
	}

	plain := map[string]any{"sandbox": "danger-full-access"}
	codexAutoReviewThreadParams(plain, map[string]any{}, &llmtypes.CallOptions{CLISecurity: policy}, "/work/repo")
	if plain["sandbox"] != "danger-full-access" || plain["approvalsReviewer"] != nil {
		t.Fatalf("an Auto turn changed: %+v", plain)
	}
}
