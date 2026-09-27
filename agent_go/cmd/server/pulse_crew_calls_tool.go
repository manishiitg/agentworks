package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// read_crew_calls gives Pulse a read-only view of the Crew calls its own
// workflow made, so it can debug how a Crew handled them, not only what came
// back. A workflow reaches a Crew two ways: a function call from a step's
// agent (call_function / ask, recorded on the Crew side with the caller) and
// a Crew step (crew-run.json in the workflow's run folder). Both are Crew
// trigger runs with their own conversation.
//
// Boundaries: only calls whose caller is this workflow; the conversation is
// read only after the Crew side confirms that this workflow's binding made
// the run (a crew-run.json can be edited by a workflow agent, so its session
// is never trusted); never a trigger that runs in the Crew's main chat, which
// holds people's conversations. Nothing is written anywhere.

const (
	crewCallsListLimit       = 20
	crewCallsScanLimit       = 300
	crewCallsDefaultMessages = 40
	crewCallsMaxMessages     = 120
	crewCallsTextChars       = 1500
	crewCallsArgChars        = 300
	crewCallsToolChars       = 600
)

type pulseCrewCall struct {
	Kind          string `json:"kind"` // "function" or "crew_step"
	CallID        string `json:"call_id,omitempty"`
	RecordPath    string `json:"record_path,omitempty"`
	Crew          string `json:"crew,omitempty"`
	CrewProfileID string `json:"crew_profile_id,omitempty"`
	CrewProjectID string `json:"crew_project_id"`
	TriggerID     string `json:"trigger_id"`
	RunID         string `json:"run_id"`
	Function      string `json:"function,omitempty"`
	StepID        string `json:"step_id,omitempty"`
	Status        string `json:"status"`
	Error         string `json:"error,omitempty"`
	At            string `json:"at,omitempty"`
	userID        string
}

func createCrewCallsTool() (llmtypes.Tool, func(context.Context, map[string]interface{}) (string, error)) {
	tool := llmtypes.Tool{Type: "function", Function: &llmtypes.FunctionDefinition{
		Name: "read_crew_calls",
		Description: "Read-only view of the Crew calls this workflow made, to debug how a Crew handled them. operation=list returns recent calls (function calls from step agents and Crew steps) with status, errors and timing. operation=read with a call_id (function call) or record_path (a Crew step's runs/.../crew-run.json) returns that call's own conversation with the Crew: its messages, tool calls and tool results. " +
			"Only calls made by this workflow, only after the Crew confirms it; never the Crew's main chat. Use it to find whether a problem is in this workflow (instruction, inputs, timeout) or inside the Crew (its skills, memory or setup, which belong to the Crew's owner).",
		Parameters: llmtypes.NewParameters(map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"workspace_path": map[string]interface{}{"type": "string", "description": "Optional. This workflow's own path; any other workflow is refused."},
				"operation":      map[string]interface{}{"type": "string", "enum": []string{"list", "read"}},
				"call_id":        map[string]interface{}{"type": "string", "description": "read: a function call ID from list (fn-...)."},
				"record_path":    map[string]interface{}{"type": "string", "description": "read: a Crew step record from list, workflow-relative, e.g. runs/iteration-5-sched/default/execution/step-review/crew-run.json."},
				"max_messages":   map[string]interface{}{"type": "integer", "minimum": 1, "maximum": crewCallsMaxMessages, "description": "read: newest messages to return (default 40)."},
			},
			"required": []string{"operation"},
		}),
	}}
	return tool, runReadCrewCalls
}

