package server

import (
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/workproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
)

// Controlled server-composition fixtures. These include product identity,
// feature constraints, memory and workspace sections. mcpagent routing, native
// skill metadata, tool schemas, conversation history and provider wrappers are
// separate costs: do not report these lengths as total provider-request tokens.
func TestComposedProductPromptFixtures(t *testing.T) {
	for _, tc := range []struct {
		name     string
		profile  agentprofiles.Profile
		readonly bool
	}{
		{"Code", codeproduct.BuiltinAgentProfile(), false},
		{"CrewBuilder", workproduct.BuiltinAgentProfile(), false},
		{"CrewRun", workproduct.BuiltinAgentProfile(), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile := tc.profile
			if tc.readonly {
				profile.SystemPromptTemplate = workproduct.RunPromptTemplate()
			}
			base, err := agentprofiles.RenderPrompt(profile, agentprofiles.PromptContext{ProjectTitle: "Example", LocalDateTime: "Thursday, 1 October 2026 at 9:00 AM UTC"})
			if err != nil {
				t.Fatal(err)
			}
			parts := &workflowPromptParts{parts: []string{base}}
			_, _, err = assemblePromptSections(parts, promptContext{Provider: "claude-code", HasProfile: true, ProfileID: profile.ID, CrewReadOnly: tc.readonly, ShellRoot: "/app/workspace-docs", PerUserChatsFolder: "_users/example/Chats", ProfileWorkspace: "/app/workspace-docs/Chats/" + profile.ID + "/projects/example", FeatureExtensions: agentprofiles.FeaturePromptExtensions(profile)})
			if err != nil {
				t.Fatal(err)
			}
			prompt := strings.Join(parts.parts, "\n\n")
			if !strings.Contains(prompt, "MEMORY.md") || !(strings.Contains(prompt, "# Assistant") || strings.Contains(prompt, "# Crew")) {
				t.Fatal("missing shared memory or product identity")
			}
			t.Logf("SERVER_FIXTURE %s bytes=%d", tc.name, len(prompt))
			if len(prompt) > 13000 {
				t.Fatal("product fixture grew beyond server-composition budget")
			}
		})
	}
}
