package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	mcpagent "github.com/manishiitg/mcpagent/agent"
)

// Scripted routes as named tools (PLAT-432). Every saved scripted route of an
// Agent step is also offered to that agent as its own function, named after the
// route, with its script_parameters as the native input schema. Calling it runs
// the same route code as call_scripted_sub_agent (same validation, call folder,
// sandbox and history), always synchronously, and returns the script's
// route_result.json when it wrote one. call_scripted_sub_agent stays for existing
// workflows.

// ScriptedRouteResultFile is the file a scripted route writes in its
// STEP_OUTPUT_DIR to hand a JSON value back to the agent that called it.
const ScriptedRouteResultFile = "route_result.json"

const maxScriptedRouteResultBytes = 1 << 20

var scriptedRouteToolNameInvalid = regexp.MustCompile(`[^a-z0-9_]+`)

// scriptedRouteToolName turns a route id into a function name providers accept.
func scriptedRouteToolName(routeID string) string {
	name := strings.Trim(scriptedRouteToolNameInvalid.ReplaceAllString(strings.ToLower(strings.TrimSpace(routeID)), "_"), "_")
	if name == "" || name[0] < 'a' || name[0] > 'z' {
		name = "route_" + name
	}
	if len(name) > 64 {
		name = strings.TrimRight(name[:64], "_")
	}
	return name
}

