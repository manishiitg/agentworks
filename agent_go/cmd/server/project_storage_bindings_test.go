package server

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/services"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/chathistory"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// One real private-state roundtrip pins the move/rollback contract. Filesystem
// cutover and live provider continuation are deliberately separate acceptance.
func TestProjectStorageBindingsSurviveMoveAndRollback(t *testing.T) {
	_, ws, ctx := newCrewCreationTestEnv(t)
	t.Setenv("AUTH_SECRET", "storage-binding-test-key-with-enough-entropy")
	t.Setenv("WORKSPACE_DOCS_PATH", t.TempDir())
	const legacy = "_users/owner/Chats/Code/projects/bindings-12345678"
	const moved = "Crew/bindings-12345678"
	registry := defaultProjectOwners()
	rec := projectOwnerRecord{Product: "code", Folder: "bindings-12345678", ProjectID: "project-id", OwnerID: "owner", Private: true, AudienceConfigured: true, RunPeople: []string{"run"}, CoOwners: []string{"builder"}}
	if err := registry.Register(rec); err != nil {
		t.Fatal(err)
	}
	owners, aliases := map[string]string{legacy: "owner"}, map[string]string{}
	stubCrewLookups(t, owners, aliases)
	store, err := chathistory.NewFilesystemStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{chatStore: store}
	if err := api.upsertSharedWorkflowSecret(ctx, legacy, "TOKEN", "test-only-value"); err != nil {
		t.Fatal(err)
	}
	beforeSecrets, err := store.ListWorkflowSecrets(ctx, chathistory.SharedWorkflowSecretsUserID, legacy)
	if err != nil || len(beforeSecrets) != 1 {
		t.Fatal("missing original secret", err)
	}
	if err := store.UpsertWorkflowProviderCredential(ctx, "owner", legacy, "provider", "opaque-test-record"); err != nil {
		t.Fatal(err)
	}
	if err := recordPlaceMCP("owner", "example", legacy); err != nil {
		t.Fatal(err)
	}
	originalStore := placeMCPStoreID("owner", legacy)
	originalDir, err := placeMCPDir(originalStore)
	if err != nil {
		t.Fatal(err)
	}
	tokenFile := placeMCPTokenFile(originalDir, originalStore, "example")
	sealed, err := (placeMCPTokenSealer{}).Seal(tokenFile, []byte("test-only-login"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenFile, sealed, 0600); err != nil {
		t.Fatal(err)
	}
	publicationFile, err := publicationRecordPath(legacy)
	if err != nil {
		t.Fatal(err)
	}
	publication, _ := json.Marshal(publishedProject{Root: legacy, OwnerID: "owner", WorkspaceID: "project-id", Revision: "revision1"})
	if err := os.MkdirAll(filepath.Dir(publicationFile), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(publicationFile, publication, 0600); err != nil {
		t.Fatal(err)
	}
	connection := services.GmailConnection{ID: "stable-account", OwnerID: "owner", Personal: true, Workspaces: []string{legacy}}
	handle := requireAgentHandle(t, testAgentWithHandle("code:project:project-id", llmtypes.CodingProviderSessionHandle{Provider: "codex-cli", WorkingDir: filepath.Join(fsutil.WorkspaceDocsRoot(), legacy), ProjectDirID: filepath.Join(fsutil.WorkspaceDocsRoot(), legacy), NativeSessionID: "native-unchanged"}))
	runtime := &ChatHistoryAgentRuntime{Kind: "coding_agent", Provider: "codex-cli", ResumeSupported: true, AgentSessionHandle: handle, ProjectDirID: handle.Provider.ProjectDirID}
	originalRuntime, _ := json.Marshal(runtime)
	for _, destination := range []string{moved, legacy, moved} {
		current, _ := registry.Lookup("code", rec.Folder)
		stale := userAccessHash(current)
		if err := registry.compareAndSetStorageLocation("code", rec.Folder, stale, destination); err != nil {
			t.Fatal(err)
		}
		if err := registry.compareAndSetStorageLocation("code", rec.Folder, stale, "Crew/other-project"); err == nil {
			t.Fatal("stale storage change was admitted")
		}
		delete(aliases, destination)
		other := legacy
		if destination == legacy {
			other = moved
		}
		aliases[other], owners[destination] = destination, "owner"
		ws.files[destination+"/product.json"] = `{"product":"code","mode":"dev"}`
		if got := projectBindingRoot(destination); got != legacy {
			t.Fatalf("private state key changed: %q", got)
		}
		if other := "_users/another-owner/Chats/Code/projects/bindings-12345678"; projectBindingRoot(other) != other {
			t.Fatal("an unregistered owner spelling selected another project's state")
		}
		path, _ := publicationRecordPath(destination)
		published, err := readPublishedProject(ctx, destination)
		if path != publicationFile || err != nil || published == nil || published.Root != destination || published.WorkspaceID != "project-id" {
			t.Fatalf("publication did not follow routing: %+v %v", published, err)
		}
		secret, err := api.projectSecretValue(destination, "TOKEN")
		stored, _ := store.ListWorkflowSecrets(ctx, chathistory.SharedWorkflowSecretsUserID, destination)
		credential, credentialErr := store.GetWorkflowProviderCredential(ctx, "owner", destination, "provider")
		if err != nil || secret != "test-only-value" || len(stored) != 1 || stored[0].EncryptedValue != beforeSecrets[0].EncryptedValue || credentialErr != nil || credential == nil || credential.EncryptedValue != "opaque-test-record" {
			t.Fatal("private state changed or became unavailable", err, credentialErr)
		}
		items, err := placeMCPAttachmentsFor(destination)
		if err != nil || len(items) != 1 || attachmentStore(items[0], destination) != originalStore {
			t.Fatal("legacy connection changed its encrypted store", err)
		}
		opened, err := (placeMCPTokenSealer{}).Open(tokenFile, sealed)
		if err != nil || string(opened) != "test-only-login" {
			t.Fatal("retained login no longer opens at its original path", err)
		}
		// Exercise the normal index rewrite too; a later rollback must retain
		// the original key even after the attachment key was canonicalized.
		placeMCPMu.Lock()
		all, readErr := readPlaceMCPAttachmentsLocked()
		if readErr == nil {
			readErr = writePlaceMCPAttachmentsLocked(all)
		}
		placeMCPMu.Unlock()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !connection.UsableFrom(services.GmailUseScope{Workspace: destination}) || connection.UsableFrom(services.GmailUseScope{Workspace: "Crew/unrelated"}) {
			t.Fatal("connection selection lost its project boundary")
		}
		agent := testAgentWithHandle("code:project:project-id", llmtypes.CodingProviderSessionHandle{Provider: "codex-cli", WorkingDir: filepath.Join(fsutil.WorkspaceDocsRoot(), destination)})
		folded, allowed := projectStorageResumeRuntime(agent, runtime)
		if !allowed || folded.AgentSessionHandle.Provider.WorkingDir != filepath.Join(fsutil.WorkspaceDocsRoot(), destination) || folded.AgentSessionHandle.Provider.NativeSessionID != "native-unchanged" {
			t.Fatal("native handle did not retain identity and follow routing")
		}
		if !api.seedCodingAgentRuntimeFromRestoredConversation("code:project:project-id", "codex-cli", "", runtime, agent) || requireAgentHandle(t, agent).Provider.NativeSessionID != "native-unchanged" {
			t.Fatal("valid alias could not restore its native handle")
		}
		// Refusal must happen in the real seed path, before importing any state.
		agent = testAgentWithHandle("code:project:project-id", llmtypes.CodingProviderSessionHandle{Provider: "codex-cli", WorkingDir: filepath.Join(fsutil.WorkspaceDocsRoot(), destination)})
		foreign := *runtime
		foreignHandle := *runtime.AgentSessionHandle
		foreignHandle.Provider.WorkingDir = filepath.Join(fsutil.WorkspaceDocsRoot(), "Crew/unrelated")
		foreign.AgentSessionHandle = &foreignHandle
		foreign.Kind, foreign.Provider, foreign.ResumeSupported = "coding_agent", "codex-cli", true
		if api.seedCodingAgentRuntimeFromRestoredConversation("code:project:project-id", "codex-cli", "", &foreign, agent) {
			t.Fatal("foreign runtime reached native session restoration")
		}
		if got := requireAgentHandle(t, agent); got.Provider.NativeSessionID != "" {
			t.Fatal("refusal imported a native session")
		}
		current, _ = registry.Lookup("code", rec.Folder)
		if current.BindingRoot != legacy || current.OwnerID != rec.OwnerID || current.Product != rec.Product || !current.Private || len(current.RunPeople) != 1 || len(current.CoOwners) != 1 {
			t.Fatal("storage change altered project authority")
		}
	}
	afterRuntime, _ := json.Marshal(runtime)
	afterPublication, _ := os.ReadFile(publicationFile)
	afterToken, _ := os.ReadFile(tokenFile)
	if !bytes.Equal(originalRuntime, afterRuntime) || !bytes.Equal(publication, afterPublication) || !bytes.Equal(sealed, afterToken) {
		t.Fatal("routing rewrote retained history, publication, or login bytes")
	}
}
