package relaypython

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

// These tests use real DBOS, SQLite, Python process crashes, and the production
// Go mailbox bridge. Only the model and remote service boundaries are fixtures.
func dbosFixture(t *testing.T, source string, input map[string]interface{}) (localWorkspace, Config) {
	t.Helper()
	python := os.Getenv("RELAY_DBOS_PYTHON")
	if python == "" {
		t.Skip("set RELAY_DBOS_PYTHON to an interpreter with requirements-dbos.txt installed")
	}
	if out, err := exec.Command(python, "-I", "-c", "from importlib.metadata import version; assert version('dbos') == '3.2.0'").CombinedOutput(); err != nil {
		t.Fatalf("DBOS interpreter: %v: %s", err, out)
	}
	c := localWorkspace{t.TempDir()}
	cfg := Config{Client: c, SourcePath: "Workflow/demo/relay.py", RunPath: "Workflow/demo/runs/run-1",
		Input: input, Variables: map[string]interface{}{"label": "v1"}, Timeout: 25,
		DBOSPrototype: &DBOSPrototype{RunID: "run-1", ReleaseHash: "fixture-release-v1", PythonExecutable: python,
			Authorize: func(context.Context) error { return nil }},
		CallAgent: func(context.Context, Call, ToolCaller) (interface{}, error) {
			t.Error("unexpected model/MCP call")
			return nil, errors.New("unexpected call")
		}}
	if _, err := c.UpdateWorkspaceFile(context.Background(), workspace.UpdateWorkspaceFileParams{Filepath: cfg.SourcePath, Content: source}); err != nil {
		t.Fatal(err)
	}
	return c, cfg
}

func runDBOS(t *testing.T, cfg Config) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return Run(ctx, cfg)
}

