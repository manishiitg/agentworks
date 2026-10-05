package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
)

// Crews, workflows and external connections call a Crew or workflow through
// one mechanism: its functions (crew_functions.go; `ask` is always there).
// Underneath, each caller has one internal trigger binding on the target,
// created on the first call. Project calls use the receiving main chat when
// the projects share an owner, otherwise a separate continuing conversation.
//
// Targets are any Crew on the server (Crews are shared read-write) and
// workflows the requester owns or can edit; workflow readers and Crew
// Run-mode readers cannot call.

const (
	triggerTargetDefaultTimeout = 60 * time.Minute
	triggerTargetMaxTimeout     = 24 * time.Hour
	triggerTargetResultLimit    = 16 * 1024
)

// triggerTargetPollInterval is how often a function call polls its target
// run. A package var so tests can shorten it.
var triggerTargetPollInterval = 10 * time.Second

type triggerTarget struct {
	Kind        string // triggerCallerCrew or triggerCallerWorkflow
	Path        string
	Label       string
	CrewID      string
	CrewProfile string
	// CrewOwner is the user whose workspace holds a Crew target. Crews are
	// shared server-wide, so it may differ from the requester.
	CrewOwner string
	Manifest  *WorkflowManifest
}

// ownerOr returns the Crew target's owner, falling back to userID.
func (t triggerTarget) ownerOr(userID string) string {
	if strings.TrimSpace(t.CrewOwner) != "" {
		return t.CrewOwner
	}
	return userID
}

func (t triggerTarget) describe() map[string]interface{} {
	return map[string]interface{}{"kind": t.Kind, "name": t.Label, "workspace_path": t.Path}
}

// triggerLinkCaller identifies the calling Crew or workflow.
type triggerLinkCaller struct {
	Stamp triggerCaller
	Label string
	Path  string
}

// resolveTriggerTarget finds one Crew on the server, or one workflow the user
// may open, by exact workspace path, ID, name, identity name, or folder name.
// A "crew:" / "workflow:" prefix (as inserted by #crew:/#workflow: tags)
// restricts the kind. Ambiguous names fail with the candidates listed.
func resolveTriggerTarget(ctx context.Context, claims *UserClaims, raw string) (triggerTarget, error) {
	query := strings.TrimSpace(raw)
	query = strings.TrimPrefix(query, "#")
	wantKind := ""
	lower := strings.ToLower(query)
	for _, kind := range []string{triggerCallerCrew, triggerCallerWorkflow} {
		if strings.HasPrefix(lower, kind+":") {
			wantKind = kind
			query = strings.TrimSpace(query[len(kind)+1:])
			break
		}
	}
	if query == "" {
		return triggerTarget{}, fmt.Errorf("target is required: pass a Crew or workflow name or its exact workspace_path")
	}
	userID := ""
	if claims != nil {
		userID = strings.TrimSpace(claims.UserID)
	}
	needle := strings.ToLower(strings.Trim(strings.TrimSpace(query), "/"))
	canonicalNeedle := strings.ToLower(strings.Trim(canonicalChatHistoryWorkspacePath(userID, query), "/"))
	matches := func(values ...string) bool {
		for _, value := range values {
			value = strings.ToLower(strings.Trim(strings.TrimSpace(value), "/"))
			if value != "" && (value == needle || value == canonicalNeedle) {
				return true
			}
		}
		return false
	}
	var found []triggerTarget
	if wantKind != triggerCallerWorkflow {
		crews, err := listAccessibleCrewProjects(ctx, userID, "")
		if err != nil {
			return triggerTarget{}, err
		}
		for _, crew := range crews {
			id, _ := crew["id"].(string)
			name, _ := crew["name"].(string)
			crewPath, _ := crew["workspace_path"].(string)
			identity := ""
			if value, ok := crew["identity"].(accessibleProjectIdentity); ok {
				identity = value.Name
			}
			if !matches(id, name, identity, crewPath, path.Base(crewPath)) {
				continue
			}
			owner := userID
			if crewOwner, ok := crewProjectOwnerID(crewPath); ok {
				owner = crewOwner
			}
			found = append(found, triggerTarget{Kind: triggerCallerCrew, Path: crewPath, Label: firstNonEmptyTrimmed(identity, name), CrewID: id, CrewProfile: "work", CrewOwner: owner})
		}
	}
	if wantKind != triggerCallerCrew {
		discovered, err := DiscoverWorkflowManifests(ctx)
		if err != nil {
			return triggerTarget{}, err
		}
		for _, workflow := range filterWorkflowManifestsForUser(claims, discovered) {
			workflowPath := strings.TrimSuffix(strings.TrimSpace(workflow.WorkspacePath), "/")
			if !matches(workflow.Manifest.ID, workflow.Manifest.Label, workflowPath, path.Base(workflowPath)) {
				continue
			}
			if workflow.MyAccess != WorkflowAccessOwner && workflow.MyAccess != WorkflowAccessWrite {
				return triggerTarget{}, fmt.Errorf("you only have read access to workflow %q; its owner or an editor must connect it", firstNonEmptyTrimmed(workflow.Manifest.Label, workflowPath))
			}
			found = append(found, triggerTarget{Kind: triggerCallerWorkflow, Path: workflowPath, Label: firstNonEmptyTrimmed(workflow.Manifest.Label, workflowPath), Manifest: workflow.Manifest})
		}
	}
	switch len(found) {
	case 0:
		return triggerTarget{}, fmt.Errorf("no Crew or workflow you can edit matches %q; call list_accessible_workflows to find its exact workspace_path", query)
	case 1:
		return found[0], nil
	default:
		names := make([]string, 0, len(found))
		for _, item := range found {
			names = append(names, item.Kind+" "+item.Label+" ("+item.Path+")")
		}
		sort.Strings(names)
		return triggerTarget{}, fmt.Errorf("%q matches more than one target: %s; pass the exact workspace_path", query, strings.Join(names, "; "))
	}
}

