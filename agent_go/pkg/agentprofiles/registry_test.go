package agentprofiles

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func validProfile() Profile {
	return Profile{
		ID:                   "video-studio",
		Name:                 "Video Studio",
		Version:              1,
		SystemPromptTemplate: "You are working on {{.ProjectTitle}} at {{.LocalDateTime}}.",
		Skills:               []string{"video-creation", "video-quality"},
		Tools:                []ToolBinding{{ID: "video.show-video", Config: json.RawMessage(`{"strict":true}`)}},
		Runtime: RuntimePolicy{
			Transport: "auto",
			Capabilities: RuntimeCapabilities{
				LiveInput:   CapabilityRequired,
				RawTerminal: CapabilityDisabled,
				WarmSession: CapabilityPreferred,
			},
		},
		BuiltIn: true,
	}
}

func TestEffectiveScopeDefaultsEmptyToProject(t *testing.T) {
	profile := validProfile()
	if profile.Scope != "" {
		t.Fatalf("expected validProfile() to declare no scope, got %q", profile.Scope)
	}
	if got := profile.EffectiveScope(); got != ProfileScopeProject {
		t.Fatalf("expected empty scope to default to %q, got %q", ProfileScopeProject, got)
	}
	profile.Scope = ProfileScopeProject
	if got := profile.EffectiveScope(); got != ProfileScopeProject {
		t.Fatalf("expected explicit %q to round-trip, got %q", ProfileScopeProject, got)
	}
	profile.Scope = ProfileScopeGlobal
	if got := profile.EffectiveScope(); got != ProfileScopeGlobal {
		t.Fatalf("expected explicit %q to round-trip, got %q", ProfileScopeGlobal, got)
	}
}

func TestValidateRejectsUnknownScope(t *testing.T) {
	profile := validProfile()
	profile.Scope = "workspace"
	if err := Validate(profile); err == nil || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("expected invalid scope to fail validation, got %v", err)
	}
}

func TestValidateAcceptsGlobalScope(t *testing.T) {
	profile := validProfile()
	profile.Scope = ProfileScopeGlobal
	if err := Validate(profile); err != nil {
		t.Fatalf("expected global scope to validate cleanly, got %v", err)
	}
}

func TestValidateRejectsStructuredLiveInput(t *testing.T) {
	profile := validProfile()
	profile.Runtime.Transport = "structured"
	if err := Validate(profile); err == nil || !strings.Contains(err.Error(), "live_input") {
		t.Fatalf("expected structured/live-input contradiction, got %v", err)
	}
}

func TestValidateRequiresCompleteRuntimeModelBinding(t *testing.T) {
	profile := validProfile()
	profile.Runtime.Provider = "claude-code"
	if err := Validate(profile); err == nil || !strings.Contains(err.Error(), "model_id") {
		t.Fatalf("expected incomplete runtime model binding to fail, got %v", err)
	}
	profile.Runtime.ModelID = "claude-sonnet-5-5"
	if err := Validate(profile); err != nil {
		t.Fatalf("complete runtime model binding failed: %v", err)
	}
}

func TestValidateProductConversationPolicies(t *testing.T) {
	tests := []struct {
		name         string
		conversation ConversationPolicy
		workspace    WorkspacePolicy
		wantError    string
	}{
		{
			name:         "singleton",
			conversation: ConversationPolicy{Mode: ConversationModeSingleton},
			workspace:    WorkspacePolicy{Mode: WorkspaceModeFixed, Root: "Chats"},
		},
		{
			name:         "project keyed",
			conversation: ConversationPolicy{Mode: ConversationModeKeyed, KeyType: ConversationKeyTypeProject},
			workspace:    WorkspacePolicy{Mode: WorkspaceModeProject, ProjectsRoot: "Chats/Video Studio/projects"},
		},
		{
			name:         "singleton missing root",
			conversation: ConversationPolicy{Mode: ConversationModeSingleton},
			workspace:    WorkspacePolicy{Mode: WorkspaceModeFixed},
			wantError:    "workspace.root",
		},
		{
			name:         "keyed missing key type",
			conversation: ConversationPolicy{Mode: ConversationModeKeyed},
			workspace:    WorkspacePolicy{Mode: WorkspaceModeProject, ProjectsRoot: "Chats/projects"},
			wantError:    "key_type",
		},
		{
			name:         "keyed with fixed workspace",
			conversation: ConversationPolicy{Mode: ConversationModeKeyed, KeyType: ConversationKeyTypeProject},
			workspace:    WorkspacePolicy{Mode: WorkspaceModeFixed, Root: "Chats"},
			wantError:    "workspace.mode",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := validProfile()
			profile.Runtime.Conversation = tt.conversation
			profile.Runtime.Workspace = tt.workspace
			err := Validate(profile)
			if tt.wantError == "" && err != nil {
				t.Fatalf("valid policy rejected: %v", err)
			}
			if tt.wantError != "" && (err == nil || !strings.Contains(err.Error(), tt.wantError)) {
				t.Fatalf("error=%v, want substring %q", err, tt.wantError)
			}
		})
	}
}

