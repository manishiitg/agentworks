package claudecode

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/interfaces"
	"github.com/manishiitg/multi-llm-provider-go/internal/clisandbox"
	"github.com/manishiitg/multi-llm-provider-go/internal/tmuxcontrol"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/fileowner"
)

// The AgentWorks mod (a Claude Code plugin with a hooks module) runs inside a
// chat's Claude Code terminal and reports turns, session switches and usage as
// one JSON file per event in the session's mod folder. Claude Code itself says
// when a turn ends and which session is current, so neither has to be read off
// the screen or guessed after /clear, /resume or /branch.
//
//go:embed all:agentworksmod
var claudeModFiles embed.FS

const claudeModDirEnv = "AGENTWORKS_MOD_DIR"

// claudeModMCPServersEnv names the chat's MCP servers, whose tools the mod
// waits for before it sends the first message.
const claudeModMCPServersEnv = "AGENTWORKS_MOD_MCP_SERVERS"

// claudeModInboxEnv is the folder of requests for the mod; claudeModSlashEnv the
// built-in slash commands it may run for them.
const (
	claudeModInboxEnv = "AGENTWORKS_MOD_INBOX"
	claudeModSlashEnv = "AGENTWORKS_MOD_SLASH_COMMANDS"
)

// claudeModSlashCommands is the built-in commands a request may run: the
// server's terminal allowlist (AGENTWORKS_TERMINAL_SLASH_COMMANDS, usage by
// default) and the session commands the mod follows. A built-in that changes
// the CLI's login or settings (/login, /config, /permissions, ...) is not one.
func claudeModSlashCommands() string {
	configured := strings.TrimSpace(os.Getenv("AGENTWORKS_TERMINAL_SLASH_COMMANDS"))
	if configured == "" || strings.EqualFold(configured, "allow") || strings.EqualFold(configured, "none") {
		configured = "usage"
	}
	return configured + ",clear,new,resume,branch,fork"
}

// claudeModBaseDir is where a launch keeps the mod and its events: inside a
// confined CLI's private home (the shared /tmp is outside its sandbox, and a
// slot account must be able to write there), else a private folder made for
// this launch, so no other local account can read answers or plant events.
func claudeModBaseDir(opts *llmtypes.CallOptions) (claudeModBase, error) {
	if home := llmtypes.SandboxHomeEnvironment(opts)["HOME"]; strings.TrimSpace(home) != "" {
		// Requests sit in a folder of their own, outside everything the sandbox
		// may write (the working folder can be the runtime that holds the home):
		// the agent can read them, never write one (it would act as the person).
		// The launcher gives a slot account read access to it.
		inbox, err := os.MkdirTemp("", "agentworks-mod-inbox-")
		if err != nil {
			return claudeModBase{}, err
		}
		// The mod's own code runs inside Claude Code, which holds the login: like
		// the inbox it sits outside everything the sandbox may write, written
		// fresh for this launch, so the agent can never change what runs next.
		plugin, err := os.MkdirTemp("", "agentworks-mod-plugin-")
		if err != nil {
			_ = os.RemoveAll(inbox)
			return claudeModBase{}, err
		}
		return claudeModBase{dir: filepath.Join(home, ".agentworks-mod"), inbox: inbox, plugin: plugin, shared: true}, nil
	}
	dir, err := os.MkdirTemp("", "agentworks-mod-")
	if err != nil {
		return claudeModBase{}, err
	}
	return claudeModBase{dir: dir, inbox: filepath.Join(dir, "inbox"), plugin: dir, private: true}, nil
}

// claudeModBase is a launch's mod folders: dir holds the events the mod writes,
// inbox the requests AgentWorks writes. shared: dir is inside a confined CLI's
// home (group-writable for a slot account) and the inbox is a folder of its own
// the CLI may only read. private: made for this launch alone (an unconfined
// CLI, which already has its person's rights).
type claudeModBase struct {
	dir     string
	inbox   string
	plugin  string
	shared  bool
	private bool
}

// claudeModBases remembers each tmux session's base folder, so readers that
// only know the session name find its events.
var claudeModBases sync.Map

