package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path"
	"strings"
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
		return fmt.Errorf("Flow needs relay.py")
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
		return fmt.Errorf("invalid Python source: %s", result.ValidationError)
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
	return inspectPythonSourceEnvironment(ctx, workspacePath, workspacePath, map[string]string{"VAR_RELAY_CHECK_SOURCE": base64.StdEncoding.EncodeToString([]byte(source))})
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