// sameTarget reports whether the caller is the target itself.
func (c triggerLinkCaller) isTarget(target triggerTarget) bool {
	if !strings.EqualFold(c.Stamp.Type, target.Kind) {
		return false
	}
	if target.Kind == triggerCallerCrew {
		return strings.TrimSpace(c.Stamp.ID) == strings.TrimSpace(target.CrewID) &&
			strings.EqualFold(normalizeInternalProfileID(c.Stamp.ProfileID), normalizeInternalProfileID(target.CrewProfile))
	}
	return target.Manifest != nil && strings.TrimSpace(c.Stamp.ID) == strings.TrimSpace(target.Manifest.ID)
}

// crewTargetMessage is the standard inbound trigger message on a Crew that
// another Crew or workflow calls.
func crewTargetMessage(caller triggerLinkCaller) string {
	kind := "Crew"
	switch caller.Stamp.Type {
	case triggerCallerWorkflow:
		kind = "workflow"
	case triggerCallerUser:
		kind = "external connection"
	}
	return fmt.Sprintf("The %s %q called you. %s The task is in the payload's `task` field; any extra input is under `payload`. Do the task, then end with a clear, self-contained final answer: it is returned to the caller.", kind, caller.Label, crewCallPrivateConversationNote)
}

// The conversation sentence of a call's instructions. Internal project calls
// swap it at delivery for where the turn really runs (crewCallMessage).
const crewCallPrivateConversationNote = "This conversation is yours and that caller's alone (earlier calls from it are above); your main chat is for people and does not see it."

// crewCallMessage fits a project call's stored instructions to where
// it runs: the called Crew's own chat (same owner) or a conversation for the
// calling person (another owner).
func crewCallMessage(stored string, sameOwner bool) string {
	note := "This conversation is only for this caller's calls to you (earlier ones are above); your main chat does not see it."
	if sameOwner {
		note = "This call runs in your main chat, so the people here see it too."
	}
	return strings.Replace(stored, crewCallPrivateConversationNote, note, 1)
}

// connectTriggerTarget reuses the internal binding on target that names this
// caller or creates the standard one. It returns the binding's trigger ID and
// whether it was created. Project ownership selects the conversation at delivery.
func (api *StreamingAPI) connectTriggerTarget(ctx context.Context, userID string, caller triggerLinkCaller, target triggerTarget) (string, bool, error) {
	if caller.isTarget(target) {
		return "", false, fmt.Errorf("a %s cannot connect to itself", target.Kind)
	}
	switch target.Kind {
	case triggerCallerCrew:
		if api.productSchedules == nil {
			return "", false, fmt.Errorf("Crew calls are unavailable")
		}
		if target.CrewProfile == codeproduct.ProfileID {
			return api.connectCodePeerTarget(ctx, userID, caller, target)
		}
		triggers, err := api.productSchedules.projectWebhookConfigs(ctx, target.ownerOr(userID), target.CrewProfile, target.CrewID)
		if err != nil {
			return "", false, err
		}
		for _, trigger := range triggers {
			if trigger.IsInternal() && trigger.Enabled && trigger.Caller.matchesAnyPresented(caller.Stamp) {
				return trigger.ID, false, nil
			}
		}
		stamp := caller.Stamp
		response, _, err := api.productSchedules.saveProductWebhookConfig(ctx, target.ownerOr(userID), productWebhookRequest{
			ProfileID: target.CrewProfile, ProjectID: target.CrewID,
			Name: "Called by " + caller.Label, Message: crewTargetMessage(caller),
			Enabled: true, RunDestination: runDestinationIsolated, Kind: triggerKindInternal, Caller: &stamp,
		}, "")
		if err != nil {
			return "", false, err
		}
		return response.ID, true, nil
	case triggerCallerWorkflow:
		return workflowTriggerBinding(ctx, api, target.Path, target.Manifest, caller.Stamp, "Called by "+caller.Label)
	default:
		return "", false, fmt.Errorf("unknown target kind %q", target.Kind)
	}
}

