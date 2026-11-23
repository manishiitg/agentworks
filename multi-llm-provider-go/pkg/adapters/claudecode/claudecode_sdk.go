package claudecode

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/nativeinput"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/sessionregistry"
)

//go:embed claude_sdk_bridge.cjs
var claudeSDKBridge []byte

// SetSDKTransport selects the official Agent SDK for this adapter instance.
func (c *ClaudeCodeInteractiveAdapter) SetSDKTransport(enabled bool) { c.sdkTransport = enabled }

// claudeSDKProcess is one turn's handle on its SDK session: live input and
// controls go to the session; done closes when the turn ends.
type claudeSDKProcess struct {
	session    *claudeSDKSession
	cancel     func()
	controlsMu sync.Mutex
	controls   map[string]chan error
	done       chan struct{}
	// interrupted is set when the person stopped the turn (Stop or Escape); the SDK then ends it with an error result.
	interrupted atomic.Bool
	// answered is set once the turn has an answer; release then ends the turn with it.
	hasAnswer   atomic.Bool
	release     chan struct{}
	releaseOnce sync.Once
	// answering is the person's message the next answer replies to: the turn's
	// prompt, then each steer once Claude took it (guarded by controlsMu).
	answering string
	steers    map[string]string
}

// SDKAnswer is one answer Claude finished inside a chat turn: the turn's own
// answer, or the answer to a steer sent while the turn ran (PLAT-872). The host
// records each in the chat history at that point, so an answer is kept even when
// the turn that contained it is later released or cancelled.
type SDKAnswer struct {
	Owner string
	// Text is Claude's answer, verbatim. Answers is the person's message it replies to.
	Text    string
	Answers string
	At      time.Time
}

var claudeSDKAnswerListener atomic.Pointer[func(SDKAnswer)]

// SetSDKAnswerListener receives each answer Claude finishes inside a chat turn.
func SetSDKAnswerListener(listener func(SDKAnswer)) {
	if listener == nil {
		claudeSDKAnswerListener.Store(nil)
		return
	}
	claudeSDKAnswerListener.Store(&listener)
}

func (p *claudeSDKProcess) answered(owner, text string) {
	if owner == "" || strings.TrimSpace(text) == "" {
		return
	}
	p.controlsMu.Lock()
	answers := p.answering
	p.controlsMu.Unlock()
	if listener := claudeSDKAnswerListener.Load(); listener != nil {
		(*listener)(SDKAnswer{Owner: owner, Text: text, Answers: answers, At: time.Now()})
	}
}

// ReleaseAnsweredSDKTurn ends a chat's running Claude SDK turn with the answer
// it already gave, when it has one (a turn held open after its answer by a
// steer or moved-aside work). The server's idle limit calls it before
// cancelling, so an answered turn completes instead of failing (PLAT-872).
func ReleaseAnsweredSDKTurn(owner string) bool {
	process, ok := claudeSDKProcesses.Get(strings.TrimSpace(owner))
	if !ok || process.release == nil || !process.hasAnswer.Load() {
		return false
	}
	process.releaseOnce.Do(func() { close(process.release) })
	return true
}

var claudeSDKProcesses = sessionregistry.NewOwnerRegistry[*claudeSDKProcess]()

// ClaudeSDKSessionActive reports whether an owner's chat runs Claude through
// the Agent SDK right now.
func ClaudeSDKSessionActive(ownerSessionID string) bool {
	_, ok := claudeSDKProcesses.Get(strings.TrimSpace(ownerSessionID))
	return ok
}

func (p *claudeSDKProcess) write(value any) error {
	err := p.session.input.write(value)
	if err != nil {
		if _, streaming := value.(claudeSDKStreamingParams); streaming {
			p.session.kill()
		}
	}
	return err
}

