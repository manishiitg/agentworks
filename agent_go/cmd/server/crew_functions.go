package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	virtualtools "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server/virtual-tools"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/codeproduct"
	storeEvents "github.com/manishiitg/coding-agent-loop/agent_go/internal/events"
	todo_creation_human "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workspaceref"
)

// Crew functions (PLAT-357): a Crew or workflow declares typed functions in
// <target>/functions.json; other Crews and workflows call them with
// call_function (or a generated per-function tool). Arguments and results
// are validated against the declared schemas. A call rides the existing
// internal-trigger binding (see trigger_link_tools.go), so it gets the same
// durable run, turn queue, and auto-notification path as any internal trigger.

const (
	crewFunctionsFileName          = "functions.json"
	crewFunctionMaxDepth           = 4
	crewFunctionChainBudget        = 20
	crewFunctionProgressKeep       = 10
	crewFunctionMaxInvalidResults  = 2
	crewFunctionEvent              = "agentworks.function_call"
	crewFunctionToolCategory       = "crew_function_tools"
	crewFunctionActivityTextLimit  = 600
	crewFunctionActivityEventsScan = 80
)

// crewFunctionFastWait is the longest call_function will wait for a result
// (wait_seconds) before returning {status:"running"} and notifying later.
// The default is no wait: functions are agentic and usually take minutes,
// and a caller whose request is cut short (a shell curl with its own
// timeout) never saw the call_id and called again (RTS 2026-09-27). A var for
// tests.
var crewFunctionFastWait = 120 * time.Second

// crewFunctionWait reads call_function's optional wait_seconds, capped at
// crewFunctionFastWait; absent means return at once.
func crewFunctionWait(raw interface{}) time.Duration {
	var seconds float64
	switch value := raw.(type) {
	case float64:
		seconds = value
	case int:
		seconds = float64(value)
	case json.Number:
		seconds, _ = value.Float64()
	}
	if seconds <= 0 {
		return 0
	}
	wait := time.Duration(seconds * float64(time.Second))
	if wait > crewFunctionFastWait {
		return crewFunctionFastWait
	}
	return wait
}

var crewFunctionNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)

type crewFunction struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description,omitempty"`
	InputSchema  map[string]interface{} `json:"input_schema,omitempty"`
	ResultSchema map[string]interface{} `json:"result_schema,omitempty"`
	Instructions string                 `json:"instructions,omitempty"`
	CreatedBy    string                 `json:"created_by,omitempty"`
	CreatedAt    time.Time              `json:"created_at"`
	UpdatedAt    time.Time              `json:"updated_at"`
	// TriggerID is set on a workflow function: the function trigger it runs.
	TriggerID string `json:"trigger_id,omitempty"`
}

type crewFunctionsDoc struct {
	Version   int            `json:"version"`
	Functions []crewFunction `json:"functions"`
}

// crewFunctionRoot is the physical folder holding a target's functions. A
// Crew the requester owns may be addressed by its logical Chats/... path;
// store under the owner's physical tree so every caller sees one file.
func crewFunctionRoot(ctx context.Context, target triggerTarget) string {
	root := strings.TrimSuffix(strings.TrimSpace(target.Path), "/")
	if target.Kind == triggerCallerCrew {
		return agentProfileRuntimeWorkspace(target.ownerOr(GetUserIDFromContext(ctx)), root)
	}
	return root
}

func crewFunctionsPath(ctx context.Context, target triggerTarget) string {
	return crewFunctionRoot(ctx, target) + "/" + crewFunctionsFileName
}

func readCrewFunctions(ctx context.Context, target triggerTarget) ([]crewFunction, error) {
	raw, exists, err := readFileFromWorkspace(ctx, crewFunctionsPath(ctx, target))
	if err != nil {
		return nil, fmt.Errorf("read functions of %s %q: %w", target.Kind, target.Label, err)
	}
	if !exists || strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var doc crewFunctionsDoc
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, fmt.Errorf("functions.json of %s %q is not valid JSON: %w", target.Kind, target.Label, err)
	}
	return doc.Functions, nil
}

func writeCrewFunctions(ctx context.Context, target triggerTarget, functions []crewFunction) error {
	sort.Slice(functions, func(i, j int) bool { return functions[i].Name < functions[j].Name })
	encoded, err := json.MarshalIndent(crewFunctionsDoc{Version: 1, Functions: functions}, "", "  ")
	if err != nil {
		return err
	}
	return writeFileToWorkspace(ctx, crewFunctionsPath(ctx, target), string(encoded)+"\n")
}

// crewFunctionSpec is one function definition as its author sends it. The
// define_function tool and the external Crew authoring tools share it, so
// both apply the same checks.
type crewFunctionSpec struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	Instructions string                 `json:"instructions"`
	InputSchema  map[string]interface{} `json:"input_schema,omitempty"`
	ResultSchema map[string]interface{} `json:"result_schema,omitempty"`
}

func (spec crewFunctionSpec) normalized() crewFunctionSpec {
	spec.Name = strings.TrimSpace(spec.Name)
	spec.Description = strings.TrimSpace(spec.Description)
	spec.Instructions = strings.TrimSpace(spec.Instructions)
	return spec
}

func (spec crewFunctionSpec) validate() error {
	if !crewFunctionNamePattern.MatchString(spec.Name) {
		return fmt.Errorf("name must be snake_case: a lowercase letter, then lowercase letters, digits or _ (max 48)")
	}
	if spec.Description == "" || spec.Instructions == "" {
		return fmt.Errorf("description and instructions are required")
	}
	if len(spec.InputSchema) > 0 && spec.InputSchema["type"] != "object" {
		return fmt.Errorf("input_schema must have type object (named arguments)")
	}
	if err := checkCrewFunctionSchema(spec.InputSchema, "input_schema"); err != nil {
		return err
	}
	return checkCrewFunctionSchema(spec.ResultSchema, "result_schema")
}

// upsertCrewFunction declares a validated spec, updating a same-named
// function in place (keeping its creator and creation time).
func upsertCrewFunction(functions []crewFunction, spec crewFunctionSpec, creator string, now time.Time) ([]crewFunction, bool) {
	for i := range functions {
		if functions[i].Name == spec.Name {
			functions[i].Description, functions[i].Instructions = spec.Description, spec.Instructions
			functions[i].InputSchema, functions[i].ResultSchema, functions[i].UpdatedAt = spec.InputSchema, spec.ResultSchema, now
			return functions, true
		}
	}
	return append(functions, crewFunction{Name: spec.Name, Description: spec.Description, Instructions: spec.Instructions, InputSchema: spec.InputSchema, ResultSchema: spec.ResultSchema, CreatedBy: creator, CreatedAt: now, UpdatedAt: now}), false
}

// crewFunctionAskName is the implicit function every Crew
// offers: a free-text question answered by the target's final reply, over
// the standard inbound trigger. A declared function of the same name wins.
const crewFunctionAskName = "ask"

func defaultAskCrewFunction() crewFunction {
	return crewFunction{
		Name:        crewFunctionAskName,
		Description: "Ask this Crew anything in free text; the answer is its final reply. Available on every Crew without setup.",
		InputSchema: map[string]interface{}{"type": "object", "required": []interface{}{"message"}, "properties": map[string]interface{}{
			"message": map[string]interface{}{"type": "string", "description": "Self-contained question or task; the target does not see this conversation."},
		}},
		ResultSchema: map[string]interface{}{"type": "object", "required": []interface{}{"answer"}, "properties": map[string]interface{}{
			"answer": map[string]interface{}{"type": "string"},
		}},
		Instructions: "Answer the caller's message. Your final reply is returned to the caller as the answer.",
		CreatedBy:    "platform (default)",
	}
}

// callableFunctions is what a target offers callers. A Crew offers its
// declared functions plus the built-in ask. A workflow offers its function
// triggers plus ask, which goes to the workflow's assistant (never straight
// into a run, where free text has no inputs to set).
func callableFunctions(ctx context.Context, target triggerTarget) ([]crewFunction, error) {
	if target.Kind == triggerCallerWorkflow {
		manifest, exists, err := ReadWorkflowManifest(ctx, target.Path)
		if err != nil || !exists || manifest == nil {
			return nil, fmt.Errorf("workflow %q is unavailable", target.Label)
		}
		return append(workflowFunctions(manifest), workflowAskFunction()), nil
	}
	functions, err := readCrewFunctions(ctx, target)
	if err != nil {
		return nil, err
	}
	// A Code project has no functions of its own (an older declaration may remain).
	if target.CrewProfile == codeproduct.ProfileID {
		return functions, nil
	}
	return withDefaultAskFunction(functions), nil
}

// errWorkflowFunctionsAreTriggers refuses define/delete_function on a
// workflow: its functions are function triggers owned by its Builder.
func errWorkflowFunctionsAreTriggers(target triggerTarget) error {
	return fmt.Errorf("workflow %q defines its functions as function triggers in its Builder chat (manage_workflow_webhook kind=function: a route plus typed inputs bound to workflow variables); ask its Builder to add or change one", target.Label)
}

// withDefaultAskFunction returns the declared functions plus the implicit
// ask, unless the target declared its own ask.
func withDefaultAskFunction(functions []crewFunction) []crewFunction {
	if _, declared := findCrewFunction(functions, crewFunctionAskName); declared {
		return functions
	}
	return append(append([]crewFunction(nil), functions...), defaultAskCrewFunction())
}

func findCrewFunction(functions []crewFunction, name string) (crewFunction, bool) {
	for _, fn := range functions {
		if fn.Name == strings.TrimSpace(name) {
			return fn, true
		}
	}
	return crewFunction{}, false
}

func crewFunctionNames(functions []crewFunction) []string {
	names := make([]string, 0, len(functions))
	for _, fn := range functions {
		names = append(names, fn.Name)
	}
	return names
}

// crewFunctionToolName is the generated tool name for one function, e.g.
// "rts_flow_tester__run_login_flow" (tool-name charset, at most 64 chars).
func crewFunctionToolName(targetLabel, function string) string {
	var b strings.Builder
	lastUnderscore := true
	for _, r := range strings.ToLower(targetLabel) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastUnderscore = false
		} else if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	prefix := strings.Trim(b.String(), "_")
	if prefix == "" {
		prefix = "target"
	}
	name := prefix + "__" + function
	if len(name) > 64 {
		keep := 64 - len(function) - 2
		if keep < 1 {
			return name[:64]
		}
		name = strings.TrimRight(prefix[:keep], "_") + "__" + function
	}
	return name
}

