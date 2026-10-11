package server

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	orchEvents "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/events"
)

// Drive the real disk deletion/retention path, preserving the other chat's
// records, then attempt late start/terminal writes through the lifecycle API.
func TestBackgroundTranscriptHistoryDeletion(t *testing.T) {
	for _, scenario := range []struct {
		name                        string
		central, retention, project bool
	}{
		{name: "workflow deletion"}, {name: "workflow retention", retention: true},
		{name: "central deletion", central: true}, {name: "central retention", central: true, retention: true},
		{name: "project deletion", project: true}, {name: "project retention", project: true, retention: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("WORKSPACE_DOCS_PATH", root)
			workspacePath := "Workflow/example"
			storagePath := workspacePath
			if scenario.project {
				workspacePath = "Chats/Work/projects/example"
				storagePath, _ = ownedWorkProjectWorkspacePath(GetDefaultUserID(), workspacePath)
			}
			const sessionID = "deleted-chat"
			old := time.Now().AddDate(0, 0, -30)
			conversation := map[string]interface{}{"session_id": sessionID, "user_id": GetDefaultUserID(), "updated_at": old.Format(time.RFC3339), "created_at": old.Format(time.RFC3339), "runtime": map[string]string{"workspace_path": workspacePath}, "conversation_history": []interface{}{map[string]interface{}{"Role": "human", "Parts": []interface{}{map[string]string{"Text": "Read the sources"}}}}}
			bucket := filepath.Join(root, storagePath, "builder", "conversation", "2026-09-01")
			deletionWorkspace := workspacePath
			if scenario.central {
				bucket = filepath.Join(root, filepath.FromSlash(chatHistoryRoot(GetDefaultUserID())), "2026-09-01")
				deletionWorkspace = ""
			}
			if err := os.MkdirAll(bucket, 0755); err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(conversation)
			if err := os.WriteFile(filepath.Join(bucket, "session-"+sessionID+"-conversation.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{sessionID, "other-chat"} {
				directory := filepath.Join(root, storagePath, "builder", "conversation", "background", id)
				if err := os.MkdirAll(directory, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(directory, "execution.json"), []byte("private record"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var result ChatHistoryCleanupResult
			var err error
			if scenario.retention {
				result, err = DeleteChatHistoryOlderThan(GetDefaultUserID(), 14, deletionWorkspace)
			} else {
				result, err = DeleteChatHistorySession(GetDefaultUserID(), sessionID, deletionWorkspace)
			}
			if err != nil || result.DeletedCount != 1 {
				t.Fatalf("delete: %+v %v", result, err)
			}
			deletedDirectory := filepath.Join(root, storagePath, "builder", "conversation", "background", sessionID)
			if _, err := os.Stat(deletedDirectory); !os.IsNotExist(err) {
				t.Fatalf("private execution records survived: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, storagePath, "builder", "conversation", "background", "other-chat", "execution.json")); err != nil {
				t.Fatalf("another chat changed: %v", err)
			}
			documents := &mockWorkspaceAPI{files: map[string]string{}}
			server := httptest.NewServer(documents)
			defer server.Close()
			t.Setenv("WORKSPACE_API_URL", server.URL)
			api := lifecycleTestAPI()
			api.activeSessions = map[string]*ActiveSessionInfo{sessionID: {SessionID: sessionID, WorkspacePath: storagePath, UserID: GetDefaultUserID()}}
			api.bgAgentRegistry.Register(sessionID, &BackgroundAgent{ID: "execution", Name: "Research", Status: BGAgentRunning, CreatedAt: time.Now()})
			api.emitBackgroundAgentStarted(sessionID, "execution", "Research", "", "", orchEvents.ExecutionKindWorkflowStep)
			api.emitBackgroundAgentCompleted(sessionID, "execution", "Research", "completed", "Late result", "", "1s")
			if len(documents.files) != 0 {
				t.Fatalf("late lifecycle callback recreated records: %v", documents.files)
			}
		})
	}
	t.Run("symlink refusal preserves the conversation and external records", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("WORKSPACE_DOCS_PATH", root)
		bucket := filepath.Join(root, "Workflow/example/builder/conversation/2026-10-11")
		if err := os.MkdirAll(bucket, 0755); err != nil {
			t.Fatal(err)
		}
		parent := filepath.Join(bucket, "session-chat-conversation.json")
		if err := os.WriteFile(parent, []byte(`{"session_id":"chat"}`), 0600); err != nil {
			t.Fatal(err)
		}
		outside := t.TempDir()
		protected := filepath.Join(outside, "chat", "execution.json")
		if err := os.MkdirAll(filepath.Dir(protected), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(protected, []byte("Other workspace"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(filepath.Dir(bucket), "background")); err != nil {
			t.Fatal(err)
		}
		if _, err := DeleteChatHistorySession(GetDefaultUserID(), "chat", "Workflow/example"); err == nil {
			t.Fatal("deletion followed a symlink")
		}
		if _, err := os.Stat(parent); err != nil {
			t.Fatal("failed cascade removed the parent conversation")
		}
		if _, err := os.Stat(protected); err != nil {
			t.Fatal("deletion touched outside records")
		}
	})

}
