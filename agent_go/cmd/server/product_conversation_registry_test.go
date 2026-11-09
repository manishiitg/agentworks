package server

import (
	"context"

	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

func memoryProductConversationStore() (productConversationRegistryStore, map[string]string) {
	files := map[string]string{}
	ids := []string{"logical-1", "session-1", "logical-2", "session-2"}
	return productConversationRegistryStore{
		read: func(_ context.Context, path string) (string, bool, error) {
			value, ok := files[path]
			return value, ok, nil
		},
		write: func(_ context.Context, path, value string) error {
			files[path] = value
			return nil
		},
		now: func() time.Time { return time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC) },
		newID: func() string {
			value := ids[0]
			ids = ids[1:]
			return value
		},
	}, files
}

func singletonConversationProfile() agentprofiles.Profile {
	profile := routeTestProfile("example-product", true, "")
	profile.Runtime.Workspace = agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeFixed, Root: "Chats"}
	profile.Runtime.Conversation = agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeSingleton}
	return profile
}

func TestProductConversationRegistryReusesStableIdentity(t *testing.T) {
	store, _ := memoryProductConversationStore()
	profile := singletonConversationProfile()
	binding, err := resolveProductConversationBinding(context.Background(), "user-1", profile, "")
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.resolveOrCreate(context.Background(), "user-1", profile, binding, "existing-session")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.resolveOrCreate(context.Background(), "user-1", profile, binding, "different-session")
	if err != nil {
		t.Fatal(err)
	}
	if first.ConversationID != second.ConversationID || second.SessionID != "existing-session" {
		t.Fatalf("durable identity changed: first=%+v second=%+v", first, second)
	}
}

func TestShouldRebindWorkConversationOnlyForVerifiedTabSpecificSession(t *testing.T) {
	profile := agentprofiles.Profile{ID: "work"}
	binding := productConversationBinding{
		ConversationKey: "project-1:chat-1",
		ResourceID:      "project-1",
	}
	record := ProductConversationRecord{SessionID: "registry-session"}

	if !shouldRebindWorkConversation(profile, binding, record, "open-tab-session", true) {
		t.Fatal("verified open Work tab should repair a drifted tab-specific registry slot")
	}
	if shouldRebindWorkConversation(profile, binding, record, "open-tab-session", false) {
		t.Fatal("unverified session must never change the registry")
	}
	if shouldRebindWorkConversation(profile, productConversationBinding{
		ConversationKey: "project-1",
		ResourceID:      "project-1",
	}, record, "open-tab-session", true) {
		t.Fatal("the permanent Builder slot must remain governed by the project manifest")
	}
	if shouldRebindWorkConversation(agentprofiles.Profile{ID: "example-product"}, binding, record, "open-tab-session", true) {
		t.Fatal("the Work-specific compatibility repair must not affect other products")
	}
}

func TestProductConversationRegistryRejectsUnverifiedHistoryForNewKey(t *testing.T) {
	store, _ := memoryProductConversationStore()
	profile := routeTestProfile("work", true, "")
	binding := productConversationBinding{ConversationKey: "project-1:browser-choice"}

	if _, err := store.switchTo(context.Background(), "user-1", profile, binding, "untrusted-session", false); err == nil || !strings.Contains(err.Error(), "has not been opened") {
		t.Fatalf("unverified session created a product conversation: %v", err)
	}
}

func TestResolveProductConversationBindingEnforcesDeclaredMode(t *testing.T) {
	profile := singletonConversationProfile()
	if _, err := resolveProductConversationBinding(context.Background(), "user-1", profile, "other"); err == nil || !strings.Contains(err.Error(), "singleton") {
		t.Fatalf("singleton accepted an arbitrary key: %v", err)
	}
	profile.Runtime.Conversation = agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject}
	profile.Runtime.Workspace = agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, ProjectsRoot: "Chats/Example Product/projects"}
	if _, err := resolveProductConversationBinding(context.Background(), "user-1", profile, ""); err == nil || !strings.Contains(err.Error(), "conversation_key") {
		t.Fatalf("keyed conversation accepted an empty key: %v", err)
	}
}

