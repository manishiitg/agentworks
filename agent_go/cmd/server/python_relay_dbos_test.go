package server

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/agentprofiles"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
	workspacehandlers "github.com/manishiitg/coding-agent-loop/workspace/handlers"
	"github.com/spf13/viper"
)

func TestDBOSRelayManifestIsExplicit(t *testing.T) {
	m := NewWorkflowManifest("Durable")
	m.Kind, m.RelayRuntime = "relay", "python"
	if isDBOSRelay(m) {
		t.Fatal("existing Relay silently opted into replay")
	}
	m.RelayDurability = "dbos"
	if err := ValidateManifest(m); err != nil {
		t.Fatal(err)
	}
	if !isDBOSRelay(m) {
		t.Fatal("DBOS setting ignored")
	}
	m.RelayRuntime = ""
	if err := ValidateManifest(m); err == nil {
		t.Fatal("legacy graph accepted Python recovery")
	}
}

// Exercise publishing, the actual workspace/sandbox handlers, server admission,
// the process supervisor and the real DBOS database without model fixtures.
func TestDBOSRelayServerRecoversPublishedCheckpoint(t *testing.T) {
	testDBOSRelayServerRecovery(t, false)
}

func TestDBOSNativeRelayServerRecoversPublishedCheckpoint(t *testing.T) {
	testDBOSRelayServerRecovery(t, true)
}

