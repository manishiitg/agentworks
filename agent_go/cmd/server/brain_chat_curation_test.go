package server

import (
	"context"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/knowledgebase"
)

// The person's own Brain chat gets the content tools and uses them with that person's folder roles, like their MCP
// connection; a session in someone else's Brain chat folder gets nothing (PLAT-618).
func TestBrainChatCuratesWithThePersonsOwnRoles(t *testing.T) {
	_, service := knowledgebaseServerTest(t)
	admin := knowledgebase.Principal{IdentityID: "admin", IsAdmin: true}
	if _, err := service.Call(t.Context(), admin, "create_knowledgebase_folder", map[string]any{"folder_path": "", "name": "Engineering", "request_id": "eng"}); err != nil {
		t.Fatal(err)
	}
	const session = "brain-chat-curation"
	t.Cleanup(func() { common.ClearSessionShellConfig(session) })
	common.SetSessionWorkingDir(session, agentProfileRuntimeWorkspace("admin", "Chats/Knowledgebase"))
	tools, executors, _ := createKnowledgebaseTools("admin", session)
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Function.Name] = true
	}
	if !names[knowledgebase.ToolBrowse] || !names[knowledgebase.ToolUpdate] || names[knowledgebase.ToolAccess] || names[knowledgebase.ToolBackup] {
		t.Fatalf("Brain chat content tools: %v", names)
	}
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "admin", Username: "admin"})
	ctx = context.WithValue(ctx, common.ChatSessionIDKey, session)
	execute := executors[knowledgebase.ToolUpdate].(func(context.Context, map[string]interface{}) (string, error))
	if _, err := execute(ctx, map[string]interface{}{"action": "create", "folder_path": "Engineering", "filename": "readme.md", "type": "note", "title": "Engineering", "content": "# Engineering\n", "request_id": "curate"}); err != nil {
		t.Fatalf("the Brain chat must be able to curate with the person's roles: %v", err)
	}
	other := "brain-chat-foreign"
	t.Cleanup(func() { common.ClearSessionShellConfig(other) })
	common.SetSessionWorkingDir(other, agentProfileRuntimeWorkspace("priya", "Chats/Knowledgebase"))
	if foreign, _, _ := createKnowledgebaseTools("admin", other); len(foreign) != 0 {
		t.Fatalf("another person's Brain chat folder must not get Brain tools: %d", len(foreign))
	}
	if !strings.HasSuffix(agentProfileRuntimeWorkspace("admin", "Chats/Knowledgebase"), "Chats/Knowledgebase") {
		t.Fatal("unexpected Brain chat workspace")
	}
}
