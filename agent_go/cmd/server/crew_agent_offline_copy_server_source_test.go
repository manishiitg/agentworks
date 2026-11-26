package server

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// The real persisted server source is captured privately only after explicit
// intent; retries append no duplicate bytes and a foreign/corrupt owner refuses.
func TestCrewOfflineServerSourceSharedOnlyAndDeltaCapture(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	env := newTriggerLinkEnv(t)
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	ctx := context.WithValue(t.Context(), UserContextKey, &UserClaims{UserID: "owner"})
	root := crewProjectRootByID("alpha")
	record := ProductConversationRecord{ProfileID: crewProfileID, ConversationKey: "alpha:chat:source", ConversationID: "conversation-server-source", SessionID: "server-source-session", WorkspacePath: root}
	key := productConversationRegistryEntryKey(crewProfileID, record.ConversationKey)
	store := defaultProductConversationRegistryStore()
	if err := store.writeDocument(ctx, productConversationRegistryPath("owner"), productConversationRegistryDocument{Entries: map[string]ProductConversationRecord{key: record}, Agents: map[string]ProductAgentIdentity{key: {Name: "Source"}}}); err != nil {
		t.Fatal(err)
	}
	// The source starts Only me, as the Team registry lists a new server agent.
	private := false
	blank, err := readCrewTeamOrNew("alpha", "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := assignCrewTeamAgentIDs(blank, []crewTeamAgentsPerson{{UserID: "owner", Agents: []crewTeamAgent{{Tab: crewAgentKeyTab(record.ConversationKey), ChatKey: record.ConversationKey, Name: "Source", Place: "server", initialInTeam: &private}}}}); err != nil {
		t.Fatal(err)
	}
	team, err := readCrewTeam("alpha")
	source, found := team.registeredAgent("owner", record.ConversationKey)
	if err != nil || !found || source.InTeam {
		t.Fatal("source registration", err)
	}
	id := source.ID
	message := func(role llmtypes.ChatMessageType, text string) llmtypes.MessageContent {
		return llmtypes.MessageContent{Role: role, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: text}}}
	}
	history := []llmtypes.MessageContent{message(llmtypes.ChatMessageTypeSystem, "PRIVATE SYSTEM FIXTURE"), message(llmtypes.ChatMessageTypeHuman, "source question")}
	path, ok := workProjectChatHistoryConversationPath("owner", root, record.SessionID, time.Now())
	if !ok {
		t.Fatal("no source history path")
	}
	persist := func(person string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"user_id": person, "session_id": record.SessionID, "conversation_history": history})
		if err := writeRawFileToWorkspace(ctx, path, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	persist("owner")
	if err := env.api.syncCrewOfflineServerTurn(ctx, "owner", record.SessionID, root); err != nil {
		t.Fatal(err)
	}
	dir, err := crewOfflineDirectory("alpha", id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "history.json")); !os.IsNotExist(err) {
		t.Fatal("Only me source copied online", err)
	}
	// Sharing it with the Team (Team on) is what turns on its online copy.
	team.On = true
	source.InTeam = true
	team.Agents[id] = source
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	if err := env.api.syncCrewOfflineServerTurn(ctx, "owner", record.SessionID, root); err != nil {
		t.Fatal("capture", err)
	}
	binding := crewOfflineBinding{Provider: "agentworks", Session: record.SessionID, Attachment: record.ConversationID}
	first, err := readCrewOfflineHistory(dir, binding)
	if err != nil || first.Revision != 1 || first.Offset == 0 || first.Offset != first.SourceSize {
		t.Fatal("incomplete initial source", err)
	}
	var projected bytes.Buffer
	for _, batch := range first.Batches {
		if err := assembleChunked(filepath.Join(dir, "chunks"), batch, &projected); err != nil {
			t.Fatal(err)
		}
	}
	if bytes.Contains(projected.Bytes(), []byte("PRIVATE SYSTEM FIXTURE")) || !bytes.Contains(projected.Bytes(), []byte("source question")) {
		t.Fatal("incorrect source projection")
	}
	if err := env.api.syncCrewOfflineServerTurn(ctx, "owner", record.SessionID, root); err != nil {
		t.Fatal(err)
	}
	unchanged, err := readCrewOfflineHistory(dir, binding)
	if err != nil || unchanged.Revision != first.Revision || unchanged.Offset != first.Offset {
		t.Fatal("retry duplicated bytes", err)
	}
	history = append(history, message(llmtypes.ChatMessageTypeAI, "source answer"))
	persist("owner")
	if err := env.api.syncCrewOfflineServerTurn(ctx, "owner", record.SessionID, root); err != nil {
		t.Fatal("append", err)
	}
	appended, err := readCrewOfflineHistory(dir, binding)
	if err != nil || appended.Revision != first.Revision+1 || appended.Offset <= first.Offset || appended.Batches[0].Hash != first.Batches[0].Hash {
		t.Fatal("not an appended delta", err)
	}
	persist("other")
	if err := env.api.syncCrewOfflineServerTurn(ctx, "owner", record.SessionID, root); err == nil {
		t.Fatal("foreign history imported")
	}
	final, err := readCrewOfflineHistory(dir, binding)
	if err != nil || final.Revision != appended.Revision {
		t.Fatal("refusal changed source", err)
	}
	// Cloud handback uses the same durable human receipt, not capture selection.
	persist("owner")
	firstReturn := crewOfflineReturnedAnswer{ID: "cloud-reply-1", At: time.Now().UTC(), Asker: "other", Question: "source question", Answer: "cloud reply"}
	if err := appendCrewOfflineReturn("alpha", id, dir, binding, firstReturn); err != nil {
		t.Fatal(err)
	}
	// Unsharing stops capture; the handback still reaches the owner.
	source.InTeam = false
	team.Agents[id] = source
	if err := writeCrewTeamAgentFixture(team); err != nil {
		t.Fatal(err)
	}
	literal := crewOfflineAppHandbackInput(ctx, "owner", record, "owner's next request")
	if literal == "owner's next request" || !strings.Contains(literal, "cloud reply") {
		t.Fatal("cloud return not injected")
	}
	foreignCtx := context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "other"})
	if got := crewOfflineAppHandbackInput(foreignCtx, "other", record, "unchanged"); got != "unchanged" {
		t.Fatal("foreign turn read return")
	}
	scopedCtx := context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "owner", Scope: "run"})
	if got := crewOfflineAppHandbackInput(scopedCtx, "owner", record, "unchanged"); got != "unchanged" {
		t.Fatal("scoped turn read return")
	}
	block := crewOfflineAppReturnsText([]crewOfflineReturnedAnswer{firstReturn})
	history = append(history, message(llmtypes.ChatMessageTypeAI, block))
	persist("owner")
	if _, err := crewOfflineCloudHandback(ctx, "owner", record); err != nil {
		t.Fatal(err)
	}
	state, err := readCrewOfflineReturns(dir, binding)
	if err != nil || len(state.Pending) != 1 {
		t.Fatal("assistant echo acknowledged return", err)
	}
	secondReturn := crewOfflineReturnedAnswer{ID: "cloud-reply-2", At: time.Now().UTC(), Asker: "other", Answer: "later cloud reply"}
	if err := appendCrewOfflineReturn("alpha", id, dir, binding, secondReturn); err != nil {
		t.Fatal(err)
	}
	if got := crewOfflineAppHandbackInput(ctx, "owner", record, "next"); strings.Contains(got, "later cloud reply") {
		t.Fatal("pending batch receipt changed under unaccepted input")
	}
	history = append(history, message(llmtypes.ChatMessageTypeHuman, block+"\nowner's next request"))
	persist("owner")
	if err := env.api.syncCrewOfflineServerTurn(ctx, "owner", record.SessionID, root); err != nil {
		t.Fatal("cloud receipt worker", err)
	}
	state, err = readCrewOfflineReturns(dir, binding)
	if err != nil || len(state.Pending) != 1 || state.Pending[0].ID != secondReturn.ID || len(state.Acknowledged) != 1 || state.Acknowledged[0] != firstReturn.ID {
		t.Fatal("wrong return acknowledgment", err)
	}
	afterReceipt, err := readCrewOfflineHistory(dir, binding)
	if err != nil || afterReceipt.Revision != appended.Revision {
		t.Fatal("handback required upload selection", err)
	}

}