func TestProductConversationP0ProjectKeyResolvesAndResumesOneDurableConversation(t *testing.T) {
	profile := routeTestProfile("example-product", true, "")
	profile.Name = "Example Product"
	profile.Runtime.Workspace = agentprofiles.WorkspacePolicy{
		Mode:         agentprofiles.WorkspaceModeProject,
		ProjectsRoot: "Chats/Example Product/projects",
	}
	profile.Runtime.Conversation = agentprofiles.ConversationPolicy{
		Mode:    agentprofiles.ConversationModeKeyed,
		KeyType: agentprofiles.ConversationKeyTypeProject,
	}

	const manifestPath = "_users/user-1/Chats/Example Product/projects/launch-film/product.json"
	projectStore := productProjectStore{
		listPaths: func(_ context.Context, root string) ([]string, bool, error) {
			if root != "_users/user-1/Chats/Example Product/projects" {
				t.Fatalf("projects root=%q", root)
			}
			return []string{
				"_users/user-1/Chats/Example Product/projects/not-a-video/product.json",
				manifestPath,
				manifestPath,
			}, true, nil
		},
		read: func(_ context.Context, path string) (string, bool, error) {
			switch path {
			case manifestPath:
				return `{"schema_version":1,"product":"example-product","id":"launch-2026","title":"Launch Film","description":"Product launch film","session_id":"existing-video-session"}`, true, nil
			case "_users/user-1/Chats/Example Product/projects/not-a-video/product.json":
				return `{"schema_version":1,"product":"other-product","id":"launch-2026","title":"Wrong product","session_id":"wrong-session"}`, true, nil
			default:
				return "", false, nil
			}
		},
	}

	binding, err := resolveProductProjectBindingWithStore(context.Background(), "user-1", profile, "launch-2026", projectStore)
	if err != nil {
		t.Fatal(err)
	}
	if binding.WorkspacePath != "_users/user-1/Chats/Example Product/projects/launch-film" ||
		binding.ManifestPath != manifestPath ||
		binding.AuthoritativeSessionID != "existing-video-session" ||
		binding.Title != "Launch Film" {
		t.Fatalf("unexpected server-owned project binding: %+v", binding)
	}

	registry, _ := memoryProductConversationStore()
	first, err := registry.resolveOrCreate(context.Background(), "user-1", profile, binding, "browser-session-must-not-win")
	if err != nil {
		t.Fatal(err)
	}
	second, err := registry.resolveOrCreate(context.Background(), "user-1", profile, binding, "another-browser-session")
	if err != nil {
		t.Fatal(err)
	}
	if first.ConversationID == "" || first.ConversationID != second.ConversationID || second.SessionID != "existing-video-session" {
		t.Fatalf("project conversation did not resume its durable identity: first=%+v second=%+v", first, second)
	}

	query, err := queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{
		Message:         "Continue the launch film",
		ConversationKey: "launch-2026",
	}, second)
	if err != nil {
		t.Fatal(err)
	}
	if query.SelectedFolder != binding.WorkspacePath || query.AgentProfileConversationKey != binding.ConversationKey || query.SessionTitle != "Launch Film" || query.RestoredConversationPath != "" {
		t.Fatalf("turn did not use only the canonical product binding: %+v", query)
	}
}

// PLAT-746: current project lookup uses a registered Crew folder and identity.
func workProjectBindingPaths(t *testing.T, id string) (string, string) {
	t.Helper()
	t.Setenv("MULTI_USER_MODE", "true")
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	docs := t.TempDir()
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	resetCrewOwnerCache()
	t.Cleanup(resetCrewOwnerCache)
	if err := defaultProjectOwners().Register(projectOwnerRecord{Product: "work", Folder: "site", OwnerID: "user-1", ProjectID: id, Shared: true, Private: true}); err != nil {
		t.Fatal(err)
	}
	private, err := crewroot.PrivateRoot(docs, id)
	if err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{filepath.Join(docs, "Crew", "site"), private} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	return "Crew/site/product.json", "Crew/site/workflow.json"
}

func TestWorkProjectChatKeySharesProjectButNotManifestSession(t *testing.T) {
	profile := routeTestProfile("work", true, "")
	profile.Runtime.Workspace = agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, ProjectsRoot: "Crew"}
	profile.Runtime.Conversation = agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject}
	manifestPath, _ := workProjectBindingPaths(t, "task-1")
	store := productProjectStore{
		listPaths: func(context.Context, string) ([]string, bool, error) { return []string{manifestPath}, true, nil },
		read: func(context.Context, string) (string, bool, error) {
			return `{"schema_version":1,"product":"work","id":"task-1","title":"Task","session_id":"legacy-project-session"}`, true, nil
		},
	}
	binding, err := resolveProductProjectBindingWithStore(context.Background(), "user-1", profile, "task-1:chat-2", store)
	if err != nil {
		t.Fatal(err)
	}
	if binding.ConversationKey != "task-1:chat-2" {
		t.Fatalf("conversation key = %q", binding.ConversationKey)
	}
	if binding.ResourceID != "task-1" || binding.WorkspacePath != "Crew/site" {
		t.Fatalf("sub-chat escaped its project binding: %+v", binding)
	}
	if binding.ManifestPath != "" || binding.AuthoritativeSessionID != "" {
		t.Fatalf("sub-chat incorrectly reused the manifest session: %+v", binding)
	}
}

