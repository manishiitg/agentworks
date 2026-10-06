// Package relaypython executes user-authored Python without a workflow plan.
package relaypython

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

//go:embed sdk.py
var SDK string

type Tool struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	Schema      map[string]interface{} `json:"schema"`
}
type MCP struct {
	Server string   `json:"server"`
	Tools  []string `json:"tools"`
}
type Call struct {
	Kind         string                 `json:"kind"`
	Server       string                 `json:"server"`
	Tool         string                 `json:"tool"`
	Arguments    map[string]interface{} `json:"arguments"`
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	SystemPrompt string                 `json:"system_prompt"`
	Messages     []string               `json:"messages"`
	Model        interface{}            `json:"model"`
	Tools        []Tool                 `json:"tools"`
	Skills       []string               `json:"skills"`
	MCP          []MCP                  `json:"mcp"`
	OutputSchema map[string]interface{} `json:"output_schema"`
	MaxTurns     int                    `json:"max_turns"`
}
type ToolCaller func(context.Context, string, map[string]interface{}) (string, error)
type AgentResult struct {
	Output   interface{}
	Tools    []map[string]interface{}
	Provider string
	Model    string
}
type AgentCaller func(context.Context, Call, ToolCaller) (interface{}, error)
type Files interface {
	ReadWorkspaceFile(context.Context, workspace.ReadWorkspaceFileParams) (workspace.ReadFileResult, error)
	UpdateWorkspaceFile(context.Context, workspace.UpdateWorkspaceFileParams) (workspace.UpdateFileResult, error)
	ExecuteShellCommand(context.Context, workspace.ExecuteShellCommandParams) (workspace.ShellCommandResult, error)
}
type Config struct {
	Client              Files
	SourcePath, RunPath string
	Input               interface{}
	Variables           map[string]interface{}
	Env                 map[string]string
	CallAgent           AgentCaller
	Timeout             int
}

func quote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
func write(ctx context.Context, c Files, file string, value interface{}) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = c.UpdateWorkspaceFile(ctx, workspace.UpdateWorkspaceFileParams{Filepath: file, Content: string(b)})
	return err
}
func read(ctx context.Context, c Files, file string, target interface{}) (bool, error) {
	result, err := c.ReadWorkspaceFile(ctx, workspace.ReadWorkspaceFileParams{Filepath: file})
	if err != nil {
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "file not found") || strings.Contains(err.Error(), "File does not exist") {
			return false, nil
		}
		return false, err
	}
	if len(result.Content) > 1024*1024 {
		return false, fmt.Errorf("Relay protocol message exceeds 1 MiB")
	}
	return true, json.Unmarshal([]byte(result.Content), target)
}