// scriptedRouteInputSchema is the native JSON schema for a route's declared
// parameters. Unknown fields are refused, as call_scripted_sub_agent does.
func scriptedRouteInputSchema(definitions map[string]ScriptParameterDefinition) map[string]interface{} {
	properties := map[string]interface{}{}
	required := []string{}
	names := make([]string, 0, len(definitions))
	for name := range definitions {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		definition := definitions[name]
		property := map[string]interface{}{"type": definition.Type, "description": definition.Description}
		if definition.Type == "array" {
			property["items"] = map[string]interface{}{}
		}
		if len(definition.Enum) > 0 {
			property["enum"] = definition.Enum
		}
		if definition.Default != nil {
			property["default"] = definition.Default
		}
		properties[name] = property
		if definition.Required && definition.Default == nil {
			required = append(required, name)
		}
	}
	schema := map[string]interface{}{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

type scriptedRouteResultCaptureKey struct{}

// scriptedRouteResultCapture receives the route's route_result.json from
// executePredefinedSubAgent, so the named tool can return it unwrapped.
type scriptedRouteResultCapture struct{ json string }

// readScriptedRouteResult returns the route's route_result.json when it is
// valid JSON of a bounded size. A missing file is no result. A file that is too
// big, not JSON, or not a plain file inside the route's own output folder is
// reported as an error; the caller logs it and falls back to the run summary
// rather than failing a route that otherwise succeeded.
//
// The script wrote this file, and this read runs in the agent server, outside
// the script's sandbox: it must never follow a link out of the assigned folder
// (PLAT-443). confineRoot is the folder the output directory must resolve
// inside (the workflow's own folder); the file must be a regular file whose
// resolved path sits directly in the resolved output directory, and what is read
// is the very file that was checked, capped even if it grows meanwhile.
func readScriptedRouteResult(confineRoot, outputDir string) (string, error) {
	root, err := filepath.EvalSymlinks(confineRoot)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", confineRoot, err)
	}
	dir, err := filepath.EvalSymlinks(outputDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	if rel, relErr := filepath.Rel(root, dir); relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("the route output folder resolves outside its workflow")
	}
	path := filepath.Join(dir, ScriptedRouteResultFile)
	linkInfo, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	if !linkInfo.Mode().IsRegular() {
		return "", fmt.Errorf("%s must be a regular file written by the script, not a link or special file", ScriptedRouteResultFile)
	}
	file, err := os.Open(path) // #nosec G304 -- resolved inside the confined output folder
	if err != nil {
		return "", err
	}
	defer file.Close()
	openInfo, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(linkInfo, openInfo) {
		return "", fmt.Errorf("%s changed while it was being read", ScriptedRouteResultFile)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxScriptedRouteResultBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxScriptedRouteResultBytes {
		return "", fmt.Errorf("%s is larger than %d bytes; a route result is at most that", ScriptedRouteResultFile, maxScriptedRouteResultBytes)
	}
	trimmed := strings.TrimSpace(string(data))
	if !json.Valid([]byte(trimmed)) {
		return "", fmt.Errorf("%s is not valid JSON", ScriptedRouteResultFile)
	}
	return trimmed, nil
}

// scriptedRouteOutputDir is the absolute STEP_OUTPUT_DIR of one route call.
func (hcpo *StepBasedWorkflowOrchestrator) scriptedRouteOutputDir(subAgentStepPath string) string {
	return filepath.Join(GetPromptDocsRoot(), hcpo.GetWorkspacePath(), "runs", hcpo.selectedRunFolder, "execution", getArtifactFolderName("", subAgentStepPath))
}

// scriptedRouteSourceReadPaths grants an Agent step read access to the saved
// code of its own scripted routes. Running a route loads its main.py in the
// calling agent's request context, so without this grant the saved script looked
// missing and the route fell back to writing a new one (PLAT-432).
func (hcpo *StepBasedWorkflowOrchestrator) scriptedRouteSourceReadPaths(execCtx *SubAgentExecutionContext) []string {
	if execCtx == nil || execCtx.OrchestratorStep == nil {
		return nil
	}
	var paths []string
	for _, route := range execCtx.OrchestratorStep.PredefinedRoutes {
		if route.SubAgentStep == nil || !isScriptedStep(route.SubAgentStep, getAgentConfigs(route.SubAgentStep)) {
			continue
		}
		paths = append(paths, filepath.ToSlash(filepath.Join(hcpo.GetWorkspacePath(), hcpo.scriptedSourceDir(route.SubAgentStep.GetID()))))
	}
	return paths
}

var scriptedRouteToolCalls atomic.Uint64

// scriptedRouteDirectTools builds one named tool per saved scripted route of the
// step. reserved holds names already taken by the agent's tools. In an ordinary
// workflow a route whose name is taken is skipped (it stays reachable through
// call_scripted_sub_agent). An authored agent has no such fallback, so a route it
// cannot register is an error: the agent must never be told about a tool it does
// not have (PLAT-444). The names actually registered are recorded on execCtx.
func (hcpo *StepBasedWorkflowOrchestrator) scriptedRouteDirectTools(execCtx *SubAgentExecutionContext, reserved map[string]bool) ([]mcpagent.ToolDefinition, error) {
	if execCtx == nil || execCtx.OrchestratorStep == nil {
		return nil, nil
	}
	authored := execCtx.OrchestratorStep.AuthoredPrompt
	execCtx.ScriptToolNames = map[string]string{}
	var definitions []mcpagent.ToolDefinition
	for _, route := range execCtx.OrchestratorStep.PredefinedRoutes {
		if route.SubAgentStep == nil || !isScriptedStep(route.SubAgentStep, getAgentConfigs(route.SubAgentStep)) {
			continue
		}
		parameters := scriptedParameterDefinitions(route.SubAgentStep)
		fields := route.SubAgentStep.GetCommonFields()
		if err := validateScriptParameterContract(&fields); err != nil {
			if authored {
				return nil, fmt.Errorf("script tool %q cannot be registered: %w", route.RouteID, err)
			}
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Scripted route %q is not offered as a named tool: %v", route.RouteID, err))
			continue
		}
		name := scriptedRouteToolName(route.RouteID)
		if reserved[name] {
			if authored {
				return nil, fmt.Errorf("script tool %q cannot be registered as %q: that name is already a tool of this agent (a platform tool or another route); rename the route", route.RouteID, name)
			}
			hcpo.GetLogger().Warn(fmt.Sprintf("⚠️ Scripted route %q is not offered as named tool %q: the name is taken; call_scripted_sub_agent still reaches it", route.RouteID, name))
			continue
		}
		reserved[name] = true
		execCtx.ScriptToolNames[route.RouteID] = name
		routeID := route.RouteID
		inputSchema := scriptedRouteInputSchema(parameters)
		if len(fields.ScriptParametersSchema) > 0 {
			inputSchema = fields.ScriptParametersSchema
		}
		description := strings.TrimSpace(ResolveVariables(route.SubAgentStep.GetDescription(), hcpo.variableValues))
		if condition := strings.TrimSpace(ResolveVariables(route.Condition, hcpo.variableValues)); condition != "" {
			description = condition + "\n\n" + description
		}
		if len(description) > 3000 {
			description = description[:3000] + "..."
		}
		description = strings.TrimSpace(description) + "\n\nRuns this workflow's saved script for route " + routeID + " and returns its result (the JSON it wrote to " + ScriptedRouteResultFile + ", or its completion summary). Same as call_scripted_sub_agent(route_id=\"" + routeID + "\"), and it waits for the result."
		inner := func(ctx context.Context, args map[string]interface{}) (string, error) {
			supplied := make(map[string]interface{}, len(args))
			for key, value := range args {
				supplied[key] = value
			}
			ctx = context.WithValue(ctx, virtualtools.SubAgentParametersKey, supplied)
			ctx = context.WithValue(ctx, virtualtools.ScriptedSubAgentInvocationKey, true)
			capture := &scriptedRouteResultCapture{}
			ctx = context.WithValue(ctx, scriptedRouteResultCaptureKey{}, capture)
			taskID := fmt.Sprintf("%s-%d", name, scriptedRouteToolCalls.Add(1))
			result, err := hcpo.createExecutePredefinedSubAgentSyncFunc(execCtx)(ctx, routeID, taskID, "")
			if err != nil {
				return "", err
			}
			if capture.json != "" {
				return capture.json, nil
			}
			return result, nil
		}
		definitions = append(definitions, mcpagent.ToolDefinition{
			Name:         name,
			Description:  description,
			InputSchema:  inputSchema,
			Execute:      hcpo.wrapSubAgentToolExecutor(inner, execCtx),
			Timeout:      30 * time.Minute,
			DisplayGroup: virtualtools.GetSubAgentToolCategory(),
		})
	}
	return definitions, nil
}
