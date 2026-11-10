package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/redaction"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/common"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/pythonruntime"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspace"
)

// automationOutlineLimit is the most code the outline reads. The code travels in environment variables, which the
// operating system limits to about 128 KiB each, so it is sent in pieces (automationSourceEnvironment). The whole
// environment must also stay under the system's total limit (1 MiB on macOS, where a laptop runs this server), so the
// outline stops at 400 KiB of code (about 530 KiB encoded).
const automationOutlineLimit = 400 * 1024

func automationSourceEnvironment(raw []byte) map[string]string {
	encoded := base64.StdEncoding.EncodeToString(raw)
	environment := map[string]string{"VAR_AUTOMATION_TYPE_CHECK": "1"}
	const piece = 96 * 1024
	count := 0
	for start := 0; start < len(encoded); start += piece {
		end := min(start+piece, len(encoded))
		environment[fmt.Sprintf("VAR_AUTOMATION_CHECK_FILES_%d", count)] = encoded[start:end]
		count++
	}
	environment["VAR_AUTOMATION_CHECK_FILES_COUNT"] = fmt.Sprint(count)
	return environment
}

func inspectPythonAutomationSource(ctx context.Context, workspacePath string, files map[string]string) (json.RawMessage, error) {
	raw, err := json.Marshal(files)
	if err != nil {
		return nil, fmt.Errorf("read automation source: %w", err)
	}
	if len(raw) > automationOutlineLimit {
		return json.RawMessage(`{"native":true,"nodes":[],"edges":[],"errors":["This code is too large to outline (400 KiB limit). You can still open and run it."]}`), nil
	}
	graph, err := inspectPythonSourceEnvironment(ctx, workspacePath, path.Join(workspacePath, "code"), automationSourceEnvironment(raw))
	if err == nil {
		rememberCrewStepCount(workspacePath, files, graph)
	}
	return graph, err
}

func inspectPythonSourceEnvironment(ctx context.Context, workspacePath, workingDirectory string, environment map[string]string) (json.RawMessage, error) {
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !found {
		return nil, fmt.Errorf("Automation manifest unavailable during source inspection")
	}
	sessionID := "source-inspection-" + uuid.NewString()
	// Inspection reads saved source and the sandbox's staged SDK, not runs,
	// credentials or another Crew's files. Network remains denied below.
	readPaths := []string{workingDirectory}
	common.SetSessionFolderGuard(sessionID, readPaths, nil)
	common.SetSessionSandbox(sessionID, true, true)
	defer workspace.ClearSessionShellConfig(sessionID)
	validationCtx := context.WithValue(ctx, common.ChatSessionIDKey, sessionID)
	client := workspace.NewClient(getWorkspaceAPIURL())
	client.UserID = workflowExecutionOwnerUserID(manifest)
	if IsMultiUserMode() && client.UserID == "" {
		return nil, fmt.Errorf("Flow execution owner unavailable")
	}
	const check = `import base64,os; exec(compile(base64.b64decode(os.environ["VAR_PYTHON_INSPECTOR"]),"<platform-inspector>","exec"))`
	timeout := 15
	environment["VAR_PYTHON_INSPECTOR"] = base64.StdEncoding.EncodeToString([]byte(pythonruntime.SourceInspector))
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
			var result map[string]interface{}
			if err := json.Unmarshal([]byte(lines[i]), &result); err != nil {
				return nil, err
			}
			if notes, ok := result["type_notes"].([]interface{}); ok {
				for _, value := range notes {
					if note, ok := value.(map[string]interface{}); ok {
						message, _ := note["message"].(string)
						text := []rune(redaction.Text(message))
						if len(text) > 500 {
							text = append(text[:499], '…')
						}
						note["message"] = string(text)
					}
				}
			}
			return json.Marshal(result)
		}
	}
	return nil, fmt.Errorf("source inspector returned no JSON")
}

// Saving has already succeeded. A missing checker, invalid source or inspection
// failure adds a note and never changes the save's success or execution rights.
func crewScriptSaveNotes(ctx context.Context, binding *crewExecutableBinding, file string) map[string]interface{} {
	if !strings.HasSuffix(file, ".py") {
		return nil
	}
	unavailable := map[string]interface{}{"notes": []string{"Type hints could not be checked this time. The script still saved and can run."}}
	owner, ok := crewProjectOwnerID(binding.CrewRoot)
	if !ok {
		return unavailable
	}
	source, err := (&crewWorkflowResources{userID: owner}).ResolveCrewScript(ctx, binding.Root, "")
	if err != nil {
		return unavailable
	}
	files := map[string]string{}
	for name, content := range source.Files {
		if strings.HasSuffix(name, ".py") {
			files[name] = string(content)
		}
	}
	graph, err := inspectPythonAutomationSource(ctx, binding.Root, files)
	if err != nil {
		return unavailable
	}
	var result map[string]interface{}
	if json.Unmarshal(graph, &result) != nil {
		return unavailable
	}
	return map[string]interface{}{"notes": result["notes"], "type_notes": result["type_notes"]}
}