func claudeSDKEntry() (string, error) {
	if path := strings.TrimSpace(os.Getenv("CLAUDE_AGENT_SDK_PATH")); path != "" {
		path, err := filepath.Abs(path)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(path); err != nil {
			return "", errors.New("Claude needs a component that is unavailable. Update the app and try again")
		}
		return path, nil
	}
	// Managed server installs put SDKs beside the coding CLI binaries.
	if binary, err := exec.LookPath("claude"); err == nil {
		path := filepath.Join(filepath.Dir(binary), "..", "lib", "node_modules", "@anthropic-ai", "claude-agent-sdk", "sdk.mjs")
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	// No dependency downloads at request time. Operators install the official SDK
	// beside the other coding runtimes; the optional path supports local installs.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "npm", "root", "-g").Output()
	if err == nil {
		path := filepath.Join(strings.TrimSpace(string(out)), "@anthropic-ai", "claude-agent-sdk", "sdk.mjs")
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", errors.New("Claude needs a component that isn't installed. Update the app and try again")
}

func claudeSDKCommand(ctx context.Context, executable string, args []string) (*exec.Cmd, func(), error) {
	entry, err := claudeSDKEntry()
	if err != nil {
		return nil, func() {}, err
	}
	node, err := exec.LookPath("node")
	if err != nil {
		return nil, func() {}, fmt.Errorf("Claude Agent SDK requires Node.js: %w", err)
	}
	// A private folder per launch: the bridge runs outside Landlock, so neither
	// it nor the policy files it writes may sit where a confined CLI can write
	// (PLAT-872-51). Confined CLIs get their own TMPDIR, never this one.
	dir, err := os.MkdirTemp("", "claude-sdk-*")
	if err != nil {
		return nil, func() {}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	bridge := filepath.Join(dir, "bridge.cjs")
	if err = os.WriteFile(bridge, claudeSDKBridge, 0o600); err != nil {
		cleanup()
		return nil, func() {}, err
	}
	return exec.CommandContext(ctx, node, append([]string{bridge, entry, executable}, args...)...), cleanup, nil
}

func claudeSDKPermission(ctx context.Context, tool string, input json.RawMessage, suggestions ...json.RawMessage) map[string]any {
	deny := map[string]any{"behavior": "deny", "message": "This request was not approved. Use the chat or account setup to continue."}
	// Tool policy still runs natively (including the existing deny hooks). Only
	// native requests that reach canUseTool use attended cards.
	var value map[string]any
	if json.Unmarshal(input, &value) != nil {
		return deny
	}
	switch tool {
	case "AskUserQuestion":
		var request struct {
			Questions []struct {
				Question    string               `json:"question"`
				Options     []nativeinput.Option `json:"options"`
				MultiSelect bool                 `json:"multiSelect"`
			} `json:"questions"`
		}
		if json.Unmarshal(input, &request) != nil || len(request.Questions) == 0 {
			return deny
		}
		questions := make([]nativeinput.Question, 0, len(request.Questions))
		for i, q := range request.Questions {
			questions = append(questions, nativeinput.Question{ID: fmt.Sprint(i), Question: q.Question, Options: q.Options, MultiSelect: q.MultiSelect, AllowOther: true})
		}
		answers, err := nativeinput.Ask(ctx, questions)
		if err != nil {
			return deny
		}
		mapped := map[string]string{}
		for i, a := range answers {
			mapped[request.Questions[i].Question] = strings.Join(nativeinput.Values(a), ", ")
		}
		value["answers"] = mapped
	case "ExitPlanMode":
		plan, _ := value["plan"].(string)
		answers, err := nativeinput.AskWithKind(ctx, nativeinput.PlanApprovalRequest, []nativeinput.Question{{ID: "plan", Question: "Approve this plan?\n" + plan, Options: []nativeinput.Option{{Label: "Approve"}, {Label: "Reject"}}}})
		if err != nil || len(answers) != 1 || len(answers[0].SelectedLabels) != 1 || answers[0].SelectedLabels[0] != "Approve" {
			return deny
		}
	case "Bash", "PowerShell", "Monitor":
		command, _ := value["command"].(string)
		if strings.TrimSpace(command) == "" {
			return deny
		}
		rules := claudeSDKChatScopedRules(suggestions...)
		choice, err := nativeinput.ApproveCommandChoice(ctx, "Allow Claude to run this command?\n\n"+command, len(rules) > 0)
		if err != nil {
			return map[string]any{"behavior": "deny", "message": "Command not run: no answer. Ask again in chat when you are ready."}
		}
		switch choice {
		case nativeinput.ApprovalDeny:
			return map[string]any{"behavior": "deny", "message": "Command not run: you chose Deny."}
		case nativeinput.ApprovalAlways:
			// Claude's own rule, kept for this chat's Claude session only: never written to the Crew's folder.
			return map[string]any{"behavior": "allow", "updatedInput": value, "updatedPermissions": rules}
		}
	case "Edit", "MultiEdit", "Write", "NotebookEdit":
		// #3133: a file change Claude may not make on its own in this mode.
		rules := claudeSDKChatScopedRules(suggestions...)
		choice, err := nativeinput.ApproveCommandChoice(ctx, claudeFileChangeDescription(tool, value), len(rules) > 0)
		if err != nil {
			log.Printf("[NATIVE_APPROVAL] provider=claude-code kind=file_change tool=%s action=decline reason=not_answered", tool)
			return map[string]any{"behavior": "deny", "message": "File not changed: no answer. Ask again in chat when you are ready."}
		}
		log.Printf("[NATIVE_APPROVAL] provider=claude-code kind=file_change tool=%s action=%s", tool, map[nativeinput.ApprovalChoice]string{nativeinput.ApprovalDeny: "decline", nativeinput.ApprovalOnce: "accept", nativeinput.ApprovalAlways: "accept_chat"}[choice])
		switch choice {
		case nativeinput.ApprovalDeny:
			return map[string]any{"behavior": "deny", "message": "File not changed: you chose Deny."}
		case nativeinput.ApprovalAlways:
			return map[string]any{"behavior": "allow", "updatedInput": value, "updatedPermissions": rules}
		}
	default:
		return deny
	}
	return map[string]any{"behavior": "allow", "updatedInput": value}
}

func (p *claudeSDKProcess) interrupt(ctx context.Context) error {
	p.interrupted.Store(true)
	return p.control(ctx, "interrupt", "")
}

func (p *claudeSDKProcess) steer(ctx context.Context, message string) error {
	return p.control(ctx, "steer", message)
}

func (p *claudeSDKProcess) control(ctx context.Context, kind, message string) (controlErr error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	var request any = map[string]any{"type": kind, "text": message}
	closeImages := func() {}
	if kind == "steer" {
		var err error
		request, closeImages, err = claudeSDKParamsWithImages(ctx, p.session.owner, request.(map[string]any), nil)
		if err != nil {
			return err
		}
	}
	defer func() { finishClaudeImageDelivery(p.session.owner, request, controlErr, "accepted"); closeImages() }()
	// Preparing an image can outlast the native turn. Never write it into a
	// completed turn or treat that definite refusal as uncertain delivery.
	if kind == "steer" {
		select {
		case <-p.done:
			return fmt.Errorf("%w: Claude SDK turn ended during input preparation", llmtypes.ErrCodingAgentInputNotSent)
		default:
		}
	}
	id := randomHex(12)
	reply := make(chan error, 1)
	p.controlsMu.Lock()
	if p.controls == nil {
		p.controls = map[string]chan error{}
	}
	p.controls[id] = reply
	if kind == "steer" {
		if p.steers == nil {
			p.steers = map[string]string{}
		}
		p.steers[id] = message
	}
	p.controlsMu.Unlock()
	defer func() { p.controlsMu.Lock(); delete(p.controls, id); p.controlsMu.Unlock() }()
	switch payload := request.(type) {
	case map[string]any:
		payload["id"] = id
	case *claudeSDKImageParams:
		payload.base["id"] = id
	}
	if err := p.write(request); err != nil {
		return err
	}
	select {
	case err := <-reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return fmt.Errorf("%w: Claude SDK turn ended before native input/control acknowledgement", llmtypes.ErrCodingAgentTurnEndedBeforeInputAck)
	}
}
func (p *claudeSDKProcess) acknowledgeControl(id string, failed, notSent bool) {
	p.controlsMu.Lock()
	reply := p.controls[id]
	if text, ok := p.steers[id]; ok {
		delete(p.steers, id)
		if !failed && !notSent {
			p.answering = text // Claude took the steer; its next answer replies to it
		}
	}
	p.controlsMu.Unlock()
	if reply != nil {
		var err error
		if notSent {
			err = fmt.Errorf("%w: Claude SDK turn is no longer accepting input", llmtypes.ErrCodingAgentInputNotSent)
		} else if failed {
			err = fmt.Errorf("Claude SDK rejected control request")
		}
		select {
		case reply <- err:
		default:
		}
	}
}

// SendClaudeCodeSDKInput never falls back to a tmux session from another transport.
func SendClaudeCodeSDKInput(ctx context.Context, owner, message string) error {
	process, ok := claudeSDKProcesses.Get(strings.TrimSpace(owner))
	if !ok {
		return fmt.Errorf("%w: no active Claude SDK turn", llmtypes.ErrCodingAgentInputNotSent)
	}
	return process.steer(ctx, message)
}

// SDKRuntimeAvailable checks the installed component without starting an agent.
func SDKRuntimeAvailable() error { _, err := claudeSDKEntry(); return err }

// claudeSDKChatScopedRules turns Claude's suggested "always allow" updates for a request into ones that last
// for this chat's Claude session: a suggestion aimed at a settings file would write a lasting rule into the
// project or the account, which the person did not ask for.
func claudeSDKChatScopedRules(suggestions ...json.RawMessage) []map[string]any {
	if len(suggestions) == 0 || len(suggestions[0]) == 0 {
		return nil
	}
	var updates []map[string]any
	if json.Unmarshal(suggestions[0], &updates) != nil {
		return nil
	}
	out := make([]map[string]any, 0, len(updates))
	for _, update := range updates {
		if update["type"] != "addRules" || update["behavior"] != "allow" {
			continue
		}
		update["destination"] = "session"
		out = append(out, update)
	}
	return out
}

const claudeApprovalPreviewBytes = 4000

// claudeFileChangeDescription names the file and shows what changes, cut to a
// readable size.
func claudeFileChangeDescription(tool string, input map[string]any) string {
	path, _ := input["file_path"].(string)
	if path == "" {
		path, _ = input["notebook_path"].(string)
	}
	verb := "change"
	if tool == "Write" {
		verb = "write"
	}
	text := "Allow Claude to " + verb + " this file?\n\n" + path
	var preview strings.Builder
	addEdit := func(edit map[string]any) {
		oldText, _ := edit["old_string"].(string)
		newText, _ := edit["new_string"].(string)
		for _, line := range strings.Split(oldText, "\n") {
			preview.WriteString("- " + line + "\n")
		}
		for _, line := range strings.Split(newText, "\n") {
			preview.WriteString("+ " + line + "\n")
		}
	}
	switch tool {
	case "Edit":
		addEdit(input)
	case "MultiEdit":
		edits, _ := input["edits"].([]any)
		for _, e := range edits {
			if edit, ok := e.(map[string]any); ok {
				addEdit(edit)
			}
		}
	case "Write":
		content, _ := input["content"].(string)
		preview.WriteString(content)
	case "NotebookEdit":
		source, _ := input["new_source"].(string)
		preview.WriteString(source)
	}
	if p := preview.String(); p != "" {
		if len(p) > claudeApprovalPreviewBytes {
			p = p[:claudeApprovalPreviewBytes] + "\n..."
		}
		text += "\n\n" + p
	}
	return text
}

// claudeSDKElicitation answers an MCP server's form or link request (#3133).
func claudeSDKElicitation(ctx context.Context, input json.RawMessage) map[string]any {
	var req struct {
		ServerName      string          `json:"serverName"`
		Message         string          `json:"message"`
		Mode            string          `json:"mode"`
		URL             string          `json:"url"`
		RequestedSchema json.RawMessage `json:"requestedSchema"`
	}
	if json.Unmarshal(input, &req) != nil {
		return map[string]any{"action": "decline"}
	}
	var result nativeinput.FormResult
	if req.Mode == "url" {
		result = nativeinput.AskURL(ctx, "claude-code", req.ServerName, req.Message, req.URL)
	} else {
		result = nativeinput.AskForm(ctx, "claude-code", req.ServerName, req.Message, req.RequestedSchema)
	}
	out := map[string]any{"action": result.Action}
	if result.Action == "accept" && result.Content != nil {
		out["content"] = result.Content
	}
	return out
}
