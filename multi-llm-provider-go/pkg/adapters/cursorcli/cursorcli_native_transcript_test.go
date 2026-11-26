package cursorcli

import (
	"context"
	"crypto/md5"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/pathidentity"
)

// writeCursorStoreFixture builds a store.db shaped like cursor-agent's own
// (hex-encoded meta JSON, protobuf root of 32-byte refs, JSON message blobs)
// under ~/.cursor/chats/<md5(cwd)>/<agentId>/ and returns its path.
func writeCursorStoreFixture(t *testing.T, workingDir, agentID string, blobs []string) string {
	t.Helper()
	home, _ := os.UserHomeDir()
	sum := md5.Sum([]byte(filepath.Clean(pathidentity.Key(workingDir)))) //nolint:gosec
	agentDir := filepath.Join(home, ".cursor", "chats", hex.EncodeToString(sum[:]), agentID)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(agentDir, "store.db")

	ids := make([][]byte, len(blobs))
	for i := range blobs {
		ids[i] = synthBlobID(byte(i + 1))
	}
	idRoot := synthBlobID(0xff)
	root := buildCursorRootBlob(t, ids)

	db, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, stmt := range []string{
		`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT)`,
		`CREATE TABLE blobs (id TEXT PRIMARY KEY, data BLOB)`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	metaJSON, _ := json.Marshal(map[string]any{"latestRootBlobId": hex.EncodeToString(idRoot)})
	if _, err := db.ExecContext(ctx, `INSERT INTO meta(key,value) VALUES('0', ?)`, hex.EncodeToString(metaJSON)); err != nil {
		t.Fatal(err)
	}
	insert := func(id []byte, body []byte) {
		if _, err := db.ExecContext(ctx, `INSERT INTO blobs(id,data) VALUES(?, ?)`, hex.EncodeToString(id), body); err != nil {
			t.Fatal(err)
		}
	}
	insert(idRoot, root)
	for i, blob := range blobs {
		insert(ids[i], []byte(blob))
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return dbPath
}

func TestReadNativeTranscriptKeepsTypedQueriesAndAssistantProse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workingDir := filepath.Join(home, "ws", "proj")
	if err := os.MkdirAll(workingDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Two turns, exactly as observed live: per turn a provider-options
	// context blob (plain string) then the query blob (text block wrapping
	// <user_query>), then assistant reasoning/text/tool-call blobs.
	dbPath := writeCursorStoreFixture(t, workingDir, "agent-1", []string{
		`{"role":"system","content":"You are Auto."}`,
		`{"role":"user","content":"<user_info>\nOS Version: darwin\n</user_info>","providerOptions":{}}`,
		`{"role":"user","content":[{"type":"text","text":"<timestamp>Thursday, Sep 3, 2026, 8:42 PM</timestamp>\n<user_query>\nwhich strategy is winning\n</user_query>"}]}`,
		`{"role":"assistant","content":[{"type":"reasoning","text":"**Checking**"},{"type":"text","text":"Let me look."}]}`,
		`{"role":"assistant","content":[{"type":"tool-call","toolCallId":"t1","toolName":"Read","args":{"path":"db"}}]}`,
		`{"role":"tool","content":[{"type":"tool-result","toolCallId":"t1","toolName":"Read","result":"rows"}]}`,
		`{"role":"assistant","content":[{"type":"text","text":"The builder flywheel is winning."}]}`,
		`{"role":"user","content":"<user_info>\nOS Version: darwin\n</user_info>","providerOptions":{}}`,
		`{"role":"user","content":[{"type":"text","text":"<user_query>\nand the runner up?\n</user_query>"}]}`,
		`{"role":"assistant","content":[{"type":"text","text":"Founder origin stories."}]}`,
	})

	transcript, ok, err := ReadNativeTranscript(workingDir, "agent-1")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if transcript.Path != dbPath {
		t.Fatalf("path = %q, want %q", transcript.Path, dbPath)
	}
	want := []struct {
		role llmtypes.ChatMessageType
		text string
	}{
		{llmtypes.ChatMessageTypeHuman, "which strategy is winning"},
		{llmtypes.ChatMessageTypeAI, "Let me look."},
		{llmtypes.ChatMessageTypeAI, "The builder flywheel is winning."},
		{llmtypes.ChatMessageTypeHuman, "and the runner up?"},
		{llmtypes.ChatMessageTypeAI, "Founder origin stories."},
	}
	if len(transcript.Messages) != len(want) {
		t.Fatalf("got %d messages, want %d: %+v", len(transcript.Messages), len(want), transcript.Messages)
	}
	for i, w := range want {
		got := transcript.Messages[i]
		text := got.Parts[0].(llmtypes.TextContent).Text
		if got.Role != w.role || text != w.text {
			t.Fatalf("message %d = %s %q, want %s %q", i, got.Role, text, w.role, w.text)
		}
	}
	// PLAT-856-145: private config lookup must retain the exact native ID.
	private, found, readErr := ReadNativeTranscriptFromConfigDir(filepath.Join(home, ".cursor"), "agent-1", workingDir)
	if readErr != nil || !found || private.Path != dbPath || len(private.Messages) != len(want) {
		t.Fatalf("private config transcript: %+v %v", private, readErr)
	}
	if _, found, err := ReadNativeTranscriptFromConfigDir(filepath.Join(home, ".cursor"), "../agent-1", workingDir); found || err != nil {
		t.Fatal("relative native ID accepted")
	}
	// PLAT-872-55: terminal stores must stay in the issued config, never another
	// account's newer conversation or the ambient HOME's matching native ID.
	configDir := t.TempDir()
	tmuxPath := filepath.Join(configDir, "chats", fmt.Sprintf("%x", md5.Sum([]byte(workingDir))), "agent-1", "store.db")
	if err := os.MkdirAll(filepath.Dir(tmuxPath), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmuxPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	terminal, found, err := ReadNativeTranscriptFromConfigDir(configDir, "agent-1", workingDir)
	if err != nil || !found || terminal.Path != tmuxPath || len(terminal.Messages) != len(want) {
		t.Fatalf("terminal private transcript: %+v found=%v err=%v", terminal, found, err)
	}
	if _, found, err := ReadNativeTranscriptFromConfigDir(t.TempDir(), "agent-1", workingDir); found || err != nil {
		t.Fatal("private runtime searched another account")
	}

	if transcript.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt should come from the store's mtime")
	}

	if _, ok, err := ReadNativeTranscript(workingDir, "agent-missing"); ok || err != nil {
		t.Fatalf("missing agent: ok=%v err=%v, want false/nil", ok, err)
	}
}

// The turn reader shares the meta/root resolution; make sure the refactor
// kept it working on the same fixture shape.
func TestReadCursorStoreDBMessagesStillReadsAfterRootRefsRefactor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workingDir := filepath.Join(home, "ws", "proj")
	if err := os.MkdirAll(workingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := writeCursorStoreFixture(t, workingDir, "agent-2", []string{
		`{"role":"user","content":"<user_info>ctx</user_info>"}`,
		`{"role":"user","content":[{"type":"text","text":"<user_query>\nhi\n</user_query>"}]}`,
		`{"role":"assistant","content":[{"type":"text","text":"hello"}]}`,
	})
	msgs := readCursorStoreDBMessagesAfter(dbPath, nil)
	if len(msgs) != 1 || msgs[0].Role != llmtypes.ChatMessageTypeAI {
		t.Fatalf("turn reader = %+v, want one assistant message", msgs)
	}
}

// A turn records only what Cursor committed after the snapshot taken before
// its prompt, however many earlier turns (live-typed or not) the session
// holds. An auto-notification turn after several live sends once replayed
// every earlier assistant message as its reply (RTS 2026-09-24,
// automationtesting).
func TestTurnRecordsOnlyWhatFollowsItsSnapshot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	workingDir := filepath.Join(home, "ws", "proj")
	if err := os.MkdirAll(workingDir, 0o755); err != nil {
		t.Fatal(err)
	}
	earlier := []string{
		`{"role":"user","content":"context"
 "crypto/md5"
 "fmt"}`,
		`{"role":"user","content":"<user_query>first question</user_query>"}`,
		`{"role":"assistant","content":[{"type":"text","text":"first answer"}]}`,
		`{"role":"user","content":"<user_query>live follow-up</user_query>"}`,
		`{"role":"assistant","content":[{"type":"text","text":"follow-up answer"}]}`,
	}
	db := writeCursorStoreFixture(t, workingDir, "sess-1", earlier)
	_, refs := cursorStoreMessagesExcept(db, nil)
	snapshot := map[string]struct{}{}
	for _, ref := range refs {
		snapshot[ref] = struct{}{}
	}
	if msgs := readCursorStoreDBMessagesAfter(db, snapshot); len(msgs) != 0 {
		t.Fatalf("nothing new since the snapshot, got %d messages: %+v", len(msgs), msgs)
	}
	if msgs := readCursorStoreDBMessagesAfter(db, nil); len(msgs) != 2 {
		t.Fatalf("no snapshot (fresh session) returns the whole store, got %d", len(msgs))
	}
}
