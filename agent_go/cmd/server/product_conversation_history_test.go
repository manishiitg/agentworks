package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	llmtypes "github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// "New chat" keeps the conversation it replaces; a product can make an earlier
// one live again (the current one is then kept in turn) or forget it — but
// never forget the live one.
func TestProductConversationHistoryRotateSwitchForget(t *testing.T) {
	store, _ := memoryProductConversationStore()
	// This scenario mints more identities than the shared fake's fixed list.
	nextID := 0
	store.newID = func() string { nextID++; return "id-" + strings.Repeat("x", nextID) }
	profile := singletonConversationProfile()
	binding, err := resolveProductConversationBinding(context.Background(), "user-1", profile, "")
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.resolveOrCreate(context.Background(), "user-1", profile, binding, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.rotate(context.Background(), "user-1", profile, binding)
	if err != nil {
		t.Fatal(err)
	}
	if second.SessionID == first.SessionID {
		t.Fatal("rotate reused the session")
	}

	current, ok, previous, err := store.history(context.Background(), "user-1", profile, binding)
	if err != nil || !ok || current.SessionID != second.SessionID {
		t.Fatalf("history current = (%+v, %v, %v), want the rotated conversation", current, ok, err)
	}
	if len(previous) != 1 || previous[0].SessionID != first.SessionID {
		t.Fatalf("previous = %+v, want the replaced conversation", previous)
	}

	// Reopen the first: it is live again, the second is kept.
	reopened, err := store.switchTo(context.Background(), "user-1", profile, binding, first.SessionID, false)
	if err != nil || reopened.SessionID != first.SessionID || reopened.ConversationID != first.ConversationID {
		t.Fatalf("switchTo = (%+v, %v), want the first conversation with its identity", reopened, err)
	}
	current, _, previous, _ = store.history(context.Background(), "user-1", profile, binding)
	if current.SessionID != first.SessionID || len(previous) != 1 || previous[0].SessionID != second.SessionID {
		t.Fatalf("after switch: current=%s previous=%+v", current.SessionID, previous)
	}
	// Switching to the live one is a no-op.
	if again, err := store.switchTo(context.Background(), "user-1", profile, binding, first.SessionID, false); err != nil || again.SessionID != first.SessionID {
		t.Fatalf("switch to live = (%+v, %v)", again, err)
	}

	// An unknown session needs verification; verified, it becomes live with a
	// fresh conversation id.
	if _, err := store.switchTo(context.Background(), "user-1", profile, binding, "legacy-session", false); err == nil {
		t.Fatal("unverified unknown session became live")
	}
	legacy, err := store.switchTo(context.Background(), "user-1", profile, binding, "legacy-session", true)
	if err != nil || legacy.SessionID != "legacy-session" || legacy.ConversationID == first.ConversationID {
		t.Fatalf("verified unknown session = (%+v, %v)", legacy, err)
	}
	_, _, previous, _ = store.history(context.Background(), "user-1", profile, binding)
	if len(previous) != 2 || previous[0].SessionID != first.SessionID {
		t.Fatalf("previous after legacy switch = %+v, want first then second", previous)
	}

	// Forget the second; the live one is refused.
	if err := store.forget(context.Background(), "user-1", profile, binding, second.SessionID); err != nil {
		t.Fatal(err)
	}
	if err := store.forget(context.Background(), "user-1", profile, binding, "legacy-session"); err == nil || !strings.Contains(err.Error(), "live") {
		t.Fatalf("forgetting the live conversation = %v, want refusal", err)
	}
	live, err := store.liveSessionIDs(context.Background(), "user-1", profile.ID)
	if err != nil || !live["legacy-session"] || live[first.SessionID] {
		t.Fatalf("live sessions = %v, want only the live one", live)
	}
	_, _, previous, _ = store.history(context.Background(), "user-1", profile, binding)
	if len(previous) != 1 || previous[0].SessionID != first.SessionID {
		t.Fatalf("previous after forget = %+v, want only the first", previous)
	}
}

func TestConversationTitleComesFromTheFirstMessage(t *testing.T) {
	if got := conversationTitleFrom(&ChatHistorySession{Query: "  How is Myra doing in fractions?\nmore"}, ""); got != "How is Myra doing in fractions?" {
		t.Fatalf("title = %q", got)
	}
	long := strings.Repeat("a", 100)
	if got := conversationTitleFrom(&ChatHistorySession{Query: long}, ""); !strings.HasSuffix(got, "…") || len([]rune(got)) > agentProfileConversationTitleLimit+1 {
		t.Fatalf("long title = %q", got)
	}
	if got := conversationTitleFrom(nil, ""); got != "New chat" {
		t.Fatalf("empty title = %q", got)
	}
	if got := normalizeConversationWorkspace("/_users/default/Chats/ExampleApp/"); got != "Chats/ExampleApp" {
		t.Fatalf("normalized workspace = %q", got)
	}
	if got := normalizeConversationWorkspace("/data/example-product/docs/_users/default/Chats/Work/projects/example"); got != "Chats/Work/projects/example" {
		t.Fatalf("normalized absolute workspace = %q", got)
	}
}

// PLAT-872-78: a shared Crew chat's history must be found, or switching its coding agent (Cursor to Claude) starts the
// chat with no history: the fallback continues a saved conversation only on the same provider.
func TestResolveProductResumeTargetFindsSharedCrewChat(t *testing.T) {
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	crew := registerTestCrew(t, "demo-12345678", "alice", true)
	private, ok := resolveLocalWorkflowDir(crew)
	if !ok {
		t.Fatal("crew storage not resolved")
	}
	conversationDir := filepath.Join(private, "builder", "conversation", "2026-10-28")
	if err := os.MkdirAll(conversationDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for session, text := range map[string]string{"crew-chat": "remember this", "other-chat": "another chat in this Crew"} {
		if err := os.WriteFile(filepath.Join(conversationDir, chatHistoryConversationFileName(session)), []byte(`{
  "session_id": "`+session+`",
  "conversation_history": [{"Role":"human","Parts":[{"Text":"`+text+`"}]}],
  "runtime": {"kind": "coding_agent", "provider": "cursor-cli", "transport": "sdk", "resume_supported": true}
}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	target, found, err := resolveProductResumeTarget("alice", ProductConversationRecord{SessionID: "crew-chat", WorkspacePath: crew})
	if err != nil || !found || target == nil {
		t.Fatalf("shared Crew chat not found: found=%v target=%#v err=%v", found, target, err)
	}
	// Only this chat's own history, never another chat saved in the same Crew folder.
	if target.SessionID != "crew-chat" || len(target.History) != 1 || !target.crossesProvider("claude-code") ||
		target.History[0].Parts[0].(llmtypes.TextContent).Text != "remember this" {
		t.Fatalf("target = %#v, want this chat's history and the saved provider", target)
	}
	// A Run person never resumes from the owner's Builder history, even by naming its session. (Their own store is
	// central; with no workspace service in this test that lookup errors, which is not a result.)
	if target, found, _ := resolveProductResumeTarget("bob", ProductConversationRecord{SessionID: "crew-chat", WorkspacePath: crew}); found || target != nil {
		t.Fatalf("Run person resumed the owner's chat: found=%v target=%#v", found, target)
	}
}

func TestResolvedResumeTargetHydratesAndKeepsOnePersistenceDestination(t *testing.T) {
	history := []llmtypes.MessageContent{{
		Role:  llmtypes.ChatMessageTypeHuman,
		Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "saved question"}},
	}}
	target := &resolvedResumeTarget{
		SessionID:        "saved-chat",
		WorkspacePath:    "Chats/Work/projects/demo",
		ConversationPath: "_users/alice/Chats/Work/projects/demo/builder/conversation/2026-09-15/session-saved-chat-conversation.json",
		History:          history,
		Runtime: &ChatHistoryAgentRuntime{
			Kind:            "coding_agent",
			Provider:        "claude-code",
			WorkshopMode:    "workshop",
			ResumeSupported: true,
		},
		WorkshopMode: "workshop",
	}
	api := &StreamingAPI{
		conversationHistory:                map[string][]llmtypes.MessageContent{},
		restoredConversationPersistTargets: map[string]restoredChatHistoryPersistTarget{},
	}
	if !api.restoreResolvedResumeTarget("saved-chat", target, "workshop") {
		t.Fatal("resolved target did not hydrate its transcript")
	}
	if got := api.conversationHistory["saved-chat"]; len(got) != 1 {
		t.Fatalf("hydrated history length=%d", len(got))
	}
	remembered, ok := api.rememberedRestoredConversationPersistTarget("saved-chat")
	if !ok || remembered.ConversationPath != target.ConversationPath || remembered.SessionID != target.SessionID {
		t.Fatalf("persistence destination changed: ok=%v target=%#v", ok, remembered)
	}
	// PLAT-872-32: the product resolves a saved target on EVERY turn. Only
	// initial hydration is a restore; fresh per-turn Agent wrappers are not.
	api.conversationHistory["saved-chat"] = append(api.conversationHistory["saved-chat"], llmtypes.TextPart(llmtypes.ChatMessageTypeAI, "live reply"))
	target.ConversationPath = "_users/alice/Chats/Work/projects/demo/builder/conversation/2026-09-16/session-saved-chat-conversation.json"
	if api.restoreResolvedResumeTarget("saved-chat", target, "workshop") {
		t.Fatal("warm follow-up must not trigger another archive restore notice")
	}
	if len(api.conversationHistory["saved-chat"]) != 2 {
		t.Fatal("warm live conversation was replaced by the older saved history")
	}
	remembered, ok = api.rememberedRestoredConversationPersistTarget("saved-chat")
	if !ok || remembered.ConversationPath != target.ConversationPath {
		t.Fatal("warm continuation must still refresh the persistence destination")
	}
	// A server restart loses the process-local cache and really does restore.
	restarted := &StreamingAPI{}
	if !restarted.restoreResolvedResumeTarget("saved-chat", target, "workshop") {
		t.Fatal("cold server must still hydrate and supply the archive notice")
	}
	if !target.crossesProvider("cursor-cli") || target.crossesProvider("claude-code") {
		t.Fatalf("provider resume policy is wrong: saved=%q", target.provider())
	}
	if !target.canPersist("workshop") {
		t.Fatal("cross-provider resume must preserve the AgentWorks conversation")
	}
}

func TestResolvedResumeTargetCannotBeSuppliedByBrowserJSON(t *testing.T) {
	req := QueryRequest{RestoredConversationSessionID: "saved-chat", resolvedResumeTarget: &resolvedResumeTarget{SessionID: "internal-only"}}
	data, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "internal-only") || strings.Contains(string(data), "resolved_resume") {
		t.Fatalf("internal resume target leaked into JSON: %s", data)
	}
}
