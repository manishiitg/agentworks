package server

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// PLAT-746-1024 slice 2: an offline copy answers only while it is bound to the source's CURRENT attachment and home,
// and is ready only when built from the complete latest synced revision. A rebound attachment, a moved home or a
// stale/partial build must never route a new ask to old history.
func TestCrewOfflineCopyBindingAndReadiness(t *testing.T) {
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("AGENTWORKS_DEPLOYMENT_MODE", "server")
	agent := "team-agent-11111111-2222-4333-8444-555555555555"
	session := "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	team := crewTeam{ProjectID: "source", OnlineHome: "home", Agents: map[string]crewTeamRegisteredAgent{
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
	// The copy base and every asker's thread are fenced from the home's owner-tab paths.
	thread := sharedThreadKey("home", crewOfflineCopyTabPrefix+"11111111-2222-4333-8444-555555555555", "asker")
	if !crewOfflineCopyHomeKey("home", key) || !crewOfflineCopyHomeKey("home", thread) || crewOfflineCopyHomeKey("home", "home"+codeChatSideMarker+"notes") {
		t.Fatal("copy base and its askers' threads, and only those, are copy keys")
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
	// An explicit Put online intent, recorded even before the first build, applies only in the current home.
	row := crewTeamReachableAgent{ID: agent, Kind: "agent"}
	write(crewTeamOfflineCopyRef{Home: "home", Binding: b, PutOnline: true}, "copy.json")
	if on, err := crewOfflineCopyPutOnline(team, row); err != nil || !on {
		t.Fatalf("a recorded Put online intent must select the copy: on=%v err=%v", on, err)
	}
	if _, ready, _ := crewOfflineCopyReady(team, a); ready {
		t.Fatal("an intent recorded before any build is not a ready copy")
	}
	rebound := a
	rebound.Terminal = &crewTeamTerminal{Provider: "claude", AttachmentID: "attach-2"}
	if _, bound, _ := crewOfflineCopyBound(team, rebound); bound {
		t.Fatal("a rebound source attachment must not reuse the old copy")
	}
	if on, _ := crewOfflineCopyPutOnline(crewTeam{ProjectID: team.ProjectID, OnlineHome: "home-2", Agents: team.Agents}, row); on {
		t.Fatal("a moved home must drop the Put online intent")
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
	if err != nil || !strings.Contains(text, "Person: Our launch city is Example Town.") || !strings.Contains(text, "Agent: Noted: Example Town.") ||
		strings.Contains(text, "raw tool output") || strings.Contains(text, "source instructions") {
		t.Fatalf("transcript must carry the conversation only: %q err=%v", text, err)
	}
	if short, _ := crewOfflineAppTranscript(bytes.NewReader(journal), 40); strings.Contains(short, "Person:") || !strings.Contains(short, "Agent:") {
		t.Fatalf("a bounded transcript keeps the most recent entries: %q", short)
	}
	if crewOfflineCopyKeyAgent("home", sharedThreadKey("home", crewOfflineCopyTabPrefix+"11111111-2222-4333-8444-555555555555", "asker")) != "team-agent-11111111-2222-4333-8444-555555555555" {
		t.Fatal("an asker's thread must name its copy's source agent")
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