var claudeModPluginMu sync.Mutex

// claudeModPluginDir writes the embedded mod under base, in a folder named
// after its content, so a new build never loads an older copy. It writes it
// every time: a copy already there is never trusted.
func claudeModPluginDir(base string) (string, error) {
	sum := sha256.New()
	_ = fs.WalkDir(claudeModFiles, "agentworksmod", func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			data, _ := claudeModFiles.ReadFile(path)
			sum.Write([]byte(path))
			sum.Write(data)
		}
		return nil
	})
	dir := filepath.Join(base, "plugin-"+hex.EncodeToString(sum.Sum(nil))[:12])
	claudeModPluginMu.Lock()
	defer claudeModPluginMu.Unlock()
	err := fs.WalkDir(claudeModFiles, "agentworksmod", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(dir, strings.TrimPrefix(path, "agentworksmod"))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := claudeModFiles.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	return dir, err
}

func claudeModBaseFor(sessionName string) (claudeModBase, bool) {
	value, ok := claudeModBases.Load(sessionName)
	if !ok {
		return claudeModBase{}, false
	}
	return value.(claudeModBase), true
}

// claudeModSessionDir is where one tmux session's mod writes its events;
// empty for a session this process did not launch with the mod.
func claudeModSessionDir(sessionName string) string {
	base, ok := claudeModBaseFor(sessionName)
	if !ok {
		return ""
	}
	return filepath.Join(base.dir, "session-"+sessionName)
}

// prepareClaudeModSessionDir creates the session's event folder. Group
// writable: a slot account runs the CLI in a folder shared with its group.
func prepareClaudeModSessionDir(sessionName string) (string, error) {
	dir := claudeModSessionDir(sessionName)
	if dir == "" {
		return "", fmt.Errorf("prepare Claude mod folder: %s was not launched with the mod", sessionName)
	}
	base, _ := claudeModBaseFor(sessionName)
	eventsMode, inboxMode := os.FileMode(0o700), os.FileMode(0o700)
	if base.shared {
		eventsMode = 0o770 // a slot account writes events; it reads requests through the launcher's grant
	}
	if err := os.MkdirAll(filepath.Join(dir, "events"), eventsMode); err != nil {
		return "", fmt.Errorf("prepare Claude mod folder: %w", err)
	}
	if err := os.MkdirAll(claudeModInboxDir(sessionName), inboxMode); err != nil {
		return "", fmt.Errorf("prepare Claude mod inbox: %w", err)
	}
	return dir, nil
}

// claudeModInboxDir is where AgentWorks puts one session's requests for its mod.
func claudeModInboxDir(sessionName string) string {
	base, ok := claudeModBaseFor(sessionName)
	if !ok {
		return ""
	}
	return base.inbox
}

func removeClaudeModSessionDir(sessionName string) {
	base, ok := claudeModBaseFor(sessionName)
	if !ok {
		return
	}
	if !base.private {
		_ = os.RemoveAll(base.inbox)
		if base.plugin != "" {
			_ = os.RemoveAll(base.plugin)
		}
	}
	if base.private {
		_ = os.RemoveAll(base.dir)
	} else {
		_ = os.RemoveAll(claudeModSessionDir(sessionName))
	}
	claudeModBases.Delete(sessionName)
}