func runReadCrewCalls(ctx context.Context, args map[string]interface{}) (string, error) {
	api := pulsePlatformAPI
	if api == nil || api.productSchedules == nil {
		return "", fmt.Errorf("Crew calls are unavailable in this process")
	}
	// The workflow and principal come from the calling session, never from
	// the arguments (pulseToolScope); both list and read need read access.
	requested, _ := args["workspace_path"].(string)
	workspacePath, claims, err := api.pulseToolScope(ctx, requested, false)
	if err != nil {
		return "", err
	}
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !found || strings.TrimSpace(manifest.ID) == "" {
		return "", fmt.Errorf("cannot read this session's workflow")
	}
	operation, _ := args["operation"].(string)
	switch strings.TrimSpace(operation) {
	case "list":
		calls := append(listWorkflowFunctionCalls(manifest.ID), listWorkflowCrewStepCalls(workspacePath)...)
		sort.SliceStable(calls, func(i, j int) bool { return calls[i].At > calls[j].At })
		if len(calls) > crewCallsListLimit {
			calls = calls[:crewCallsListLimit]
		}
		encoded, _ := json.MarshalIndent(map[string]interface{}{"workflow_id": manifest.ID, "calls": calls}, "", "  ")
		return string(encoded), nil
	case "read":
		call, err := resolveWorkflowCrewCall(ctx, workspacePath, manifest.ID, claims.UserID, args)
		if err != nil {
			return "", err
		}
		maxMessages := crewCallsDefaultMessages
		if raw, ok := args["max_messages"].(float64); ok && raw >= 1 {
			maxMessages = int(raw)
			if maxMessages > crewCallsMaxMessages {
				maxMessages = crewCallsMaxMessages
			}
		}
		return readVerifiedCrewCall(ctx, api, manifest.ID, claims.UserID, call, maxMessages)
	default:
		return "", fmt.Errorf("operation must be list or read")
	}
}

