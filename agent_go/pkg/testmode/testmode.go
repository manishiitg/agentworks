// Package testmode runs a workflow step once without real-world side effects
// (PLAT-562, design: docs/design/step_test_mode.md).
//
// A test run owns a set of tool sessions. Every tool call made under one of
// them passes the Guard (installed into mcpagent's toolguard): reads run,
// writes are redirected to the run's own copy (workflow DB, run folder) or
// answered with a recorded "would have done X" stub. Anything the guard
// cannot classify is stubbed: test mode fails closed.
//
// This package is a leaf: it imports no platform packages, so pkg/common can
// propagate test mode to child sessions.
package testmode

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/mcpagent/toolguard"
)

// EnvFlag is set to "1" in a test run's shell and script environment.
const EnvFlag = "AGENTWORKS_TEST_MODE"

// FolderPrefix starts every test run's folder under runs/.
const FolderPrefix = "test-"

// Run is one test-mode execution.
type Run struct {
	ID           string // e.g. "test-<execution id>"; also the iteration folder under runs/
	WorkflowPath string // workspace-relative workflow folder
	RunFolder    string // relative to runs/, e.g. "test-<id>/<group>"
	DBPath       string // workspace-relative path of the run's DB copy ("" when the workflow has no DB)
	DBAbsPath    string // absolute path of the DB copy
	ActionsPath  string // absolute path of the recorded actions (JSON lines)
	DocsRootAbs  string // absolute workspace root
	// FixedBlockedWrites denies writes to every entry of the workflow folder
	// except the run's own folder (DB, learnings, knowledgebase, code,
	// planning, real runs), computed when the run starts.
	FixedBlockedWrites []string

	mu      sync.Mutex
	actions []Action
}

// Action is one guarded call, as recorded in the run.
type Action struct {
	Time      time.Time `json:"time"`
	SessionID string    `json:"session_id,omitempty"`
	Kind      string    `json:"kind"`
	Server    string    `json:"server,omitempty"`
	Tool      string    `json:"tool"`
	Decision  string    `json:"decision"` // "stubbed" | "redirected"
	Reason    string    `json:"reason"`
	Args      string    `json:"args,omitempty"`
}

// Actions returns a copy of the recorded actions.
func (r *Run) Actions() []Action {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Action(nil), r.actions...)
}

func (r *Run) record(a Action) {
	r.mu.Lock()
	r.actions = append(r.actions, a)
	r.mu.Unlock()
	if r.ActionsPath == "" {
		return
	}
	line, err := json.Marshal(a)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(r.ActionsPath), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(r.ActionsPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644) //nolint:gosec // server-owned path inside the test run folder
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

// Summary is a short human-readable account of what the run did not do.
func (r *Run) Summary() string {
	actions := r.Actions()
	var b strings.Builder
	fmt.Fprintf(&b, "TEST MODE run %s: nothing was sent, submitted, posted or spent; the workflow DB was a copy (%s) and files went to runs/%s.\n", r.ID, emptyAs(r.DBPath, "no DB"), r.RunFolder)
	stubbed := 0
	for _, a := range actions {
		if a.Decision == "stubbed" {
			stubbed++
		}
	}
	fmt.Fprintf(&b, "%d action(s) stubbed (would have run):\n", stubbed)
	for _, a := range actions {
		if a.Decision != "stubbed" {
			continue
		}
		name := a.Tool
		if a.Server != "" {
			name = a.Server + "/" + a.Tool
		}
		fmt.Fprintf(&b, "- %s %s — %s\n", name, truncate(a.Args, 200), a.Reason)
	}
	return b.String()
}

var (
	mu       sync.RWMutex
	sessions = map[string]*Run{}
)

// Register marks a tool session as part of run. Every call it makes is guarded.
func Register(sessionID string, run *Run) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" || run == nil {
		return
	}
	mu.Lock()
	sessions[sessionID] = run
	mu.Unlock()
}

// Inherit gives child the parent's test run, if the parent has one. Called
// wherever a child session copies a parent's guard (delegation, sub-agents).
func Inherit(parentSessionID, childSessionID string) {
	if run := ForSession(parentSessionID); run != nil {
		Register(childSessionID, run)
	}
}