// claudeModEvent is one event the mod wrote. Every event names the session
// that was current when it happened.
type claudeModEvent struct {
	Type          string  `json:"type"`
	At            int64   `json:"at"`
	Seq           int64   `json:"seq"`
	SessionID     string  `json:"session_id"`
	Reason        string  `json:"reason,omitempty"`
	Command       string  `json:"command,omitempty"`
	TurnID        string  `json:"turn_id,omitempty"`
	Aborted       bool    `json:"aborted,omitempty"`
	Answer        string  `json:"answer,omitempty"`
	DurationMS    int64   `json:"duration_ms,omitempty"`
	ContextTokens int64   `json:"context_tokens,omitempty"`
	ContextWindow int64   `json:"context_window,omitempty"`
	CostUSD       float64 `json:"cost_usd,omitempty"`
	InputID       string  `json:"input_id,omitempty"`
	Delivery      string  `json:"delivery,omitempty"`
	Error         string  `json:"error,omitempty"`
	Trigger       string  `json:"trigger,omitempty"`
	TokensBefore  int64   `json:"tokens_before,omitempty"`
	TokensAfter   int64   `json:"tokens_after,omitempty"`
	RateLimits    []struct {
		Kind        string  `json:"kind"`
		PercentUsed float64 `json:"percentUsed"`
		ResetsAt    string  `json:"resetsAt"`
	} `json:"rate_limits,omitempty"`
	Step    string                `json:"step,omitempty"`
	Items   []claudeModStreamItem `json:"items,omitempty"`
	Door    string                `json:"door,omitempty"`
	UUID    string                `json:"uuid,omitempty"`
	Message json.RawMessage       `json:"message,omitempty"`
	// name is the event's file name: unique, and its order is event order.
	name string
}

// readClaudeModEvents returns the session's events in the order they happened.
// File names start with the time and the mod's own counter, so name order is
// event order.
//
// Anything in the session can write the folder, the agent's own shell too, so
// events only time turns and name a session that is then checked; links and
// other non-regular files are skipped, and a private folder takes only files
// this process's account wrote.
func readClaudeModEvents(sessionName string) []claudeModEvent {
	sessionDir := claudeModSessionDir(sessionName)
	if sessionDir == "" {
		return nil
	}
	base, _ := claudeModBaseFor(sessionName)
	dir := filepath.Join(sessionDir, "events")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		if base.private && !claudeModOwnedByProcess(filepath.Join(dir, entry.Name())) {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	events := make([]claudeModEvent, 0, len(names))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			continue
		}
		var event claudeModEvent
		if json.Unmarshal(data, &event) == nil && event.Type != "" {
			event.name = name
			events = append(events, event)
		}
	}
	return events
}

// claudeModCurrentSessionID is the session the terminal is on now: the one
// its latest event names. Empty when the mod has reported nothing.
func claudeModCurrentSessionID(sessionName string) string {
	events := readClaudeModEvents(sessionName)
	for i := len(events) - 1; i >= 0; i-- {
		if id := strings.TrimSpace(events[i].SessionID); id != "" {
			return id
		}
	}
	return ""
}

// ClaudeModCurrentSessionID reports the native session a Claude terminal is
// on now, for a caller that follows a terminal between its own turns.
func ClaudeModCurrentSessionID(tmuxSessionName string) string {
	return claudeModCurrentSessionID(tmuxSessionName)
}

// waitForClaudeModTurnComplete waits until the turns started at or after since
// have ended, and returns the last one's end. A message queued while Claude
// answered starts another turn right after the first ends, so an end counts
// only once it is still the latest event one poll later.
func waitForClaudeModTurnComplete(ctx context.Context, sessionName string, since time.Time) (claudeModEvent, bool) {
	sinceMS := since.UnixMilli()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	var pending int64 = -1
	for polls := 0; ; polls++ {
		if polls > 0 && polls%10 == 0 && claudeModTerminalLost(ctx, sessionName) != nil {
			return claudeModEvent{Type: "terminal_lost"}, false
		}
		started := false
		var end claudeModEvent
		ended := false
		for _, event := range readClaudeModEvents(sessionName) {
			if event.At < sinceMS {
				continue
			}
			switch event.Type {
			case "turn_start":
				started, ended = true, false
			case "turn_complete":
				if started {
					end, ended = event, true
				}
			}
		}
		if ended {
			if pending == end.Seq {
				return end, true
			}
			pending = end.Seq
		} else {
			pending = -1
		}
		// An operator marked the visible terminal complete.
		if tmuxcontrol.ConsumeForceComplete(sessionName) {
			return claudeModEvent{Type: "force_complete"}, false
		}
		select {
		case <-ctx.Done():
			return claudeModEvent{}, false
		case <-ticker.C:
		}
	}
}

func claudeArgsHavePluginDir(args []string) bool {
	for _, arg := range args {
		if arg == "--plugin-dir" {
			return true
		}
	}
	return false
}

