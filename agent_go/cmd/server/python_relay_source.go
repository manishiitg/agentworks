package server

import (
	"context"
	"encoding/base64"
	"fmt"
	"path"
	"strings"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
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
	if !exists || strings.TrimSpace(source) == "" {
		return fmt.Errorf("Relay needs relay.py defining async def run(INPUT, ctx)")
	}
	if len(source) > 64*1024 {
		return fmt.Errorf("relay.py exceeds 64 KiB")
	}
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !found {
		return fmt.Errorf("Relay manifest unavailable during source validation")
	}
	sessionID := "relay-syntax-" + uuid.NewString()
	common.SetSessionFolderGuard(sessionID, []string{workspacePath}, nil)
	common.SetSessionSandbox(sessionID, true, true)
	defer workspace.ClearSessionShellConfig(sessionID)
	validationCtx := context.WithValue(ctx, common.ChatSessionIDKey, sessionID)
	client := workspace.NewClient(getWorkspaceAPIURL())
	client.UserID = workflowExecutionOwnerUserID(manifest)
	if IsMultiUserMode() && client.UserID == "" {
		return fmt.Errorf("Relay execution owner unavailable")
	}
	const check = `import ast,base64,os; s=base64.b64decode(os.environ["VAR_RELAY_CHECK_SOURCE"]).decode("utf-8"); tree=ast.parse(s,filename="relay.py"); compile(tree,"relay.py","exec"); runs=[n for n in tree.body if isinstance(n,ast.AsyncFunctionDef) and n.name=="run"]; assert len(runs)==1,"relay.py must define one async def run(INPUT, ctx)"; a=runs[0].args; assert [v.arg for v in a.posonlyargs+a.args]==["INPUT","ctx"] and not a.vararg and not a.kwarg and not a.kwonlyargs,"run must accept exactly INPUT and ctx"`
	timeout := 15
	result, err := client.ExecuteShellCommand(validationCtx, workspace.ExecuteShellCommandParams{
		Command: "python3 -I -c '" + check + "'", WorkingDirectory: workspacePath, Timeout: &timeout,
		ExtraEnv: map[string]string{"VAR_RELAY_CHECK_SOURCE": base64.StdEncoding.EncodeToString([]byte(source))},
	})
	if err != nil {
		return fmt.Errorf("validate relay.py: %w", err)
	}
	if result.CommandFailed() {
		return fmt.Errorf("invalid relay.py: %s", strings.TrimSpace(result.Stderr+" "+result.Error))
	}
	return nil
}

func initializePythonRelayWorkspace(ctx context.Context, workspacePath string) error {
	if err := createWorkspaceFolder(ctx, path.Join(workspacePath, "variables")); err != nil {
		return err
	}
	if _, exists, err := readFileFromWorkspace(ctx, path.Join(workspacePath, "relay.py")); err != nil {
		return err
	} else if !exists {
		if err := writeFileToWorkspace(ctx, path.Join(workspacePath, "relay.py"), defaultPythonRelaySource); err != nil {
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