func TestWorkProjectChatKeyUsesSameFolderWithIndependentSession(t *testing.T) {
	profile := routeTestProfile("work", true, "")
	profile.Runtime.Workspace = agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, ProjectsRoot: "Crew"}
	profile.Runtime.Conversation = agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject}

	manifestPath, _ := workProjectBindingPaths(t, "project-1")
	store := productProjectStore{
		listPaths: func(context.Context, string) ([]string, bool, error) { return []string{manifestPath}, true, nil },
		read: func(context.Context, string) (string, bool, error) {
			return `{"schema_version":1,"product":"work","id":"project-1","title":"Site","session_id":"legacy-project-session"}`, true, nil
		},
	}
	binding, err := resolveProductProjectBindingWithStore(context.Background(), "user-1", profile, "project-1:chat-2", store)
	if err != nil {
		t.Fatal(err)
	}
	if binding.ConversationKey != "project-1:chat-2" || binding.ResourceID != "project-1" || binding.WorkspacePath != "Crew/site" {
		t.Fatalf("unexpected project chat binding: %+v", binding)
	}
	if binding.AuthoritativeSessionID != "" || binding.ManifestPath != "" {
		t.Fatalf("additional chat must not reuse or rewrite the manifest session: %+v", binding)
	}
}

func TestProductProjectBindingRejectsDuplicateProjectIDs(t *testing.T) {
	profile := routeTestProfile("example-product", true, "")
	profile.Runtime.Workspace = agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, ProjectsRoot: "Chats/Example Product/projects"}
	profile.Runtime.Conversation = agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject}
	store := productProjectStore{
		listPaths: func(context.Context, string) ([]string, bool, error) {
			return []string{
				"_users/user-1/Chats/Example Product/projects/one/product.json",
				"_users/user-1/Chats/Example Product/projects/two/product.json",
			}, true, nil
		},
		read: func(context.Context, string) (string, bool, error) {
			return `{"product":"example-product","id":"duplicate","title":"Video","session_id":"session"}`, true, nil
		},
	}

	_, err := resolveProductProjectBindingWithStore(context.Background(), "user-1", profile, "duplicate", store)
	if err == nil || !strings.Contains(err.Error(), "duplicated") {
		t.Fatalf("duplicate project id was not rejected: %v", err)
	}
}

// An isolated schedule's binding resolves to a distinct registry entry (own
// session id, own conversation) from the profile's own singleton
// conversation, on the same workspace — this is what stops a scheduled run
// from ever sharing the tmux-backed CLI process the person's own live chat
// uses. A caller cannot reach this path through the client-facing
// conversation_key parameter (TestResolveProductConversationBindingEnforcesDeclaredMode
// already covers that it is refused there); this is the server-internal
// path the scheduler alone uses.
func TestIsolatedScheduleBindingIsASeparateConversationFromTheProfilesOwn(t *testing.T) {
	store, _ := memoryProductConversationStore()
	profile := singletonConversationProfile()

	ownBinding, err := resolveProductConversationBinding(context.Background(), "user-1", profile, "")
	if err != nil {
		t.Fatal(err)
	}
	own, err := store.resolveOrCreate(context.Background(), "user-1", profile, ownBinding, "")
	if err != nil {
		t.Fatal(err)
	}

	isolatedBinding, err := resolveIsolatedScheduleBinding(context.Background(), "user-1", profile)
	if err != nil {
		t.Fatal(err)
	}
	if isolatedBinding.ConversationKey == ownBinding.ConversationKey {
		t.Fatalf("isolated binding key %q collides with the profile's own %q", isolatedBinding.ConversationKey, ownBinding.ConversationKey)
	}
	if isolatedBinding.WorkspacePath != ownBinding.WorkspacePath {
		t.Fatalf("isolated binding workspace = %q, want the same family workspace %q", isolatedBinding.WorkspacePath, ownBinding.WorkspacePath)
	}
	isolated, err := store.resolveOrCreate(context.Background(), "user-1", profile, isolatedBinding, "")
	if err != nil {
		t.Fatal(err)
	}
	if isolated.SessionID == own.SessionID {
		t.Fatalf("isolated schedule got the same session id %q as the profile's own conversation", own.SessionID)
	}

	// Calling resolveIsolatedScheduleBinding again and reopening must return
	// the SAME session — successive runs of the schedule stay continuous
	// with each other, just never with the profile's own conversation.
	again, err := store.resolveOrCreate(context.Background(), "user-1", profile, isolatedBinding, "")
	if err != nil {
		t.Fatal(err)
	}
	if again.SessionID != isolated.SessionID {
		t.Fatalf("a second isolated run got a different session (%q vs %q); should persist across runs", again.SessionID, isolated.SessionID)
	}
}

