package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
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

func automationSourceEnvironment(raw []byte, typeCheck bool) map[string]string {
	encoded := base64.StdEncoding.EncodeToString(raw)
	environment := map[string]string{}
	if typeCheck {
		environment["VAR_AUTOMATION_TYPE_CHECK"] = "1"
	}
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

var pythonImportLine = regexp.MustCompile(`(?m)^[ \t]*(?:from[ \t]+(\.*)([\w.]*)[ \t]+import[ \t]+\(?([^\n#]+)|import[ \t]+([^\n#]+))`)

// automationReachableFiles is main.py and the files it imports, following imports from file to file. A big Crew's
// other scripts (each its own step, never imported) are left out, so its outline still fits the limit.
func automationReachableFiles(files map[string]string) map[string]string {
	if _, ok := files["main.py"]; !ok {
		return files
	}
	reached := map[string]string{}
	queue := []string{"main.py"}
	add := func(name string) {
		if source, ok := files[name]; ok {
			if _, done := reached[name]; !done {
				reached[name] = source
				queue = append(queue, name)
			}
		}
	}
	module := func(base, dotted string) {
		location := path.Join(append([]string{base}, strings.Split(strings.Trim(dotted, "."), ".")...)...)
		add(location + ".py")
		add(location + "/__init__.py")
		for parent := path.Dir(location); parent != "." && parent != "/"; parent = path.Dir(parent) {
			add(parent + "/__init__.py")
		}
	}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, match := range pythonImportLine.FindAllStringSubmatch(reached[current], -1) {
			if match[4] != "" { // import a, b.c as d
				for _, item := range strings.Split(match[4], ",") {
					if name := strings.Fields(item); len(name) > 0 {
						module(".", name[0])
					}
				}
				continue
			}
			base := "."
			if dots := len(match[1]); dots > 0 { // from .x import y: relative to this file
				base = path.Dir(current)
				for i := 1; i < dots; i++ {
					base = path.Dir(base)
				}
			}
			module(base, match[2])
			for _, item := range strings.Split(strings.NewReplacer("(", "", ")", "").Replace(match[3]), ",") {
				if name := strings.Fields(item); len(name) > 0 && match[2] != "" {
					module(base, match[2]+"."+name[0])
				} else if len(name) > 0 {
					module(base, name[0])
				}
			}
		}
	}
	return reached
}

func inspectPythonAutomationSource(ctx context.Context, workspacePath string, files map[string]string) (json.RawMessage, error) {
	raw, err := json.Marshal(files)
	if err != nil {
		return nil, fmt.Errorf("read automation source: %w", err)
	}
	typeCheck := true
	if len(raw) > automationOutlineLimit {
		// Too big as a whole: outline what main.py reaches; type notes are only for a whole-source read.
		reached := automationReachableFiles(files)
		if raw, err = json.Marshal(reached); err != nil || len(raw) > automationOutlineLimit {
			return json.RawMessage(`{"native":true,"nodes":[],"edges":[],"errors":["This code is too large to outline (400 KiB limit). You can still open and run it."]}`), nil
		}
		typeCheck = false
	}
	graph, err := inspectPythonSourceEnvironment(ctx, workspacePath, path.Join(workspacePath, "code"), automationSourceEnvironment(raw, typeCheck))
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
