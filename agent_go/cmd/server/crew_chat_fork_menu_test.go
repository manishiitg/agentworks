package server

import (
	"context"
	"encoding/json"
	storeevents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Real workspace handlers (not the in-memory workspace seam) prove a
// project-bound fork keeps history and listing without reading personal menu preferences.
func TestProjectBoundForkKeepsHistoryWithoutPersonalMenuDependency(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("SUPPORTED_LLM_PROVIDERS", "claude-code")
	f, _ := newExternalCrewCommandFixture(t)
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

	// The source transcript is in the Crew's private runtime storage, not the
	// public docs root used by f.write.
	private, ok := resolveLocalWorkflowDir(parent.WorkspacePath)
	if !ok {
		t.Fatal("missing private history root")
	}
	historyFile := filepath.Join(private, "builder", "conversation", time.Now().Format("2006-01-02"), chatHistoryConversationFileName(parent.SessionID))
	if err := os.MkdirAll(filepath.Dir(historyFile), 0700); err != nil {
		t.Fatal(err)
	}
	history := `{"session_id":"` + parent.SessionID + `","conversation_history":[{"Role":"human","Parts":[{"Text":"Review this design"}]},{"Role":"ai","Parts":[{"Text":"Reviewed"}]}]}`
	if err := os.WriteFile(historyFile, []byte(history), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadChatHistoryConversation("owner", parent.SessionID, parent.WorkspacePath); err != nil {
		t.Fatalf("source fixture unreadable: %v", err)
	}

	// Same HTTP handler as UI and sessions fork; the token is bound to this Crew.
	claims := crewWriter("owner")
	claims.AccessToken.CrewIDs = []string{"team"}
	claims.AccessToken.AllCrews = false
	tokenCtx := context.WithValue(t.Context(), UserContextKey, claims)
	request := httptest.NewRequest("POST", "/", nil).WithContext(tokenCtx)
	status, body := scheduleHandler(request, f.api.handleForkCrewChat, "POST", "/api/agent-profiles/work/conversation/fork", map[string]string{"id": "work"}, nil, map[string]any{"conversation_key": parent.ConversationKey})
	if status != 200 {
		t.Fatalf("fork: %d %s", status, body)
	}
	var fork struct {
		ConversationKey string `json:"conversation_key"`
	}
	if err := json.Unmarshal(body, &fork); err != nil {
		t.Fatal(err)
	}
	// Both external and native sessions still expose the admitted fork and source.
	rows := f.api.crewChats(request, "owner", "team", parent.WorkspacePath)
	seenParent, seenFork := false, false
	for _, row := range rows {
		if row.key == parent.ConversationKey {
			seenParent = true
		}
		if row.key == fork.ConversationKey {
			seenFork = true
		}
	}
	if !seenParent || !seenFork {
		t.Fatalf("external list missing chat: %+v", rows)
	}
	native, err := f.api.codeChatsFor(ctx, "owner", parent.SessionID, parent.WorkspacePath)
	if err != nil {
		t.Fatal(err)
	}
	seenParent = false
	for _, row := range native.Chats {
		if row.Key == parent.ConversationKey {
			seenParent = true
		}
	}
	if !seenParent {
		t.Fatalf("native list missing chat: %+v", native.Chats)
	}
	if strings.Contains(string(body), "menu_groups") || strings.Contains(string(body), "group_names") {
		t.Fatal("scoped fork disclosed unrelated personal menu")
	}
}
