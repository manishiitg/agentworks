package llmproviders

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestClaudeCodeOAuthTokenClonesButDoesNotSerialize(t *testing.T) {
	token := "workflow-oauth-secret"
	keys := &ProviderAPIKeys{ClaudeCodeOAuthToken: &token}
	cloned := keys.Clone()
	if cloned == nil || cloned.ClaudeCodeOAuthToken == nil || *cloned.ClaudeCodeOAuthToken != token {
		t.Fatalf("Clone() lost Claude Code OAuth token: %#v", cloned)
	}
	data, err := json.Marshal(keys)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(data), token) || strings.Contains(string(data), "ClaudeCodeOAuthToken") {
		t.Fatalf("serialized provider keys exposed Claude Code OAuth token: %s", data)
	}
}