// targetStampID is the ID a trigger caller stamp carries for this target.
func (t triggerTarget) stampID() string {
	if t.Kind == triggerCallerCrew {
		return strings.TrimSpace(t.CrewID)
	}
	if t.Manifest != nil {
		return strings.TrimSpace(t.Manifest.ID)
	}
	return ""
}

func crewFunctionKey(kind, profileID, id string) string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == triggerCallerCrew {
		profileID = strings.ToLower(strings.TrimSpace(normalizeInternalProfileID(profileID)))
	} else {
		profileID = ""
	}
	return kind + ":" + profileID + ":" + strings.TrimSpace(id)
}

func crewFunctionScopedKey(kind, profileID, id, workspacePath string) string {
	key := crewFunctionKey(kind, profileID, id)
	if profileID == codeproduct.ProfileID {
		if ownerID, ok := crewProjectOwnerID(workspacePath); ok {
			return key + ":" + ownerID
		}
	}
	return key
}

// selfTriggerTarget addresses the calling Crew or workflow itself, for
// managing its own functions.
func selfTriggerTarget(caller triggerLinkCaller) triggerTarget {
	target := triggerTarget{Kind: caller.Stamp.Type, Path: caller.Path, Label: caller.Label}
	if target.Kind == triggerCallerCrew {
		target.CrewID = caller.Stamp.ID
		target.CrewProfile = normalizeInternalProfileID(caller.Stamp.ProfileID)
	} else {
		target.Manifest = &WorkflowManifest{ID: caller.Stamp.ID}
	}
	return target
}

// --- call records ---

type crewFunctionProgress struct {
	At      time.Time `json:"at"`
	Message string    `json:"message"`
	Percent *float64  `json:"percent,omitempty"`
}