// claudeModSandboxPaths adds the mod to a confined launch: its code and its
// request inbox are read, only its event folder is written.
func claudeModSandboxPaths(args []string, sessionName string, read, write []string) ([]string, []string) {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--plugin-dir" {
			read = append(read, args[i+1], claudeModInboxDir(sessionName))
			write = append(write, filepath.Join(claudeModSessionDir(sessionName), "events"))
		}
	}
	return read, write
}

// adoptClaudeModSession moves a retained terminal to the session its mod says
// is current: /clear, /resume or /branch typed in the terminal changed it, and
// the turn, its transcript and the next resume belong to that session now.
func adoptClaudeModSession(session *claudeInteractivePersistentSession, logger interfaces.Logger) {
	if session == nil {
		return
	}
	current := claudeModCurrentSessionID(session.tmuxSessionName)
	if current == "" || current == session.nativeSessionID || !isUUIDLike(current) {
		return
	}
	// Only a session of this launch's own project folder: never one an event
	// names from elsewhere.
	if path, _ := resolveClaudeTranscriptPath(current, session.workingDir, false, session.accountHome); path == "" {
		return
	}
	if logger != nil {
		logger.Infof("[CLAUDE_MOD] session switch tmux=%s from=%s to=%s", session.tmuxSessionName, session.nativeSessionID, current)
	}
	session.nativeSessionID = current
}

// claudeModReadyTimeout bounds the wait for the mod's first event after the
// terminal starts; Claude Code loads plugins before its first prompt.
const claudeModReadyTimeout = 60 * time.Second

// waitForClaudeModReady waits for the mod's first event. A terminal without it
// cannot be driven: Claude Code older than mods, or a policy that turns mods off.
func waitForClaudeModReady(ctx context.Context, sessionName string) error {
	ctx, cancel := context.WithTimeout(ctx, claudeModReadyTimeout)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for claudeModCurrentSessionID(sessionName) == "" {
		select {
		case <-ctx.Done():
			return fmt.Errorf("Claude Code did not load the AgentWorks mod in %s: update Claude Code to 2.1.290 or later, and check that no managed setting turns mods off", sessionName)
		case <-ticker.C:
		}
	}
	return nil
}

// ClaudeModRunsIn reports whether a Claude terminal this process launched runs
// the AgentWorks mod. Its session switches are followed, so every slash
// command may be typed into it.
func ClaudeModRunsIn(tmuxSessionName string) bool {
	_, ok := claudeModBaseFor(tmuxSessionName)
	return ok
}

func claudeModOwnedByProcess(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return fileowner.OwnedByMe(path, info)
}

// claudeModInputTimeout bounds the wait for the mod to take a message. A new
// turn is confirmed as it starts, so a busy Claude can hold it a while.
const claudeModInputTimeout = 2 * time.Minute

var claudeModInputSeq atomic.Int64

// deliverClaudeModInput puts text in the terminal's mod inbox and waits until
// the mod has handed it to Claude: a new turn when Claude was idle, or a row
// the running turn reads at its next step. It returns how it was delivered.
func deliverClaudeModInput(ctx context.Context, sessionName, text string) (string, error) {
	event, err := claudeModRequest(ctx, sessionName, "input", text)
	return event.Delivery, err
}