// A non-singleton profile has no well-defined "isolated" variant yet
// (keyed/project conversations already have per-resource identity); the
// helper should say so clearly rather than silently doing something wrong.
func TestIsolatedScheduleBindingRejectsNonSingletonProfiles(t *testing.T) {
	profile := singletonConversationProfile()
	profile.Runtime.Conversation = agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject}
	if _, err := resolveIsolatedScheduleBinding(context.Background(), "user-1", profile); err == nil || !strings.Contains(err.Error(), "singleton") {
		t.Fatalf("expected a singleton-only error, got %v", err)
	}
}

func TestIsolatedProjectAutomationBindingKeepsProjectPolicyButOwnsConversation(t *testing.T) {
	base := productConversationBinding{
		ConversationKey: "project-1", WorkspacePath: "Chats/Work/projects/project-1",
		ManifestPath: "Chats/Work/projects/project-1/product.json", ResourceID: "project-1",
		Title: "Prod issue", AuthoritativeSessionID: "crew-session",
		ProjectSelectedServers: []string{"github"},
	}
	schedule, err := isolateProjectAutomationBinding(base, "project-1", "schedule", "daily", "Daily triage")
	if err != nil {
		t.Fatal(err)
	}
	trigger, err := isolateProjectAutomationBinding(base, "project-1", "trigger", "daily", "Incoming issue")
	if err != nil {
		t.Fatal(err)
	}
	if schedule.ConversationKey == base.ConversationKey || schedule.ConversationKey == trigger.ConversationKey {
		t.Fatalf("isolated keys collided: base=%q schedule=%q trigger=%q", base.ConversationKey, schedule.ConversationKey, trigger.ConversationKey)
	}
	if schedule.ManifestPath != "" || schedule.AuthoritativeSessionID != "" {
		t.Fatalf("isolated binding retained the Crew session authority: %+v", schedule)
	}
	if schedule.WorkspacePath != base.WorkspacePath || schedule.ResourceID != base.ResourceID || strings.Join(schedule.ProjectSelectedServers, ",") != "github" {
		t.Fatalf("isolated binding lost project policy: %+v", schedule)
	}
}

// A caller binding or webhook keeps one continuing conversation: two runs of
// the same trigger resume the same session, and another trigger gets its own.
func TestTriggerRunsShareTheTriggersOwnConversation(t *testing.T) {
	store, _ := memoryProductConversationStore()
	ctx := context.Background()
	profile := singletonConversationProfile()
	base := productConversationBinding{
		ConversationKey: "project-1", WorkspacePath: "Chats/Work/projects/project-1",
		ManifestPath: "Chats/Work/projects/project-1/product.json", ResourceID: "project-1",
		Title: "Crew", AuthoritativeSessionID: "crew-session",
	}
	firstBinding, err := isolateProjectAutomationBinding(base, "project-1", "trigger", "trig-1", "t")
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.resolveOrCreate(ctx, "user-1", profile, firstBinding, "")
	if err != nil {
		t.Fatal(err)
	}
	secondBinding, err := isolateProjectAutomationBinding(base, "project-1", "trigger", "trig-1", "t")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.resolveOrCreate(ctx, "user-1", profile, secondBinding, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.SessionID != second.SessionID {
		t.Fatalf("two runs of one trigger got sessions %q and %q; want one continuing conversation", first.SessionID, second.SessionID)
	}
	otherBinding, err := isolateProjectAutomationBinding(base, "project-1", "trigger", "trig-2", "t")
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.resolveOrCreate(ctx, "user-1", profile, otherBinding, "")
	if err != nil {
		t.Fatal(err)
	}
	if other.SessionID == first.SessionID {
		t.Fatalf("two different triggers shared session %q", first.SessionID)
	}
}
