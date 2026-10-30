package virtualtools

import (
	"strings"
	"testing"
)

func TestGoogleCLIToolDelegatesCommandSyntaxToUpstreamSkills(t *testing.T) {
	tool := createGoogleCLITool()
	if tool.Function == nil {
		t.Fatal("google_workspace_cli is missing its function definition")
	}
	description := tool.Function.Description
	if !strings.Contains(description, "install_skill") || !strings.Contains(description, "openclaw/gogcli@gog") || !strings.Contains(description, "never the whole repository") || !strings.Contains(description, "`gog-*` service skill") {
		t.Fatalf("tool description does not direct the agent to versioned gog skills: %q", description)
	}
	if !strings.Contains(description, "Gmail drafts and send/reply") || !strings.Contains(description, "explicitly enables agent writes") {
		t.Fatalf("tool description does not explain the opt-in Gmail write capability: %q", description)
	}
	for _, copiedExample := range []string{`["drive"`, `["gmail"`} {
		if strings.Contains(description, copiedExample) {
			t.Fatalf("tool description still embeds drift-prone command examples: %q", description)
		}
	}
}