// claudeModRequest asks the terminal's mod for one thing (input, abort,
// compact) and waits for its answer.
func claudeModRequest(ctx context.Context, sessionName, kind, text string) (claudeModEvent, error) {
	dir := claudeModSessionDir(sessionName)
	if dir == "" {
		return claudeModEvent{}, fmt.Errorf("Claude Code session %s does not run the AgentWorks mod", sessionName)
	}
	id := fmt.Sprintf("%019d-%06d-%s", time.Now().UnixNano(), claudeModInputSeq.Add(1), kind)
	data, _ := json.Marshal(map[string]string{"id": id, "kind": kind, "text": text})
	inbox := claudeModInboxDir(sessionName)
	// Written aside and renamed in, so the mod never reads half a request.
	tmp := filepath.Join(inbox, id+".tmp")
	// 0640: on a slot server the CLI reads requests through a group ACL, and
	// the ACL mask comes from the group bits.
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return claudeModEvent{}, fmt.Errorf("queue %s for Claude Code: %w", kind, err)
	}
	final := filepath.Join(inbox, id+".json")
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return claudeModEvent{}, fmt.Errorf("queue %s for Claude Code: %w", kind, err)
	}
	defer os.Remove(final)
	ctx, cancel := context.WithTimeout(ctx, claudeModInputTimeout)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for polls := 0; ; polls++ {
		// A terminal that is gone takes nothing; say so the way the stack recovers from.
		if polls > 0 && polls%10 == 0 {
			if lost := claudeModTerminalLost(ctx, sessionName); lost != nil {
				return claudeModEvent{}, lost
			}
		}
		for _, event := range readClaudeModEvents(sessionName) {
			if event.Type != "input" || event.InputID != id {
				continue
			}
			if event.Error != "" {
				return event, fmt.Errorf("Claude Code did not take the %s: %s", kind, event.Error)
			}
			return event, nil
		}
		select {
		case <-ctx.Done():
			return claudeModEvent{}, fmt.Errorf("Claude Code did not take the %s in %s: %w", kind, claudeModInputTimeout, ctx.Err())
		case <-ticker.C:
		}
	}
}

// claudeModTaken remembers each owner's messages the mod confirmed, for the
// durable receipt the server asks for after a send.
var (
	claudeModTakenMu sync.Mutex
	claudeModTaken   = map[string][]claudeModTakenInput{}
)

type claudeModTakenInput struct {
	message string
	latency time.Duration
	at      time.Time
}

func recordClaudeModInputTaken(ownerSessionID, message string, latency time.Duration) {
	claudeModTakenMu.Lock()
	defer claudeModTakenMu.Unlock()
	taken := append(claudeModTaken[ownerSessionID], claudeModTakenInput{message: message, latency: latency, at: time.Now()})
	if len(taken) > 64 {
		taken = taken[len(taken)-64:]
	}
	claudeModTaken[ownerSessionID] = taken
}

func takeClaudeModInputTaken(ownerSessionID, message string) (claudeModTakenInput, bool) {
	claudeModTakenMu.Lock()
	defer claudeModTakenMu.Unlock()
	for i, input := range claudeModTaken[ownerSessionID] {
		if input.message == message {
			claudeModTaken[ownerSessionID] = append(claudeModTaken[ownerSessionID][:i:i], claudeModTaken[ownerSessionID][i+1:]...)
			return input, true
		}
	}
	return claudeModTakenInput{}, false
}