// End removes every session of run.
func End(run *Run) {
	if run == nil {
		return
	}
	mu.Lock()
	for id, r := range sessions {
		if r == run {
			delete(sessions, id)
		}
	}
	mu.Unlock()
}

// ForSession returns the test run a session belongs to, or nil.
func ForSession(sessionID string) *Run {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	mu.RLock()
	defer mu.RUnlock()
	return sessions[sessionID]
}

// ContextSessionID, when set by the server, reads the trusted session id a
// tool call carries in its context; the guard checks it as well as the
// call's own session id.
var ContextSessionID func(ctx context.Context) string

func runFor(ctx context.Context, call toolguard.Call) (*Run, string) {
	if run := ForSession(call.SessionID); run != nil {
		return run, call.SessionID
	}
	if ContextSessionID != nil && ctx != nil {
		if sid := ContextSessionID(ctx); sid != "" {
			if run := ForSession(sid); run != nil {
				return run, sid
			}
		}
	}
	return nil, ""
}

// The guard is installed wherever this package is linked (pkg/common imports
// it), so no binary can run a test session without it.
func init() { toolguard.Set(Guard) }

// Guard is the toolguard policy. Calls outside a test run are untouched.
func Guard(ctx context.Context, call toolguard.Call) toolguard.Decision {
	run, sessionID := runFor(ctx, call)
	if run == nil {
		return toolguard.Decision{}
	}
	verdict, reason := classify(ctx, call)
	switch verdict {
	case allow:
		return toolguard.Decision{}
	case redirect:
		run.record(newAction(call, sessionID, "redirected", reason))
		return toolguard.Decision{}
	}
	run.record(newAction(call, sessionID, "stubbed", reason))
	name := call.Tool
	if call.Server != "" {
		name = call.Server + "/" + call.Tool
	}
	return toolguard.Decision{Stub: true, Result: fmt.Sprintf(
		"TEST MODE: %s was NOT run (%s). It was recorded as \"would have run\" with these arguments. "+
			"Continue the step as if it succeeded where you can; do not retry it or look for another way to perform it.",
		name, reason)}
}

func newAction(call toolguard.Call, sessionID, decision, reason string) Action {
	args, _ := json.Marshal(call.Args)
	return Action{
		Time:      time.Now().UTC(),
		SessionID: sessionID,
		Kind:      string(call.Kind),
		Server:    call.Server,
		Tool:      call.Tool,
		Decision:  decision,
		Reason:    reason,
		Args:      truncate(string(args), 2000),
	}
}

type verdict int

const (
	stub verdict = iota
	allow
	redirect
)

// Platform tools that only read, or whose writes the platform already
// redirects into the test run (files, the DB copy). Everything not listed is
// stubbed.
var platformTools = map[string]verdict{
	// mcpagent's own virtual tools: tool discovery and large-output reads.
	"get_api_spec":        allow,
	"search_tools":        allow,
	"search_large_output": allow,
	"read_skill":          allow,
	// Workspace files: writes are confined by the session's folder guard,
	// which in a test run only grants the test run folder.
	"read_image":                    allow,
	"diff_patch_workspace_file":     allow,
	"read_workspace_file":           allow,
	"list_workspace_files":          allow,
	"search_workspace_files":        allow,
	"scan_workflow_script_db_usage": allow,
	// Shell runs (see the design: network is NOT contained); its DB_PATH is
	// the copy and its file writes are confined to the test run folder.
	"execute_shell_command": redirect,
	// A model call costs tokens but has no external effect.
	"generate_text_llm": allow,
	// Workflow DB: reads run; mutations go to the run's DB copy.
	"query_workflow_db":    allow,
	"query_workflow_costs": allow,
	"mutate_workflow_db":   redirect,
	"get_goal_metrics":     allow,
	// Knowledgebase reads.
	"browse_knowledgebase": allow,
	"read_knowledgebase":   allow,
	// Orchestrator sub-steps run inside the same test run (their sessions
	// are registered by the step controller).
	"call_sub_agent":             allow,
	"call_scripted_sub_agent":    allow,
	"query_sub_agent":            allow,
	"get_sub_agent_conversation": allow,
	"get_route_description":      allow,
	"stop_sub_agent":             allow,
	"mark_step_complete":         allow,
	// The browser is decided per command below.
	"agent_browser": allow,
}

