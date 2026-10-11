// Package relaypython executes user-authored Python without a workflow plan.
package relaypython

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

//go:embed sdk.py
var SDK string

//go:embed dbos_runner.py
var dbosRunner string

//go:embed native.py
var nativeBridge string

// SourceInspector uses only Python's standard AST library, without imports of
// authored modules. Shared by source admission and the UI source overview.
//
//go:embed inspect_source.py
var SourceInspector string

// DBOSConfig enables durable execution in an isolated database per invocation.
// Authorize must recheck live invocation access and the
// complete published release checksum on every attempt, including recovery.
// PythonExecutable must have the version in requirements-dbos.txt installed.
type DBOSConfig struct {
	RunID, ReleaseHash, PythonExecutable string
	Attempt                              int
	Authorize                            func(context.Context) error
}

// DBOSPrototype is retained for callers of the initial experimental adapter.
type DBOSPrototype = DBOSConfig

// A Python executor outlives an HTTP caller in some workspace deployments.
// Restart recovery waits for that executor's heartbeat lease to expire first.
const RecoveryLeaseGracePeriod = 17 * time.Second

// InterruptedError means the admitted Python process exited while its durable
// workflow was still pending. Application errors and cancelled contexts never
// become recovery candidates.
type InterruptedError struct{ Cause error }

