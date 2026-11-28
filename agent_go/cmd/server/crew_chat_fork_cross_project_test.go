package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
)

type captureCodeChatToolRegistrar struct {
	tools map[string]func(context.Context, map[string]interface{}) (string, error)
}

func (c *captureCodeChatToolRegistrar) RegisterCustomTool(name, _ string, _ map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), _ string) error {
	if c.tools == nil {
		c.tools = map[string]func(context.Context, map[string]interface{}) (string, error){}
	}
	c.tools[name] = execute
	return nil
}

func (c *captureCodeChatToolRegistrar) RegisterCustomToolWithTimeout(name, description string, params map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error), _ time.Duration, category string) error {
	return c.RegisterCustomTool(name, description, params, execute, category)
}

func TestForkCrewChatAcrossProjects(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("SUPPORTED_LLM_PROVIDERS", "claude-code")
	f, _ := newExternalCrewCommandFixture(t)
	owners := projectOwnersAt(os.Getenv("AGENTWORKS_STATE_ROOT"))

	// Register a second project owned by "owner" and a third owned by "other".
	if err := owners.Register(projectOwnerRecord{Product: "work", Folder: "dest-proj", ProjectID: "dest", OwnerID: "owner"}); err != nil {
		t.Fatal(err)
	}
	if err := owners.Register(projectOwnerRecord{Product: "work", Folder: "foreign-proj", ProjectID: "foreign", OwnerID: "other"}); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"Crew/dest-proj", "_users/other/Crew/foreign-proj"} {
		if err := os.MkdirAll(filepath.Join(f.docs, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	f.write(t, "Crew/dest-proj/.agentworks/settings/product.json", `{"schema_version":1,"product":"work","id":"dest","title":"Finance Workspace","session_id":"sess-dest"}`)
	f.write(t, "Crew/dest-proj/.agentworks/settings/workflow.json", `{"id":"dest","label":"Finance Workspace","created_by":"owner"}`)
	f.write(t, "_users/other/Crew/foreign-proj/.agentworks/settings/product.json", `{"schema_version":1,"product":"work","id":"foreign","title":"Other Workspace","session_id":"sess-foreign"}`)
	f.write(t, "_users/other/Crew/foreign-proj/.agentworks/settings/workflow.json", `{"id":"foreign","label":"Other Workspace","created_by":"other"}`)

	journal, err := storeevents.OpenSQLiteEventJournal(filepath.Join(t.TempDir(), "events.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	f.api.eventStore = storeevents.NewEventStore(100)
	defer f.api.eventStore.Stop()
	f.api.eventStore.SetDurableJournal(journal)

	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner"})
	profile, err := f.api.agentProfiles.Resolve("work", 0, "owner")
	if err != nil {
		t.Fatal(err)
	}
	binding, _, err := resolveConversationBindingForUser(ctx, "owner", profile, "team:chat:source")
	if err != nil {
		t.Fatal(err)
	}
	parent, err := defaultProductConversationRegistryStore().resolveOrCreate(ctx, "owner", profile, binding, "")
	if err != nil {
		t.Fatal(err)
	}
	destBinding, destCanBuild, err := resolveConversationBindingForUser(ctx, "owner", profile, "dest")
	if err != nil || !destCanBuild {
		t.Fatalf("resolve dest binding: %v canBuild=%t", err, destCanBuild)
	}

	private, ok := resolveLocalWorkflowDir(parent.WorkspacePath)
	if !ok {
		t.Fatal("missing private history root")
	}
	historyFile := filepath.Join(private, "builder", "conversation", time.Now().Format("2006-01-02"), chatHistoryConversationFileName(parent.SessionID))
	if err := os.MkdirAll(filepath.Dir(historyFile), 0700); err != nil {
		t.Fatal(err)
	}
	history := `{"session_id":"` + parent.SessionID + `","conversation_history":[{"Role":"human","Parts":[{"Text":"Compare Q4 spend"}]},{"Role":"ai","Parts":[{"Text":"Done"}]}]}`
	if err := os.WriteFile(historyFile, []byte(history), 0600); err != nil {
		t.Fatal(err)
	}

	ownerReq := httptest.NewRequest("POST", "/", nil).WithContext(ctx)

	// 1. Refuse forking into another person's project.
	status, body := scheduleHandler(ownerReq, f.api.handleForkCrewChat, "POST", "/api/agent-profiles/work/conversation/fork", map[string]string{"id": "work"}, nil, map[string]any{
		"conversation_key": parent.ConversationKey,
		"target_project":   "foreign",
	})
	if status != 403 {
		t.Fatalf("expected 403 for foreign target_project, got %d %s", status, body)
	}

	// 2. Refuse forking into dest with a bearer token restricted to team only.
	scoped := crewWriter("owner")
	scoped.AccessToken.CrewIDs = []string{"team"}
	scoped.AccessToken.AllCrews = false
	scopedReq := httptest.NewRequest("POST", "/", nil).WithContext(context.WithValue(t.Context(), UserContextKey, scoped))
	status, body = scheduleHandler(scopedReq, f.api.handleForkCrewChat, "POST", "/api/agent-profiles/work/conversation/fork", map[string]string{"id": "work"}, nil, map[string]any{
		"conversation_key": parent.ConversationKey,
		"target_project":   "dest",
	})
	if status != 403 {
		t.Fatalf("expected 403 for token without dest scope, got %d %s", status, body)
	}

	// 3. Refuse combining side_question with cross-project target_project.
	status, body = scheduleHandler(ownerReq, f.api.handleForkCrewChat, "POST", "/api/agent-profiles/work/conversation/fork", map[string]string{"id": "work"}, nil, map[string]any{
		"conversation_key": parent.ConversationKey,
		"target_project":   "dest",
		"side_question":    "Quick check?",
	})
	if status != 400 {
		t.Fatalf("expected 400 for side_question with target_project, got %d %s", status, body)
	}

	// 4. Succeed when owner forks into dest.
	status, body = scheduleHandler(ownerReq, f.api.handleForkCrewChat, "POST", "/api/agent-profiles/work/conversation/fork", map[string]string{"id": "work"}, nil, map[string]any{
		"conversation_key": parent.ConversationKey,
		"target_project":   "dest",
		"name":             "Moved Planner",
	})
	if status != 200 {
		t.Fatalf("cross-project fork failed: %d %s", status, body)
	}
	var resp struct {
		ChatID          string `json:"chat_id"`
		ProjectID       string `json:"project_id"`
		ProjectName     string `json:"project_name"`
		ConversationKey string `json:"conversation_key"`
		SessionID       string `json:"session_id"`
		ForkedFrom      string `json:"forked_from"`
		CrossProject    bool   `json:"cross_project"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ProjectID != "dest" || resp.ProjectName != "Finance Workspace" || !resp.CrossProject || resp.ForkedFrom != "" || !strings.HasPrefix(resp.ConversationKey, "dest:chat:fork-") {
		t.Fatalf("unexpected cross-project fork response: %+v", resp)
	}

	// Listed in dest, not in team.
	destRows := f.api.crewChats(ownerReq, "owner", "dest", destBinding.WorkspacePath)
	foundInDest := false
	for _, row := range destRows {
		if row.key == resp.ConversationKey {
			foundInDest = true
		}
	}
	if !foundInDest {
		t.Fatalf("fork not listed in dest project: %+v", destRows)
	}
	for _, row := range f.api.crewChats(ownerReq, "owner", "team", parent.WorkspacePath) {
		if row.key == resp.ConversationKey {
			t.Fatalf("cross-project fork leaked into source project list: %+v", row)
		}
	}

	// Top-level in destination (no alien ForkedFrom).
	identity, err := defaultProductConversationRegistryStore().agentIdentity(ctx, "owner", "work", resp.ConversationKey)
	if err != nil || identity.ForkedFrom != "" {
		t.Fatalf("cross-project fork should not nest under source key: %+v %v", identity, err)
	}

	// One-time ForkNote names old project/folder and new project/folder, and is single-bracketed.
	note := defaultProductConversationRegistryStore().takeForkNote(ctx, "owner", "work", resp.ConversationKey)
	if !strings.HasPrefix(note, "[This chat is a fork moved from another project.") || !strings.HasSuffix(note, "]") || strings.Count(note, "]") != 1 {
		t.Fatalf("unexpected cross-project fork note format: %q", note)
	}
	for _, want := range []string{"Test Team", "Finance Workspace", "#team", parent.WorkspacePath, destBinding.WorkspacePath} {
		if !strings.Contains(note, want) {
			t.Fatalf("cross-project fork note missing %q: %q", want, note)
		}
	}
	if again := defaultProductConversationRegistryStore().takeForkNote(ctx, "owner", "work", resp.ConversationKey); again != "" {
		t.Fatalf("fork note should clear after first take: %q", again)
	}

	// Journals record source_project_name and fork_project_name for cross-project dividers.
	destEvents, err := journal.LoadTail(resp.SessionID, 20)
	if err != nil || len(destEvents) == 0 || destEvents[len(destEvents)-1].Type != "chat_forked" {
		t.Fatalf("missing chat_forked in destination journal: %v %v", destEvents, err)
	}
	forkedMarker := eventPayloadMap(destEvents[len(destEvents)-1])
	if forkedMarker["source_project_id"] != "team" || forkedMarker["source_project_name"] != "Test Team" {
		t.Fatalf("unexpected chat_forked marker: %+v", forkedMarker)
	}

	srcEvents, err := journal.LoadTail(parent.SessionID, 20)
	if err != nil || len(srcEvents) == 0 || srcEvents[len(srcEvents)-1].Type != "chat_fork_created" {
		t.Fatalf("missing chat_fork_created in source journal: %v %v", srcEvents, err)
	}
	createdMarker := eventPayloadMap(srcEvents[len(srcEvents)-1])
	if createdMarker["fork_project_id"] != "dest" || createdMarker["fork_project_name"] != "Finance Workspace" {
		t.Fatalf("unexpected chat_fork_created marker: %+v", createdMarker)
	}

	// 5. Agent sessions fork tool requires #dest in the turn for cross-project target_project.
	reg := &captureCodeChatToolRegistrar{}
	if err := f.api.registerCodeChatTools(reg, nil, "owner", parent.SessionID, parent.WorkspacePath, nil); err != nil {
		t.Fatal(err)
	}
	forkTool := reg.tools["fork_project_chat"]
	if forkTool == nil {
		t.Fatal("fork_project_chat tool was not registered")
	}
	if _, err := forkTool(ctx, map[string]interface{}{"chat": "source", "target_project": "dest", "name": "Untagged Fork"}); err == nil || !strings.Contains(err.Error(), "tag that project with #") {
		t.Fatalf("expected untagged sessions fork target_project refusal, got: %v", err)
	}
	taggedCtx := withTurnReachReferences(ctx, []string{destBinding.WorkspacePath})
	toolOut, err := forkTool(taggedCtx, map[string]interface{}{"chat": "source", "target_project": "dest", "name": "Tagged Fork"})
	if err != nil {
		t.Fatalf("tagged sessions fork target_project failed: %v", err)
	}
	if !strings.Contains(toolOut, `"project_id":"dest"`) || !strings.Contains(toolOut, `"cross_project":true`) {
		t.Fatalf("unexpected tagged sessions fork output: %s", toolOut)
	}
}
