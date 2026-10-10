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

const defaultPythonRelaySource = `# @relay node {"id":"input","type":"input","label":"Receive a name","description":"Your app can send a name; it defaults to world.","input":{"name":"optional text"}}
# @relay node {"id":"greeting","type":"script","label":"Create a greeting","description":"Read the name. This starter does not call an agent."}
# @relay node {"id":"result","type":"output","label":"Return the greeting","output":{"hello":"the supplied name, or world"}}
# @relay edge {"from":"input","to":"greeting"}
# @relay edge {"from":"greeting","to":"result"}
async def run(INPUT, ctx):
    return {"hello": INPUT.get("name", "world")}
`

const defaultNativeDBOSRelaySource = `from dbos import DBOS

@DBOS.step(name="create_greeting")
async def create_greeting(name):
    """Create a greeting from the supplied name."""
    return {"hello": name}

@DBOS.workflow(name="greeting", max_recovery_attempts=3)
async def run(INPUT):
    return await create_greeting(INPUT.get("name", "world"))
`

func isPythonRelay(manifest *WorkflowManifest) bool {
	return manifest != nil && manifest.Kind == "relay" && manifest.RelayRuntime == "python"
}

// Syntax checking never imports the draft or executes its module-level code.
// It uses the existing workspace shell service with an isolated read-only
// session, so validation runs where the published program will run.
func validatePythonRelaySource(ctx context.Context, workspacePath string) error {
	source, exists, err := readFileFromWorkspace(ctx, path.Join(workspacePath, "relay.py"))
	if err != nil {
		return fmt.Errorf("read relay.py: %w", err)
	}
	if !exists {
		return fmt.Errorf("Relay needs relay.py")
	}
	inspected, err := inspectPythonRelaySource(ctx, workspacePath, source)
	if err != nil {
		return err
	}
	var result struct {
		Native          bool   `json:"native"`
		ValidationError string `json:"validation_error"`
	}
	if err := json.Unmarshal(inspected, &result); err != nil {
		return err
	}
	if result.ValidationError != "" {
		return fmt.Errorf("invalid relay.py: %s", result.ValidationError)
	}
	if result.Native {
		manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
		if err != nil || !found || !isDBOSRelay(manifest) {
			return fmt.Errorf("native run(INPUT) requires DBOS recovery enabled")
		}
	}
	return nil
}

func inspectPythonRelaySource(ctx context.Context, workspacePath, source string) (json.RawMessage, error) {
	if strings.TrimSpace(source) == "" || len(source) > 64*1024 {
		return nil, fmt.Errorf("relay.py must contain 1–65536 bytes")
	}
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !found {
		return nil, fmt.Errorf("Relay manifest unavailable during source validation")
	}
	sessionID := "relay-syntax-" + uuid.NewString()
	common.SetSessionFolderGuard(sessionID, []string{workspacePath}, nil)
	common.SetSessionSandbox(sessionID, true, true)
	defer workspace.ClearSessionShellConfig(sessionID)
	validationCtx := context.WithValue(ctx, common.ChatSessionIDKey, sessionID)
	client := workspace.NewClient(getWorkspaceAPIURL())
	client.UserID = workflowExecutionOwnerUserID(manifest)
	if IsMultiUserMode() && client.UserID == "" {
		return nil, fmt.Errorf("Relay execution owner unavailable")
	}
	const check = `import base64,os; exec(compile(base64.b64decode(os.environ["VAR_RELAY_INSPECTOR"]),"<platform-inspector>","exec"))`
	timeout := 15
	result, err := client.ExecuteShellCommand(validationCtx, workspace.ExecuteShellCommandParams{
		Command: "python3 -I -c '" + check + "'", WorkingDirectory: workspacePath, Timeout: &timeout,
		ExtraEnv: map[string]string{"VAR_RELAY_CHECK_SOURCE": base64.StdEncoding.EncodeToString([]byte(source)), "VAR_RELAY_INSPECTOR": base64.StdEncoding.EncodeToString([]byte(relaypython.SourceInspector))},
	})
	if err != nil {
		return nil, fmt.Errorf("inspect relay.py: %w", err)
	}
	if result.CommandFailed() {
		return nil, fmt.Errorf("invalid relay.py: %s", strings.TrimSpace(result.Stderr+" "+result.Error))
	}
	lines := strings.Split(strings.TrimSpace(result.Stdout), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if json.Valid([]byte(lines[i])) {
			return json.RawMessage(lines[i]), nil
		}
	}
	return nil, fmt.Errorf("source inspector returned no JSON")
}

func initializePythonRelayWorkspace(ctx context.Context, workspacePath string) error {
	source := defaultPythonRelaySource
	if manifest, found, err := ReadWorkflowManifest(ctx, workspacePath); err != nil {
		return err
	} else if found && isDBOSRelay(manifest) {
		source = defaultNativeDBOSRelaySource
	}
	return initializePythonRelayWorkspaceWithSource(ctx, workspacePath, source)
}

func initializePythonRelayWorkspaceWithSource(ctx context.Context, workspacePath, source string) error {
	if err := createWorkspaceFolder(ctx, path.Join(workspacePath, "variables")); err != nil {
		return err
	}
	if _, exists, err := readFileFromWorkspace(ctx, path.Join(workspacePath, "relay.py")); err != nil {
		return err
	} else if !exists {
		if err := writeFileToWorkspace(ctx, path.Join(workspacePath, "relay.py"), source); err != nil {
			return err
		}

	}
	if _, exists, err := readFileFromWorkspace(ctx, path.Join(workspacePath, "variables/variables.json")); err != nil {
		return err
	} else if !exists {
		return writeFileToWorkspace(ctx, path.Join(workspacePath, "variables/variables.json"), `{"variables":[{"name":"INPUT","type":"object","value":"{}"}]}`)
	}
	return nil
}