func TestDBOSRecoversCompletedAgentAndServiceCheckpoints(t *testing.T) {
	c, cfg := dbosFixture(t, `import json, os
from pathlib import Path
from relay_sdk import tool

async def run(INPUT, ctx):
    @tool
    async def lookup(customer_id: str):
        return {"id": customer_id, "label": ctx.variables["label"], "token": ctx.vault("DEMO")}
    extracted = await ctx.call_agent(name="extract", system_prompt="Extract", messages=["first", "second"], tools=[lookup])
    async def service(idempotency_key):
        ledger = ctx.run_dir / "service.json"
        if not ledger.exists():
            ledger.write_text(json.dumps({"key": idempotency_key, "effects": 1}))
        return json.loads(ledger.read_text())
    saved = await ctx.step("save", service, arguments={"idempotency_key": INPUT["id"]}, replay_safe=True)
    if os.environ.get("FAULT_AFTER_CHECKPOINT") == "1":
        os._exit(97)
    if extracted["score"] > 5:
        reviewed = await ctx.call_agent(name="review", system_prompt="Review", user_message=json.dumps(extracted))
    external = await ctx.call_mcp(server="authorized", tool="fetch", arguments={"id": INPUT["id"]})
    return {"reviewed": reviewed, "external": external, "saved": saved}
`, map[string]interface{}{"id": "order-1"})
	counts := map[string]int{}
	cfg.Env = map[string]string{"SECRET_DEMO": "scoped-fixture", "FAULT_AFTER_CHECKPOINT": "1"}
	cfg.CallAgent = func(ctx context.Context, call Call, tool ToolCaller) (interface{}, error) {
		counts[call.Name+call.Kind]++
		if call.Name == "extract" {
			if len(call.Messages) != 2 {
				t.Fatal("ordered messages lost")
			}
			value, err := tool(ctx, "lookup", map[string]interface{}{"customer_id": "order-1"})
			if err != nil || !strings.Contains(value, "scoped-fixture") {
				t.Fatalf("real closure callback: %s, %v", value, err)
			}
			return AgentResult{Output: map[string]interface{}{"score": 7}, Tools: []map[string]interface{}{{"name": "lookup"}}, Provider: "fixture", Model: "fixture"}, nil
		}
		if call.Name == "review" {
			return map[string]interface{}{"ok": true}, nil
		}
		if call.Kind == "mcp" && call.Server == "authorized" {
			return []string{"external"}, nil
		}
		return nil, errors.New("unexpected call")
	}
	if err := runDBOS(t, cfg); err == nil {
		t.Fatal("process crash reported success")
	}
	// Recheck live authorization before returning even a previously saved result.
	cfg.DBOSPrototype.Authorize = func(context.Context) error { return errors.New("access revoked") }
	if err := runDBOS(t, cfg); err == nil || !strings.Contains(err.Error(), "access revoked") {
		t.Fatalf("revocation ignored: %v", err)
	}
	cfg.DBOSPrototype.Authorize = func(context.Context) error { return nil }
	delete(cfg.Env, "FAULT_AFTER_CHECKPOINT")
	if err := runDBOS(t, cfg); err != nil {
		t.Fatal(err)
	}
	if counts["extract"] != 1 || counts["review"] != 1 || counts["mcp"] != 1 {
		t.Fatalf("completed calls repeated or pending calls skipped: %v", counts)
	}
	trace, err := os.ReadFile(filepath.Join(c.root, cfg.RunPath, "relay_trace.json"))
	var record struct {
		Status string
		Calls  []struct {
			ID               string
			CheckpointReused bool `json:"checkpoint_reused"`
			Tools            []interface{}
		}
	}
	if err != nil || json.Unmarshal(trace, &record) != nil || record.Status != "completed" || len(record.Calls) != 4 {
		t.Fatalf("recovered trace: %s, %v", trace, err)
	}
	for i, call := range record.Calls {
		if call.CheckpointReused != (i < 2) {
			t.Fatalf("checkpoint provenance missing: %s", trace)
		}
	}
	if len(record.Calls[0].Tools) != 2 { // Python callback and platform tool receipt.
		t.Fatalf("tool receipts lost: %s", trace)
	}
	result, err := os.ReadFile(filepath.Join(c.root, cfg.RunPath, "relay_result.json"))
	if err != nil || !strings.Contains(string(result), `"effects": 1`) {
		t.Fatalf("service effect/result: %s, %v", result, err)
	}
	// A completed invocation returns the original result without model calls.
	if err := runDBOS(t, cfg); err != nil || counts["extract"] != 1 || counts["review"] != 1 || counts["mcp"] != 1 {
		t.Fatalf("completed-run deduplication: %v, %v", counts, err)
	}
	// Changing an input or a release may never bind to old checkpoints.
	cfg.Input = map[string]interface{}{"id": "different"}
	if err := runDBOS(t, cfg); err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("different input reused a checkpoint: %v", err)
	}
	cfg.Input = map[string]interface{}{"id": "order-1"}
	cfg.DBOSPrototype.ReleaseHash = "fixture-release-v2"
	if err := runDBOS(t, cfg); err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("different release reused a checkpoint: %v", err)
	}
	cfg.DBOSPrototype.ReleaseHash = "fixture-release-v1"
	_, _ = c.UpdateWorkspaceFile(context.Background(), workspace.UpdateWorkspaceFileParams{Filepath: cfg.SourcePath, Content: "async def run(INPUT, ctx):\n    return {'changed': True}\n"})
	if err := runDBOS(t, cfg); err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("modified source reused a checkpoint: %v", err)
	}
}