// Run uses a per-run file mailbox in the already authorized workspace. No new
// global/session-selectable RPC or credential is introduced.
// Agent tools call back into the *same* live Python process (including closures).
func Run(ctx context.Context, cfg Config) (runErr error) {
	if cfg.Client == nil || cfg.CallAgent == nil {
		return fmt.Errorf("Relay runtime is not configured")
	}
	ipc := path.Join(cfg.RunPath, ".relay_ipc")
	if err := write(ctx, cfg.Client, path.Join(ipc, "input.json"), map[string]interface{}{"input": cfg.Input, "variables": cfg.Variables}); err != nil {
		return err
	}
	runner := path.Join(ipc, "runner.py")
	if _, err := cfg.Client.UpdateWorkspaceFile(ctx, workspace.UpdateWorkspaceFileParams{Filepath: runner, Content: SDK}); err != nil {
		return err
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 3600
	}
	env := map[string]string{"PYTHONDONTWRITEBYTECODE": "1"}
	for k, v := range cfg.Env {
		env[k] = v
	}
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer func() {
		if runErr == nil {
			return
		}
		finalizeCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		trace := map[string]interface{}{"version": 1, "calls": []interface{}{}}
		_, _ = read(finalizeCtx, cfg.Client, path.Join(cfg.RunPath, "relay_trace.json"), &trace)
		trace["status"], trace["error"] = "failed", runErr.Error()
		if calls, ok := trace["calls"].([]interface{}); ok {
			for _, entry := range calls {
				if call, ok := entry.(map[string]interface{}); ok && call["status"] == "running" {
					call["status"], call["error"] = "failed", runErr.Error()
				}
			}
		}
		_ = write(finalizeCtx, cfg.Client, path.Join(cfg.RunPath, "relay_trace.json"), trace)
	}()
	complete := func(err error) error {
		if err != nil {
			return err
		}
		var result interface{}
		exists, err := read(ctx, cfg.Client, path.Join(cfg.RunPath, "relay_result.json"), &result)
		if err != nil {
			return err
		}
		if !exists {
			return fmt.Errorf("relay.py exited without returning a result")
		}
		var trace map[string]interface{}
		exists, err = read(ctx, cfg.Client, path.Join(cfg.RunPath, "relay_trace.json"), &trace)
		if err != nil {
			return err
		}
		if !exists || trace["status"] != "completed" {
			return fmt.Errorf("relay.py exited before completing")
		}
		return nil
	}
	finished := make(chan error, 1)
	sourceDir := path.Dir(cfg.SourcePath)
	runRelative := strings.TrimPrefix(cfg.RunPath, sourceDir+"/")
	runnerRelative := strings.TrimPrefix(runner, sourceDir+"/")
	go func() {
		result, err := cfg.Client.ExecuteShellCommand(childCtx, workspace.ExecuteShellCommandParams{Command: "python3 -I " + quote(runnerRelative) + " " + quote(path.Base(cfg.SourcePath)) + " " + quote(runRelative), WorkingDirectory: sourceDir, Timeout: &timeout, ExtraEnv: env})
		if err == nil && result.CommandFailed() {
			err = fmt.Errorf("relay.py failed: %s", strings.TrimSpace(result.Stderr+" "+result.Error))
		}
		finished <- err
		cancel()
	}()
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for n := 1; ; {
		select {
		case err := <-finished:
			return complete(err)
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		id := fmt.Sprintf("call-%d", n)
		var call Call
		exists, err := read(childCtx, cfg.Client, path.Join(ipc, id+".request.json"), &call)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if call.ID != id {
			return fmt.Errorf("invalid Relay call identity")
		}
		output, callErr := cfg.CallAgent(childCtx, call, func(toolCtx context.Context, name string, args map[string]interface{}) (string, error) {
			toolID := id + ".tool-" + uuid.NewString()
			if err := write(toolCtx, cfg.Client, path.Join(ipc, toolID+".request.json"), map[string]interface{}{"name": name, "args": args}); err != nil {
				return "", err
			}
			poll := time.NewTicker(100 * time.Millisecond)
			defer poll.Stop()
			for {
				var reply struct {
					Output interface{} `json:"output"`
					Error  string      `json:"error"`
				}
				exists, err := read(toolCtx, cfg.Client, path.Join(ipc, toolID+".response.json"), &reply)
				if err != nil {
					return "", err
				}
				if exists {
					if reply.Error != "" {
						return "", fmt.Errorf("Python tool %s: %s", name, reply.Error)
					}
					b, err := json.Marshal(reply.Output)
					return string(b), err
				}
				select {
				case <-toolCtx.Done():
					return "", toolCtx.Err()
				case <-poll.C:
				}
			}
		})
		select {
		case err := <-finished:
			return complete(err)
		default:
		}
		reply := map[string]interface{}{"output": output}
		if result, ok := output.(AgentResult); ok {
			reply["output"], reply["tools"] = result.Output, result.Tools
			reply["provider"], reply["model"] = result.Provider, result.Model
		}
		if callErr != nil {
			reply["error"] = callErr.Error()
		}
		if err := write(childCtx, cfg.Client, path.Join(ipc, id+".response.json"), reply); err != nil {
			return err
		}
		n++
	}
}