// listWorkflowFunctionCalls returns recent function calls this workflow made,
// from the server-written call records (a workflow agent cannot write them).
func listWorkflowFunctionCalls(workflowID string) []pulseCrewCall {
	dir := filepath.Join(fsutil.WorkspaceDocsRoot(), "_system", "function_calls")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type file struct {
		id  string
		mod time.Time
	}
	files := make([]file, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		files = append(files, file{strings.TrimSuffix(entry.Name(), ".json"), info.ModTime()})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	if len(files) > crewCallsScanLimit {
		files = files[:crewCallsScanLimit]
	}
	var calls []pulseCrewCall
	for _, f := range files {
		if len(calls) >= crewCallsListLimit {
			break
		}
		call := lookupCrewFunctionCall(f.id)
		if call == nil {
			continue
		}
		if ref, ok := functionCallRef(call, workflowID); ok {
			calls = append(calls, ref)
		}
	}
	return calls
}

// functionCallRef describes a function call when this workflow made it to a
// Crew.
func functionCallRef(call *crewFunctionCall, workflowID string) (pulseCrewCall, bool) {
	call.mu.Lock()
	defer call.mu.Unlock()
	if call.CallerKind != triggerCallerWorkflow || call.CallerID != workflowID || call.TargetKind != triggerCallerCrew {
		return pulseCrewCall{}, false
	}
	runID := call.RunID
	if runID == "" && len(call.RunIDs) > 0 {
		runID = call.RunIDs[len(call.RunIDs)-1]
	}
	return pulseCrewCall{
		Kind: "function", CallID: call.ID, Crew: call.TargetLabel, CrewProjectID: call.TargetID,
		TriggerID: call.TriggerID, RunID: runID, Function: call.Function, Status: call.Status,
		Error: call.Error, At: call.UpdatedAt.UTC().Format(time.RFC3339), userID: call.UserID,
	}, true
}

// listWorkflowCrewStepCalls returns this workflow's newest Crew step records.
func listWorkflowCrewStepCalls(workspacePath string) []pulseCrewCall {
	root := filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(workspacePath))
	runs := filepath.Join(root, "runs")
	type file struct {
		rel string
		mod time.Time
	}
	var files []file
	_ = filepath.WalkDir(runs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if strings.Count(strings.TrimPrefix(p, runs), string(filepath.Separator)) > 6 {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "crew-run.json" {
			return nil
		}
		if info, err := d.Info(); err == nil {
			rel, _ := filepath.Rel(root, p)
			files = append(files, file{filepath.ToSlash(rel), info.ModTime()})
		}
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	var calls []pulseCrewCall
	for _, f := range files {
		if len(calls) >= crewCallsListLimit {
			break
		}
		if ref, err := crewStepRecordRef(context.Background(), workspacePath, f.rel); err == nil {
			calls = append(calls, ref)
		}
	}
	return calls
}

// crewStepRecordRef reads a Crew step record from this workflow's runs.
func crewStepRecordRef(ctx context.Context, workspacePath, recordPath string) (pulseCrewCall, error) {
	recordPath = path.Clean(strings.TrimSpace(recordPath))
	if !strings.HasPrefix(recordPath, "runs/") || path.Base(recordPath) != "crew-run.json" || strings.Contains(recordPath, "..") {
		return pulseCrewCall{}, fmt.Errorf("record_path must be a runs/.../crew-run.json in this workflow")
	}
	raw, exists, err := readFileFromWorkspace(ctx, workspacePath+"/"+recordPath)
	if err != nil || !exists {
		return pulseCrewCall{}, fmt.Errorf("Crew step record %q not found", recordPath)
	}
	var record struct {
		StepID        string    `json:"step_id"`
		CrewProfileID string    `json:"crew_profile_id"`
		CrewProjectID string    `json:"crew_project_id"`
		TriggerID     string    `json:"trigger_id"`
		CrewRunID     string    `json:"crew_run_id"`
		Status        string    `json:"status"`
		Error         string    `json:"error"`
		StartedAt     time.Time `json:"started_at"`
	}
	if err := json.Unmarshal([]byte(raw), &record); err != nil || record.CrewRunID == "" {
		return pulseCrewCall{}, fmt.Errorf("Crew step record %q is unreadable", recordPath)
	}
	return pulseCrewCall{
		Kind: "crew_step", RecordPath: recordPath, CrewProfileID: record.CrewProfileID, CrewProjectID: record.CrewProjectID,
		TriggerID: record.TriggerID, RunID: record.CrewRunID, StepID: record.StepID, Status: record.Status,
		Error: record.Error, At: record.StartedAt.UTC().Format(time.RFC3339),
	}, nil
}

func resolveWorkflowCrewCall(ctx context.Context, workspacePath, workflowID, ownerID string, args map[string]interface{}) (pulseCrewCall, error) {
	if id, _ := args["call_id"].(string); strings.TrimSpace(id) != "" {
		call := lookupCrewFunctionCall(strings.TrimSpace(id))
		if call == nil {
			return pulseCrewCall{}, fmt.Errorf("function call %q not found", id)
		}
		ref, ok := functionCallRef(call, workflowID)
		if !ok {
			return pulseCrewCall{}, fmt.Errorf("function call %q was not made by this workflow to a Crew", id)
		}
		return ref, nil
	}
	if recordPath, _ := args["record_path"].(string); strings.TrimSpace(recordPath) != "" {
		ref, err := crewStepRecordRef(ctx, workspacePath, recordPath)
		if err != nil {
			return pulseCrewCall{}, err
		}
		ref.userID = ownerID
		return ref, nil
	}
	return pulseCrewCall{}, fmt.Errorf("read needs a call_id or a record_path from list")
}

// readVerifiedCrewCall confirms with the Crew side that this workflow's
// binding made the run, then returns that run's own conversation.
func readVerifiedCrewCall(ctx context.Context, api *StreamingAPI, workflowID, principalID string, call pulseCrewCall, maxMessages int) (string, error) {
	profileID := normalizeInternalProfileID(call.CrewProfileID)
	userID := strings.TrimSpace(call.userID)
	if userID == "" {
		userID = GetDefaultUserID()
	}
	_, binding, manifest, trigger, err := api.productSchedules.findInternalProductTrigger(ctx, userID, profileID, call.CrewProjectID, call.TriggerID)
	if err != nil {
		return "", fmt.Errorf("cannot resolve the Crew trigger for this call: %w", err)
	}
	if !trigger.ownConversation() {
		return "", fmt.Errorf("this trigger runs in the Crew's main chat, which is private; only its status is available")
	}
	status, err := api.productSchedules.getInternalProductTriggerRun(ctx, userID, profileID, call.CrewProjectID, call.TriggerID, call.RunID, triggerCaller{Type: triggerCallerWorkflow, ID: workflowID})
	if err != nil {
		return "", fmt.Errorf("the Crew does not confirm this run as this workflow's call: %w", err)
	}
	out := map[string]interface{}{
		"call": call,
		"crew": map[string]interface{}{"name": firstNonEmptyTrimmed(manifest.Title, manifest.Label, manifest.Identity.Name), "project_id": call.CrewProjectID},
		"run":  status,
	}
	sessionID := strings.TrimSpace(status.SessionID)
	// Second guard: never the Crew's main chat, whatever the trigger says.
	if sessionID != "" && sessionID == strings.TrimSpace(manifest.SessionID) {
		return "", fmt.Errorf("this run is in the Crew's main chat, which is private; only its status is available")
	}
	// Fail closed on an unknown Crew owner, and show another person's Crew
	// only as its run status and final answer: its internal tool calls and
	// results stay with its owner.
	crewOwnerID, ok := crewProjectOwnerID(binding.WorkspacePath)
	crewOwnerID = strings.TrimSpace(crewOwnerID)
	if !ok || crewOwnerID == "" {
		return "", fmt.Errorf("cannot determine who owns this Crew; its conversation is not available")
	}
	switch {
	case sessionID == "":
		out["conversation"] = "no conversation recorded for this run yet"
	case crewOwnerID != strings.TrimSpace(principalID):
		out["conversation"] = "this Crew belongs to someone else; only its run status and final answer are shown"
	default:
		raw, readErr := ReadChatHistoryConversation(crewOwnerID, sessionID, binding.WorkspacePath)
		if readErr != nil {
			out["conversation"] = "conversation unavailable: " + readErr.Error()
		} else {
			out["conversation"] = renderCrewConversation(raw, maxMessages)
		}
	}
	encoded, err := json.MarshalIndent(out, "", "  ")
	return string(encoded), err
}

type crewConversationMessage struct {
	Role        string   `json:"role"`
	Text        string   `json:"text,omitempty"`
	ToolCalls   []string `json:"tool_calls,omitempty"`
	ToolName    string   `json:"tool,omitempty"`
	ToolResult  string   `json:"tool_result,omitempty"`
	ToolIsError bool     `json:"tool_error,omitempty"`
}

func clipText(text string, limit int) string {
	text = strings.TrimSpace(text)
	if len([]rune(text)) <= limit {
		return text
	}
	return string([]rune(text)[:limit]) + "…"
}

// renderCrewConversation keeps the newest messages, without the system prompt,
// with long text, tool arguments and tool results clipped.
func renderCrewConversation(raw json.RawMessage, maxMessages int) map[string]interface{} {
	var doc struct {
		History []struct {
			Role  string                   `json:"Role"`
			Parts []map[string]interface{} `json:"Parts"`
		} `json:"conversation_history"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return map[string]interface{}{"error": "conversation is unreadable"}
	}
	var messages []crewConversationMessage
	for _, entry := range doc.History {
		if strings.EqualFold(entry.Role, "system") {
			continue
		}
		msg := crewConversationMessage{Role: entry.Role}
		for _, part := range entry.Parts {
			if text, ok := part["Text"].(string); ok && strings.TrimSpace(text) != "" {
				msg.Text = strings.TrimSpace(msg.Text + "\n" + clipText(text, crewCallsTextChars))
			}
			if callPart, ok := part["FunctionCall"].(map[string]interface{}); ok {
				name, _ := callPart["Name"].(string)
				arguments, _ := callPart["Arguments"].(string)
				msg.ToolCalls = append(msg.ToolCalls, strings.TrimSpace(name+" "+clipText(arguments, crewCallsArgChars)))
			}
			if content, ok := part["Content"].(string); ok {
				msg.ToolName, _ = part["Name"].(string)
				msg.ToolResult = clipText(content, crewCallsToolChars)
				msg.ToolIsError, _ = part["IsError"].(bool)
			}
		}
		if msg.Text != "" || len(msg.ToolCalls) > 0 || msg.ToolResult != "" {
			messages = append(messages, msg)
		}
	}
	total := len(messages)
	if total > maxMessages {
		messages = messages[total-maxMessages:]
	}
	return map[string]interface{}{"messages_total": total, "messages_shown": len(messages), "messages": messages}
}