func classify(ctx context.Context, call toolguard.Call) (verdict, string) {
	switch call.Kind {
	case toolguard.KindMCP:
		return classifyMCP(ctx, call)
	case toolguard.KindCustom, toolguard.KindVirtual:
		v, known := platformTools[call.Tool]
		if !known {
			return stub, "not on the test-mode list of read-only or redirected platform tools"
		}
		if call.Tool == "agent_browser" {
			return classifyBrowser(call.Args)
		}
		switch v {
		case redirect:
			if call.Tool == "execute_shell_command" {
				return redirect, "ran with the DB copy and test run folder; outbound network is not blocked"
			}
			return redirect, "written to the test run's copy"
		case allow:
			return allow, ""
		}
	}
	return stub, "unknown tool kind"
}

// classifyMCP allows only a tool its own server marks read-only and not
// destructive. No annotation, an error, or a tool the server does not list
// means stub.
func classifyMCP(ctx context.Context, call toolguard.Call) (verdict, string) {
	if call.Annotations == nil {
		return stub, "the tool's read-only annotation could not be checked"
	}
	ann, found, err := call.Annotations(ctx)
	if err != nil {
		return stub, "the tool's read-only annotation could not be read: " + err.Error()
	}
	if !found {
		return stub, "the server does not list this tool"
	}
	if ann.ReadOnlyHint == nil || !*ann.ReadOnlyHint {
		return stub, "the tool is not annotated read-only (readOnlyHint)"
	}
	if ann.DestructiveHint != nil && *ann.DestructiveHint {
		return stub, "the tool is annotated destructive"
	}
	return allow, ""
}

// Browser commands that only look: navigate, read the page, wait. Clicks,
// typing, form fills, uploads, downloads, JavaScript (eval can submit a form
// as easily as read a title), network routing and recordings are stubbed.
var browserReadCommands = map[string]bool{
	"status": true, "skills": true, "tab": true,
	"open": true, "goto": true, "navigate": true, "back": true, "forward": true, "reload": true,
	"snapshot": true, "get": true, "is": true, "screenshot": true, "pdf": true,
	"console": true, "errors": true, "wait": true, "scroll": true, "scrollintoview": true,
}

func classifyBrowser(args map[string]interface{}) (verdict, string) {
	command, _ := args["command"].(string)
	command = strings.ToLower(strings.TrimSpace(command))
	if browserReadCommands[command] {
		return allow, ""
	}
	if command == "" {
		return stub, "browser command missing"
	}
	return stub, fmt.Sprintf("browser command %q can change the page or the site; test mode only navigates and reads", command)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func emptyAs(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// Paths: guard paths are workspace-relative ("Workflow/x/runs/...") or
// absolute under the docs root; both forms are handled.

// Root returns the test run's own folder, workspace-relative.
func (r *Run) Root() string {
	return filepath.ToSlash(filepath.Join(r.WorkflowPath, "runs", r.ID))
}

func (r *Run) relative(path string) string {
	clean := filepath.ToSlash(filepath.Clean(strings.TrimSpace(path)))
	if r.DocsRootAbs != "" {
		root := filepath.ToSlash(filepath.Clean(r.DocsRootAbs))
		if clean == root {
			return ""
		}
		if strings.HasPrefix(clean, root+"/") {
			return strings.TrimPrefix(clean, root+"/")
		}
	}
	return strings.TrimPrefix(clean, "./")
}

func under(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+"/")
}

// Writable reports whether path may be written in the test run: only inside
// the run's own folder.
func (r *Run) Writable(path string) bool {
	return under(r.relative(path), r.Root())
}

// BlockedWrites returns the write-deny list for a session of the run: the
// fixed list made when the run started (every sibling of the run folder in
// the workflow), plus every granted write path outside the run folder that is
// not an ancestor of it (attached folders, Crew folders, other workflows).
func (r *Run) BlockedWrites(writePaths, blocked []string) []string {
	out := append([]string(nil), blocked...)
	out = append(out, r.FixedBlockedWrites...)
	root := r.Root()
	for _, p := range writePaths {
		rel := r.relative(p)
		if rel == "" || under(rel, root) || under(root, rel) {
			continue
		}
		out = append(out, p)
	}
	return out
}