// triggerTargetRunState is one poll of a target run.
type triggerTargetRunState struct {
	Terminal bool
	Failed   bool
	Status   string
	Result   string
	Raw      interface{}
}

func (api *StreamingAPI) readTriggerTargetRun(ctx context.Context, userID string, caller triggerLinkCaller, target triggerTarget, triggerID, runID string) (triggerTargetRunState, error) {
	switch target.Kind {
	case triggerCallerCrew:
		status, err := api.productSchedules.getInternalProductTriggerRun(ctx, userID, target.CrewProfile, target.CrewID, triggerID, runID, caller.Stamp, target.CrewOwner)
		if err != nil {
			return triggerTargetRunState{}, crewWorkflowRunError(err)
		}
		state := triggerTargetRunState{Terminal: status.Terminal, Status: status.Status, Raw: status}
		state.Failed = status.Terminal && !strings.EqualFold(status.Status, "success")
		if state.Failed {
			state.Result = firstNonEmptyTrimmed(status.Error, "no error detail recorded")
		} else {
			state.Result = strings.TrimSpace(status.FinalResponse)
		}
		return state, nil
	case triggerCallerWorkflow:
		manifest, exists, err := ReadWorkflowManifest(ctx, target.Path)
		if err != nil || !exists || manifest == nil {
			return triggerTargetRunState{}, fmt.Errorf("workflow is unavailable")
		}
		result, err := api.scheduler.readInternalWorkflowTriggerRun(ctx, target.Path, manifest, triggerID, runID, caller.Stamp)
		if err != nil {
			return triggerTargetRunState{}, crewWorkflowRunError(err)
		}
		state := triggerTargetRunState{Terminal: result.Terminal, Status: result.Status, Raw: result}
		state.Failed = result.Terminal && (strings.TrimSpace(result.Error) != "" || workflowRunStatusFailed(result.Status))
		if manifest.Kind == "relay" && !state.Failed && len(result.Result) > 0 {
			state.Result = string(result.Result)
			return state, nil
		}
		state.Result = workflowCallResultJSON(result)
		return state, nil
	default:
		return triggerTargetRunState{}, fmt.Errorf("unknown target kind %q", target.Kind)
	}
}

// triggerTargetRunIsRunning says whether a target's run has started executing. A Crew run reports
// "running"; a workflow run reports where it is in the scheduler's own vocabulary, so a workflow-backed
// call stayed "queued" for its whole run (PLAT-535). Only a run that is still starting or waiting behind
// another one is not running yet.
func triggerTargetRunIsRunning(kind, status string) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	if kind != triggerCallerWorkflow {
		return status == "running"
	}
	switch status {
	case string(schedulerstate.StateWorkflowRunning), string(schedulerstate.StateWorkflowFinished),
		string(schedulerstate.StatePulseGate), string(schedulerstate.StatePulseModules), string(schedulerstate.StatePulseFinalizing):
		return true
	}
	return false
}

// workflowCallResultJSON is a workflow-backed call's result: the run's status, error and folder, and for
// each step only its file names, paths and sizes. The files themselves are read with read_file / get_run.
// It used to inline every step's output files, 400 KB for one review run (PLAT-536).
func workflowCallResultJSON(result webhookRunResult) string {
	type file struct {
		Name string `json:"name"`
		Path string `json:"path"`
		Size int64  `json:"size_bytes"`
	}
	type step struct {
		StepID string `json:"step_id"`
		Group  string `json:"group,omitempty"`
		Files  []file `json:"files"`
	}
	steps := make([]step, 0, len(result.Steps))
	for _, source := range result.Steps {
		slim := step{StepID: source.StepID, Group: source.Group, Files: make([]file, 0, len(source.Artifacts))}
		for _, artifact := range source.Artifacts {
			slim.Files = append(slim.Files, file{Name: artifact.Name, Path: artifact.Path, Size: artifact.Size})
		}
		steps = append(steps, slim)
	}
	doc := map[string]interface{}{"status": result.Status, "error": result.Error, "run_folder": result.RunFolder, "steps": steps,
		"note": "Step output files are listed by name and size. Read the ones you need from run_folder with read_file, or with get_run."}
	if result.Truncated {
		doc["truncated"] = true
	}
	encoded, _ := json.MarshalIndent(doc, "", "  ")
	return string(encoded)
}