func (e *InterruptedError) Error() string { return "Relay process interrupted: " + e.Cause.Error() }
func (e *InterruptedError) Unwrap() error { return e.Cause }
func IsInterrupted(err error) bool {
	var interrupted *InterruptedError
	return errors.As(err, &interrupted)
}

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
	// AttemptID isolates agent artifacts when transport call IDs restart on recovery.
	AttemptID    string                 `json:"-"`
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
	Recovery     string                 `json:"recovery"`
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
	DBOSPrototype       *DBOSPrototype
	DBOS                *DBOSConfig
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
	if cfg.DBOS != nil {
		cfg.DBOSPrototype = cfg.DBOS
	}
	if cfg.Client == nil || cfg.CallAgent == nil {
		return fmt.Errorf("Relay runtime is not configured")
	}
	ipc := path.Join(cfg.RunPath, ".relay_ipc")
	input := map[string]interface{}{"input": cfg.Input, "variables": cfg.Variables}
	runnerSource, pythonCommand := SDK, "python3"
	if prototype := cfg.DBOSPrototype; prototype != nil {
		if prototype.RunID == "" || prototype.ReleaseHash == "" || prototype.Authorize == nil {
			return fmt.Errorf("DBOS prototype requires run identity, release hash and live authorization")
		}
		if err := prototype.Authorize(ctx); err != nil {
			return fmt.Errorf("DBOS prototype admission: %w", err)
		}
		source, err := cfg.Client.ReadWorkspaceFile(ctx, workspace.ReadWorkspaceFileParams{Filepath: cfg.SourcePath})
		if err != nil {
			return err
		}
		input["dbos"] = map[string]interface{}{"run_id": prototype.RunID, "release_hash": prototype.ReleaseHash, "source_sha256": fmt.Sprintf("%x", sha256.Sum256([]byte(source.Content)))}
		input["attempt_number"] = prototype.Attempt
		// Every process gets a fresh mailbox. Old responses and tool requests
		// must never be mistaken for a new operation during recovery.
		ipc = path.Join(ipc, "attempt-"+uuid.NewString())
		if _, err := cfg.Client.UpdateWorkspaceFile(ctx, workspace.UpdateWorkspaceFileParams{Filepath: path.Join(ipc, "sdk.py"), Content: SDK}); err != nil {
			return err
		}
		if _, err := cfg.Client.UpdateWorkspaceFile(ctx, workspace.UpdateWorkspaceFileParams{Filepath: path.Join(ipc, "native.py"), Content: nativeBridge}); err != nil {
			return err
		}
		runnerSource = dbosRunner
		if prototype.PythonExecutable != "" {
			pythonCommand = quote(prototype.PythonExecutable)
		}
	}
	if err := write(ctx, cfg.Client, path.Join(ipc, "input.json"), input); err != nil {
		return err
	}
	runner := path.Join(ipc, "runner.py")
	if _, err := cfg.Client.UpdateWorkspaceFile(ctx, workspace.UpdateWorkspaceFileParams{Filepath: runner, Content: runnerSource}); err != nil {
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
	if cfg.DBOSPrototype != nil {
		lease := path.Join(ipc, "lease.json")
		if err := write(ctx, cfg.Client, lease, map[string]bool{"alive": true}); err != nil {
			return err
		}
		go func() {
			tick := time.NewTicker(2 * time.Second)
			defer tick.Stop()
			for {
				select {
				case <-childCtx.Done():
					return
				case <-tick.C:
					beatCtx, stop := context.WithTimeout(childCtx, 3*time.Second)
					_ = write(beatCtx, cfg.Client, lease, map[string]bool{"alive": true})
					stop()
				}
			}
		}()
	}
	defer func() {
		if runErr == nil {
			return
		}
		finalizeCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer stop()
		trace := map[string]interface{}{"version": 1, "calls": []interface{}{}}
		_, _ = read(finalizeCtx, cfg.Client, path.Join(cfg.RunPath, "relay_trace.json"), &trace)
		if cfg.DBOSPrototype != nil && trace["attempt_id"] != path.Base(ipc) {
			// A rejected concurrent launcher must not finalize another process's trace.
			return
		}
		if IsInterrupted(runErr) {
			trace["status"] = "recovering"
			delete(trace, "error")
			_ = write(finalizeCtx, cfg.Client, path.Join(cfg.RunPath, "relay_trace.json"), trace)
			return
		}
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
			if cfg.DBOSPrototype != nil && ctx.Err() == nil {
				var state struct {
					Status    string `json:"status"`
					AttemptID string `json:"attempt_id"`
				}
				if exists, readErr := read(ctx, cfg.Client, path.Join(cfg.RunPath, "relay_durability.json"), &state); readErr == nil && exists && state.AttemptID == path.Base(ipc) && state.Status == "running" {
					return &InterruptedError{Cause: err}
				}
			}
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
	transportError := func(err error) error {
		// Process completion cancels mailbox requests. Its result is already
		// queued before that cancellation, so preserve it rather than treating
		// the cancelled read/write as a caller-requested stop.
		select {
		case processErr := <-finished:
			return complete(processErr)
		default:
			return err
		}
	}
	sourceDir := path.Dir(cfg.SourcePath)
	runRelative := strings.TrimPrefix(cfg.RunPath, sourceDir+"/")
	runnerRelative := strings.TrimPrefix(runner, sourceDir+"/")
	go func() {
		result, err := cfg.Client.ExecuteShellCommand(childCtx, workspace.ExecuteShellCommandParams{Command: pythonCommand + " -I " + quote(runnerRelative) + " " + quote(path.Base(cfg.SourcePath)) + " " + quote(runRelative), WorkingDirectory: sourceDir, Timeout: &timeout, ExtraEnv: env})
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
			return transportError(err)
		}
		if !exists {
			continue
		}
		if call.ID != id {
			return fmt.Errorf("invalid Relay call identity")
		}
		if cfg.DBOSPrototype != nil {
			call.AttemptID = path.Base(ipc)
		}
		callTool := func(toolCtx context.Context, name string, args map[string]interface{}) (string, error) {
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
		}
		var output interface{}
		var callErr error
		if cfg.DBOSPrototype != nil {
			if err := cfg.DBOSPrototype.Authorize(childCtx); err != nil {
				callErr = fmt.Errorf("DBOS prototype call admission: %w", err)
			}
		}
		if callErr == nil && call.Kind == "admission" && cfg.DBOSPrototype == nil {
			callErr = fmt.Errorf("unexpected durable admission request")
		}
		if callErr == nil && call.Kind != "admission" {
			output, callErr = cfg.CallAgent(childCtx, call, callTool)
		}
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
			return transportError(err)
		}
		n++
	}
}
