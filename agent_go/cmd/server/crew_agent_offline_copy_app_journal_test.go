package server

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

func TestCrewOfflineAppJournalPreservesPrefixAndExcludesSystem(t *testing.T) {
	private := t.TempDir()
	roots := crewroot.Roots{Private: private, Virtual: "Crew/example"}
	record := ProductConversationRecord{ConversationID: "conversation-fixture", ProfileID: crewProfileID, SessionID: "source-session", WorkspacePath: roots.Virtual, ConversationKey: "example:chat:source"}
	message := func(role llmtypes.ChatMessageType, text string) llmtypes.MessageContent {
		return llmtypes.MessageContent{Role: role, Parts: []llmtypes.ContentPart{llmtypes.TextContent{Text: text}}}
	}
	history := []llmtypes.MessageContent{message(llmtypes.ChatMessageTypeSystem, "PRIVATE ROOT INSTRUCTIONS"), message(llmtypes.ChatMessageTypeHuman, "first")}
	path, err := crewOfflineAppJournal(roots, record, history)
	if err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(first, []byte("PRIVATE ROOT")) {
		t.Fatal("root instructions copied into journal")
	}
	history = append(history, message(llmtypes.ChatMessageTypeAI, "answer"))
	if _, err = crewOfflineAppJournal(roots, record, history); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil || !bytes.HasPrefix(second, first) || bytes.Equal(second, first) {
		t.Fatal("not an append-only portable journal", err)
	}
	// A compacted history (live: a server Code chat cut to 2 messages) appends after the kept bytes instead of
	// stalling the copy forever, and the next turn appends only its own message.
	history = []llmtypes.MessageContent{message(llmtypes.ChatMessageTypeHuman, "compacted summary")}
	if _, err = crewOfflineAppJournal(roots, record, history); err != nil {
		t.Fatal(err)
	}
	third, _ := os.ReadFile(path)
	if !bytes.HasPrefix(third, second) || !bytes.Contains(third[len(second):], []byte("compacted summary")) {
		t.Fatal("rewritten history replaced acknowledged bytes or was not appended")
	}
	history = append(history, message(llmtypes.ChatMessageTypeAI, "next"))
	if _, err = crewOfflineAppJournal(roots, record, history); err != nil {
		t.Fatal(err)
	}
	fourth, _ := os.ReadFile(path)
	if !bytes.HasPrefix(fourth, third) || bytes.Count(fourth, []byte("compacted summary")) != 1 || !bytes.Contains(fourth[len(third):], []byte("next")) {
		t.Fatal("turn after a rewrite must append only its new message")
	}
	// Live: a Mac Main chat whose saved history window drops its oldest messages each turn
	// re-appended the whole history every turn, growing the journal past 1.6 GB and timing out chunk uploads.
	history = []llmtypes.MessageContent{message(llmtypes.ChatMessageTypeAI, "next"), message(llmtypes.ChatMessageTypeHuman, "again")}
	if _, err = crewOfflineAppJournal(roots, record, history); err != nil {
		t.Fatal(err)
	}
	fifth, _ := os.ReadFile(path)
	if added := fifth[len(fourth):]; !bytes.HasPrefix(fifth, fourth) || !bytes.Contains(added, []byte("again")) || bytes.Contains(added, []byte("next")) {
		t.Fatal("a trimmed history window must append only its new messages")
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "private")
	if err = os.WriteFile(outside, []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err = crewOfflineAppJournal(roots, record, history); err == nil {
		t.Fatal("source journal followed a symlink")
	}
}
