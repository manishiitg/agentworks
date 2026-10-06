package relaypython

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

// An actual Python program drives the production mailbox and callback loop.
// The model boundary is deterministic; no SDK method or Python tool is mocked.
type localWorkspace struct{ root string }

func (c localWorkspace) ReadWorkspaceFile(_ context.Context, p workspace.ReadWorkspaceFileParams) (workspace.ReadFileResult, error) {
	b, e := os.ReadFile(filepath.Join(c.root, p.Filepath))
	if os.IsNotExist(e) {
		return workspace.ReadFileResult{}, fmt.Errorf("file not found")
	}
	return workspace.ReadFileResult{Content: string(b)}, e
}
func (c localWorkspace) UpdateWorkspaceFile(_ context.Context, p workspace.UpdateWorkspaceFileParams) (workspace.UpdateFileResult, error) {
	file := filepath.Join(c.root, p.Filepath)
	if e := os.MkdirAll(filepath.Dir(file), 0700); e != nil {
		return workspace.UpdateFileResult{}, e
	}
	return workspace.UpdateFileResult{Success: true}, os.WriteFile(file, []byte(p.Content), 0600)
}
func (c localWorkspace) ExecuteShellCommand(ctx context.Context, p workspace.ExecuteShellCommandParams) (workspace.ShellCommandResult, error) {
	cmd := exec.CommandContext(ctx, "sh", "-c", p.Command)
	cmd.Dir = filepath.Join(c.root, p.WorkingDirectory)
	cmd.Env = os.Environ()
	for key, value := range p.ExtraEnv {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		code = -1
	}
	return workspace.ShellCommandResult{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: code}, nil
}

func TestPythonChainCallsToolsAndReturnsData(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
	c := localWorkspace{t.TempDir()}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	source := `import json
from relay_sdk import tool

async def run(INPUT, ctx):
    @tool
    async def lookup(customer_id: str):
        """Read the configured customer."""
        try:
            await ctx.call_mcp(server="nested", tool="blocked", arguments={})
        except RuntimeError as exc:
            if "Nested agent/MCP calls" not in str(exc):
                raise
        else:
            raise RuntimeError("nested call should fail promptly, not block the callback")
        return {"customer_id": customer_id, "token": ctx.vault("DEMO"), "label": ctx.variables["label"]}
    extracted = await ctx.call_agent(name="extract", system_prompt="Exact prompt", messages=["first", "second"], tools=[lookup])
    if extracted["score"] > 5:
        reviewed = await ctx.call_agent(name="review", system_prompt="Review", user_message=json.dumps(extracted))
    else:
        raise RuntimeError("wrong branch")
    mcp = await ctx.call_mcp(server="authorized", tool="fetch", arguments={"id": INPUT["id"]})
    return {"reviewed": reviewed, "external": mcp}
`
	if _, err := c.UpdateWorkspaceFile(ctx, workspace.UpdateWorkspaceFileParams{Filepath: "Workflow/demo/relay.py", Content: source}); err != nil {
		t.Fatal(err)
	}
	count := 0
	err := Run(ctx, Config{Client: c, SourcePath: "Workflow/demo/relay.py", RunPath: "Workflow/demo/runs/iteration-1-hook", Input: map[string]interface{}{"id": "c1"}, Variables: map[string]interface{}{"label": "draft"}, Env: map[string]string{"SECRET_DEMO": "scoped"}, CallAgent: func(ctx context.Context, call Call, tool ToolCaller) (interface{}, error) {
		count++
		switch count {
		case 1:
			if call.SystemPrompt != "Exact prompt" || len(call.Messages) != 2 || len(call.Tools) != 1 {
				t.Errorf("authored call lost identity: %+v", call)
			}
			value, err := tool(ctx, "lookup", map[string]interface{}{"customer_id": "c1"})
			if err != nil {
				return nil, err
			}
			if value != `{"customer_id":"c1","label":"draft","token":"scoped"}` {
				t.Errorf("Python closure/env/config: %s", value)
			}
			return map[string]interface{}{"score": 7}, nil
		case 2:
			if call.Name != "review" || len(call.Tools) != 0 {
				t.Errorf("separate call inherited tools: %+v", call)
			}
			return map[string]interface{}{"ok": true}, nil
		case 3:
			if call.Kind != "mcp" || call.Server != "authorized" || call.Arguments["id"] != "c1" {
				t.Errorf("MCP call: %+v", call)
			}
			return []interface{}{"external"}, nil
		}
		return nil, fmt.Errorf("unexpected call")
	}})
	if err != nil {
		t.Fatal(err)
	}
	output, _ := c.ReadWorkspaceFile(ctx, workspace.ReadWorkspaceFileParams{Filepath: "Workflow/demo/runs/iteration-1-hook/relay_result.json"})
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(output.Content), &result); err != nil || result["reviewed"] == nil || result["external"] == nil || count != 3 {
		t.Fatalf("actual returned JSON: %s (calls %d, err %v)", output.Content, count, err)
	}
	trace, _ := c.ReadWorkspaceFile(ctx, workspace.ReadWorkspaceFileParams{Filepath: "Workflow/demo/runs/iteration-1-hook/relay_trace.json"})
	var recorded struct {
		Status string
		Calls  []struct {
			Status string
			Tools  []interface{}
		}
	}
	if err := json.Unmarshal([]byte(trace.Content), &recorded); err != nil || recorded.Status != "completed" || len(recorded.Calls) != 3 || len(recorded.Calls[0].Tools) != 1 {
		t.Fatalf("actual trace: %s", trace.Content)
	}
	// Errors terminate, rather than replaying any earlier Python effects.
	_, _ = c.UpdateWorkspaceFile(ctx, workspace.UpdateWorkspaceFileParams{Filepath: "Workflow/demo/relay.py", Content: "async def run(INPUT, ctx):\n    raise RuntimeError('terminal failure')\n"})
	err = Run(ctx, Config{Client: c, SourcePath: "Workflow/demo/relay.py", RunPath: "Workflow/demo/runs/iteration-2-hook", Input: map[string]interface{}{}, CallAgent: func(context.Context, Call, ToolCaller) (interface{}, error) {
		t.Error("unexpected agent after Python failure")
		return nil, nil
	}})
	if err == nil {
		t.Fatal("Python error reported success")
	}
}
