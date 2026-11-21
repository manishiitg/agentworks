package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// PLAT-746-1024 slice 2: an offline copy answers only while it is bound to the source's CURRENT attachment and home,
// and is ready only when built from the complete latest synced revision. A same-session
// attachment renewal keeps it; a new native session, moved home or stale build does not.
func TestCrewOfflineCopyBindingAndReadiness(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	agent := "team-agent-11111111-2222-4333-8444-555555555555"
	session := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	team := crewTeam{ProjectID: "source", OwnerID: "owner", OnlineHome: "home", Agents: map[string]crewTeamRegisteredAgent{
		agent: {ID: agent, Kind: "terminal", OwnerID: "owner", ChatKey: session, InTeam: true, Terminal: &crewTeamTerminal{Provider: "claude", AttachmentID: "attach-1"}},
	}}
	a := team.Agents[agent]
	b, ok := crewOfflineRowBinding(team, a)
	if !ok {
		t.Fatal("a joined, shared terminal row must have a binding")
	}
	teamDir, err := crewTeamDir(team.ProjectID)
	if err != nil || os.MkdirAll(teamDir, 0700) != nil {
		t.Fatal(err)
	}
	dir, err := crewOfflineDirectory(team.ProjectID, agent)
	if err != nil {
		t.Fatal(err)
	}
	key := crewOfflineCopyChatKey("home", agent)
	if crewOfflineCopySourceAgent("home", key) != agent || crewOfflineCopySourceAgent("other", key) != "" {
		t.Fatal("a copy key must name its source agent only inside its own home")
	}
	// The copy's one chat is fenced from the home's owner-tab paths.
	if !crewOfflineCopyHomeKey("home", key) || crewOfflineCopyHomeKey("home", "home"+codeChatSideMarker+"notes") {
		t.Fatal("the copy's chat, and only it, is a copy key")
	}
	write := func(v any, name string) {
		t.Helper()
		if err := fsutil.WriteJSONAtomic(filepath.Join(dir, name), v, 0600); err != nil {
			t.Fatal(err)
		}
	}
	history := crewOfflineHistory{crewOfflineBinding: b, crewOfflineCursor: crewOfflineCursor{Revision: 2, Offset: 100}, SourceSize: 100}
	write(history, "history.json")
	write(crewTeamOfflineCopyRef{Home: "home", ChatKey: key, Session: "s1", Revision: 2, Binding: b}, "copy.json")
	if _, ready, err := crewOfflineCopyReady(team, a); err != nil || !ready {
		t.Fatalf("complete current copy must be ready: ready=%v err=%v", ready, err)
	}
	// Header projection uses only the source owner's metadata and same binding.
	a.Tab = "native-tab"
	team.Agents[agent] = a
	src := crewTabSetupSource{}
	view := src.setup(team, crewTeamAgent{ID: agent}, "owner", "owner", true, nil)
	if view.OnlineCopy == nil || view.OnlineCopy.State != "ready" || view.OnlineCopy.SyncedAt == nil {
		t.Fatal("missing automatic copy metadata")
	}
	raw, _ := json.Marshal(view)
	if strings.Contains(string(raw), session) || strings.Contains(string(raw), key) {
		t.Fatal("private copy binding leaked into header")
	}
	other := src.setup(team, crewTeamAgent{ID: agent}, "owner", "other", true, nil)
	if other.OnlineCopy != nil {
		t.Fatal("own header metadata exposed to another person")
	}
	write(crewTeamOfflineCopyRef{Home: "home", ChatKey: key, Session: "s1", Revision: 1, Binding: b}, "copy.json")
	if _, ready, _ := crewOfflineCopyReady(team, a); ready {
		t.Fatal("a copy built from an older revision must not be ready")
	}
	if _, bound, _ := crewOfflineCopyBound(team, a); !bound {
		t.Fatal("an older build stays bound so replies and threads keep working while it rebuilds")
	}
	history.SourceSize = 200
	write(history, "history.json")
	write(crewTeamOfflineCopyRef{Home: "home", ChatKey: key, Session: "s1", Revision: 2, Binding: b}, "copy.json")
	if _, ready, _ := crewOfflineCopyReady(team, a); ready {
		t.Fatal("a partial bootstrap (offset short of source size) must not be ready")
	}
	row := crewTeamReachableAgent{ID: agent, Kind: "agent"}
	// A live source answers itself, never its copy.
	onlineRow := row
	onlineRow.Online = true
	routed, err := crewTeamRouteAsk(t.Context(), team, onlineRow, crewTeamOfflineCopy)
	if err != nil || routed.ID != onlineRow.ID || routed.OfflineCopyID != "" {
		t.Fatal("copy overrode online source", err)
	}

	rebound := a
	rebound.Terminal = &crewTeamTerminal{Provider: "claude", AttachmentID: "attach-2"}
	write(crewTeamOfflineCopyRef{Home: "home", ChatKey: key, Session: "s1", Revision: 2, Binding: b}, "copy.json")
	if ref, bound, err := crewOfflineCopyBound(team, rebound); err != nil || !bound || ref.Session != "s1" {
		t.Fatal("reattach discarded the same copy runtime", err)
	}
	different := rebound
	different.ChatKey = "22222222-3333-4444-8555-666666666666"
	if _, bound, _ := crewOfflineCopyBound(team, different); bound {
		t.Fatal("different native session inherited copy runtime")
	}
	moved := team
	moved.OnlineHome = "home-2"
	if _, bound, _ := crewOfflineCopyBound(moved, a); bound {
		t.Fatal("a copy in a previous home must not be used")
	}

}