func TestDBOSUncertainServiceRequiresReplaySafety(t *testing.T) {
	for _, safe := range []bool{false, true} {
		t.Run(map[bool]string{false: "stop_for_reconciliation", true: "idempotent_retry"}[safe], func(t *testing.T) {
			c, cfg := dbosFixture(t, `import json, os
async def run(INPUT, ctx):
    async def service(idempotency_key):
        ledger = ctx.run_dir / "service.json"
        if not ledger.exists():
            ledger.write_text(json.dumps({"key": idempotency_key, "effects": 1}))
        attempts = ctx.run_dir / "attempts.txt"
        with attempts.open("a") as file:
            file.write("attempt\n")
        if os.environ.get("FAULT_DURING_SERVICE") == "1":
            os._exit(98)
        return json.loads(ledger.read_text())
    return await ctx.step("save", service, arguments={"idempotency_key": "order-1"}, replay_safe=INPUT["safe"])
`, map[string]interface{}{"safe": safe})
			cfg.Env = map[string]string{"FAULT_DURING_SERVICE": "1"}
			if err := runDBOS(t, cfg); err == nil {
				t.Fatal("service crash reported success")
			}
			delete(cfg.Env, "FAULT_DURING_SERVICE")
			err := runDBOS(t, cfg)
			if safe && err != nil {
				t.Fatal(err)
			}
			if !safe && (err == nil || !strings.Contains(err.Error(), "requires reconciliation")) {
				t.Fatalf("uncertain effect retried: %v", err)
			}
			attempts, _ := os.ReadFile(filepath.Join(c.root, cfg.RunPath, "attempts.txt"))
			want := 1
			if safe {
				want = 2
			}
			if strings.Count(string(attempts), "attempt") != want {
				t.Fatalf("service attempts: %q", attempts)
			}
			ledger, _ := os.ReadFile(filepath.Join(c.root, cfg.RunPath, "service.json"))
			if !strings.Contains(string(ledger), `"effects": 1`) {
				t.Fatalf("idempotency ledger: %s", ledger)
			}
		})
	}
}

func TestDBOSModelErrorsAreTerminal(t *testing.T) {
	_, cfg := dbosFixture(t, `async def run(INPUT, ctx):
    return await ctx.call_agent(name="denied", system_prompt="Read", user_message="Read")
`, map[string]interface{}{})
	calls := 0
	cfg.CallAgent = func(context.Context, Call, ToolCaller) (interface{}, error) {
		calls++
		return nil, errors.New("live MCP permission denied")
	}
	for range 2 {
		if err := runDBOS(t, cfg); err == nil || !strings.Contains(err.Error(), "live MCP permission denied") {
			t.Fatalf("terminal error lost: %v", err)
		}
	}
	if calls != 1 {
		t.Fatalf("terminal failure retried %d times", calls)
	}
}

func TestDBOSRechecksAdmissionBeforeEachPlatformCall(t *testing.T) {
	_, cfg := dbosFixture(t, `async def run(INPUT, ctx):
    await ctx.call_agent(name="first", system_prompt="Read", user_message="Read")
    return await ctx.call_mcp(server="revoked", tool="read", arguments={})
`, map[string]interface{}{})
	checks, calls := 0, 0
	cfg.DBOSPrototype.Authorize = func(context.Context) error {
		checks++
		if checks >= 3 {
			return errors.New("access revoked between calls")
		}
		return nil
	}
	cfg.CallAgent = func(context.Context, Call, ToolCaller) (interface{}, error) {
		calls++
		return map[string]interface{}{"ok": true}, nil
	}
	if err := runDBOS(t, cfg); err == nil || !strings.Contains(err.Error(), "access revoked between calls") || calls != 1 {
		t.Fatalf("call admission: calls=%d checks=%d err=%v", calls, checks, err)
	}
}

func TestDBOSRejectsConcurrentExecutorWithoutCorruptingTrace(t *testing.T) {
	c, cfg := dbosFixture(t, `async def run(INPUT, ctx):
    return await ctx.call_agent(name="wait", system_prompt="Read", user_message="Read")
`, map[string]interface{}{})
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	cfg.CallAgent = func(ctx context.Context, _ Call, _ ToolCaller) (interface{}, error) {
		close(started)
		select {
		case <-release:
			return map[string]interface{}{"ok": true}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, cfg) }()
	select {
	case <-started:
	case err := <-done:
		t.Fatalf("first executor failed: %v", err)
	case <-ctx.Done():
		t.Fatal("first executor did not start")
	}
	if err := runDBOS(t, cfg); err == nil || !strings.Contains(err.Error(), "already has a live executor") {
		t.Fatalf("concurrent executor admitted: %v", err)
	}
	trace, _ := os.ReadFile(filepath.Join(c.root, cfg.RunPath, "relay_trace.json"))
	var state map[string]interface{}
	if json.Unmarshal(trace, &state) != nil || state["status"] != "running" {
		t.Fatalf("rejected executor overwrote live trace: %s", trace)
	}
	once.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
