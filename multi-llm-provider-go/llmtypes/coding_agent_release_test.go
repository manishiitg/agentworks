package llmtypes

import (
	"strings"
	"testing"
)

func TestReleaseSessionDoesNotChangeCredentialScope(t *testing.T) {
	opts := &CallOptions{}
	WithCodingAgentReleaseSession("chat-a")(opts)
	if CodingAgentScopeDeclared(opts) {
		t.Fatal("runtime key declared a credential scope")
	}
	base := []string{"SECRET_EXISTING=keep", "AGENTWORKS_CLI_SESSION_KEY=stale"}
	env := MergeCodingAgentSecretEnvironment(base, opts)
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "SECRET_EXISTING=keep") || strings.Contains(joined, "stale") {
		t.Fatal(env)
	}
	if base[1] != "AGENTWORKS_CLI_SESSION_KEY=stale" {
		t.Fatal("mutated caller environment")
	}
	export, unset := ScopedCodingAgentEnvironmentPlan(nil, nil, opts)
	if len(export) != 1 || len(unset) != 0 || len(strings.TrimPrefix(export[0], "AGENTWORKS_CLI_SESSION_KEY=")) != 64 {
		t.Fatalf("%v %v", export, unset)
	}
	WithCodingAgentSecretEnvironment(map[string]string{"SECRET_SELECTED": "allowed"})(opts)
	joined = strings.Join(MergeCodingAgentSecretEnvironment(base, opts), "\n")
	if strings.Contains(joined, "SECRET_EXISTING") || !strings.Contains(joined, "SECRET_SELECTED=allowed") || !strings.Contains(joined, export[0]) {
		t.Fatal(joined)
	}
	other := &CallOptions{}
	WithCodingAgentReleaseSession("chat-b")(other)
	if codingAgentReleaseSessionKey(opts) == codingAgentReleaseSessionKey(other) {
		t.Fatal("different chats shared a release pin")
	}
	resume := &CallOptions{}
	WithCodingAgentReleaseSession("chat-a")(resume)
	if codingAgentReleaseSessionKey(opts) != codingAgentReleaseSessionKey(resume) {
		t.Fatal("resume changed release pin")
	}
}
