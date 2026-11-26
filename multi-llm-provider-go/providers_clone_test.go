package llmproviders

import "testing"

func TestProviderAPIKeysClonePreservesClaudeCodeOAuthToken(t *testing.T) {
	token := "workflow-token"
	original := &ProviderAPIKeys{ClaudeCodeOAuthToken: &token}
	clone := original.Clone()
	if clone == nil || clone.ClaudeCodeOAuthToken == nil || *clone.ClaudeCodeOAuthToken != token {
		t.Fatalf("Clone() lost Claude Code OAuth token: %#v", clone)
	}
}