// PLAT-746-1024 slice 2b: an app source's earlier conversation reaches the copy only as a per-turn private block. It
// is rendered from the real journal format, and every save path's cleaner drops it, so it never lands in the saved chat.
func TestCrewOfflineAppPreludeStaysOutOfSavedHistory(t *testing.T) {
	journal, err := crewOfflineAppLines([]llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "source instructions"}}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Our launch city is Example Town."}}},
		{Role: llmtypes.ChatMessageTypeTool, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "raw tool output"}}},
		{Role: llmtypes.ChatMessageTypeAI, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Noted: Example Town."}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	text, err := crewOfflineAppTranscript(bytes.NewReader(journal), crewOfflinePreludeLimit)
	if err != nil || !strings.Contains(text, "[user · message]\nOur launch city is Example Town.") || !strings.Contains(text, "[assistant · message]\nNoted: Example Town.") ||
		strings.Contains(text, "raw tool output") || strings.Contains(text, "source instructions") {
		t.Fatalf("transcript must carry the conversation only: %q err=%v", text, err)
	}
	if short, _ := crewOfflineAppTranscript(bytes.NewReader(journal), 50); strings.Contains(short, "launch city") || !strings.Contains(short, "Noted:") {
		t.Fatalf("a bounded transcript keeps the most recent entries: %q", short)
	}
	prelude := "\n\n" + crewOfflinePreludeOpen + "\n" + text + "\n" + crewOfflinePreludeClose
	note := "Someone else is asking."
	rendered, err := agentprofiles.RenderPrompt(agentprofiles.Profile{ID: "offline-copy-test", Name: "Offline copy test", Version: 1, BuiltIn: true, SystemPromptTemplate: "base prompt"}, agentprofiles.PromptContext{OthersAsking: note + prelude})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "Example Town") {
		t.Fatal("the turn's instructions must carry the private block")
	}
	saved := cleanChatHistoryForPersistence([]llmtypes.MessageContent{
		{Role: llmtypes.ChatMessageTypeSystem, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: rendered}}},
		{Role: llmtypes.ChatMessageTypeHuman, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: "Which city?"}}},
	})
	raw, err := chatHistoryToRaw(saved)
	if err != nil {
		t.Fatal(err)
	}
	encoded := ""
	for _, b := range raw {
		encoded += string(b)
	}
	if strings.Contains(encoded, "Example Town") || strings.Contains(encoded, crewOfflinePreludeOpen) || !strings.Contains(encoded, note) || !strings.Contains(encoded, "Which city?") {
		t.Fatalf("saved history must drop only the private block: %s", encoded)
	}
}

// PLAT-746-1024: a terminal source's raw journal is kept privately as synced, but the copy's runtime model journal
// must not carry its secrets. A known secret in the raw ingest is absent after staging and forking, for both programs,
// while native identities and ordinary text survive.
func TestCrewOfflineNativeCopyRedactsSecretsBeforeImport(t *testing.T) {
	const id = "11111111-1111-4111-8111-111111111111"
	const secret = "sk-exampleSECRETvalue0123456789"
	cwd := t.TempDir()
	cases := map[string]struct{ provider, raw string }{
		"claude-code": {"claude", fmt.Sprintf(`{"type":"user","sessionId":%q,"cwd":%q,"message":{"content":"use key %s please"}}`+"\n"+
			`{"type":"assistant","sessionId":%q,"message":{"content":[{"type":"tool_use","input":{"command":"OPENAI_API_KEY=plainvalue123 run"}}]}}`+"\n", id, cwd, secret, id)},
		"codex-cli": {"codex", fmt.Sprintf(`{"type":"session_meta","payload":{"id":%q,"cwd":%q}}`+"\n"+
			`{"type":"response_item","payload":{"content":[{"text":"Bearer %s and OPENAI_API_KEY=plainvalue123"}]}}`+"\n", id, cwd, secret)},
	}
	for program, c := range cases {
		t.Run(program, func(t *testing.T) {
			_, format, ok := crewOfflineNativeFormat(c.provider)
			if !ok {
				t.Fatal("native program must be supported")
			}
			b := crewOfflineBinding{Provider: c.provider, Session: id, Attachment: "attach-1"}
			bundle, err := stageCrewOfflineNativeBundle(strings.NewReader(c.raw), b, program, format, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			forked, err := sessiontransfer.ForkJSONL(bundle, filepath.Join(t.TempDir(), "native"), cwd)
			if err != nil {
				t.Fatal(err)
			}
			for _, root := range []string{bundle.Root, forked.Root} {
				copied, err := os.ReadFile(filepath.Join(root, "session.jsonl"))
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(copied), secret) || strings.Contains(string(copied), "plainvalue123") {
					t.Fatalf("runtime journal kept a secret: %s", copied)
				}
				if !strings.Contains(string(copied), cwd) {
					t.Fatal("redaction must keep native structure and paths")
				}
			}
			if !strings.Contains(c.raw, secret) {
				t.Fatal("fixture must carry the secret in the private raw ingest")
			}
		})
	}
}