// Hybrid keeps the bridge shell (Codex reaches product APIs only through it),
// and native_shell is rejected outright: coding agents never get a native
// shell (2026-09-24), so that transport would leave no route to product APIs.
// See docs/design/product_api_transport_for_coding_agents.md.
func TestValidateRejectsBothAPITransportsAtOnce(t *testing.T) {
	profile := validProfile()
	profile.ToolPolicy = ToolPolicy{Mode: ToolPolicyModeAllowlist, Enabled: []string{"execute_shell_command"}}
	profile.Runtime.AgentTools.Mode = "hybrid"

	if err := Validate(profile); err != nil {
		t.Fatalf("native tools with the bridge shell is the supported default; it must validate: %v", err)
	}

	profile.Runtime.APITransport.Mode = "native_shell"
	if err := Validate(profile); err == nil || !strings.Contains(err.Error(), "native_shell is unsupported") {
		t.Fatalf("expected native_shell to be rejected, got %v", err)
	}
	profile.ToolPolicy.Enabled = []string{"show_video"}
	if err := Validate(profile); err == nil {
		t.Fatal("native_shell must be rejected even without the bridge shell")
	}
}

func TestValidateAcceptsAToolWithNoPresentationDeclared(t *testing.T) {
	profile := validProfile()
	if profile.Tools[0].Presentation != nil {
		t.Fatal("fixture already declares a presentation; test needs the undeclared case")
	}
	if err := Validate(profile); err != nil {
		t.Fatalf("a tool that presents nothing must not be required to declare a kind: %v", err)
	}
}

func TestValidateRejectsAMalformedPresentationKind(t *testing.T) {
	for _, kind := range []string{"", "Media.Video", "media video", "media_video", "media."} {
		profile := validProfile()
		profile.Tools[0].Presentation = &PresentationBinding{Kind: kind}
		if err := Validate(profile); err == nil || !strings.Contains(err.Error(), "presentation kind") {
			t.Errorf("kind %q should have been rejected, got %v", kind, err)
		}
	}
}

func TestValidateAcceptsADottedLowercasePresentationKind(t *testing.T) {
	profile := validProfile()
	profile.Tools[0].Presentation = &PresentationBinding{Kind: "media.video", Activity: &PresentationActivityBinding{Label: "Video ready", Destination: "Videos panel", Detail: "Ready to review"}}
	if err := Validate(profile); err != nil {
		t.Fatalf("valid presentation kind was rejected: %v", err)
	}
}

func TestValidateRequiresTranscriptActivityForAPresentation(t *testing.T) {
	profile := validProfile()
	profile.Tools[0].Presentation = &PresentationBinding{Kind: "media.video"}
	if err := Validate(profile); err == nil || !strings.Contains(err.Error(), "activity.label") {
		t.Fatalf("presentation without its YAML activity should be rejected, got %v", err)
	}
}