func testDBOSRelayServerRecovery(t *testing.T, native bool) {
	if os.Getenv("RELAY_DBOS_PYTHON") == "" {
		t.Skip("set RELAY_DBOS_PYTHON for the real workspace recovery check")
	}
	docs := t.TempDir()
	docs, canonicalErr := filepath.EvalSymlinks(docs)
	if canonicalErr != nil {
		t.Fatal(canonicalErr)
	}
	t.Setenv("AGENTWORKS_STATE_ROOT", t.TempDir())
	t.Setenv("WORKSPACE_DOCS_PATH", docs)
	t.Setenv("LOCAL_MODE", "true")
	t.Setenv("MULTI_USER_MODE", "false")
	t.Setenv("NATIVE_WORKSPACE", "true")
	old := viper.Get("docs-dir")
	viper.Set("docs-dir", docs)
	t.Cleanup(func() { viper.Set("docs-dir", old) })
	gin.SetMode(gin.TestMode)
	ws := gin.New()
	ws.Any("/api/documents/*filepath", workspacehandlers.HandleDocumentRequest)
	ws.GET("/api/documents", workspacehandlers.ListDocuments)
	ws.POST("/api/folders", workspacehandlers.CreateFolder)
	ws.POST("/api/execute", workspacehandlers.ExecuteShellCommand)
	files := httptest.NewServer(ws)
	defer files.Close()
	t.Setenv("WORKSPACE_API_URL", files.URL)
	const draft = "Workflow/dbos-server"
	m := NewWorkflowManifest("DBOS server recovery")
	m.Kind, m.RelayRuntime, m.RelayDurability = "relay", "python", "dbos"
	m.CreatedBy = "owner"
	m.Access = &WorkflowAccess{Owners: []string{"owner"}}
	m.Schedules = []WorkflowSchedule{{ID: "process", Name: "Process", Enabled: true, ScheduleType: "webhook", Kind: triggerKindFunction, WorkshopMode: "run", Function: &WorkflowFunctionSpec{Name: "process", Inputs: []WorkflowFunctionInput{{Name: "INPUT", Type: "object", Required: true}}}}}
	write := func(rel, text string) {
		t.Helper()
		file := filepath.Join(docs, draft, rel)
		if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("variables/variables.json", `{"variables":[{"name":"INPUT","type":"object","value":"{}"}]}`)
	write("relay.py", `import os
async def run(INPUT, ctx):
    async def prepare():
        file = ctx.run_dir / "service-count.txt"
        count = int(file.read_text()) + 1 if file.exists() else 1
        file.write_text(str(count))
        return {"value": INPUT["value"], "effects": count}
    saved = await ctx.step("prepare", prepare)
    # Test-only process fault after a durable checkpoint.
    marker = ctx.run_dir / "crashed-once"
    if not marker.exists():
        marker.write_text("yes")
        os._exit(97)
    return saved
`)
	if native {
		write("relay.py", `import os
from dbos import DBOS
from agentworks import run_dir

@DBOS.step(name="prepare")
async def prepare(value):
    file = run_dir / "service-count.txt"
    count = int(file.read_text()) + 1 if file.exists() else 1
    file.write_text(str(count))
    return {"value": value, "effects": count}

@DBOS.workflow(max_recovery_attempts=3)
async def run(INPUT):
    saved = await prepare(INPUT["value"])
    marker = run_dir / "crashed-once"
    if not marker.exists():
        marker.write_text("yes")
        os._exit(97)
    return saved
`)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	// Functions now live in the common project entry store; exercise the
	// production writer rather than a retired schedules array in workflow.json.
	if err := WriteWorkflowManifest(ctx, draft, m); err != nil {
		t.Fatal(err)
	}
	api := &StreamingAPI{}
	if native {
		registry := agentprofiles.NewRegistry()
		registerWorkCrewProfile(t, registry)
		api.projectActions = NewProjectActionDispatcher(api, registry)
		created, e := api.projectActions.CreateCrewProject(context.WithValue(ctx, UserContextKey, &UserClaims{UserID: "owner"}), CreateCrewRequest{UserID: "owner", WorkflowPath: draft, Title: "Program lab", IdempotencyKey: "program-chat", StepInstruction: "Record deterministic program outcomes", Purpose: "Record program runs in its chat"})
		if e != nil {
			t.Fatal(e)
		}
		binding := crewExecutableBinding{ID: m.ID, CrewID: created.CrewID, CrewRoot: created.WorkspacePath, Root: draft, Kind: "relay"}
		if e := registerCrewExecutable(binding); e != nil {
			t.Fatal(e)
		}
		if e := WriteWorkflowManifest(ctx, draft, m); e != nil {
			t.Fatal(e)
		}
	}
	release, err := publishRelayRelease(ctx, draft)
	if err != nil {
		t.Fatal(err)
	}
	published := relayReleaseWorkspace(draft, release.Version)
	store, err := schedulerstate.Open(filepath.Join(docs, "ledger.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	svc := NewSchedulerService(api)
	svc.stateStore = store
	runID := "durable-server-run"
	if err := store.BeginRun(ctx, schedulerstate.Run{RunID: runID, ScheduleID: "process", ScopeType: "workflow", ScopeID: published, LockKey: runID}); err != nil {
		t.Fatal(err)
	}
	folder, err := allocateWebhookRunFolder(published, runID)
	if err != nil {
		t.Fatal(err)
	}
	sctx := buildScheduleContext(published, m, m.Schedules[0])
	sctx.WebhookInput = &WorkflowWebhookDelivery{RunID: runID, Variables: map[string]string{"INPUT": `{"value":"original"}`}, Payload: json.RawMessage(`{"function":"process","relay_caller":"owner"}`)}
	if native {
		if e := AppendScheduleRun(ctx, published, &ScheduleRunEntry{ID: runID, ScheduleID: "process", Status: "queued", StartedAt: time.Now()}); e != nil {
			t.Fatal(e)
		}
		_, actualFolder, e := svc.executeJob(ctx, sctx, runID)
		if e != nil {
			t.Fatal(e)
		}
		folder = actualFolder
		target, e := svc.openCrewAutomationChat(ctx, sctx)
		if e != nil {
			t.Fatal(e)
		}
		again, e := svc.openCrewAutomationChat(ctx, sctx)
		if e != nil || target.SessionID != again.SessionID {
			t.Fatal("automation chat identity changed", e)
		}
		file, found, e := api.liveConversationPath("owner", target.SessionID, target.WorkspacePath)
		if e != nil || !found {
			t.Fatal("missing chat transcript", e)
		}
		saved, found, e := readFileFromWorkspace(ctx, file)
		if e != nil || !found || !strings.Contains(saved, "durable-server-run:completed") || !strings.Contains(saved, "effects") {
			t.Fatal("program result not in target chat", saved, e)
		}
		if e := svc.appendCrewAutomationMessage(ctx, sctx, target, runID, "completed", "repeat"); e != nil {
			t.Fatal(e)
		}
		repeated, _, _ := readFileFromWorkspace(ctx, file)
		if strings.Count(repeated, "durable-server-run:completed") != 1 {
			t.Fatal("duplicate completion receipt")
		}
		// Selecting an existing side chat reuses that saved conversation. A deleted
		// or foreign tab must not silently redirect the automation to Main.
		profile, e := api.projectActions.registry.Resolve("work", 0, "owner")
		if e != nil {
			t.Fatal(e)
		}
		sideBinding, e := resolveProductConversationBinding(ctx, "owner", profile, sctx.CrewExecutable.CrewID+codeChatSideMarker+"reporting")
		if e != nil {
			t.Fatal(e)
		}
		side, e := defaultProductConversationRegistryStore().resolveOrCreate(ctx, "owner", profile, sideBinding, "")
		if e != nil {
			t.Fatal(e)
		}
		sctx.Schedule.ChatID = "reporting"
		selected, e := svc.openCrewAutomationChat(ctx, sctx)
		if e != nil || selected.SessionID != side.SessionID || selected.SessionID == target.SessionID {
			t.Fatal("wrong chat selected", e)
		}
		sctx.Schedule.ChatID = "missing"
		if _, e := svc.openCrewAutomationChat(ctx, sctx); e == nil {
			t.Fatal("missing chat fell back to main")
		}
		sctx.Schedule.ChatID = "main"

	} else if err := svc.executePythonRelay(ctx, sctx, runID, folder, "dbos-server-session"); err != nil {
		t.Fatal(err)
	}
	result, err := os.ReadFile(filepath.Join(docs, published, "runs", folder, "relay_result.json"))
	if err != nil || !strings.Contains(string(result), `"effects": 1`) {
		t.Fatalf("result: %s %v", result, err)
	}
	var trace struct {
		Attempt int `json:"attempt_number"`
		Calls   []struct {
			Reused bool `json:"checkpoint_reused"`
		} `json:"calls"`
	}
	traceRaw, err := os.ReadFile(filepath.Join(docs, published, "runs", folder, "relay_trace.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(traceRaw, &trace); err != nil || trace.Attempt != 2 || len(trace.Calls) != 1 || !trace.Calls[0].Reused {
		t.Fatalf("not recovered: %s %v", traceRaw, err)
	}
	if err := verifyRelayRelease(ctx, release, published); err != nil {
		t.Fatal(err)
	}
	// Re-entering with a disabled live function must fail before any checkpoint
	// or completed result can bypass current invocation permission.
	m.Schedules[0].Enabled = false
	if err := WriteWorkflowManifest(ctx, draft, m); err != nil {
		t.Fatal(err)
	}
	if err := svc.executePythonRelay(ctx, sctx, runID, folder, "dbos-revoked-session"); err == nil || !strings.Contains(err.Error(), "no longer allowed") {
		t.Fatalf("revoked function admitted: %v", err)
	}
	traceRaw, err = os.ReadFile(filepath.Join(docs, published, "runs", folder, "relay_trace.json"))
	var finalTrace map[string]interface{}
	if err != nil || json.Unmarshal(traceRaw, &finalTrace) != nil || finalTrace["status"] != "failed" || !strings.Contains(fmt.Sprint(finalTrace["error"]), "no longer allowed") {
		t.Fatalf("terminal admission error not reflected in Runs: %s %v", traceRaw, err)
	}
}
