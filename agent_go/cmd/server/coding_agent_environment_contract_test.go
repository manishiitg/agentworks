package server

import (
	"strings"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// The scoped coding-agent environment crosses three modules: AgentWorks mints
// the values, mcpagent copies them into the provider call, and
// multi-llm-provider-go overlays them onto the child process. Each layer used
// to decide independently which keys were legitimate, and the definitions
// drifted: AgentWorks created the MCP routes, the layer below admitted only
// SECRET_*, and the child never saw MCP_CUSTOM. Nothing errored. The feature
// simply did not work, which is indistinguishable from it not existing.
//
// llmtypes.IsScopedCodingAgentEnvironmentKey is now the single owner. These
// tests run from AgentWorks because AgentWorks is the layer that knows what it
// actually needs to pass; a test living beside the helper would only prove the
// helper agrees with itself.
//
// If you add a key to the policy, add a case here. The failure mode for an
// unlisted key is silence, so an untested key is an untested outage.

func mergedChildEnvironment(t *testing.T, environment map[string]string) map[string]string {
	t.Helper()
	opts := &llmtypes.CallOptions{}
	llmtypes.WithCodingAgentSecretEnvironment(environment)(opts)

	merged := map[string]string{}
	for _, entry := range llmtypes.MergeCodingAgentSecretEnvironment(nil, opts) {
		key, value, found := strings.Cut(entry, "=")
		if found {
			merged[key] = value
		}
	}
	return merged
}

// The acceptance criterion from native_coding_agent_environment_policy.md: a
// native structured child sees the routes and a selected secret, and does not
// inherit unrelated server state.
func TestScopedEnvironmentReachesTheChildProcess(t *testing.T) {
	merged := mergedChildEnvironment(t, map[string]string{
		"MCP_CUSTOM":         "http://127.0.0.1:19743/s/sess/custom",
		"MCP_AUTH":           "Authorization: Bearer token",
		"MCP_API_URL":        "http://127.0.0.1:19743/s/sess",
		"MCP_API_TOKEN":      "token",
		"MCP_SESSION_ID":     "sess",
		"MCP_MCP":            "http://127.0.0.1:19743/s/sess/mcp",
		"MCP_VIRTUAL":        "http://127.0.0.1:19743/s/sess/virtual",
		"SECRET_PEXELS_KEY":  "value",
		"VAR_GROUP_NAME":     "group-1",
		"VAR_WORKSPACE_PATH": "/workspace/project",

		// Must not cross the boundary. PATH in particular would let the child
		// inherit the server's toolchain resolution.
		"PATH":          "/usr/bin",
		"AWS_SECRET":    "not-a-SECRET_-prefixed-key",
		"HOME":          "/root",
		"DATABASE_URL":  "postgres://server",
		"MCP_UNRELATED": "not in the closed route set",
	})

	for _, key := range []string{
		"MCP_CUSTOM", "MCP_AUTH", "MCP_API_URL", "MCP_API_TOKEN",
		"MCP_SESSION_ID", "MCP_MCP", "MCP_VIRTUAL",
	} {
		if merged[key] == "" {
			t.Errorf("child cannot reach product APIs: %q did not survive to the subprocess environment", key)
		}
	}
	if merged["SECRET_PEXELS_KEY"] != "value" {
		t.Error("a selected secret did not reach the child")
	}
	for _, key := range []string{"PATH", "AWS_SECRET", "HOME", "DATABASE_URL", "MCP_UNRELATED"} {
		if _, present := merged[key]; present {
			t.Errorf("%q crossed the boundary; only SECRET_*, VAR_*, and the closed MCP route set may", key)
		}
	}
}

// Empty values are dropped rather than exported as empty strings, so a missing
// secret reads as absent instead of as a defined-but-blank variable that a
// `${VAR:?missing}` guard would happily accept.
func TestBlankValuesDoNotReachTheChildProcess(t *testing.T) {
	merged := mergedChildEnvironment(t, map[string]string{
		"SECRET_EMPTY": "",
		"VAR_EMPTY":    "",
		"MCP_CUSTOM":   "",
		"SECRET_REAL":  "value",
	})

	for _, key := range []string{"SECRET_EMPTY", "VAR_EMPTY", "MCP_CUSTOM"} {
		if _, present := merged[key]; present {
			t.Errorf("%q was exported with an empty value; a guard like ${%s:?missing} would not catch it", key, key)
		}
	}
	if merged["SECRET_REAL"] != "value" {
		t.Error("dropping blanks must not drop real values")
	}
}

// The policy must agree with itself at both ends: what the option setter admits
// is what the merge exports. These were separate inline predicates before, which
// is the shape the original drift took.
func TestPolicyOwnerAgreesAcrossBothBoundaries(t *testing.T) {
	cases := map[string]bool{
		"SECRET_A":                        true,
		"VAR_A":                           true,
		"MCP_CUSTOM":                      true,
		"MCP_AUTH":                        true,
		"CLAUDE_CODE_DISABLE_AUTO_MEMORY": true,
		"PATH":                            false,
		"MCP_ANYTHING":                    false,
		"SECRETX":                         false,
		"VARX":                            false,
	}
	for key, admitted := range cases {
		if got := llmtypes.IsScopedCodingAgentEnvironmentKey(key); got != admitted {
			t.Errorf("IsScopedCodingAgentEnvironmentKey(%q) = %v, want %v", key, got, admitted)
		}
		merged := mergedChildEnvironment(t, map[string]string{key: "v"})
		if _, present := merged[key]; present != admitted {
			t.Errorf("%q: policy says admitted=%v but the merged child environment disagrees", key, admitted)
		}
	}
}