// The clone path used by every profile lookup (Registry.Profile) must not
// alias the original's Presentation pointer. Two profile lookups sharing one
// *PresentationBinding would let a caller that mutates one corrupt the other
// — the same aliasing risk the existing Config clone already guards against.
func TestClonedProfileDoesNotAliasThePresentationBinding(t *testing.T) {
	profile := validProfile()
	profile.Tools[0].Presentation = &PresentationBinding{Kind: "media.video", Activity: &PresentationActivityBinding{Label: "Video ready", Destination: "Videos panel", Detail: "Ready to review"}}
	cloned := cloneProfile(profile)
	if cloned.Tools[0].Presentation == profile.Tools[0].Presentation {
		t.Fatal("clone shares the original's *PresentationBinding pointer")
	}
	cloned.Tools[0].Presentation.Kind = "media.image"
	if profile.Tools[0].Presentation.Kind != "media.video" {
		t.Fatal("mutating the clone's Presentation changed the original")
	}
	cloned.Tools[0].Presentation.Activity.Label = "Image ready"
	if profile.Tools[0].Presentation.Activity.Label != "Video ready" {
		t.Fatal("mutating the clone's Presentation activity changed the original")
	}
}

func TestRenderPromptUsesAllowListedContext(t *testing.T) {
	profile := validProfile()
	rendered, err := RenderPrompt(profile, PromptContext{ProjectTitle: "Launch Film", LocalDateTime: "Friday"})
	if err != nil {
		t.Fatal(err)
	}
	if rendered != "You are working on Launch Film at Friday." {
		t.Fatalf("unexpected rendered prompt: %q", rendered)
	}
}

func TestRegistryVersionsAreImmutableAndOwnerScoped(t *testing.T) {
	registry := NewRegistry()
	builtin := validProfile()
	if err := registry.RegisterProfile(builtin); err != nil {
		t.Fatal(err)
	}
	if err := registry.RegisterProfile(builtin); err == nil {
		t.Fatal("duplicate immutable version was accepted")
	}

	owned := validProfile()
	owned.ID = "my-agent"
	owned.BuiltIn = false
	owned.OwnerID = "user-1"
	if err := registry.RegisterProfile(owned); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Resolve("my-agent", 1, "user-2"); err == nil {
		t.Fatal("another user's profile was exposed")
	}
	if got, err := registry.Resolve("my-agent", 0, "user-1"); err != nil || got.Version != 1 {
		t.Fatalf("owner could not resolve latest profile: profile=%+v err=%v", got, err)
	}
}

func TestToolFactoriesAreRegisteredCodeNotProfilePayloads(t *testing.T) {
	registry := NewRegistry()
	if err := registry.RegisterToolFactory("video.show-video", func(runtime ToolRuntimeContext, config json.RawMessage) (ToolSpec, error) {
		return ToolSpec{
			Name:        "show_video",
			Description: runtime.WorkspacePath,
			Execute: func(context.Context, map[string]interface{}) (string, error) {
				return string(config), nil
			},
		}, nil
	}); err != nil {
		t.Fatal(err)
	}
	tool, err := registry.BuildTool(ToolBinding{ID: "video.show-video", Config: json.RawMessage(`{"strict":true}`)}, ToolRuntimeContext{WorkspacePath: "Workflow/video"})
	if err != nil {
		t.Fatal(err)
	}
	if tool.Name != "show_video" || tool.Description != "Workflow/video" {
		t.Fatalf("unexpected tool: %+v", tool)
	}
	result, err := tool.Execute(context.Background(), nil)
	if err != nil || result != `{"strict":true}` {
		t.Fatalf("unexpected tool result %q: %v", result, err)
	}
}

func TestProfileInitializerReceivesTrustedRuntime(t *testing.T) {
	registry := NewRegistry()
	var received RuntimeContext
	if err := registry.RegisterInitializer("video-studio", func(_ context.Context, runtime RuntimeContext) error {
		received = runtime
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := RuntimeContext{UserID: "user-1", SessionID: "session-1", WorkspacePath: "Chats/Video Studio/projects/demo"}
	if err := registry.Initialize(context.Background(), "video-studio", want); err != nil {
		t.Fatal(err)
	}
	if received != want {
		t.Fatalf("initializer runtime = %+v, want %+v", received, want)
	}
	if err := registry.Initialize(context.Background(), "profile-without-initializer", want); err != nil {
		t.Fatalf("missing initializer should be a no-op: %v", err)
	}
}
