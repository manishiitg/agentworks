package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRawConversationSnapshotPreservesRecoveredStructuredRows(t *testing.T) {
	const path = "Workflow/test/builder/conversation/raw.json"
	workspace := &mockWorkspaceAPI{files: map[string]string{path: `{"session_id":"chat","user_id":"alice","revision":8,"conversation_history":[{"Role":"human","Parts":[{"Text":"first"}]},{"Role":"ai","Parts":[{"Text":"answer","ToolCall":{"ID":"keep"}}]}]}`}}
	server := httptest.NewServer(workspace)
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	incoming := `{"session_id":"chat","user_id":"alice","conversation_history":[{"Role":"human","Parts":[{"Text":"first"}]},{"Role":"human","Parts":[{"Text":"next"}]}]}`
	if err := persistRawConversationSnapshot(context.Background(), path, incoming); err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal([]byte(workspace.files[path]), &got); err != nil {
		t.Fatal(err)
	}
	if got["revision"] != float64(9) || !strings.Contains(workspace.files[path], `"keep"`) {
		t.Fatalf("revision or structure lost: %s", workspace.files[path])
	}
	var conv builderConversationLog
	_ = json.Unmarshal([]byte(workspace.files[path]), &conv)
	if len(conv.ConversationHistory) != 3 || conv.ConversationHistory[1].Parts[0].Text != "answer" {
		t.Fatal("raw snapshot lost or reordered answer")
	}
}

func TestProductConversationContinuationNeverSilentlySubstitutesSession(t *testing.T) {
	for _, tc := range []struct {
		name                string
		continuation        bool
		requested, resolved string
		fail                bool
	}{
		{"fresh provisional ID", false, "local-uuid", "product-new", false},
		{"verified same session", true, "established", "established", false},
		{"missing indexed session", true, "open-tab", "registry-other", true},
		{"continuation without ID", true, "", "registry-other", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateProductConversationContinuation(tc.continuation, tc.requested, tc.resolved)
			if (err != nil) != tc.fail {
				t.Fatalf("unexpected continuation result: %v", err)
			}
		})
	}
}
