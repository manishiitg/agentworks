package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
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
	profile := routeTestProfile("dominion", true, "")
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

func TestProductConversationRegistrySeparatesUsersAndKeys(t *testing.T) {
	store, files := memoryProductConversationStore()
	profile := singletonConversationProfile()
	binding, _ := resolveProductConversationBinding(context.Background(), "user-1", profile, "main")
	userOne, err := store.resolveOrCreate(context.Background(), "user-1", profile, binding, "")
	if err != nil {
		t.Fatal(err)
	}
	userTwo, err := store.resolveOrCreate(context.Background(), "user-2", profile, binding, "")
	if err != nil {
		t.Fatal(err)
	}
	if userOne.ConversationID == userTwo.ConversationID || userOne.SessionID == userTwo.SessionID {
		t.Fatalf("users shared a product conversation: one=%+v two=%+v", userOne, userTwo)
	}
	if len(files) != 2 {
		t.Fatalf("registry files=%d, want one per user", len(files))
	}
}

func TestProductConversationRegistryRemovesCompleteProjectSlot(t *testing.T) {
	store, _ := memoryProductConversationStore()
	profile := singletonConversationProfile()
	binding, _ := resolveProductConversationBinding(context.Background(), "user-1", profile, "main")
	first, err := store.resolveOrCreate(context.Background(), "user-1", profile, binding, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.rotate(context.Background(), "user-1", profile, binding)
	if err != nil {
		t.Fatal(err)
	}
	removed, err := store.removeSlot(context.Background(), "user-1", profile, binding)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 2 || removed[0].SessionID != second.SessionID || removed[1].SessionID != first.SessionID {
		t.Fatalf("removed records=%+v, want live then previous", removed)
	}
	if current, ok, previous, err := store.history(context.Background(), "user-1", profile, binding); err != nil || ok || current.SessionID != "" || len(previous) != 0 {
		t.Fatalf("slot survived deletion: current=%+v ok=%v previous=%+v err=%v", current, ok, previous, err)
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
	if shouldRebindWorkConversation(agentprofiles.Profile{ID: "video-studio"}, binding, record, "open-tab-session", true) {
		t.Fatal("the Work-specific compatibility repair must not affect other products")
	}
}

func TestProductConversationRegistryBindsVerifiedHistoryToNewProjectChatKey(t *testing.T) {
	store, _ := memoryProductConversationStore()
	profile := routeTestProfile("work", true, "")
	binding := productConversationBinding{
		ConversationKey: "project-1:historical-session",
		WorkspacePath:   "_users/user-1/Chats/Work/projects/project-1",
		ResourceID:      "project-1",
		Title:           "Project 1",
	}

	restored, err := store.switchTo(context.Background(), "user-1", profile, binding, "historical-session", true)
	if err != nil {
		t.Fatal(err)
	}
	if restored.SessionID != "historical-session" || restored.ConversationKey != binding.ConversationKey || restored.ConversationID == "" {
		t.Fatalf("verified historical session was not bound directly: %+v", restored)
	}
	resolved, err := store.resolveOrCreate(context.Background(), "user-1", profile, binding, "different-session")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.SessionID != restored.SessionID || resolved.ConversationID != restored.ConversationID {
		t.Fatalf("later resolve replaced restored identity: restored=%+v resolved=%+v", restored, resolved)
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

func TestProductConversationRotationChangesProjectSessionAndKeepsNewBinding(t *testing.T) {
	store, files := memoryProductConversationStore()
	profile := routeTestProfile("video-studio", true, "")
	profile.Runtime.Conversation = agentprofiles.ConversationPolicy{
		Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject,
	}
	const manifestPath = "_users/user-1/Chats/Video Studio/projects/launch/product.json"
	files[manifestPath] = `{"product":"video-studio","id":"launch","title":"Launch","session_id":"existing-session","unrelated":"preserved"}`
	binding := productConversationBinding{
		ConversationKey: "launch", WorkspacePath: "_users/user-1/Chats/Video Studio/projects/launch",
		ManifestPath: manifestPath, ResourceID: "launch", Title: "Launch", AuthoritativeSessionID: "existing-session",
	}
	original, err := store.resolveOrCreate(context.Background(), "user-1", profile, binding, "")
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := store.rotate(context.Background(), "user-1", profile, binding)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.ConversationID == original.ConversationID || rotated.SessionID == original.SessionID {
		t.Fatalf("rotation reused the old durable identity: original=%+v rotated=%+v", original, rotated)
	}
	var manifest map[string]interface{}
	if err := json.Unmarshal([]byte(files[manifestPath]), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest["session_id"] != rotated.SessionID || manifest["unrelated"] != "preserved" {
		t.Fatalf("manifest not safely updated: %+v", manifest)
	}

	// A normal later resolve reads the rotated project binding and must not
	// revive the conversation it replaced.
	binding.AuthoritativeSessionID = rotated.SessionID
	resolved, err := store.resolveOrCreate(context.Background(), "user-1", profile, binding, "")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ConversationID != rotated.ConversationID || resolved.SessionID != rotated.SessionID {
		t.Fatalf("resolve revived prior conversation: got=%+v want=%+v", resolved, rotated)
	}
}

func TestResolveProductConversationBindingEnforcesDeclaredMode(t *testing.T) {
	profile := singletonConversationProfile()
	if _, err := resolveProductConversationBinding(context.Background(), "user-1", profile, "other"); err == nil || !strings.Contains(err.Error(), "singleton") {
		t.Fatalf("singleton accepted an arbitrary key: %v", err)
	}
	profile.Runtime.Conversation = agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject}
	profile.Runtime.Workspace = agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, ProjectsRoot: "Chats/Video Studio/projects"}
	if _, err := resolveProductConversationBinding(context.Background(), "user-1", profile, ""); err == nil || !strings.Contains(err.Error(), "conversation_key") {
		t.Fatalf("keyed conversation accepted an empty key: %v", err)
	}
}

func TestProductConversationP0ProjectKeyResolvesAndResumesOneDurableConversation(t *testing.T) {
	profile := routeTestProfile("video-studio", true, "")
	profile.Name = "Video Studio"
	profile.Runtime.Workspace = agentprofiles.WorkspacePolicy{
		Mode:         agentprofiles.WorkspaceModeProject,
		ProjectsRoot: "Chats/Video Studio/projects",
	}
	profile.Runtime.Conversation = agentprofiles.ConversationPolicy{
		Mode:    agentprofiles.ConversationModeKeyed,
		KeyType: agentprofiles.ConversationKeyTypeProject,
	}

	const manifestPath = "_users/user-1/Chats/Video Studio/projects/launch-film/product.json"
	projectStore := productProjectStore{
		listPaths: func(_ context.Context, root string) ([]string, bool, error) {
			if root != "_users/user-1/Chats/Video Studio/projects" {
				t.Fatalf("projects root=%q", root)
			}
			return []string{
				"_users/user-1/Chats/Video Studio/projects/not-a-video/product.json",
				manifestPath,
				manifestPath,
			}, true, nil
		},
		read: func(_ context.Context, path string) (string, bool, error) {
			switch path {
			case manifestPath:
				return `{"schema_version":1,"product":"video-studio","id":"launch-2026","title":"Launch Film","description":"Product launch film","session_id":"existing-video-session"}`, true, nil
			case "_users/user-1/Chats/Video Studio/projects/not-a-video/product.json":
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
	if binding.WorkspacePath != "_users/user-1/Chats/Video Studio/projects/launch-film" ||
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

func TestWorkProjectBindingUsesCreatedProjectFolder(t *testing.T) {
	profile := routeTestProfile("work", true, "")
	profile.Runtime.Workspace = agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, ProjectsRoot: "Chats/Work/projects"}
	profile.Runtime.Conversation = agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject}

	manifestPath := "_users/user-1/Chats/Work/projects/site/product.json"
	runtimePath := "_users/user-1/Chats/Work/projects/site/workflow.json"
	store := productProjectStore{
		listPaths: func(context.Context, string) ([]string, bool, error) {
			return []string{manifestPath}, true, nil
		},
		read: func(_ context.Context, path string) (string, bool, error) {
			if path == runtimePath {
				return `{"schema_version":1,"id":"task-1","label":"Task","workflow_context_paths":["Workflow/reference"],"capabilities":{"llm_config":{"schema_version":2,"mode":"explicit","builder_llm":{"provider":"muse-cli","model_id":"muse-spark-1.3-contributor"}}}}`, true, nil
			}
			return `{"schema_version":1,"product":"work","id":"task-1","title":"Task","session_id":"work:task-1"}`, true, nil
		},
	}
	binding, err := resolveProductProjectBindingWithStore(context.Background(), "user-1", profile, "task-1", store)
	if err != nil {
		t.Fatal(err)
	}
	if binding.WorkspacePath != "_users/user-1/Chats/Work/projects/site" {
		t.Fatalf("unexpected durable binding: %+v", binding)
	}
	if binding.ProjectLLMConfig == nil || binding.ProjectLLMConfig.BuilderLLM == nil || binding.ProjectLLMConfig.BuilderLLM.Provider != "muse-cli" {
		t.Fatalf("project LLM configuration was not loaded: %+v", binding.ProjectLLMConfig)
	}
	if got := strings.Join(binding.ProjectWorkflowContextPaths, ","); got != "Workflow/reference" {
		t.Fatalf("project workflow references were not loaded: %v", binding.ProjectWorkflowContextPaths)
	}
}

func TestWorkProjectChatKeySharesProjectButNotManifestSession(t *testing.T) {
	profile := routeTestProfile("work", true, "")
	profile.Runtime.Workspace = agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, ProjectsRoot: "Chats/Work/projects"}
	profile.Runtime.Conversation = agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject}
	manifestPath := "_users/user-1/Chats/Work/projects/site/product.json"
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
	if binding.ResourceID != "task-1" || binding.WorkspacePath != "_users/user-1/Chats/Work/projects/site" {
		t.Fatalf("sub-chat escaped its project binding: %+v", binding)
	}
	if binding.ManifestPath != "" || binding.AuthoritativeSessionID != "" {
		t.Fatalf("sub-chat incorrectly reused the manifest session: %+v", binding)
	}
}

func TestWorkProjectChatKeyUsesSameFolderWithIndependentSession(t *testing.T) {
	profile := routeTestProfile("work", true, "")
	profile.Runtime.Workspace = agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, ProjectsRoot: "Chats/Work/projects"}
	profile.Runtime.Conversation = agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject}

	manifestPath := "_users/user-1/Chats/Work/projects/site/product.json"
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
	if binding.ConversationKey != "project-1:chat-2" || binding.ResourceID != "project-1" || binding.WorkspacePath != "_users/user-1/Chats/Work/projects/site" {
		t.Fatalf("unexpected project chat binding: %+v", binding)
	}
	if binding.AuthoritativeSessionID != "" || binding.ManifestPath != "" {
		t.Fatalf("additional chat must not reuse or rewrite the manifest session: %+v", binding)
	}
}

func TestProductProjectBindingRejectsDuplicateProjectIDs(t *testing.T) {
	profile := routeTestProfile("video-studio", true, "")
	profile.Runtime.Workspace = agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, ProjectsRoot: "Chats/Video Studio/projects"}
	profile.Runtime.Conversation = agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject}
	store := productProjectStore{
		listPaths: func(context.Context, string) ([]string, bool, error) {
			return []string{
				"_users/user-1/Chats/Video Studio/projects/one/product.json",
				"_users/user-1/Chats/Video Studio/projects/two/product.json",
			}, true, nil
		},
		read: func(context.Context, string) (string, bool, error) {
			return `{"product":"video-studio","id":"duplicate","title":"Video","session_id":"session"}`, true, nil
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

// The crew's "Native agent tools" switch lives in workflow.json capabilities
// and must reach the binding that resolveAgentProfileForQuery reads.
func TestWorkProjectBindingLoadsNativeAgentTools(t *testing.T) {
	profile := routeTestProfile("work", true, "")
	profile.Runtime.Workspace = agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, ProjectsRoot: "Chats/Work/projects"}
	profile.Runtime.Conversation = agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject}
	manifestPath := "_users/user-1/Chats/Work/projects/site/product.json"
	runtimePath := "_users/user-1/Chats/Work/projects/site/workflow.json"
	for _, tc := range []struct {
		capabilities string
		want         bool
	}{
		{`{"native_agent_tools":true}`, true},
		{`{}`, true}, // on by default
		{`{"native_agent_tools":false}`, false},
	} {
		store := productProjectStore{
			listPaths: func(context.Context, string) ([]string, bool, error) { return []string{manifestPath}, true, nil },
			read: func(_ context.Context, path string) (string, bool, error) {
				if path == runtimePath {
					return `{"schema_version":1,"id":"task-1","capabilities":` + tc.capabilities + `}`, true, nil
				}
				return `{"schema_version":1,"product":"work","id":"task-1","title":"Task","session_id":"work:task-1"}`, true, nil
			},
		}
		binding, err := resolveProductProjectBindingWithStore(context.Background(), "user-1", profile, "task-1", store)
		if err != nil {
			t.Fatal(err)
		}
		if binding.ProjectNativeAgentTools != tc.want {
			t.Fatalf("capabilities %s: ProjectNativeAgentTools = %v, want %v", tc.capabilities, binding.ProjectNativeAgentTools, tc.want)
		}
	}
}

