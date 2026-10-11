package videoproduct

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

func TestIntegratedProjectCreatesEveryFolderGuardBaseline(t *testing.T) {
	folders := integratedProjectFolders()
	for _, required := range []string{"builder", "soul", "planning", "db"} {
		if !slices.Contains(folders, required) {
			t.Fatalf("integrated project folders omit Folder Guard baseline %q: %v", required, folders)
		}
	}
}

func TestVideoStudioProjectsHaveOneHiddenDefaultExecutionGroup(t *testing.T) {
	manifest := videoStudioDefaultVariablesManifest()
	if len(manifest.Groups) != 1 {
		t.Fatalf("default Video Studio manifest has %d groups, want one", len(manifest.Groups))
	}
	group := manifest.Groups[0]
	if group.Name != "default" || !group.Enabled {
		t.Fatalf("default execution group = %#v, want enabled default", group)
	}
	if len(group.Values) != 0 {
		t.Fatalf("default execution group unexpectedly contains user variables: %#v", group.Values)
	}
}

func TestProfileWorkspaceRootMatchesSessionFolderGuard(t *testing.T) {
	const sessionID = "video-studio:test"
	root := profileWorkspaceRoot("default", "Chats/Video Studio/projects/demo")
	if root != "_users/default/Chats/Video Studio/projects/demo" {
		t.Fatalf("unexpected scoped root: %q", root)
	}

	workspace.SetSessionFolderGuard(sessionID, []string{root}, []string{root})
	defer workspace.ClearSessionShellConfig(sessionID)
	client := workspace.NewClient("http://unused", workspace.WithExtraEnv(map[string]string{"MCP_SESSION_ID": sessionID}))
	ctx := context.WithValue(context.Background(), common.ChatSessionIDKey, sessionID)

	qaPath := filepath.ToSlash(filepath.Join(root, "work/qa/final/quality-report.json"))
	if err := client.ValidatePathWithContext(ctx, qaPath, false); err != nil {
		t.Fatalf("profile tool path should match its session guard: %v", err)
	}
	if err := client.ValidatePathWithContext(ctx, "Chats/Video Studio/projects/demo/work/qa/final/quality-report.json", false); err == nil {
		t.Fatal("unscoped profile path unexpectedly bypassed the user-scoped session guard")
	}
}

func TestProfileWorkspaceLocalPathUsesScopedWorkspaceDocsRoot(t *testing.T) {
	root := t.TempDir()
	previous := os.Getenv("WORKSPACE_DOCS_PATH")
	t.Cleanup(func() { _ = os.Setenv("WORKSPACE_DOCS_PATH", previous) })
	if err := os.Setenv("WORKSPACE_DOCS_PATH", root); err != nil {
		t.Fatal(err)
	}
	got, err := profileWorkspaceLocalPath("user-1", "Chats/Video Studio/projects/demo")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "_users", "user-1", "Chats", "Video Studio", "projects", "demo")
	if got != want {
		t.Fatalf("local profile workspace = %q, want %q", got, want)
	}
}

func TestProfileWorkspaceRootDoesNotDoublePrefixCanonicalPath(t *testing.T) {
	got := profileWorkspaceRoot("default", "_users/default/Chats/Video Studio/projects/demo")
	if got != "_users/default/Chats/Video Studio/projects/demo" {
		t.Fatalf("unexpected canonical root: %q", got)
	}
}

func TestGeneratedVideoStudioPlanRefreshesWhenCritiqueGatesAreMissing(t *testing.T) {
	plan, err := json.Marshal(planForAll(pipelineRegistry))
	if err != nil {
		t.Fatal(err)
	}
	currentPlan := string(plan)
	if shouldRefreshGeneratedVideoStudioPlan(currentPlan) {
		t.Fatal("the current critique-gated product plan should not refresh repeatedly")
	}
	prettyCurrentPlan := strings.ReplaceAll(currentPlan, `"route_id":"infographic"`, `"route_id": "infographic"`)
	if shouldRefreshGeneratedVideoStudioPlan(prettyCurrentPlan) {
		t.Fatal("the current pretty-printed product plan should not refresh repeatedly")
	}

	preCritiquePlan := `{"routes":[{"route_id": "infographic"}],"steps":[{"id":"infographic-research"}]}`
	if !shouldRefreshGeneratedVideoStudioPlan(preCritiquePlan) {
		t.Fatal("a pre-critique Video Studio infographic plan should upgrade")
	}

	userPlan := `{"steps":[{"id":"my-video-review","title":"Video review"}]}`
	if shouldRefreshGeneratedVideoStudioPlan(userPlan) {
		t.Fatal("an unrelated user-authored video plan must be preserved")
	}

	preCharacterStep := `{"steps":[{"id":"route","routes":[{"route_id": "infographic"}]},` +
		`{"id":"infographic-research"},{"id":"infographic-creative-critique"},` +
		`{"id":"infographic-render-critique"}]}`
	if !shouldRefreshGeneratedVideoStudioPlan(preCharacterStep) {
		t.Fatal("a Video Studio plan without the short-form character gate should upgrade")
	}

	preAudioDirection := strings.ReplaceAll(currentPlan, `"shortform-look-sound"`, `"legacy-shortform-look-sound"`)
	if !shouldRefreshGeneratedVideoStudioPlan(preAudioDirection) {
		t.Fatal("a Video Studio plan without the short-form look/sound stage should upgrade")
	}
	preNarration := strings.ReplaceAll(currentPlan, `"shortform-narration"`, `"legacy-shortform-narration"`)
	if !shouldRefreshGeneratedVideoStudioPlan(preNarration) {
		t.Fatal("a Video Studio plan without the short-form narration stage should upgrade")
	}

	// The case marker-matching cannot see: every current identifier is present,
	// so no fingerprint fires, but the stored plan is one the platform refuses
	// to load. This is what a project seeded between the old orchestrator landing
	// and its next_step_id fix held, and workflow execution failed with
	// "plan.json uses an invalid or legacy format" rather than running.
	invalidButCurrent := `{"steps":[{"type":"routing","id":"route","routes":[{"route_id": "infographic","next_step_id":"infographic-preproduction"}]},` +
		`{"type":"todo_task","id":"infographic-preproduction","title":"Brief","description":"d",` +
		`"predefined_routes":[{"route_id":"infographic-research","route_name":"r","condition":"c",` +
		`"sub_agent_step":{"type":"agent","id":"infographic-research","title":"t","description":"d",` +
		`"items":[{"id":"i","type":"user_message","message":"m"}]}}]},` +
		`{"type":"agent","id":"infographic-render-critique","title":"t","description":"d",` +
		`"items":[{"id":"i","type":"user_message","message":"m"}],"next_step_id":"end"}]}`
	if planLoadsOnThisPlatform(invalidButCurrent) {
		t.Fatal("fixture is meant to be a plan the platform rejects; it now loads, so this asserts nothing")
	}
	if !shouldRefreshGeneratedVideoStudioPlan(invalidButCurrent) {
		t.Fatal("a stored plan the platform will not load must be re-seeded, whatever markers it carries")
	}
}