// waitForClaudeModEvent waits for the first event of a type written at or
// after since.
func waitForClaudeModEvent(ctx context.Context, sessionName, eventType string, since time.Time) (claudeModEvent, error) {
	sinceMS := since.UnixMilli()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		for _, event := range readClaudeModEvents(sessionName) {
			if event.Type == eventType && event.At >= sinceMS {
				return event, nil
			}
		}
		select {
		case <-ctx.Done():
			return claudeModEvent{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

// claudeModTurnError turns an API error that ended a turn into the error the
// stack acts on: a usage limit suspends until its reset, low credit stops.
func claudeModTurnError(sessionName, text string) error {
	switch {
	case IsClaudeUsageLimitText(text):
		return NewClaudeUsageLimitErrorForSession("claudecode", "", sessionName, text, time.Now())
	case IsClaudeCreditLowText(text):
		log.Printf("[TURN] stalled cause=credit_low provider=claude-code tmux=%s", sessionName)
		return &ClaudeCreditLowError{}
	default:
		return fmt.Errorf("claude code turn failed: %s", strings.TrimSpace(text))
	}
}

// claudeMCPServerNames lists the servers in the launch's MCP config.
func claudeMCPServerNames(opts *llmtypes.CallOptions) []string {
	if opts == nil || opts.Metadata == nil || opts.Metadata.Custom == nil {
		return nil
	}
	raw, _ := opts.Metadata.Custom[MetadataKeyMCPConfig].(string)
	var config struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if json.Unmarshal([]byte(raw), &config) != nil {
		return nil
	}
	names := make([]string, 0, len(config.MCPServers))
	for name := range config.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ClaudeModIdle reports whether a Claude terminal running the mod is between
// turns: its session has started and every turn it started has ended. known
// is false for a terminal without the mod.
func ClaudeModIdle(tmuxSessionName string) (idle, known bool) {
	if !ClaudeModRunsIn(tmuxSessionName) {
		return false, false
	}
	started, running := false, false
	for _, event := range readClaudeModEvents(tmuxSessionName) {
		switch event.Type {
		case "session":
			started = true
		case "turn_start":
			running = true
		case "turn_complete":
			running = false
		}
	}
	return started && !running, true
}

// claudeModTerminalLost is the session-lost error when the terminal's tmux
// session no longer exists; nil while it does, or when tmux cannot say.
func claudeModTerminalLost(ctx context.Context, sessionName string) error {
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if exists, err := claudeTmuxSessionExists(checkCtx, sessionName); err == nil && !exists {
		return &llmtypes.CodingAgentTmuxSessionLostError{Provider: "claude-code", SessionName: sessionName, Reason: "the Claude Code terminal is gone"}
	}
	return nil
}

// claudeModRateLimitWindows is the plan usage the mod last reported (after a
// turn, or when a limit's use changed); false when it has reported none.
func claudeModRateLimitWindows(sessionName string) ([]llmtypes.RateLimitWindow, bool) {
	events := readClaudeModEvents(sessionName)
	for i := len(events) - 1; i >= 0; i-- {
		if len(events[i].RateLimits) == 0 {
			continue
		}
		windows := make([]llmtypes.RateLimitWindow, 0, len(events[i].RateLimits))
		for _, limit := range events[i].RateLimits {
			window := llmtypes.RateLimitWindow{Name: limit.Kind, UsedPercent: limit.PercentUsed}
			if at, err := time.Parse(time.RFC3339, limit.ResetsAt); err == nil {
				window.ResetsAt = at.UTC()
			}
			windows = append(windows, window)
		}
		return windows, true
	}
	return nil, false
}

// claudeModInboxOutsideWrites refuses a confined launch whose request inbox the
// CLI could write: the mod acts on each request as the person.
func claudeModInboxOutsideWrites(policy *llmtypes.CLISecurityPolicy, workingDir string, runtimeWrites []string, sessionName string) error {
	inbox := claudeModInboxDir(sessionName)
	if inbox == "" || policy == nil || !policy.LandlockEnforced() {
		return nil
	}
	writes := clisandbox.LandlockWritePaths(policy, workingDir, runtimeWrites)
	if clisandbox.WritableUnder(writes, inbox) {
		return fmt.Errorf("the AgentWorks mod inbox %s would be writable by the confined CLI; not starting it", inbox)
	}
	if base, ok := claudeModBaseFor(sessionName); ok && base.plugin != "" && clisandbox.WritableUnder(writes, base.plugin) {
		return fmt.Errorf("the AgentWorks mod %s would be writable by the confined CLI; not starting it", base.plugin)
	}
	return nil
}

// ClaudeModChangedFile is one file AgentWorks' snapshots saw change in a chat: an absolute path and a status
// (added, modified, deleted, renamed).
type ClaudeModChangedFile struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// SetClaudeModChanges gives a Claude terminal's mod the files this chat changed, as AgentWorks' file
// snapshots see them (every file, git or not), so its band and Changes pane match the app's Changes. The
// file is not a request (not .json), so the mod's inbox delivery never takes it.
func SetClaudeModChanges(tmuxSessionName string, files []ClaudeModChangedFile) error {
	inbox := claudeModInboxDir(tmuxSessionName)
	if inbox == "" {
		return fmt.Errorf("Claude Code session %s does not run the AgentWorks mod", tmuxSessionName)
	}
	data, err := json.Marshal(map[string]any{"files": files, "at": time.Now().UTC().Format(time.RFC3339Nano)})
	if err != nil {
		return err
	}
	tmp := filepath.Join(inbox, "changes.state.tmp")
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(inbox, "changes.state"))
}