// A Code has no "Native agent tools" switch: they are always on, even for a
// Code whose workflow.json still says off from before the switch was removed.
func TestCodeProjectBindingAlwaysHasNativeAgentTools(t *testing.T) {
	profile := routeTestProfile("code", true, "")
	profile.Runtime.Workspace = agentprofiles.WorkspacePolicy{Mode: agentprofiles.WorkspaceModeProject, ProjectsRoot: "Chats/Code/projects"}
	profile.Runtime.Conversation = agentprofiles.ConversationPolicy{Mode: agentprofiles.ConversationModeKeyed, KeyType: agentprofiles.ConversationKeyTypeProject}
	manifestPath := "_users/user-1/Chats/Code/projects/app/product.json"
	runtimePath := "_users/user-1/Chats/Code/projects/app/workflow.json"
	for _, capabilities := range []string{`{}`, `{"native_agent_tools":true}`, `{"native_agent_tools":false}`} {
		store := productProjectStore{
			listPaths: func(context.Context, string) ([]string, bool, error) { return []string{manifestPath}, true, nil },
			read: func(_ context.Context, path string) (string, bool, error) {
				if path == runtimePath {
					return `{"schema_version":1,"id":"task-1","capabilities":` + capabilities + `}`, true, nil
				}
				return `{"schema_version":1,"product":"code","id":"task-1","title":"App","session_id":"code:project:task-1"}`, true, nil
			},
		}
		binding, err := resolveProductProjectBindingWithStore(context.Background(), "user-1", profile, "task-1", store)
		if err != nil {
			t.Fatal(err)
		}
		if !binding.ProjectNativeAgentTools {
			t.Fatalf("capabilities %s: a Code lost native agent tools", capabilities)
		}
	}
}