type crewFunctionCall struct {
	mu sync.Mutex

	ID              string                 `json:"call_id"`
	Function        string                 `json:"function"`
	UserID          string                 `json:"-"`
	CallerKind      string                 `json:"caller_kind"`
	CallerID        string                 `json:"caller_id"`
	CallerProfileID string                 `json:"caller_profile_id,omitempty"`
	CallerPath      string                 `json:"-"`
	CallerLabel     string                 `json:"caller_label"`
	TargetKind      string                 `json:"target_kind"`
	TargetID        string                 `json:"target_id"`
	TargetProfileID string                 `json:"target_profile_id,omitempty"`
	TargetLabel     string                 `json:"target_label"`
	TargetPath      string                 `json:"target_path"`
	Chain           []string               `json:"chain"`
	Root            string                 `json:"root"`
	TriggerID       string                 `json:"trigger_id"`
	RunID           string                 `json:"run_id"`
	RunIDs          []string               `json:"run_ids"`
	Status          string                 `json:"status"`
	Result          interface{}            `json:"result,omitempty"`
	Error           string                 `json:"error,omitempty"`
	Progress        []crewFunctionProgress `json:"progress,omitempty"`
	InvalidResults  int                    `json:"invalid_results,omitempty"`
	// PartialResult / FinalReply keep what the target actually produced when
	// the call fails on the result contract, so the caller never loses real
	// work (e.g. test outcomes and video links) to a formatting mistake.
	PartialResult interface{} `json:"partial_result,omitempty"`
	FinalReply    string      `json:"final_reply,omitempty"`
	Retried       bool        `json:"retried,omitempty"`
	// FreeText marks the implicit ask: the result is the target's final
	// reply ({"answer": ...}) and return_function_result is optional.
	FreeText     bool                   `json:"free_text,omitempty"`
	ResultSchema map[string]interface{} `json:"result_schema,omitempty"`
	// TimedOut marks a call whose caller stopped waiting while the target
	// may still be working; the target's answer is still accepted (Late)
	// and delivered to the caller.
	TimedOut  bool      `json:"timed_out,omitempty"`
	Late      bool      `json:"late,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	// Joined counts identical calls made while this one was in flight and
	// answered with it instead of starting another run.
	Joined       int    `json:"joined,omitempty"`
	SubmissionID string `json:"submission_id,omitempty"`
	ArgumentsKey string `json:"arguments_key,omitempty"`
	// A call between two chats of one Code (PLAT-648): the conversation key
	// and session of each side. Only those two chats are its caller and target.
	CallerChat        string `json:"caller_chat,omitempty"`
	CallerChatSession string `json:"caller_chat_session,omitempty"`
	TargetChat        string `json:"target_chat,omitempty"`
	TargetChatSession string `json:"target_chat_session,omitempty"`

	// argsKey identifies the exact arguments, for joining identical calls.
	argsKey string
	target  triggerTarget
	caller  triggerLinkCaller
	done    chan struct{}
	closed  bool
	// onLate tells the caller's chat about a late answer.
	onLate func()
	// poll is captured at start so a supervisor never reads the package
	// interval after it changes.
	poll time.Duration
}

var crewFunctionCalls = struct {
	sync.Mutex
	m map[string]*crewFunctionCall
}{m: map[string]*crewFunctionCall{}}

// lookupCrewFunctionCall returns a call by ID: from memory, or after a
// server restart from its saved record.
func lookupCrewFunctionCall(id string) *crewFunctionCall {
	id = strings.TrimSpace(id)
	crewFunctionCalls.Lock()
	call := crewFunctionCalls.m[id]
	crewFunctionCalls.Unlock()
	if call != nil {
		return call
	}
	return loadSavedCrewFunctionCall(id)
}

// crewFunctionCallIndexPath points from a call ID to its target, so a call
// saved under the target can be found again after a restart.
func crewFunctionCallIndexPath(id string) string {
	return "_system/function_calls/" + id + ".json"
}

type crewFunctionCallIndex struct {
	TargetPath string `json:"target_path"`
	RecordPath string `json:"record_path,omitempty"`
	CallerPath string `json:"caller_path,omitempty"`
	UserID     string `json:"user_id"`
}

// loadSavedCrewFunctionCall reads a call saved before a restart. A call that
// was still open lost its supervisor with the old process, so it is settled
// as interrupted; its progress is kept.
func loadSavedCrewFunctionCall(id string) *crewFunctionCall {
	if !strings.HasPrefix(id, "fn-") || strings.ContainsAny(id, "/\\") || strings.Contains(id, "..") {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, exists, err := readFileFromWorkspace(ctx, crewFunctionCallIndexPath(id))
	if err != nil || !exists {
		return nil
	}
	var index crewFunctionCallIndex
	if json.Unmarshal([]byte(raw), &index) != nil || strings.TrimSpace(index.TargetPath) == "" {
		return nil
	}
	recordPath := index.RecordPath
	if recordPath == "" {
		recordPath = strings.TrimSuffix(index.TargetPath, "/") + "/functions/calls/" + id + ".json"
	}
	raw, exists, err = readFileFromWorkspace(ctx, recordPath)
	if err != nil || !exists {
		return nil
	}
	call := &crewFunctionCall{}
	if json.Unmarshal([]byte(raw), call) != nil || call.ID != id {
		return nil
	}
	call.UserID, call.CallerPath, call.done, call.closed = index.UserID, index.CallerPath, make(chan struct{}), true
	close(call.done)
	if !call.terminalLocked() || (call.TimedOut && !call.Late) {
		call.Status = "failed"
		call.Error = "interrupted: the server restarted while this call was open (its last progress is kept); call the function again if you still need it"
		call.TimedOut = false
		call.UpdatedAt = time.Now().UTC()
		call.persist()
	}
	crewFunctionCalls.Lock()
	defer crewFunctionCalls.Unlock()
	if existing := crewFunctionCalls.m[id]; existing != nil {
		return existing
	}
	crewFunctionCalls.m[id] = call
	return call
}

func (c *crewFunctionCall) saveIndex() {
	encoded, err := json.Marshal(crewFunctionCallIndex{TargetPath: c.TargetPath, RecordPath: c.recordPath(), CallerPath: c.CallerPath, UserID: c.UserID})
	if err != nil {
		return
	}
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: c.UserID})
	if err := writeFileToWorkspace(ctx, crewFunctionCallIndexPath(c.ID), string(encoded)+"\n"); err != nil {
		log.Printf("[CREW_FUNCTION] could not index call %s: %v", c.ID, err)
	}
}

func (c *crewFunctionCall) terminalLocked() bool {
	return c.Status == "completed" || c.Status == "failed"
}

// acceptsLateLocked reports whether the caller stopped waiting but the
// target may still report progress and deliver its answer.
func (c *crewFunctionCall) acceptsLateLocked() bool {
	return c.TimedOut && !c.Late
}

// timeOut releases the caller's wait without closing the call to the target.
func (c *crewFunctionCall) timeOut(reason string) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.TimedOut = true
	c.mu.Unlock()
	c.finish("failed", nil, reason)
}

// settleLate records the target's answer after the caller's wait timed out,
// and tells the caller's chat.
func (c *crewFunctionCall) settleLate(status string, result interface{}, errText string) bool {
	c.mu.Lock()
	if !c.acceptsLateLocked() {
		c.mu.Unlock()
		return false
	}
	c.Status, c.Result, c.Error, c.Late, c.UpdatedAt = status, result, errText, true, time.Now().UTC()
	onLate := c.onLate
	c.mu.Unlock()
	c.persist()
	virtualtools.GetHumanFeedbackStore().WithdrawOperation(c.ID)
	if onLate != nil {
		go onLate()
	}
	return true
}

// settle finishes the call, or records a late answer after a timeout.
func (c *crewFunctionCall) settle(status string, result interface{}, errText string) bool {
	return c.finish(status, result, errText) || c.settleLate(status, result, errText)
}

// crewFunctionHardCap bounds how long a call may run while its target keeps
// showing activity: four idle timeouts, never beyond the maximum timeout.
func crewFunctionHardCap(timeout time.Duration) time.Duration {
	limit := 4 * timeout
	if limit > triggerTargetMaxTimeout {
		limit = triggerTargetMaxTimeout
	}
	if limit < timeout {
		limit = timeout
	}
	return limit
}

// finish records the outcome once and releases every waiter.
func (c *crewFunctionCall) finish(status string, result interface{}, errText string) bool {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return false
	}
	c.Status, c.Result, c.Error, c.UpdatedAt = status, result, errText, time.Now().UTC()
	withdraw := !c.TimedOut
	c.closed = true
	close(c.done)
	c.mu.Unlock()
	c.persist()
	if withdraw {
		virtualtools.GetHumanFeedbackStore().WithdrawOperation(c.ID)
	}
	return true
}

func (c *crewFunctionCall) snapshot() map[string]interface{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]interface{}{
		"call_id": c.ID, "function": c.Function, "status": c.Status,
		"target":   map[string]interface{}{"kind": c.TargetKind, "name": c.TargetLabel, "workspace_path": c.TargetPath},
		"run_id":   c.RunID,
		"progress": append([]crewFunctionProgress(nil), c.Progress...),
	}
	if c.TargetChat != "" {
		out["target"] = map[string]interface{}{"kind": "chat", "name": c.TargetLabel, "chat": c.TargetChat}
	}
	if c.Result != nil {
		out["result"] = c.Result
	}
	if c.Error != "" {
		out["error"] = c.Error
	}
	if c.PartialResult != nil {
		out["partial_result"] = c.PartialResult
	}
	if c.FinalReply != "" {
		out["final_reply"] = c.FinalReply
	}
	if c.acceptsLateLocked() {
		out["timed_out"] = true
		out["note"] = "The wait timed out but the target may still be working. Its answer is still accepted and is sent to the caller's chat when it arrives; ask_function_update still reaches it."
	}
	if c.Late {
		out["late"] = true
	}
	if c.Joined > 0 {
		out["joined"] = c.Joined
		if _, ok := out["note"]; !ok {
			out["note"] = "An identical call (same function and arguments) was already running, so this is that call, not a new run. Do not call again; wait for its result or use get_function_call."
		}
	}
	return out
}

// joinInFlightCrewFunctionCallLocked returns an in-flight call from the same
// caller for the same function and target with the same arguments. A caller
// that retries a call it believes failed (a shell curl that timed out while
// call_function was still waiting) joins the running call instead of starting
// a duplicate run (RTS 2026-09-27: one PR reviewed three times at once).
// Needs crewFunctionCalls locked.
func joinInFlightCrewFunctionCallLocked(userID, callerKind, callerProfileID, callerID, callerPath, callerChat, targetKind, targetProfileID, targetID, targetPath, targetChat, function, argsKey string) *crewFunctionCall {
	for _, call := range crewFunctionCalls.m {
		call.mu.Lock()
		same := !call.terminalLocked() && call.UserID == userID &&
			call.CallerChat == callerChat && call.TargetChat == targetChat &&
			crewFunctionKey(call.CallerKind, call.CallerProfileID, call.CallerID) == crewFunctionKey(callerKind, callerProfileID, callerID) &&
			call.TargetKind == targetKind && call.TargetID == targetID &&
			(call.CallerProfileID != codeproduct.ProfileID || canonicalCrewWorkspaceRoot(call.CallerPath) == canonicalCrewWorkspaceRoot(callerPath)) &&
			(targetProfileID != codeproduct.ProfileID || canonicalCrewWorkspaceRoot(call.TargetPath) == canonicalCrewWorkspaceRoot(targetPath)) &&
			call.Function == function && call.argsKey == argsKey
		if same {
			call.Joined++
			call.UpdatedAt = time.Now().UTC()
		}
		call.mu.Unlock()
		if same {
			return call
		}
	}
	return nil
}

// crewFunctionFreeTextAnswer normalises an ask result to {"answer": text}:
// a string answer passes through; any other answer (or a bare value) is kept
// verbatim as indented JSON text.
func crewFunctionFreeTextAnswer(result interface{}) interface{} {
	asText := func(value interface{}) string {
		if text, ok := value.(string); ok {
			return text
		}
		encoded, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return fmt.Sprint(value)
		}
		return string(encoded)
	}
	if object, ok := result.(map[string]interface{}); ok {
		if answer, has := object["answer"]; has && len(object) == 1 {
			return map[string]interface{}{"answer": asText(answer)}
		}
	}
	return map[string]interface{}{"answer": asText(result)}
}

// crewFunctionFailureDetail renders what a failed call still produced, for
// the caller's auto-notification.
func crewFunctionFailureDetail(snapshot map[string]interface{}) string {
	var detail strings.Builder
	if partial, ok := snapshot["partial_result"]; ok && partial != nil {
		encoded, _ := json.MarshalIndent(partial, "", "  ")
		detail.WriteString("\n\nThe target's last (non-conforming) result — its work is not lost:\n" + truncateTriggerTargetResult(string(encoded)))
	}
	if reply, _ := snapshot["final_reply"].(string); strings.TrimSpace(reply) != "" {
		detail.WriteString("\n\nThe target's final reply:\n" + truncateTriggerTargetResult(reply))
	}
	return detail.String()
}

// persist keeps a JSON copy next to the target's functions, so
// get_function_call can still answer after a server restart.
func (c *crewFunctionCall) persist() {
	c.mu.Lock()
	encoded, err := json.MarshalIndent(c, "", "  ")
	path := c.recordPath()
	userID := c.UserID
	c.mu.Unlock()
	if err != nil {
		return
	}
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: userID})
	if err := writeFileToWorkspace(ctx, path, string(encoded)+"\n"); err != nil {
		log.Printf("[CREW_FUNCTION] could not save call %s: %v", c.ID, err)
	}
}

func (c *crewFunctionCall) recordPath() string {
	if c.TargetProfileID == codeproduct.ProfileID {
		return workspaceref.PhysicalPath(c.UserID, "chat_history", "code-peer-calls", c.ID+".json")
	}
	return strings.TrimSuffix(c.TargetPath, "/") + "/functions/calls/" + c.ID + ".json"
}

// crewFunctionChatMarker scopes a chain key to one chat of a Code (PLAT-648),
// so the chats of one Code are separate participants of a call chain.
const crewFunctionChatMarker = "#chat:"

func crewFunctionChatChainKey(key, chat string) string {
	if strings.TrimSpace(chat) == "" {
		return key
	}
	return key + crewFunctionChatMarker + chat
}

// crewFunctionChainBase is the Code (or Crew/workflow) a chain key names.
func crewFunctionChainBase(key string) string {
	if i := strings.Index(key, crewFunctionChatMarker); i >= 0 {
		return key[:i]
	}
	return key
}

// crewFunctionChainConflict reports whether target is already in the chain:
// the same participant, or a Code one of whose chats took part (and back).
func crewFunctionChainConflict(chain []string, targetKey string) bool {
	for _, key := range chain {
		if key == targetKey || crewFunctionChainBase(key) == targetKey || key == crewFunctionChainBase(targetKey) {
			return true
		}
	}
	return false
}

func (c *crewFunctionCall) targetChainKeyLocked() string {
	return crewFunctionChatChainKey(crewFunctionScopedKey(c.TargetKind, c.TargetProfileID, c.TargetID, c.TargetPath), c.TargetChat)
}

// crewFunctionChainFor returns the call chain the caller is currently part
// of: the longest chain among in-flight calls whose target is the caller. A
// caller that is not a specific chat (a Code's call_function) is part of the
// chains of calls into any of its chats.
func crewFunctionChainFor(callerKey string) (chain []string, root string) {
	crewFunctionCalls.Lock()
	defer crewFunctionCalls.Unlock()
	scoped := strings.Contains(callerKey, crewFunctionChatMarker)
	for _, call := range crewFunctionCalls.m {
		call.mu.Lock()
		inFlight := !call.terminalLocked()
		targetKey := call.targetChainKeyLocked()
		matches := targetKey == callerKey || (!scoped && crewFunctionChainBase(targetKey) == callerKey)
		if inFlight && matches && len(call.Chain) > len(chain) {
			chain, root = append([]string(nil), call.Chain...), call.Root
		}
		call.mu.Unlock()
	}
	return chain, root
}

func crewFunctionChainCalls(root string) int {
	crewFunctionCalls.Lock()
	defer crewFunctionCalls.Unlock()
	count := 0
	for _, call := range crewFunctionCalls.m {
		if call.Root == root {
			count++
		}
	}
	return count
}

// --- dispatch ---

// dispatchTargetTrigger fires one internal trigger delivery on target.
func (api *StreamingAPI) dispatchTargetTrigger(ctx context.Context, userID string, caller triggerLinkCaller, target triggerTarget, triggerID, deliveryID, event string, body map[string]interface{}) (internalTriggerDeliveryResult, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return internalTriggerDeliveryResult{}, fmt.Errorf("invalid payload")
	}
	var delivery internalTriggerDeliveryResult
	switch target.Kind {
	case triggerCallerCrew:
		delivery, err = api.productSchedules.dispatchInternalProductTrigger(ctx, internalCrewTriggerCall{
			UserID: userID, ProfileID: target.CrewProfile, ProjectID: target.CrewID, TriggerID: triggerID,
			Caller: caller.Stamp, DeliveryID: deliveryID, Event: event, Payload: data, CallerLabel: caller.Label,
			CallerPath: caller.Path,
		})
	case triggerCallerWorkflow:
		delivery, err = api.scheduler.dispatchInternalWorkflowTrigger(ctx, internalWorkflowTriggerCall{
			WorkflowID: target.Manifest.ID, TriggerID: triggerID, Caller: caller.Stamp,
			DeliveryID: deliveryID, Event: event, Payload: data,
		})
	default:
		return internalTriggerDeliveryResult{}, fmt.Errorf("unknown target kind %q", target.Kind)
	}
	if err != nil {
		return internalTriggerDeliveryResult{}, crewWorkflowRunError(err)
	}
	return delivery, nil
}

func crewFunctionTaskText(call *crewFunctionCall, fn crewFunction, args map[string]interface{}) string {
	if call.FreeText {
		message, _ := args["message"].(string)
		return fmt.Sprintf(`[Function call %[1]s] The %[2]s %[3]q asks:

%[4]s

Reply with your answer: your final reply in this turn is returned to the caller as the answer. For longer work, report milestones with report_function_progress(call_id=%[1]q, message=...). If you notice the same kind of ask arriving repeatedly, suggest to the user that it be exposed as a typed function (define_function).`,
			call.ID, call.CallerKind, call.CallerLabel, strings.TrimSpace(message))
	}
	encodedArgs, _ := json.MarshalIndent(args, "", "  ")
	resultShape := "any JSON value"
	if len(fn.ResultSchema) > 0 {
		encoded, _ := json.MarshalIndent(fn.ResultSchema, "", "  ")
		resultShape = "JSON matching this schema:\n" + string(encoded)
	}
	instructions := strings.TrimSpace(fn.Instructions)
	if instructions == "" {
		instructions = strings.TrimSpace(fn.Description)
	}
	return fmt.Sprintf(`[Function call %[1]s] The %[2]s %[3]q called your function %[4]q.

What to do:
%[5]s

Arguments (validated against the function's input schema):
%[6]s

Report progress at meaningful milestones with report_function_progress(call_id=%[1]q, message=..., percent=...), so the caller can follow along without interrupting you.
When you are done you MUST call return_function_result(call_id=%[1]q, result=<%[7]s>). If you cannot do it, call return_function_result(call_id=%[1]q, error="<why>"). The caller receives exactly that result, not your chat reply.`,
		call.ID, call.CallerKind, call.CallerLabel, fn.Name, instructions, string(encodedArgs), resultShape)
}

// startCrewFunctionCall validates, records and dispatches one call and
// starts its supervisor.
func (api *StreamingAPI) startCrewFunctionCall(ctx context.Context, userID string, caller triggerLinkCaller, target triggerTarget, fn crewFunction, args map[string]interface{}, timeout time.Duration, submissionIDs ...string) (*crewFunctionCall, error) {
	if caller.isTarget(target) {
		return nil, fmt.Errorf("a %s cannot call its own function", target.Kind)
	}
	if args == nil {
		args = map[string]interface{}{}
	}
	// A workflow function checks its own inputs on dispatch, with messages
	// that name the missing or unknown input.
	if target.Kind != triggerCallerWorkflow {
		if problems := validateCrewFunctionValue(fn.InputSchema, args); len(problems) > 0 {
			return nil, fmt.Errorf("arguments do not match %s's input schema: %s", fn.Name, strings.Join(problems, "; "))
		}
	}
	// encoding/json sorts map keys, so equal arguments give equal keys.
	argsJSON, _ := json.Marshal(args)
	argsKey := string(argsJSON)
	if isWorkflowAsk(target, fn) || isPersonCrewAsk(target, caller, fn) || target.Chat != nil {
		message, _ := args["message"].(string)
		if strings.TrimSpace(message) == "" {
			return nil, fmt.Errorf("ask needs a message")
		}
	}
	callerChat, callerChatSession, targetChat, targetChatSession := "", "", "", ""
	if caller.Chat != nil {
		callerChat, callerChatSession = caller.Chat.Key, caller.Chat.SessionID
	}
	if target.Chat != nil {
		targetChat, targetChatSession = target.Chat.Key, target.Chat.SessionID
	}
	submissionID := ""
	if len(submissionIDs) > 0 {
		submissionID = strings.TrimSpace(submissionIDs[0])
		if submissionIDs[0] != "" && submissionID == "" {
			return nil, fmt.Errorf("submission_id cannot be blank")
		}
	}
	if submissionID != "" {
		if len(submissionID) > 128 {
			return nil, fmt.Errorf("submission_id must be at most 128 characters")
		}
		if existing, found, err := lookupCrewFunctionSubmission(ctx, userID, caller.Stamp, agentProfileRuntimeWorkspace(userID, caller.Path), callerChat, submissionID, target, fn.Name, argsKey); err != nil || found {
			return existing, err
		}
	}
	crewFunctionCalls.Lock()
	var joined *crewFunctionCall
	if submissionID == "" {
		joined = joinInFlightCrewFunctionCallLocked(userID, caller.Stamp.Type, caller.Stamp.ProfileID, caller.Stamp.ID, agentProfileRuntimeWorkspace(userID, caller.Path), callerChat, target.Kind, target.CrewProfile, target.stampID(), crewFunctionRoot(ctx, target), targetChat, fn.Name, argsKey)
	}
	crewFunctionCalls.Unlock()
	if joined != nil {
		return joined, nil
	}
	callerKey := crewFunctionChatChainKey(crewFunctionScopedKey(caller.Stamp.Type, caller.Stamp.ProfileID, caller.Stamp.ID, agentProfileRuntimeWorkspace(userID, caller.Path)), callerChat)
	targetKey := crewFunctionChatChainKey(crewFunctionScopedKey(target.Kind, target.CrewProfile, target.stampID(), target.Path), targetChat)
	chain, root := crewFunctionChainFor(callerKey)
	if len(chain) == 0 {
		chain = []string{callerKey}
	} else if last := chain[len(chain)-1]; last != callerKey && crewFunctionChainBase(last) != callerKey {
		chain = append(chain, callerKey)
	}
	if crewFunctionChainConflict(chain, targetKey) {
		if target.Chat != nil {
			return nil, fmt.Errorf("refused: chat %q already took part in this exchange (%s); your answer goes back to it on its own", target.Label, strings.Join(chain, " -> "))
		}
		return nil, fmt.Errorf("refused: %s %q is already in this call chain (%s); calling it again would loop", target.Kind, target.Label, strings.Join(chain, " -> "))
	}
	if len(chain) >= crewFunctionMaxDepth {
		return nil, fmt.Errorf("refused: call depth limit %d reached (%s)", crewFunctionMaxDepth, strings.Join(chain, " -> "))
	}
	id := "fn-" + uuid.NewString()
	if root == "" {
		root = id
	}
	if crewFunctionChainCalls(root) >= crewFunctionChainBudget {
		return nil, fmt.Errorf("refused: this call chain already made %d function calls", crewFunctionChainBudget)
	}
	// A Crew call rides this caller's binding on the Crew; a workflow
	// function runs its own function trigger.
	triggerID := fn.TriggerID
	// A person's ask runs in their own chat, and a sibling chat call in that
	// chat, not through a trigger binding.
	if target.Kind != triggerCallerWorkflow && !isPersonCrewAsk(target, caller, fn) && target.Chat == nil {
		var err error
		triggerID, _, err = api.connectTriggerTarget(ctx, userID, caller, target)
		if err != nil {
			return nil, err
		}
	}
	now := time.Now().UTC()
	call := &crewFunctionCall{
		ID: id, Function: fn.Name, UserID: userID,
		CallerKind: caller.Stamp.Type, CallerID: caller.Stamp.ID, CallerProfileID: caller.Stamp.ProfileID, CallerPath: agentProfileRuntimeWorkspace(userID, caller.Path), CallerLabel: caller.Label,
		TargetKind: target.Kind, TargetID: target.stampID(), TargetProfileID: target.CrewProfile, TargetLabel: target.Label, TargetPath: crewFunctionRoot(ctx, target),
		Chain: append(chain, targetKey), Root: root, TriggerID: triggerID, Status: "queued",
		ResultSchema: fn.ResultSchema, CreatedAt: now, UpdatedAt: now,
		FreeText:     fn.Name == crewFunctionAskName && fn.CreatedBy == defaultAskCrewFunction().CreatedBy,
		SubmissionID: submissionID, ArgumentsKey: crewFunctionArgumentsFingerprint(argsKey),
		CallerChat: callerChat, CallerChatSession: callerChatSession, TargetChat: targetChat, TargetChatSession: targetChatSession,
		target: target, caller: caller, done: make(chan struct{}), poll: triggerTargetPollInterval,
		argsKey: argsKey,
	}
	fromPath := caller.Path
	if caller.Stamp.ProfileID == codeproduct.ProfileID {
		fromPath = ""
	}
	body := map[string]interface{}{
		"task":    crewFunctionTaskText(call, fn, args),
		"from":    map[string]interface{}{"kind": caller.Stamp.Type, "name": caller.Label, "workspace_path": fromPath},
		"payload": map[string]interface{}{"function": fn.Name, "call_id": id, "args": args},
	}
	crewFunctionCalls.Lock()
	if submissionID != "" {
		if existing, found, err := inMemoryCrewFunctionSubmissionLocked(userID, caller.Stamp, agentProfileRuntimeWorkspace(userID, caller.Path), callerChat, submissionID, target, fn.Name, argsKey); err != nil || found {
			crewFunctionCalls.Unlock()
			return existing, err
		}
	}
	// Re-check under the same lock as the insert: two identical calls racing
	// past the early check must still start one run.
	if submissionID == "" {
		if joined := joinInFlightCrewFunctionCallLocked(userID, caller.Stamp.Type, caller.Stamp.ProfileID, caller.Stamp.ID, agentProfileRuntimeWorkspace(userID, caller.Path), callerChat, target.Kind, target.CrewProfile, target.stampID(), crewFunctionRoot(ctx, target), targetChat, fn.Name, argsKey); joined != nil {
			crewFunctionCalls.Unlock()
			return joined, nil
		}
	}
	crewFunctionCalls.m[id] = call
	crewFunctionCalls.Unlock()
	if submissionID != "" {
		if err := saveCrewFunctionSubmission(ctx, call, caller.Stamp); err != nil {
			// Another retry may have joined this in-memory call while the
			// submission index was being written. Settle it so that retry's
			// call_id cannot hang or disappear under its feet.
			call.finish("failed", nil, "cannot record submission_id: "+err.Error())
			return nil, fmt.Errorf("cannot record submission_id: %w", err)
		}
		call.saveIndex()
		call.persist()
	}
	if isPulseBuilderAsk(target, fn) {
		// ask_builder: a turn in the workflow's Builder chat.
		call.saveIndex()
		call.persist()
		go api.runPulseBuilderAsk(call, target, caller, args, timeout)
		return call, nil
	}
	if isGoalLeadAsk(target, fn) {
		// ask_pulse: a turn in the workflow's Pulse conversation.
		call.saveIndex()
		call.persist()
		go api.runGoalLeadAsk(call, target, caller, args, timeout)
		return call, nil
	}
	if isWorkflowAsk(target, fn) {
		message, _ := args["message"].(string)
		call.saveIndex()
		call.persist()
		go api.runWorkflowAsk(call, target, caller, message, timeout)
		return call, nil
	}
	if isPersonCrewAsk(target, caller, fn) {
		message, _ := args["message"].(string)
		call.saveIndex()
		call.persist()
		go api.runCrewOwnChatAsk(call, target, strings.TrimSpace(message), timeout)
		return call, nil
	}
	if target.Chat != nil {
		call.saveIndex()
		call.persist()
		go api.runCodeChatCall(call, target, args, timeout)
		return call, nil
	}
	var delivery internalTriggerDeliveryResult
	var err error
	if target.Kind == triggerCallerWorkflow {
		// Inputs are checked here, before anything runs: a missing or unknown
		// input comes straight back to the caller.
		_, delivery, err = api.scheduler.dispatchWorkflowFunction(ctx, workflowFunctionCall{
			WorkflowID: target.Manifest.ID, Function: fn.Name, Caller: caller.Stamp, DeliveryID: id, Args: args,
			Payload: map[string]interface{}{"call_id": id, "from": body["from"]},
		})
		if err != nil {
			err = crewWorkflowRunError(err)
		}
	} else {
		delivery, err = api.dispatchTargetTrigger(ctx, userID, caller, target, triggerID, id, crewFunctionEvent, body)
	}
	if err != nil {
		if submissionID != "" {
			call.finish("failed", nil, err.Error())
			return call, nil
		}
		crewFunctionCalls.Lock()
		delete(crewFunctionCalls.m, id)
		crewFunctionCalls.Unlock()
		return nil, err
	}
	call.mu.Lock()
	call.RunID, call.RunIDs = delivery.RunID, []string{delivery.RunID}
	call.mu.Unlock()
	call.saveIndex()
	call.persist()
	go api.superviseCrewFunctionCall(call, timeout)
	return call, nil
}

// superviseCrewFunctionCall watches the call's trigger run. A workflow run's
// outcome is the result. A Crew run must deliver its result through
// return_function_result; one that ends without it gets one retry turn,
// then the call fails.
//
// The timeout counts from the target's last sign of life (a progress report
// or any event in its session), bounded by crewFunctionHardCap. When it
// fires the caller stops waiting, but supervision continues until the run
// ends so a late answer still reaches the caller.
func (api *StreamingAPI) superviseCrewFunctionCall(call *crewFunctionCall, timeout time.Duration) {
	ctx := context.WithValue(context.Background(), UserContextKey, &UserClaims{UserID: call.UserID})
	started := time.Now()
	hardCap := crewFunctionHardCap(timeout)
	lastSign := started
	lookupFailures := 0
	ticker := time.NewTicker(call.poll)
	defer ticker.Stop()
	for {
		if call.settled() {
			return
		}
		call.mu.Lock()
		runID := call.RunID
		if call.UpdatedAt.After(lastSign) {
			lastSign = call.UpdatedAt
		}
		call.mu.Unlock()
		state, err := api.readTriggerTargetRun(ctx, call.UserID, call.caller, call.target, call.TriggerID, runID)
		if err == nil {
			lookupFailures = 0
			call.mu.Lock()
			if !call.terminalLocked() && triggerTargetRunIsRunning(call.TargetKind, state.Status) {
				call.Status = "running"
			}
			call.mu.Unlock()
			if state.Terminal {
				if api.settleCrewFunctionRun(ctx, call, state) {
					return
				}
			} else if at := api.crewFunctionLastEventAt(crewTargetRunSessionID(state)); at.After(lastSign) {
				lastSign = at
			}
		} else {
			lookupFailures++
			if lookupFailures == 1 {
				log.Printf("[CREW_FUNCTION] call %s cannot read run %s: %v", call.ID, runID, err)
			}
			// A short outage gets retries, but a missing/revoked binding must
			// not leave the caller and its notification watcher running forever.
			if lookupFailures >= 3 {
				call.settle("failed", nil, fmt.Sprintf("cannot check the target run after %d attempts: %v", lookupFailures, err))
				return
			}
		}
		now := time.Now()
		if now.Sub(started) > hardCap {
			if !call.settleLate("failed", nil, fmt.Sprintf("%s %q was still not done after %s; stopped waiting for a late answer", call.TargetKind, call.TargetLabel, hardCap)) {
				call.timeOut(fmt.Sprintf("%s %q was still not done after %s (the limit for a call that keeps showing activity)", call.TargetKind, call.TargetLabel, hardCap))
			}
			return
		}
		if now.Sub(lastSign) > timeout {
			call.timeOut(fmt.Sprintf("no progress or activity from %s %q for %s; stopped waiting. It may still finish: a late answer is sent to you automatically", call.TargetKind, call.TargetLabel, timeout))
		}
		<-ticker.C
	}
}

// settled reports whether nothing more can change the call's outcome.
func (c *crewFunctionCall) settled() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed && !c.acceptsLateLocked()
}

// crewFunctionLastEventAt is when the target session last produced an event
// (streamed text, a tool call, a status change): its latest sign of life.
func (api *StreamingAPI) crewFunctionLastEventAt(sessionID string) time.Time {
	if api == nil || api.eventStore == nil || strings.TrimSpace(sessionID) == "" {
		return time.Time{}
	}
	latest, ok := api.eventStore.GetLatestEventIndex(sessionID)
	if !ok || latest < 0 {
		return time.Time{}
	}
	events := api.eventStore.GetEvents(sessionID, storeEvents.GetEventsOptions{SinceIndex: latest - 1, IncludeStreaming: true}).Events
	if len(events) == 0 {
		return time.Time{}
	}
	return events[len(events)-1].Timestamp
}

// settleCrewFunctionRun handles a terminal trigger run. It returns true when
// the call is settled (or already was). After a timeout the outcome is
// recorded as a late answer.
func (api *StreamingAPI) settleCrewFunctionRun(ctx context.Context, call *crewFunctionCall, state triggerTargetRunState) bool {
	if call.settled() {
		return true
	}
	if call.TargetKind == triggerCallerWorkflow {
		if state.Failed {
			call.settle("failed", nil, truncateTriggerTargetResult(state.Result))
			return true
		}
		if call.FreeText {
			call.settle("completed", map[string]interface{}{"answer": truncateTriggerTargetResult(state.Result)}, "")
			return true
		}
		var decoded interface{}
		if json.Unmarshal([]byte(state.Result), &decoded) != nil {
			decoded = state.Result
		}
		call.settle("completed", decoded, "")
		return true
	}
	// Give a result delivered at the very end of the turn a moment to land.
	time.Sleep(minDuration(call.poll, 2*time.Second))
	if call.settled() {
		return true
	}
	if state.Failed {
		call.settle("failed", nil, fmt.Sprintf("%s %q run ended %s: %s", call.TargetKind, call.TargetLabel, state.Status, truncateTriggerTargetResult(state.Result)))
		return true
	}
	if call.FreeText {
		answer := strings.TrimSpace(state.Result)
		if answer == "" {
			call.settle("failed", nil, "the target finished without a reply")
			return true
		}
		call.settle("completed", map[string]interface{}{"answer": truncateTriggerTargetResult(answer)}, "")
		return true
	}
	call.mu.Lock()
	retried := call.Retried
	call.Retried = true
	call.mu.Unlock()
	if retried {
		reason := "the target finished without returning a valid result"
		if final := strings.TrimSpace(state.Result); final != "" {
			call.mu.Lock()
			call.FinalReply = final
			call.mu.Unlock()
			reason += "; its final reply is included"
		}
		call.settle("failed", nil, reason)
		return true
	}
	retry := map[string]interface{}{
		"task": fmt.Sprintf("[Function call %[1]s — result missing] You finished the %[2]q call from %[3]q without calling return_function_result. Call return_function_result(call_id=%[1]q, result=...) now with the result of the work you already did (or error=\"...\" if it failed). Do not redo the work.", call.ID, call.Function, call.CallerLabel),
		"from": map[string]interface{}{"kind": call.CallerKind, "name": call.CallerLabel},
		"payload": map[string]interface{}{
			"function": call.Function, "call_id": call.ID, "retry": true,
		},
	}
	delivery, err := api.dispatchTargetTrigger(ctx, call.UserID, call.caller, call.target, call.TriggerID, call.ID+"-retry", crewFunctionEvent, retry)
	if err != nil {
		call.settle("failed", nil, "the target finished without a result and the retry could not be sent: "+err.Error())
		return true
	}
	call.mu.Lock()
	call.RunID = delivery.RunID
	call.RunIDs = append(call.RunIDs, delivery.RunID)
	call.mu.Unlock()
	call.persist()
	return false
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// startCrewFunctionWatch resumes the caller's chat with the call's outcome
// through the auto-notification pipeline. A call that times out and later
// gets its answer resumes the chat again with that late answer.
func (api *StreamingAPI) startCrewFunctionWatch(parentReq QueryRequest, sessionID, userID string, call *crewFunctionCall, timeout time.Duration) (string, error) {
	notifier, executionID, name, runCtx, cancel, err := api.beginCrewFunctionNotification(parentReq, sessionID, userID, call, crewFunctionHardCap(timeout)+time.Minute)
	if err != nil {
		return "", err
	}
	call.mu.Lock()
	call.onLate = func() {
		lateNotifier, lateID, lateName, _, lateCancel, lateErr := api.beginCrewFunctionNotification(parentReq, sessionID, userID, call, time.Minute)
		if lateErr != nil {
			log.Printf("[CREW_FUNCTION] late answer for call %s could not reach the caller: %v", call.ID, lateErr)
			return
		}
		defer lateCancel()
		completeCrewFunctionNotification(lateNotifier, lateID, lateName, call)
	}
	call.mu.Unlock()
	go func() {
		defer cancel()
		select {
		case <-call.done:
		case <-runCtx.Done():
			notifier.OnExecutionComplete(executionID, name, "", nil, fmt.Errorf("function call %s did not settle; check it with get_function_call", call.ID))
			return
		}
		completeCrewFunctionNotification(notifier, executionID, name, call)
	}()
	return executionID, nil
}

func (api *StreamingAPI) beginCrewFunctionNotification(parentReq QueryRequest, sessionID, userID string, call *crewFunctionCall, limit time.Duration) (*workshopExecutionBgNotifier, string, string, context.Context, context.CancelFunc, error) {
	if api == nil || api.bgAgentRegistry == nil {
		return nil, "", "", nil, nil, fmt.Errorf("auto-notification is unavailable in this chat")
	}
	name := "Function " + call.TargetLabel + "." + call.Function
	if call.TargetChat != "" {
		name = "Ask " + call.TargetLabel
	}
	executionID := "function-call-" + api.bgAgentRegistry.NextID(name)
	runCtx, cancel := context.WithTimeout(context.Background(), limit)
	parentExecutionID := api.currentConversationTurnExecutionID(sessionID)
	if strings.TrimSpace(parentExecutionID) == "" {
		parentExecutionID = "session:" + sessionID
	}
	notifier := &workshopExecutionBgNotifier{
		api: api, sessionID: sessionID, workspacePath: parentReq.SelectedFolder,
		presetQueryID: parentReq.PresetQueryID, userID: userID,
	}
	notifier.OnExecutionStart(todo_creation_human.WorkshopExecutionStart{
		ID: executionID, ParentExecutionID: parentExecutionID, Name: name,
		Kind: "trigger_auto_notify", Cancel: cancel,
		Metadata: map[string]string{
			"execution_type": "function-call-auto-notify",
			"call_id":        call.ID,
			"target_kind":    call.TargetKind,
			"target_path":    call.TargetPath,
		},
	})
	registered := api.bgAgentRegistry.Get(sessionID, executionID)
	if registered == nil || registered.GetStatus() == BGAgentCanceled {
		cancel()
		return nil, "", "", nil, nil, fmt.Errorf("auto-notification could not be registered")
	}
	return notifier, executionID, name, runCtx, cancel, nil
}

// completeCrewFunctionNotification resumes the caller's chat with the call's
// current outcome.
func completeCrewFunctionNotification(notifier *workshopExecutionBgNotifier, executionID, name string, call *crewFunctionCall) {
	snapshot := call.snapshot()
	header := fmt.Sprintf("Function call %s (%s %q, %s)", call.ID, call.TargetKind, call.TargetLabel, call.Function)
	if call.TargetChat != "" {
		header = fmt.Sprintf("Function call %s (ask to chat %q of this Code)", call.ID, call.TargetLabel)
	}
	if late, _ := snapshot["late"].(bool); late {
		header += " — late answer, after your wait had timed out"
	}
	if snapshot["status"] == "failed" {
		errText, _ := snapshot["error"].(string)
		notifier.OnExecutionComplete(executionID, name, "", nil, fmt.Errorf("%s failed: %s%s", header, errText, crewFunctionFailureDetail(snapshot)))
		return
	}
	encoded, _ := json.MarshalIndent(snapshot["result"], "", "  ")
	notifier.OnExecutionComplete(executionID, name, header+" completed.\n\nResult:\n"+truncateTriggerTargetResult(string(encoded)), nil, nil)
}

// --- activity tail ---

// crewFunctionActivity summarises what a target session is doing right now
// from its recent events: its latest assistant text and any tool it started
// after that text. It never interrupts the session.
func (api *StreamingAPI) crewFunctionActivity(sessionID string) map[string]interface{} {
	if api == nil || api.eventStore == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	result := api.eventStore.GetEvents(sessionID, storeGetEventsAll())
	events := result.Events
	if len(events) > crewFunctionActivityEventsScan {
		events = events[len(events)-crewFunctionActivityEventsScan:]
	}
	activity := map[string]interface{}{}
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		if event.Data == nil {
			continue
		}
		fields := map[string]interface{}{}
		if encoded, err := json.Marshal(event.Data.Data); err == nil {
			_ = json.Unmarshal(encoded, &fields)
		}
		kind := strings.ToLower(string(event.Data.Type))
		if _, seen := activity["current_tool"]; !seen && strings.Contains(kind, "tool_call_start") {
			if tool, _ := fields["tool_name"].(string); tool != "" {
				activity["current_tool"] = tool
				activity["tool_started_at"] = event.Timestamp
			}
		}
		if text := crewFunctionEventText(fields); text != "" && (strings.Contains(kind, "llm_generation_end") || strings.Contains(kind, "streaming_chunk") || strings.Contains(kind, "assistant")) {
			if len(text) > crewFunctionActivityTextLimit {
				text = "…" + text[len(text)-crewFunctionActivityTextLimit:]
			}
			activity["last_text"] = text
			activity["last_text_at"] = event.Timestamp
			break
		}
	}
	if len(activity) == 0 {
		return nil
	}
	return activity
}

func storeGetEventsAll() storeEvents.GetEventsOptions {
	return storeEvents.GetEventsOptions{SinceIndex: -1, IncludeStreaming: true}
}

func crewFunctionEventText(fields map[string]interface{}) string {
	for _, key := range []string{"content", "text", "final_response", "response", "chunk"} {
		if value, ok := fields[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// crewTargetRunSessionID is the session a Crew trigger run executes in.
func crewTargetRunSessionID(state triggerTargetRunState) string {
	if status, ok := state.Raw.(productWebhookRunStatus); ok {
		return strings.TrimSpace(status.SessionID)
	}
	return ""
}

// --- tools ---

// registerCrewFunctionTools registers the function tools for one calling
// Crew or workflow Builder chat, plus one generated tool per function of the
// Crews and workflows tagged or attached to this chat. declare, when set,
// admits generated tool names through the product tool gate.
func (api *StreamingAPI) registerCrewFunctionTools(registrar definitionToolRegistrar, userID, sessionID string, parentReq QueryRequest, resolveCaller func(context.Context) (triggerLinkCaller, error), declare func(string)) error {
	if api == nil || api.scheduler == nil || api.productSchedules == nil {
		return nil
	}
	claims := &UserClaims{UserID: strings.TrimSpace(userID)}
	withClaims := func(ctx context.Context) context.Context {
		copy := *claims
		return context.WithValue(ctx, UserContextKey, &copy)
	}
	register := func(name, description string, parameters map[string]interface{}, execute func(context.Context, map[string]interface{}) (string, error)) error {
		return registrar.RegisterCustomTool(name, description, parameters, execute, crewFunctionToolCategory)
	}
	jsonOut := func(value interface{}) (string, error) {
		encoded, err := json.MarshalIndent(value, "", "  ")
		return string(encoded), err
	}
	// resolveOptionalTarget returns the named target, or the caller itself.
	resolveOptionalTarget := func(ctx context.Context, caller triggerLinkCaller, raw interface{}) (triggerTarget, error) {
		name, _ := raw.(string)
		if strings.TrimSpace(name) == "" {
			return selfTriggerTarget(caller), nil
		}
		return resolveFunctionTarget(ctx, claims, caller, name)
	}
	targetSchema := map[string]interface{}{"type": "string", "description": "The Crew or workflow name/tag/path. A Code project has no functions."}
	callFunction := func(ctx context.Context, caller triggerLinkCaller, target triggerTarget, function string, args map[string]interface{}, submissionID string, notify bool, timeout, wait time.Duration) (string, error) {
		functions, err := callableFunctions(ctx, target)
		if err != nil {
			return "", err
		}
		fn, ok := findCrewFunction(functions, function)
		if !ok {
			if len(functions) == 0 {
				return "", fmt.Errorf("%s %q offers no functions; ask its Builder to expose one (a function trigger with typed inputs)", target.Kind, target.Label)
			}
			return "", fmt.Errorf("%s %q has no function %q; it has: %s", target.Kind, target.Label, function, strings.Join(crewFunctionNames(functions), ", "))
		}
		call, err := api.startCrewFunctionCall(ctx, userID, caller, target, fn, args, timeout, submissionID)
		if err != nil {
			return "", err
		}
		if wait > 0 {
			timer := time.NewTimer(wait)
			defer timer.Stop()
			select {
			case <-call.done:
				out := call.snapshot()
				addFunctionCallPending(out, call)
				return jsonOut(out)
			case <-timer.C:
			case <-ctx.Done():
			}
		} else {
			select {
			case <-call.done: // already settled (e.g. a joined call that finished)
				out := call.snapshot()
				addFunctionCallPending(out, call)
				return jsonOut(out)
			default:
			}
		}
		response := call.snapshot()
		addFunctionCallPending(response, call)
		response["status"] = "running"
		if _, joined := response["joined"]; !joined {
			response["note"] = "Started; the target is working on it. Poll with get_function_call, or ask a Crew target for an update with ask_function_update. Do not call again for the same work."
		}
		if notify {
			executionID, watchErr := api.startCrewFunctionWatch(parentReq, sessionID, userID, call, timeout)
			if watchErr != nil {
				response["auto_notification"] = "unavailable: " + watchErr.Error() + "; poll with get_function_call"
			} else {
				response["auto_notification"] = map[string]interface{}{"execution_id": executionID}
				response["next"] = "Tell the user what was called and end your turn; the result arrives as an [AUTO-NOTIFICATION] in this chat."
			}
		}
		return jsonOut(response)
	}
	schemaSchema := map[string]interface{}{"type": "object", "description": "JSON Schema subset: type object|array|string|number|integer|boolean, properties, required, items, enum."}

	if err := register("define_function", "Declare or update a typed function on this workspace (omit target), or another Crew. Code projects have no functions. Workflow functions are managed in the workflow Builder. Callers use call_function and receive a result validated against result_schema.", map[string]interface{}{
		"type": "object", "required": []string{"name", "description", "instructions"}, "properties": map[string]interface{}{
			"target":        targetSchema,
			"name":          map[string]interface{}{"type": "string", "description": "snake_case name, e.g. run_login_flow."},
			"description":   map[string]interface{}{"type": "string", "description": "What the function does, for callers."},
			"instructions":  map[string]interface{}{"type": "string", "description": "What the target Crew does when this function is called."},
			"input_schema":  schemaSchema,
			"result_schema": schemaSchema,
		},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		ctx = withClaims(ctx)
		caller, err := resolveCaller(ctx)
		if err != nil {
			return "", err
		}
		target, err := resolveOptionalTarget(ctx, caller, args["target"])
		if err != nil {
			return "", err
		}
		inputSchema, _ := args["input_schema"].(map[string]interface{})
		resultSchema, _ := args["result_schema"].(map[string]interface{})
		spec := crewFunctionSpec{InputSchema: inputSchema, ResultSchema: resultSchema}
		spec.Name, _ = args["name"].(string)
		spec.Description, _ = args["description"].(string)
		spec.Instructions, _ = args["instructions"].(string)
		spec = spec.normalized()
		if err := spec.validate(); err != nil {
			return "", err
		}
		name := spec.Name
		if target.Kind == triggerCallerWorkflow {
			return "", errWorkflowFunctionsAreTriggers(target)
		}
		if target.CrewProfile == codeproduct.ProfileID || (caller.Stamp.ProfileID == codeproduct.ProfileID && args["target"] == nil) {
			return "", errCodeHasNoFunctions
		}
		functions, err := readCrewFunctions(ctx, target)
		if err != nil {
			return "", err
		}
		creator := caller.Stamp.Type + ":" + caller.Stamp.ID + " (" + caller.Label + ")"
		functions, updated := upsertCrewFunction(functions, spec, creator, time.Now().UTC())
		if err := writeCrewFunctions(ctx, target, functions); err != nil {
			return "", err
		}
		return jsonOut(map[string]interface{}{"target": target.describe(), "function": name, "updated": updated, "tool_name_for_callers": crewFunctionToolName(target.Label, name)})
	}); err != nil {
		return err
	}

	if err := register("delete_function", "Remove a function from this Crew/workflow (omit target) or from another Crew or editable workflow.", map[string]interface{}{
		"type": "object", "required": []string{"name"}, "properties": map[string]interface{}{"target": targetSchema, "name": map[string]interface{}{"type": "string"}},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		ctx = withClaims(ctx)
		caller, err := resolveCaller(ctx)
		if err != nil {
			return "", err
		}
		target, err := resolveOptionalTarget(ctx, caller, args["target"])
		if err != nil {
			return "", err
		}
		name, _ := args["name"].(string)
		if target.Kind == triggerCallerWorkflow {
			return "", errWorkflowFunctionsAreTriggers(target)
		}
		if target.CrewProfile == codeproduct.ProfileID && caller.Stamp.ProfileID != codeproduct.ProfileID {
			return "", fmt.Errorf("declare or remove Code functions in its owner's Code chat")
		}
		functions, err := readCrewFunctions(ctx, target)
		if err != nil {
			return "", err
		}
		kept := functions[:0]
		removed := false
		for _, fn := range functions {
			if fn.Name == strings.TrimSpace(name) {
				removed = true
				continue
			}
			kept = append(kept, fn)
		}
		if !removed {
			return "", fmt.Errorf("%s %q has no function %q", target.Kind, target.Label, name)
		}
		if err := writeCrewFunctions(ctx, target, kept); err != nil {
			return "", err
		}
		return jsonOut(map[string]interface{}{"target": target.describe(), "removed": strings.TrimSpace(name)})
	}); err != nil {
		return err
	}

	if err := register("list_functions", "List the typed functions a Crew or workflow offers (name, description, input and result schemas). Omit target for this workspace's own functions.", map[string]interface{}{
		"type": "object", "properties": map[string]interface{}{"target": targetSchema},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		ctx = withClaims(ctx)
		caller, err := resolveCaller(ctx)
		if err != nil {
			return "", err
		}
		target, err := resolveOptionalTarget(ctx, caller, args["target"])
		if err != nil {
			return "", err
		}
		functions, err := callableFunctions(ctx, target)
		if err != nil {
			return "", err
		}
		listed := make([]map[string]interface{}, 0, len(functions))
		for _, fn := range functions {
			listed = append(listed, map[string]interface{}{"name": fn.Name, "description": fn.Description, "input_schema": fn.InputSchema, "result_schema": fn.ResultSchema, "created_by": fn.CreatedBy})
		}
		return jsonOut(map[string]interface{}{"target": target.describe(), "functions": listed, "call_with": "call_function(target, function, args)"})
	}); err != nil {
		return err
	}

	if err := register("call_function", "Call a typed function of another Crew or workflow. Arguments are validated against its input schema; the target works in its own chat and returns a result validated against its result schema. It returns status=running and a call_id; the result arrives later as an [AUTO-NOTIFICATION] unless notify=false. Pass wait_seconds only for a quick function. Reuse submission_id after an uncertain retry. Poll with get_function_call and answer pending_inputs with reply_function_call.", map[string]interface{}{
		"type": "object", "required": []string{"target", "function"}, "properties": map[string]interface{}{
			"target":          targetSchema,
			"function":        map[string]interface{}{"type": "string", "description": "Function name from list_functions."},
			"args":            map[string]interface{}{"type": "object", "description": "Arguments matching the function's input schema."},
			"submission_id":   map[string]interface{}{"type": "string", "minLength": 1, "maxLength": 128, "description": "Stable ID for this intended call; reuse it after an uncertain retry to get the original call_id."},
			"notify":          map[string]interface{}{"type": "boolean", "description": "Resume this chat with the result when it arrives (default true)."},
			"wait_seconds":    map[string]interface{}{"type": "integer", "minimum": 0, "maximum": int(crewFunctionFastWait / time.Second), "description": "Wait up to this long for the result before returning status=running (default 0: return at once)."},
			"timeout_minutes": map[string]interface{}{"type": "integer", "minimum": 1, "maximum": int(triggerTargetMaxTimeout / time.Minute), "description": "How long the call may take before it fails (default 60)."},
		},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		ctx = withClaims(ctx)
		caller, err := resolveCaller(ctx)
		if err != nil {
			return "", err
		}
		raw, _ := args["target"].(string)
		target, err := resolveFunctionTarget(ctx, claims, caller, raw)
		if err != nil {
			return "", err
		}
		function, _ := args["function"].(string)
		callArgs, _ := args["args"].(map[string]interface{})
		if raw, present := args["args"]; present && raw != nil && callArgs == nil {
			return "", fmt.Errorf("args must be a JSON object")
		}
		notify := true
		if value, ok := args["notify"].(bool); ok {
			notify = value
		}
		timeout, err := triggerTargetTimeout(args["timeout_minutes"])
		if err != nil {
			return "", err
		}
		submissionID, _ := args["submission_id"].(string)
		return callFunction(ctx, caller, target, function, callArgs, submissionID, notify, timeout, crewFunctionWait(args["wait_seconds"]))
	}); err != nil {
		return err
	}

	callRecord := func(ctx context.Context, args map[string]interface{}) (*crewFunctionCall, triggerLinkCaller, error) {
		caller, err := resolveCaller(ctx)
		if err != nil {
			return nil, triggerLinkCaller{}, err
		}
		id, _ := args["call_id"].(string)
		call := lookupCrewFunctionCall(id)
		if call == nil {
			return nil, caller, fmt.Errorf("unknown function call %q (calls are tracked while the server runs)", id)
		}
		if call.TargetProfileID == codeproduct.ProfileID && call.UserID != userID {
			return nil, caller, fmt.Errorf("function call %s belongs to another caller", id)
		}
		if call.TargetProfileID == codeproduct.ProfileID {
			ownerID, ok := crewProjectOwnerID(call.TargetPath)
			source := triggerLinkCaller{Stamp: triggerCaller{Type: call.CallerKind, ProfileID: call.CallerProfileID, ID: call.CallerID}, Path: call.CallerPath}
			if !ok || authorizeOwnedCodeCaller(ctx, userID, ownerID, source) != nil ||
				authorizeOwnedCodeCaller(ctx, userID, ownerID, caller) != nil {
				return nil, caller, fmt.Errorf("private Code access denied")
			}
		}
		if caller.Stamp.ProfileID == codeproduct.ProfileID {
			ownerID, ok := crewProjectOwnerID(caller.Path)
			if !ok && isCodeProjectPath(caller.Path) {
				ownerID, ok = sanitizeUserIDForPath(userID), true
			}
			if !ok || !codeRoleFor(ctx, userID, ownerID, caller.Stamp.ID).atLeast(codeRoleEditor) {
				return nil, caller, fmt.Errorf("private Code access denied")
			}
			// Calls belong to their actual owner and source workspace.
			if call.CallerProfileID == codeproduct.ProfileID && call.UserID != userID {
				return nil, caller, fmt.Errorf("function call %s belongs to another caller", id)
			}
			isCodeSource := crewFunctionKey(call.CallerKind, call.CallerProfileID, call.CallerID) == crewFunctionKey(caller.Stamp.Type, caller.Stamp.ProfileID, caller.Stamp.ID)
			if isCodeSource && canonicalCrewWorkspaceRoot(call.CallerPath) != canonicalCrewWorkspaceRoot(agentProfileRuntimeWorkspace(userID, caller.Path)) {
				return nil, caller, fmt.Errorf("private Code access denied")
			}
			if call.TargetChat != "" {
				// A call between two chats of this same Code (PLAT-648).
				if call.CallerID != call.TargetID || call.CallerID != caller.Stamp.ID ||
					canonicalCrewWorkspaceRoot(call.TargetPath) != canonicalCrewWorkspaceRoot(agentProfileRuntimeWorkspace(userID, caller.Path)) {
					return nil, caller, fmt.Errorf("private Code access denied")
				}
			} else if call.TargetProfileID == codeproduct.ProfileID {
				// Only calls between chats of one Code exist; no call targets another Code.
				return nil, caller, fmt.Errorf("private Code access denied")
			}
		}
		return call, caller, nil
	}
	// A call between two chats of one Code belongs to exactly those two chats:
	// this turn's session must be the sending (or receiving) chat.
	isCaller := func(call *crewFunctionCall, caller triggerLinkCaller) bool {
		if call.CallerChatSession != "" && call.CallerChatSession != sessionID {
			return false
		}
		return crewFunctionKey(call.CallerKind, call.CallerProfileID, call.CallerID) == crewFunctionKey(caller.Stamp.Type, caller.Stamp.ProfileID, caller.Stamp.ID)
	}
	isTargetOf := func(call *crewFunctionCall, caller triggerLinkCaller) bool {
		if call.TargetChatSession != "" && call.TargetChatSession != sessionID {
			return false
		}
		if call.TargetProfileID == codeproduct.ProfileID && canonicalCrewWorkspaceRoot(call.TargetPath) != canonicalCrewWorkspaceRoot(agentProfileRuntimeWorkspace(userID, caller.Path)) {
			return false
		}
		return crewFunctionKey(call.TargetKind, call.TargetProfileID, call.TargetID) == crewFunctionKey(caller.Stamp.Type, caller.Stamp.ProfileID, caller.Stamp.ID)
	}
	callIDSchema := map[string]interface{}{"type": "string", "description": "call_id from call_function, or from the [Function call ...] task you received."}

	if err := register("get_function_call", "Check a function call you made: status, result or error, pending_inputs, the target's latest progress reports, and a short tail of what the target is doing right now. Answer a pending request with reply_function_call. Does not interrupt the target.", map[string]interface{}{
		"type": "object", "required": []string{"call_id"}, "properties": map[string]interface{}{"call_id": callIDSchema},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		ctx = withClaims(ctx)
		call, caller, err := callRecord(ctx, args)
		if err != nil {
			return "", err
		}
		if !isCaller(call, caller) && !isTargetOf(call, caller) {
			return "", fmt.Errorf("function call %s belongs to another caller", call.ID)
		}
		out := call.snapshot()
		addFunctionCallPending(out, call)
		call.mu.Lock()
		terminal, runID := call.terminalLocked(), call.RunID
		call.mu.Unlock()
		if !terminal && call.TargetChat != "" {
			// A sibling chat call runs in that chat's own conversation.
			out["run_status"] = "idle"
			if api.conversationTurnOccupied(call.TargetChatSession) {
				out["run_status"] = "busy"
			}
			if activity := api.crewFunctionActivity(call.TargetChatSession); activity != nil {
				out["recent_activity"] = activity
			}
		} else if !terminal && call.TargetKind == triggerCallerCrew && runID != "" {
			state, stateErr := api.readTriggerTargetRun(ctx, userID, call.caller, call.target, call.TriggerID, runID)
			if stateErr != nil {
				return "", fmt.Errorf("cannot check function call %s: %w", call.ID, stateErr)
			}
			out["run_status"] = state.Status
			if activity := api.crewFunctionActivity(crewTargetRunSessionID(state)); activity != nil {
				out["recent_activity"] = activity
			}
		}
		return jsonOut(out)
	}); err != nil {
		return err
	}

	if err := register("reply_function_call", "Answer a pending input request on a function call you made. Use the request_id in get_function_call.pending_inputs; a listed choice must match exactly. Works for Crew and workflow targets.", map[string]interface{}{
		"type": "object", "required": []string{"call_id", "request_id", "response"}, "properties": map[string]interface{}{
			"call_id":    callIDSchema,
			"request_id": map[string]interface{}{"type": "string", "description": "Pending request ID from get_function_call."},
			"response":   map[string]interface{}{"type": "string", "description": "Answer or exact listed choice."},
		},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		ctx = withClaims(ctx)
		call, caller, err := callRecord(ctx, args)
		if err != nil {
			return "", err
		}
		if !isCaller(call, caller) || call.UserID != userID {
			return "", fmt.Errorf("only the caller of function call %s can answer its pending input", call.ID)
		}
		// The call may have started before the actor lost access. Resolve the
		// exact target again before letting an answer steer its live turn.
		currentTarget, err := resolveFunctionTarget(ctx, claims, caller, call.TargetPath)
		if err != nil || currentTarget.Kind != call.TargetKind || currentTarget.stampID() != call.TargetID ||
			(currentTarget.Kind == triggerCallerCrew && crewFunctionKey(currentTarget.Kind, currentTarget.CrewProfile, currentTarget.CrewID) != crewFunctionKey(call.TargetKind, call.TargetProfileID, call.TargetID)) ||
			canonicalCrewWorkspaceRoot(crewFunctionRoot(ctx, currentTarget)) != canonicalCrewWorkspaceRoot(call.TargetPath) {
			return "", fmt.Errorf("function call %s target is unavailable or access denied", call.ID)
		}
		requestID, _ := args["request_id"].(string)
		response, _ := args["response"].(string)
		if err := submitFunctionCallInput(call, strings.TrimSpace(requestID), response); err != nil {
			return "", err
		}
		return jsonOut(map[string]interface{}{"call_id": call.ID, "request_id": requestID, "status": "submitted"})
	}); err != nil {
		return err
	}

	if err := register("ask_function_update", "Ask a Crew that is running your function call how it is going. The question is delivered into its running turn (Crew targets only; workflow runs do not take mid-run messages). It answers with report_function_progress; read the answer with get_function_call, or it arrives with the final result.", map[string]interface{}{
		"type": "object", "required": []string{"call_id", "question"}, "properties": map[string]interface{}{
			"call_id":  callIDSchema,
			"question": map[string]interface{}{"type": "string", "description": "What you want to know, self-contained."},
		},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		ctx = withClaims(ctx)
		call, caller, err := callRecord(ctx, args)
		if err != nil {
			return "", err
		}
		if !isCaller(call, caller) {
			return "", fmt.Errorf("only the caller of function call %s can ask for an update", call.ID)
		}
		if call.TargetKind != triggerCallerCrew {
			return "", fmt.Errorf("workflow runs do not accept mid-run messages; use get_function_call for status and progress")
		}
		if call.TargetChat != "" {
			return "", fmt.Errorf("a chat of this Code takes no mid-turn questions; read its progress and recent activity with get_function_call")
		}
		question, _ := args["question"].(string)
		if strings.TrimSpace(question) == "" {
			return "", fmt.Errorf("question is required")
		}
		call.mu.Lock()
		terminal, runID := call.terminalLocked() && !call.acceptsLateLocked(), call.RunID
		call.mu.Unlock()
		if terminal {
			return "", fmt.Errorf("function call %s already finished; read it with get_function_call", call.ID)
		}
		message := fmt.Sprintf("[Update request for function call %s] %s\nAnswer with report_function_progress(call_id=%q, message=...) and keep working on the call.", call.ID, strings.TrimSpace(question), call.ID)
		result, err := api.sendToCrewTriggerRun(ctx, userID, call.caller, call.target, call.TriggerID, runID, message)
		if err != nil {
			return "", err
		}
		result["call_id"] = call.ID
		result["next"] = "The answer arrives as a progress report: read it with get_function_call."
		return jsonOut(result)
	}); err != nil {
		return err
	}

	if err := register("report_function_progress", "While working on a function call you received ([Function call <call_id>] task), report a milestone or answer an update request. The caller sees the latest reports without interrupting you.", map[string]interface{}{
		"type": "object", "required": []string{"call_id", "message"}, "properties": map[string]interface{}{
			"call_id": callIDSchema,
			"message": map[string]interface{}{"type": "string"},
			"percent": map[string]interface{}{"type": "number", "minimum": 0, "maximum": 100},
		},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		ctx = withClaims(ctx)
		call, caller, err := callRecord(ctx, args)
		if err != nil {
			return "", err
		}
		if !isTargetOf(call, caller) {
			return "", fmt.Errorf("only the target of function call %s can report its progress", call.ID)
		}
		message, _ := args["message"].(string)
		if strings.TrimSpace(message) == "" {
			return "", fmt.Errorf("message is required")
		}
		entry := crewFunctionProgress{At: time.Now().UTC(), Message: strings.TrimSpace(message)}
		if percent, ok := crewFunctionNumber(args["percent"]); ok {
			entry.Percent = &percent
		}
		call.mu.Lock()
		if call.terminalLocked() && !call.acceptsLateLocked() {
			call.mu.Unlock()
			return "", fmt.Errorf("function call %s already finished", call.ID)
		}
		call.Progress = append(call.Progress, entry)
		if len(call.Progress) > crewFunctionProgressKeep {
			call.Progress = call.Progress[len(call.Progress)-crewFunctionProgressKeep:]
		}
		if call.Status == "queued" {
			call.Status = "running"
		}
		call.UpdatedAt = entry.At
		call.mu.Unlock()
		call.persist()
		return jsonOut(map[string]interface{}{"call_id": call.ID, "recorded": true})
	}); err != nil {
		return err
	}

	if err := register("return_function_result", "Finish a function call you received ([Function call <call_id>] task): pass result as JSON matching the function's result schema, or error to report that it could not be done. The caller receives exactly this, not your chat reply. An invalid result is rejected with the reason so you can fix it.", map[string]interface{}{
		"type": "object", "required": []string{"call_id"}, "properties": map[string]interface{}{
			"call_id": callIDSchema,
			"result":  map[string]interface{}{"description": "The result, matching the function's result schema."},
			"error":   map[string]interface{}{"type": "string", "description": "Set instead of result when the call could not be completed."},
		},
	}, func(ctx context.Context, args map[string]interface{}) (string, error) {
		ctx = withClaims(ctx)
		call, caller, err := callRecord(ctx, args)
		if err != nil {
			return "", err
		}
		if !isTargetOf(call, caller) {
			return "", fmt.Errorf("only the target of function call %s can return its result", call.ID)
		}
		if errText, _ := args["error"].(string); strings.TrimSpace(errText) != "" {
			if !call.settle("failed", nil, strings.TrimSpace(errText)) {
				return "", fmt.Errorf("function call %s already finished", call.ID)
			}
			return jsonOut(map[string]interface{}{"call_id": call.ID, "status": "failed"})
		}
		result, present := args["result"]
		if !present {
			return "", fmt.Errorf("pass result (or error)")
		}
		call.mu.Lock()
		schema := call.ResultSchema
		freeText := call.FreeText
		call.mu.Unlock()
		if freeText {
			// The implicit ask is free text by contract: never reject a real
			// answer over its shape. Structured answers are kept as JSON text.
			result = crewFunctionFreeTextAnswer(result)
		}
		if problems := validateCrewFunctionValue(schema, result); len(problems) > 0 {
			call.mu.Lock()
			call.InvalidResults++
			invalid := call.InvalidResults
			call.PartialResult = result
			call.mu.Unlock()
			reason := "result does not match the result schema: " + strings.Join(problems, "; ")
			if invalid >= crewFunctionMaxInvalidResults {
				call.settle("failed", nil, "the target returned invalid results twice; last: "+reason)
				return "", fmt.Errorf("%s. The call has now failed", reason)
			}
			return "", fmt.Errorf("%s. Your submission is kept for the caller; fix the shape and call return_function_result again (put anything that doesn't fit, like extra links, into an existing string field or error)", reason)
		}
		if !call.settle("completed", result, "") {
			return "", fmt.Errorf("function call %s already finished", call.ID)
		}
		return jsonOut(map[string]interface{}{"call_id": call.ID, "status": "completed"})
	}); err != nil {
		return err
	}

	// Generated tools: one per function of every Crew/workflow tagged or
	// attached to this chat.
	ctx := withClaims(context.Background())
	caller, err := resolveCaller(ctx)
	if err != nil {
		return nil
	}
	paths := append([]string(nil), parentReq.WorkflowContextPaths...)
	if caller.Stamp.Type == triggerCallerCrew {
		if attached, attachedErr := readWorkWorkflowReferences(ctx, caller.Path); attachedErr == nil {
			paths = append(paths, attached...)
		}
	}
	seen := map[string]bool{}
	for _, raw := range paths {
		target, err := resolveFunctionTarget(ctx, claims, caller, raw)
		if err != nil || caller.isTarget(target) {
			continue
		}
		functions, err := callableFunctions(ctx, target)
		if err != nil {
			continue
		}
		for _, fn := range functions {
			toolName := crewFunctionToolName(target.Label, fn.Name)
			if seen[toolName] {
				continue
			}
			seen[toolName] = true
			params := fn.InputSchema
			if len(params) == 0 {
				params = map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
			}
			description := fmt.Sprintf("Function %q of %s %q: %s Returns at once with status=running and a call_id; the result, validated against its result schema, arrives later as an [AUTO-NOTIFICATION] in this chat. Do not call again for the same work.", fn.Name, target.Kind, target.Label, fn.Description)
			if declare != nil {
				declare(toolName)
			}
			fnTarget, fnName := target, fn.Name
			if err := register(toolName, description, params, func(ctx context.Context, args map[string]interface{}) (string, error) {
				ctx = withClaims(ctx)
				caller, err := resolveCaller(ctx)
				if err != nil {
					return "", err
				}
				return callFunction(ctx, caller, fnTarget, fnName, args, "", true, triggerTargetDefaultTimeout, 0)
			}); err != nil {
				return err
			}
		}
	}
	return nil
}
