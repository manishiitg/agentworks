package virtualtools

import (
	"strings"
	"testing"
)

func TestAgentWorksChatInstructionsAreDirectAndDoNotAdvertiseOrchestration(t *testing.T) {
	prompt := GetAgentWorksChatInstructionsWithUser("_users/alice/Chats", "alice")

	for _, required := range []string{
		"direct conversational assistant",
		"Perform the work directly",
		"_users/alice/Chats",
	} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("direct chat prompt is missing %q", required)
		}
	}
	for _, forbidden := range []string{
		"delegate(",
		"query_agent",
		"terminate_agent",
		"list_background_agents",
		"create_workflow_schedule",
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("direct chat prompt still advertises removed orchestration %q", forbidden)
		}
	}
}