func workflowRunStatusFailed(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "error", "canceled", "cancelled", "timeout", "timed_out", "stopped":
		return true
	}
	return false
}

func truncateTriggerTargetResult(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= triggerTargetResultLimit {
		return value
	}
	return value[:triggerTargetResultLimit] + "\n[result truncated]"
}

func triggerTargetTimeout(value interface{}) (time.Duration, error) {
	if value == nil {
		return triggerTargetDefaultTimeout, nil
	}
	minutes, ok := value.(float64)
	if !ok {
		if integer, isInt := value.(int); isInt {
			minutes, ok = float64(integer), true
		}
	}
	if !ok || minutes < 1 || time.Duration(minutes)*time.Minute > triggerTargetMaxTimeout {
		return 0, fmt.Errorf("timeout_minutes must be between 1 and %d", int(triggerTargetMaxTimeout/time.Minute))
	}
	return time.Duration(minutes) * time.Minute, nil
}

// sendToCrewTriggerRun delivers a follow-up message from the caller into the
// Crew conversation running the caller's call. It goes through the same
// /api/query handling as a person typing into that chat: a live coding CLI
// takes it as steering input, otherwise it waits in the durable turn queue and
// runs right after the current turn. Only while the run is still running.
func (api *StreamingAPI) sendToCrewTriggerRun(ctx context.Context, userID string, caller triggerLinkCaller, target triggerTarget, triggerID, runID, message string) (map[string]interface{}, error) {
	status, err := api.productSchedules.getInternalProductTriggerRun(ctx, userID, target.CrewProfile, target.CrewID, triggerID, runID, caller.Stamp, target.CrewOwner)
	if err != nil {
		return nil, crewWorkflowRunError(err)
	}
	if status.Terminal {
		return nil, fmt.Errorf("run %s already finished (%s); make a new call_function call", runID, status.Status)
	}
	if strings.TrimSpace(status.SessionID) == "" || !strings.EqualFold(status.Status, "running") {
		return nil, fmt.Errorf("run %s has not started yet (%s); wait for it to start or put the details in a new call", runID, status.Status)
	}
	profile, binding, manifest, trigger, err := api.productSchedules.findInternalProductTrigger(ctx, userID, target.CrewProfile, target.CrewID, triggerID, codePeerTriggerAccess{target.CrewOwner, caller.Stamp})
	if err != nil {
		return nil, crewWorkflowRunError(err)
	}
	ownerID := userID
	if owner, ok := crewProjectOwnerID(binding.WorkspacePath); ok {
		ownerID = owner
	}
	var conversationBinding productConversationBinding
	if profile.ID == codeproduct.ProfileID {
		ownerID = userID
		conversationBinding, err = codePeerRunBinding(ctx, userID, profile, target.CrewID, binding.WorkspacePath, trigger.ID, manifest.displayTitle()+" · "+trigger.Name)
	} else if strings.EqualFold(caller.Stamp.Type, triggerCallerCrew) {
		// Same routing as the call itself: the owner's own call is in the
		// Crew's chat, another person's in their own conversation.
		if guest := crewGuestCaller(userID, ownerID); guest != "" {
			conversationBinding, err = resolveIsolatedProjectAutomationBinding(ctx, ownerID, profile, target.CrewID, "trigger", crewCallIsolatedKey(trigger.ID, guest), manifest.displayTitle()+" · "+trigger.Name)
		} else {
			conversationBinding, err = resolveProductConversationBinding(ctx, ownerID, profile, target.CrewID)
		}
	} else if trigger.ownConversation() {
		conversationBinding, err = resolveIsolatedProjectAutomationBinding(ctx, ownerID, profile, target.CrewID, "trigger", trigger.ID, manifest.displayTitle()+" · "+trigger.Name)
	} else {
		conversationBinding, err = resolveProductConversationBinding(ctx, ownerID, profile, target.CrewID)
	}
	if err != nil {
		return nil, fmt.Errorf("Crew conversation is unavailable: %w", err)
	}
	conversation, err := defaultProductConversationRegistryStore().resolveOrCreate(ctx, ownerID, profile, conversationBinding, "")
	if err != nil {
		return nil, fmt.Errorf("Crew conversation is unavailable: %w", err)
	}
	if conversation.SessionID != status.SessionID {
		return nil, fmt.Errorf("the Crew's conversation has moved to a new session since run %s started; make a new call", runID)
	}
	framed := fmt.Sprintf("[Follow-up from %s %q about its task in run %s]\n\n%s", caller.Stamp.Type, caller.Label, runID, strings.TrimSpace(message))
	req, err := queryRequestForAgentProfileChat(profile, AgentProfileChatRequest{Message: framed}, conversation)
	if err != nil {
		return nil, err
	}
	reqMap, err := queryRequestToMap(req)
	if err != nil {
		return nil, err
	}
	reqMap["triggered_by"] = "manual"
	reqMap["triggered_by_label"] = "Follow-up from " + caller.Label
	reqMap["session_title"] = firstNonEmptyTrimmed(conversation.Title, profile.Name)
	body, err := json.Marshal(reqMap)
	if err != nil {
		return nil, err
	}
	// A fresh context for the target Crew's owner, like a queued turn: the
	// caller's context carries its own session and folder guard, and the
	// target's prompt variables (its product.json) were read under the
	// caller's folders and denied (issue #213, E3).
	httpReq, err := http.NewRequestWithContext(internalBotRequestContext(context.Background(), ownerID, reqMap), http.MethodPost, "/api/query", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("X-Session-ID", conversation.SessionID)
	httpReq.Header.Set("X-User-ID", ownerID)
	httpReq.Header.Set("Idempotency-Key", "call-followup-"+uuid.NewString())
	recorder := httptest.NewRecorder()
	api.handleQuery(recorder, httpReq)
	if recorder.Code >= 400 {
		return nil, fmt.Errorf("the Crew did not accept the message (%d): %s", recorder.Code, strings.TrimSpace(recorder.Body.String()))
	}
	var response QueryResponse
	_ = json.Unmarshal(recorder.Body.Bytes(), &response)
	delivery := firstNonEmptyTrimmed(response.DeliveryStatus, response.Status, "accepted")
	return map[string]interface{}{
		"target": target.describe(), "run_id": runID, "delivery_status": delivery,
		"note": "Delivered into the Crew's running conversation with you. If it was queued, it runs right after the current turn; the call's own result still arrives as usual.",
	}, nil
}

// crewTriggerLinkCaller stamps a calling Crew from its own product manifest.
func crewTriggerLinkCaller(workspacePath string) func(context.Context) (triggerLinkCaller, error) {
	return func(ctx context.Context) (triggerLinkCaller, error) {
		stamp, err := crewWorkflowRunCaller(ctx, workspacePath)
		if err != nil {
			return triggerLinkCaller{}, err
		}
		if strings.EqualFold(stamp.ProfileID, codeproduct.ProfileID) {
			// The target sees the call, but a private Code's title is not
			// disclosed just because it asked a Crew or workflow for help.
			return triggerLinkCaller{Stamp: stamp, Label: "private Code workspace", Path: workspacePath}, nil
		}
		label := path.Base(strings.TrimSuffix(strings.TrimSpace(workspacePath), "/"))
		if raw, exists, readErr := readFileFromWorkspace(ctx, strings.TrimSuffix(strings.TrimSpace(workspacePath), "/")+"/product.json"); readErr == nil && exists {
			var manifest productProjectManifest
			if json.Unmarshal([]byte(raw), &manifest) == nil {
				label = firstNonEmptyTrimmed(manifest.Identity.Name, manifest.Title, label)
			}
		}
		return triggerLinkCaller{Stamp: stamp, Label: label, Path: workspacePath}, nil
	}
}

// workflowTriggerLinkCaller stamps a calling workflow from its manifest.
func workflowTriggerLinkCaller(workspacePath string) func(context.Context) (triggerLinkCaller, error) {
	return func(ctx context.Context) (triggerLinkCaller, error) {
		manifest, exists, err := ReadWorkflowManifest(ctx, workspacePath)
		if err != nil || !exists || manifest == nil || strings.TrimSpace(manifest.ID) == "" {
			return triggerLinkCaller{}, fmt.Errorf("workflow is unavailable or access denied")
		}
		if _, err := authorizeWorkflowContextPaths(ctx, []string{workspacePath}); err != nil {
			return triggerLinkCaller{}, fmt.Errorf("workflow is unavailable or access denied")
		}
		return triggerLinkCaller{
			Stamp: triggerCaller{Type: triggerCallerWorkflow, ID: strings.TrimSpace(manifest.ID)},
			Label: firstNonEmptyTrimmed(manifest.Label, workspacePath), Path: workspacePath,
		}, nil
	}
}
