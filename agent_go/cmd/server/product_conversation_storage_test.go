package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/manishiitg/coding-agent-loop/workspace/sqliteopen"
)

func registryStorageFixture(t *testing.T) (string, string) {
	t.Helper()
	docs, state := t.TempDir(), t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	physical, err := sqliteopen.CanonicalPath(state)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		conversationRegistryConnections.Lock()
		defer conversationRegistryConnections.Unlock()
		for file, db := range conversationRegistryConnections.byFile {
			if strings.HasPrefix(file, physical+string(filepath.Separator)) {
				db.Close()
				delete(conversationRegistryConnections.byFile, file)
			}
		}
	})
	return docs, state
}
func writeRegistryFixture(t *testing.T, docs, user string, document productConversationRegistryDocument) (string, []byte) {
	t.Helper()
	file := filepath.Join(docs, filepath.FromSlash(productConversationRegistryPath(user)))
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	return file, raw
}

// PLAT-746-827: importing preserves exact bindings/history; ordinary updates
// touch one slot, and a failed rotation cannot leave current/prior out of sync
// or alter another user's identically named slot.
func TestConversationRegistryStorageImportAndSlotTransactions(t *testing.T) {
	docs, _ := registryStorageFixture(t)
	ctx := context.Background()
	key := crewProfileID + "/project"
	record := ProductConversationRecord{ConversationID: "logical", ConversationKey: "project", ProfileID: crewProfileID, SessionID: "old-session", WorkspacePath: "Crew/example", ConnectionID: "own-account", ModelID: "old-model", FreshNative: true, SelectedSkills: []string{"saved-skill"}}
	original := productConversationRegistryDocument{Version: 1, Entries: map[string]ProductConversationRecord{key: record}, Previous: map[string][]ProductConversationRecord{key: {{SessionID: "duplicate", ConversationID: "first"}, {SessionID: "duplicate", ConversationID: "second"}}}, Agents: map[string]ProductAgentIdentity{key: {Name: "original", ConnectionID: "own-account"}, crewProfileID + "/orphan": {Name: "retained orphan"}}}
	source, raw := writeRegistryFixture(t, docs, "owner", original)
	rows, err := activateConversationRegistryRows(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	current, exists, prior, err := rows.history(ctx, crewProfileID, "project")
	if err != nil || !exists || !reflect.DeepEqual(current, record) || !reflect.DeepEqual(prior, original.Previous[key]) {
		t.Fatal("import changed bindings or ordered duplicate history", err)
	}
	dir, err := sqliteopen.StateDirectory(docs, "conversation-registry")
	if err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(dir, "imports", sqliteopen.PathHash(rows.namespace), productConversationRegistryFile+".imported")
	if saved, err := os.ReadFile(backup); err != nil || !bytes.Equal(saved, raw) {
		t.Fatal("registry backup lost original bytes", err)
	}
	if _, err = os.Stat(source); !os.IsNotExist(err) {
		t.Fatal("imported source remains live")
	}
	if info, err := os.Stat(filepath.Join(dir, "registry.sqlite")); err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("registry database is not private")
	}
	foreign := original
	foreign.Entries = map[string]ProductConversationRecord{key: {ConversationID: "other-logical", ConversationKey: "project", ProfileID: crewProfileID, SessionID: "other-session", WorkspacePath: "Crew/example"}}
	writeRegistryFixture(t, docs, "other", foreign)
	other, err := activateConversationRegistryRows(ctx, "other")
	if err != nil {
		t.Fatal(err)
	}
	// This trigger reproduces a failed second half of a rotation. It also proves
	// ordinary runtime/identity edits do not rewrite prior conversations.
	if _, err = rows.db.ExecContext(ctx, `CREATE TRIGGER refuse_fixture_prior BEFORE INSERT ON registry_previous BEGIN SELECT RAISE(ABORT,'fixture insertion refused'); END`); err != nil {
		t.Fatal(err)
	}
	if err = rows.mutateCurrent(ctx, crewProfileID, "project", func(current *ProductConversationRecord) (*ProductConversationRecord, error) {
		current.ModelID = "new-model"
		return current, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = rows.mutateIdentity(ctx, crewProfileID, "project", func(identity ProductAgentIdentity) (*ProductAgentIdentity, error) {
		identity.Name = "renamed"
		return &identity, nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = rows.mutateIdentity(ctx, crewProfileID, "orphan", func(identity ProductAgentIdentity) (*ProductAgentIdentity, error) { return &identity, nil }); err == nil {
		t.Fatal("orphan identity granted a writable chat")
	}
	rotate := func(current *ProductConversationRecord, previous []ProductConversationRecord) (*ProductConversationRecord, []ProductConversationRecord, error) {
		old := *current
		next := *current
		next.SessionID = "new-session"
		return &next, append([]ProductConversationRecord{old}, previous...), nil
	}
	if err = rows.mutateSlot(ctx, crewProfileID, "project", rotate); err == nil {
		t.Fatal("fixture rotation failure was hidden")
	}
	after, _, kept, err := rows.history(ctx, crewProfileID, "project")
	if err != nil || after.SessionID != "old-session" || after.ModelID != "new-model" || !reflect.DeepEqual(kept, prior) {
		t.Fatal("failed rotation left a partial binding/history", err)
	}
	if _, err = rows.db.ExecContext(ctx, `DROP TRIGGER refuse_fixture_prior`); err != nil {
		t.Fatal(err)
	}
	if err = rows.mutateSlot(ctx, crewProfileID, "project", rotate); err != nil {
		t.Fatal(err)
	}
	after, _, kept, err = rows.history(ctx, crewProfileID, "project")
	if err != nil || after.SessionID != "new-session" || len(kept) != 3 || kept[0].SessionID != "old-session" || kept[1].ConversationID != "first" || kept[2].ConversationID != "second" {
		t.Fatal("rotation changed history identity/order", err)
	}
	identity, err := rows.agentIdentity(ctx, crewProfileID, "project")
	if err != nil || identity.Name != "renamed" || identity.ConnectionID != "own-account" {
		t.Fatal("identity update changed account binding", err)
	}
	untouched, _, _, err := other.history(ctx, crewProfileID, "project")
	if err != nil || untouched.SessionID != "other-session" {
		t.Fatal("another user's binding changed", err)
	}
	// The committed marker is the only authority after import, even if an old
	// file reappears. It must never restore the previous runtime/account state.
	if err = os.WriteFile(source, raw, 0600); err != nil {
		t.Fatal(err)
	}
	again, err := activateConversationRegistryRows(ctx, "owner")
	if err != nil {
		t.Fatal(err)
	}
	retained, _, _, err := again.history(ctx, crewProfileID, "project")
	if err != nil || retained.SessionID != "new-session" || retained.ModelID != "new-model" {
		t.Fatal("stale source was replayed", err)
	}
	broken := filepath.Join(docs, filepath.FromSlash(productConversationRegistryPath("broken")))
	if err = os.MkdirAll(filepath.Dir(broken), 0700); err != nil {
		t.Fatal(err)
	}
	const invalid = `{"entries":{"unfinished":`
	if err = os.WriteFile(broken, []byte(invalid), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = activateConversationRegistryRows(ctx, "broken"); err == nil {
		t.Fatal("corrupt identity registry was replaced with empty bindings")
	}
	if saved, err := os.ReadFile(broken); err != nil || string(saved) != invalid {
		t.Fatal("failed identity import changed the source")
	}
}

// PLAT-746-827: the operator's real command may only GET the old API, must not
// create/activate live SQLite state, and may print counts rather than content.
// Ambiguous current profile/session/workspace bindings fail the comparison and
// the candidate getter instead of selecting one of two possible conversations.
func TestRegistryCheckIsReadOnlyAndReportsAmbiguousBindings(t *testing.T) {
	docs := t.TempDir()
	state := filepath.Join(t.TempDir(), "must-stay-missing")
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("AGENTWORKS_STATE_ROOT", state)
	const secret = "synthetic-private-content-must-not-print"
	key := crewProfileID + "/project"
	document := productConversationRegistryDocument{Version: 1, Entries: map[string]ProductConversationRecord{key: {ConversationID: "logical", ConversationKey: "project", ProfileID: crewProfileID, SessionID: "session", WorkspacePath: "Crew/example", Description: secret, ConnectionID: secret}}, Agents: map[string]ProductAgentIdentity{key: {Name: "name", Purpose: secret}}}
	source, raw := writeRegistryFixture(t, docs, "owner", document)
	logical := productConversationRegistryPath("owner")
	mock := &mockWorkspaceAPI{files: map[string]string{logical: string(raw)}}
	var mutations atomic.Int64
	var malformed atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			mutations.Add(1)
		}
		if malformed.Load() {
			writeJSON(w, 200, map[string]interface{}{"success": true, "data": map[string]any{"unexpected": secret}})
			return
		}
		mock.ServeHTTP(w, r)
	}))
	defer server.Close()
	t.Setenv("WORKSPACE_API_URL", server.URL)
	execute := func() ([]byte, error) {
		var output bytes.Buffer
		previousLog := log.Writer()
		log.SetOutput(&output)
		defer log.SetOutput(previousLog)
		cmd := newRegistryCheckCommand()
		cmd.SetOut(&output)
		cmd.SetErr(&output)
		cmd.SetArgs([]string{"--docs-root", docs})
		err := cmd.ExecuteContext(context.Background())
		if strings.Contains(output.String(), secret) {
			t.Fatal("registry check printed private content/account metadata")
		}
		return output.Bytes(), err
	}
	output, err := execute()
	if err != nil {
		t.Fatalf("equivalent snapshot refused: %s %v", output, err)
	}
	var report registryCheckReport
	if err = json.Unmarshal(bytes.TrimSpace(output), &report); err != nil || len(report.Differences) != 0 || report.Ambiguous != 0 {
		t.Fatal("equivalent comparison report invalid", err)
	}
	duplicate := document.Entries[key]
	duplicate.ConversationID = "second-logical"
	duplicate.ConversationKey = "project:chat:side"
	document.Entries[crewProfileID+"/"+duplicate.ConversationKey] = duplicate
	_, raw = writeRegistryFixture(t, docs, "owner", document)
	mock.mu.Lock()
	mock.files[logical] = string(raw)
	mock.mu.Unlock()
	output, err = execute()
	if err == nil {
		t.Fatal("ambiguous binding passed registry check")
	}
	// Cobra may append its ordinary error/usage text; decode only the first
	// report, which never contains the original binding fields.
	decoder := json.NewDecoder(bytes.NewReader(output))
	if err = decoder.Decode(&report); err != nil || report.Ambiguous != 1 {
		t.Fatalf("ambiguous count missing: %s %v", output, err)
	}
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	candidate := conversationRegistryRows{db: db, namespace: logical}
	if err = candidate.importSnapshot(context.Background(), string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err = candidate.workflowStepRun(context.Background(), "session", "Crew/example", ""); !errors.Is(err, errAmbiguousConversationRegistry) {
		t.Fatal("candidate reader guessed an ambiguous current binding", err)
	}
	// The legacy reader's unexpected-response debug line includes its data.
	// The standalone command must suppress that logger and report only a count.
	malformed.Store(true)
	output, err = execute()
	if err == nil {
		t.Fatal("malformed upstream response passed check")
	}
	decoder = json.NewDecoder(bytes.NewReader(output))
	if err = decoder.Decode(&report); err != nil || report.Differences["registry_read"] != 1 {
		t.Fatalf("malformed upstream report invalid: %s %v", output, err)
	}
	if mutations.Load() != 0 {
		t.Fatal("registry check issued a mutation request")
	}
	if saved, err := os.ReadFile(source); err != nil || !bytes.Equal(saved, raw) {
		t.Fatal("registry check modified its live source")
	}
	if _, err = os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("registry check created live private state")
	}
}
