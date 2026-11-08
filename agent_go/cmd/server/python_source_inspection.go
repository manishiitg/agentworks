package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/relaypython"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

func inspectPythonAutomationSource(ctx context.Context, workspacePath string, files map[string]string) (json.RawMessage, error) {
	raw, err := json.Marshal(files)
	if err != nil {
		return nil, fmt.Errorf("read automation source: %w", err)
	}
	if len(raw) > 64*1024 {
		return json.RawMessage(`{"native":true,"nodes":[],"edges":[],"errors":["This code is too large to outline (64 KiB limit). You can still open and run it."]}`), nil
	}
	return inspectPythonSourceEnvironment(ctx, workspacePath, path.Join(workspacePath, "code"), map[string]string{"VAR_AUTOMATION_CHECK_FILES": base64.StdEncoding.EncodeToString(raw)})
}

func inspectPythonSourceEnvironment(ctx context.Context, workspacePath, workingDirectory string, environment map[string]string) (json.RawMessage, error) {
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !found {
		return nil, fmt.Errorf("Automation manifest unavailable during source inspection")
	}
	sessionID := "source-inspection-" + uuid.NewString()
	readPaths := []string{workspacePath}
	if workingDirectory != workspacePath {
		readPaths = append(readPaths, workingDirectory)
	}
	common.SetSessionFolderGuard(sessionID, readPaths, nil)
	common.SetSessionSandbox(sessionID, true, true)
	defer workspace.ClearSessionShellConfig(sessionID)
	validationCtx := context.WithValue(ctx, common.ChatSessionIDKey, sessionID)
	client := workspace.NewClient(getWorkspaceAPIURL())
	client.UserID = workflowExecutionOwnerUserID(manifest)
	if IsMultiUserMode() && client.UserID == "" {
		return nil, fmt.Errorf("Flow execution owner unavailable")
	}
	const check = `import base64,os; exec(compile(base64.b64decode(os.environ["VAR_RELAY_INSPECTOR"]),"<platform-inspector>","exec"))`
	timeout := 15
	environment["VAR_RELAY_INSPECTOR"] = base64.StdEncoding.EncodeToString([]byte(relaypython.SourceInspector))
	result, err := client.ExecuteShellCommand(validationCtx, workspace.ExecuteShellCommandParams{
		Command: "python3 -I -c '" + check + "'", WorkingDirectory: workingDirectory, Timeout: &timeout,
		ExtraEnv: environment,
	})
	if err != nil {
		return nil, fmt.Errorf("inspect Python source: %w", err)
	}
	if result.CommandFailed() {
		return nil, fmt.Errorf("invalid Python source: %s", strings.TrimSpace(result.Stderr+" "+result.Error))
	}
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if json.Valid([]byte(lines[i])) {
			return json.RawMessage(lines[i]), nil
		}
	}
	return nil, fmt.Errorf("source inspector returned no JSON")
}
