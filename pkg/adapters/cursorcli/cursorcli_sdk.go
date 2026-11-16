package cursorcli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/multi-llm-provider-go/internal/clisandbox"
	"github.com/manishiitg/multi-llm-provider-go/llmerrors"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
	"github.com/manishiitg/multi-llm-provider-go/pkg/adapters/internal/procshutdown"
	"github.com/manishiitg/multi-llm-provider-go/pkg/sessiontransfer"
)

// The Cursor SDK transport (CURSOR_CLI_TRANSPORT=sdk) runs the official @cursor/sdk local agent in a
// node bridge, one per chat, kept between turns. It steers a running turn: run.steer adds
// the message at the next safe step (owner decision 2026-10-27, PLAT-872).

//go:embed cursor_sdk_bridge.cjs
var cursorSDKBridge []byte

const cursorSDKMarker = "\x01aw "

type cursorSDKEvent struct {
	DurationMS  int64           `json:"duration_ms"`
	Type        string          `json:"type"`
	InputID     string          `json:"input_id"`
	RunID       string          `json:"run_id"`
	ParentRunID string          `json:"parent_run_id"`
	EventType   string          `json:"event_type"`
	Phase       string          `json:"phase"`
	AgentID     string          `json:"agent_id"`
	Resumed     bool            `json:"resumed"`
	Kind        string          `json:"kind"`
	Text        string          `json:"text"`
	CallID      string          `json:"call_id"`
	Name        string          `json:"name"`
	Status      string          `json:"status"`
	Args        json.RawMessage `json:"args"`
	Result      json.RawMessage `json:"result"`
	Usage       *cursorSDKUsage `json:"usage"`
	ID          string          `json:"id"`
	Accepted    bool            `json:"accepted"`
	Error       string          `json:"error"`
	// Stale is how the bridge cleared a run an earlier bridge left active: cancelled or new_agent.
	Stale      string `json:"stale"`
	StaleAgent string `json:"stale_agent"`
	Action     string `json:"action"`
	// ModelCatalog is how the bridge found the account's default model settings: ok, not_needed, timeout, empty or
	// error; ModelParams the settings it sent.
	ModelCatalog  string `json:"model_catalog"`
	ModelLookupMS *int   `json:"model_lookup_ms"`
	ModelParams   string `json:"model_params"`
}

// cursorRememberedParams keeps, per Cursor account and model, the default settings a start found in the account's
// catalog (PLAT-872-93). A later chat sends them itself, so it starts without waiting for the lookup; after a day
// the next start asks Cursor again.
var cursorRememberedParams = struct {
	sync.Mutex
	byKey map[string]cursorParamsEntry
}{byKey: map[string]cursorParamsEntry{}}

type cursorParamsEntry struct {
	params string
	at     time.Time
}

const cursorRememberedParamsTTL = 24 * time.Hour

// cursorAccountKey names the Cursor account a launch uses, from the environment the bridge really gets (the selected
// account's key or login paths replace the adapter's own) plus the login home a confined launch reads.
func (c *CursorCLIAdapter) cursorAccountKey(opts *llmtypes.CallOptions) string {
	sum := sha256.New()
	for _, entry := range llmtypes.MergeCodingAgentSecretEnvironment(buildCursorStructuredEnv(c.apiKey), opts) {
		switch key, _, _ := strings.Cut(entry, "="); key {
		case "CURSOR_API_KEY", "CURSOR_AUTH_TOKEN", "HOME", "XDG_CONFIG_HOME":
			sum.Write([]byte(entry + "\n"))
		}
	}
	if opts != nil && opts.CLISecurity != nil {
		sum.Write([]byte("login=" + strings.TrimSpace(opts.CLISecurity.CredentialHome)))
	}
	return hex.EncodeToString(sum.Sum(nil)[:8])
}

// withRememberedParams adds the remembered settings to a bare model; explicit settings are kept.
func withRememberedParams(model map[string]any, account string, now time.Time) (map[string]any, bool) {
	if _, explicit := model["params"]; explicit {
		return model, false
	}
	id, _ := model["id"].(string)
	cursorRememberedParams.Lock()
	entry, ok := cursorRememberedParams.byKey[account+"\x00"+id]
	cursorRememberedParams.Unlock()
	if !ok || entry.params == "" || now.Sub(entry.at) > cursorRememberedParamsTTL {
		return model, false
	}
	return cursorSDKModel(id + "[" + entry.params + "]"), true
}

func rememberParams(account, id, params string, now time.Time) {
	if params == "" {
		return
	}
	cursorRememberedParams.Lock()
	cursorRememberedParams.byKey[account+"\x00"+id] = cursorParamsEntry{params: params, at: now}
	cursorRememberedParams.Unlock()
}

type cursorSDKUsage struct {
	InputTokens     int `json:"inputTokens"`
	OutputTokens    int `json:"outputTokens"`
	CacheReadTokens int `json:"cacheReadTokens"`
	TotalTokens     int `json:"totalTokens"`
}

type cursorSDKProcess struct {
	images      map[string]*cursorImageSource
	imageActive bool
	observe     func(cursorSDKEvent)
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	writeMu     sync.Mutex
	events      chan cursorSDKEvent
	done        chan struct{}
	exited      chan struct{}
	process     *llmtypes.AgentProcess
	once        sync.Once
	mu          sync.Mutex
	err         error
	acks        map[string]chan bool
	stop        context.CancelFunc
}

type cursorSDKSession struct {
	turnMu      sync.Mutex
	mu          sync.Mutex
	proc        *cursorSDKProcess
	agentID     string
	cwd, model  string
	fingerprint [32]byte
	// parts names which launch setting changed when the process is replaced (#2886).
	parts      map[string][32]byte
	idle       *time.Timer
	hasHistory bool
	running    bool
	prompted   bool // the bridge has this turn's prompt: a steer handed over now cannot be lost to a failed launch
}

var cursorSDKSharedStoreStart sync.Mutex

var cursorSDKSessions = struct {
	sync.Mutex
	sessions map[string]*cursorSDKSession
}{sessions: map[string]*cursorSDKSession{}}

func (p *cursorSDKProcess) send(v any) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	if stream, ok := v.(cursorSDKStreamingParams); ok {
		err := stream.writeJSON(p.stdin)
		if err == nil {
			_, err = io.WriteString(p.stdin, "\n")
		}
		if err != nil {
			p.finish(fmt.Errorf("Cursor image frame write failed"))
		}
		return err
	}
	return json.NewEncoder(p.stdin).Encode(v)
}

func (p *cursorSDKProcess) finish(err error) {
	p.once.Do(func() {
		p.clearImages()
		p.mu.Lock()
		p.err = err
		p.mu.Unlock()
		close(p.done)
		p.stop()
		if p.cmd.Process != nil {
			_ = procshutdown.KillGroup(p.cmd.Process.Pid)
		}
		_ = p.stdin.Close()
	})
}

func (p *cursorSDKProcess) failure() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return p.err
	}
	return fmt.Errorf("Cursor SDK bridge closed")
}

// close lets the bridge cancel its in-flight run before it is killed: a SIGKILL first left the run
// active in Cursor's agent store, and every later turn of that chat failed "already has active run".
func (p *cursorSDKProcess) close() {
	_ = p.stdin.Close()
	select {
	case <-p.exited:
	case <-time.After(5 * time.Second):
	}
	p.finish(fmt.Errorf("Cursor SDK bridge closed"))
	<-p.exited
}

// read keeps only marked protocol lines: the SDK logs to the same stdout, and that text is never kept.
func (p *cursorSDKProcess) read(stdout io.Reader) {
	defer func() {
		if p.observe != nil {
			p.observe(cursorSDKEvent{Type: "helper_runtime_closed"})
		}
	}()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line, ok := bytes.CutPrefix(scanner.Bytes(), []byte(cursorSDKMarker))
		if !ok {
			continue
		}
		var event cursorSDKEvent
		if err := json.Unmarshal(line, &event); err != nil {
			p.finish(fmt.Errorf("invalid Cursor SDK bridge event: %w", err))
			return
		}
		if event.Type == "image_notice" {
			p.imageNotice(event.InputID)
			continue
		}
		if event.Type == "image_fetch" {
			p.fetchImages(event)
			continue
		}
		if event.Type == "background_meta" || event.Type == "helper" {
			// Metadata never enters the foreground event queue: the bridge
			// can report a late signal when no prompt is consuming it.
			if p.observe != nil {
				p.observe(event)
			}
			continue
		}
		if event.Type == "steer_accepted" {
			p.mu.Lock()
			ack := p.acks[event.ID]
			delete(p.acks, event.ID)
			p.mu.Unlock()
			if ack != nil {
				ack <- event.Accepted
			}
			continue
		}
		select {
		case p.events <- event:
		case <-p.done:
			return
		}
	}
	if err := scanner.Err(); err != nil {
		p.finish(fmt.Errorf("read Cursor SDK bridge: %w", err))
	}
}

// cursorSDKEntry checks the explicit path, managed CLI prefix, then global npm root.
func cursorSDKEntry() (string, error) {
	if path := strings.TrimSpace(os.Getenv("CURSOR_SDK_PATH")); path != "" {
		if _, err := os.Stat(path); err != nil {
			return "", errors.New("Cursor needs a component that is unavailable. Update the app and try again")
		}
		return filepath.Abs(path)
	}
	if binary, err := exec.LookPath("cursor-agent"); err == nil {
		path := filepath.Join(filepath.Dir(binary), "..", "lib", "node_modules", "@cursor", "sdk", "dist", "cjs", "index.js")
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "npm", "root", "-g").Output(); err == nil {
		path := filepath.Join(strings.TrimSpace(string(out)), "@cursor", "sdk", "dist", "cjs", "index.js")
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
	}
	return "", errors.New("Cursor needs a component that isn't installed. Update the app and try again")
}

// cursorSDKModel turns a Cursor selector ("claude-opus-4-8[effort=high,fast=false]") into the SDK's
// {id, params} form.
func cursorSDKModel(selector string) map[string]any {
	base, parameters, ok := strings.Cut(strings.TrimSpace(selector), "[")
	model := map[string]any{"id": base}
	if !ok {
		return model
	}
	params := []map[string]string{}
	for _, setting := range strings.Split(strings.TrimSuffix(parameters, "]"), ",") {
		if key, value, found := strings.Cut(strings.TrimSpace(setting), "="); found && key != "" {
			params = append(params, map[string]string{"id": key, "value": value})
		}
	}
	if len(params) > 0 {
		model["params"] = params
	}
	return model
}

func (c *CursorCLIAdapter) launchSDK(ctx context.Context, opts *llmtypes.CallOptions, cwd, owner, system, model, resume string) (*cursorSDKProcess, string, bool, error) {
	if err := llmtypes.PrepareCodingSDK(ctx, "cursor-cli", opts.StreamChan); err != nil {
		return nil, "", false, err
	}
	// Rules, MCP and skills go to the private runtime for a Crew bound to a person's own
	// folder (PLAT-856-145), never .cursor/ inside that folder.
	artifacts := llmtypes.CLIArtifactDirectory(opts, cwd)
	privateArtifacts := ""
	// A server chat's working folder is already its own runtime folder (…/cli-runtimes/v1/<id>), private to that
	// chat: its .cursor files stay there, where the SDK reads the project's MCP servers and tool permissions.
	// Moving them out (a temporary folder passed as local.dirs) left the SDK without api-bridge on Linux servers:
	// "MCP server does not exist: api-bridge" on every tool call (PLAT-872-97).
	if artifacts == cwd && !cursorChatRuntimeFolder(cwd) {
		// Never write this chat's .cursor files into the working folder either: the SDK merges the MCP
		// servers of every project folder it loads, so a folder two chats share gave each chat the other's
		// servers. This chat's own folder is passed as an extra root (local.dirs), as with a private runtime.
		dir, err := os.MkdirTemp("", "agentworks-cursor-chat-*")
		if err != nil {
			return nil, "", false, err
		}
		privateArtifacts, artifacts = dir, dir
	}
	ensureMetadata(opts)
	opts.Metadata.Custom[metadataKeySDKSubagents] = true
	cleanup, err := prepareCursorProjectFiles(artifacts, system, opts, owner)
	if err != nil {
		if privateArtifacts != "" {
			_ = os.RemoveAll(privateArtifacts)
		}
		return nil, "", false, err
	}
	if privateArtifacts != "" {
		cleanup = func(inner func()) func() { return func() { inner(); _ = os.RemoveAll(privateArtifacts) } }(cleanup)
	}
	storeRoot := ""
	if llmtypes.CLIHasPrivateRuntime(opts) {
		storeRoot, err = sessiontransfer.PathDirectory(opts.CLIRuntimeDir, ".sandbox-cache/cursor-sdk-state", true)
		if err != nil {
			cleanup()
			return nil, "", false, err
		}
	}
	if skills := llmtypes.AttachedSkillsFromOptions(opts); len(skills) > 0 {
		if err = c.ProjectSkills(artifacts, skills); err != nil {
			cleanup()
			return nil, "", false, err
		}
	}
	entry, err := cursorSDKEntry()
	if err != nil {
		cleanup()
		return nil, "", false, err
	}
	node, err := exec.LookPath("node")
	if err != nil {
		cleanup()
		return nil, "", false, fmt.Errorf("Cursor SDK requires Node.js: %w", err)
	}
	dir, err := os.MkdirTemp("", "cursor-sdk-*")
	if err != nil {
		cleanup()
		return nil, "", false, err
	}
	cleanup = func(inner func()) func() { return func() { inner(); _ = os.RemoveAll(dir) } }(cleanup)
	bridge := filepath.Join(dir, "bridge.cjs")
	if err = os.WriteFile(bridge, cursorSDKBridge, 0o600); err != nil {
		cleanup()
		return nil, "", false, err
	}
	processCtx, stop := context.WithCancel(context.Background())
	cmd := exec.CommandContext(processCtx, node, bridge, entry)
	cmd.Dir = cwd
	procshutdown.SetGroup(cmd)
	procshutdown.KillGroupOnCancel(cmd)
	cmd.Env = append(llmtypes.MergeCodingAgentSecretEnvironment(buildCursorStructuredEnv(c.apiKey), opts), "NO_OPEN_BROWSER=1")
	if llmtypes.CLIHasPrivateRuntime(opts) {
		configDir, configErr := prepareCursorPrivateRuntime(opts, cmd.Env)
		if configErr != nil {
			stop()
			cleanup()
			return nil, "", false, configErr
		}
		cmd.Env = append(cmd.Env, "CURSOR_CONFIG_DIR="+configDir)
	}
	// The agent loop runs in node itself, so node is the confined process (no spawned CLI child).
	sdkPackages := cursorSDKReadRoot(entry)
	reads := append(cursorLandlockReads(opts, nil, cwd), bridge, sdkPackages, artifacts)
	sandboxCleanup, err := clisandbox.LandlockCmd(opts.CLISecurity, cmd, cwd, reads, nil)
	if err != nil {
		stop()
		cleanup()
		return nil, "", false, err
	}
	allCleanup := func() { sandboxCleanup(); cleanup() }
	stderr := &clisandbox.StderrTail{}
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		stop()
		allCleanup()
		return nil, "", false, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stop()
		stdin.Close()
		allCleanup()
		return nil, "", false, err
	}
	// The SDK reports MCP server start failures only on stderr. Keep its MCP and error lines (bounded), or a
	// missing bridge shows up only as "MCP server does not exist" with no reason.
	if stderr, stderrErr := cmd.StderrPipe(); stderrErr == nil && c.logger != nil {
		go func() {
			scanner := bufio.NewScanner(stderr)
			scanner.Buffer(make([]byte, 64*1024), 1024*1024)
			for kept := 0; scanner.Scan(); {
				line := strings.TrimSpace(scanner.Text())
				lower := strings.ToLower(line)
				if kept >= 200 || !(strings.Contains(lower, "mcp") || strings.Contains(lower, "error") || strings.Contains(lower, "warn")) {
					continue
				}
				kept++
				if len(line) > 400 {
					line = line[:400]
				}
				c.logger.Infof("[CURSOR_SDK] stderr owner=%s %s", owner, line)
			}
		}()
	}
	p := &cursorSDKProcess{cmd: cmd, stdin: stdin, events: make(chan cursorSDKEvent, 4096), done: make(chan struct{}), exited: make(chan struct{}), acks: map[string]chan bool{}, stop: stop, process: llmtypes.NewAgentProcess("cursor-cli", owner)}
	observeHelpers := c.sdkHelperObserver(owner)
	p.observe = func(event cursorSDKEvent) {
		if event.Type == "helper" || event.Type == "helper_runtime_closed" {
			observeHelpers(event)
			return
		}
		if c.logger != nil {
			c.logger.Infof("[CURSOR_BACKGROUND] owner=%s agent=%s run=%s parent_run=%s call=%s action=observed kind=%s event=%s status=%s phase=%s", owner, event.AgentID, event.RunID, event.ParentRunID, event.CallID, event.Kind, event.EventType, event.Status, event.Phase)
		}
	}

	if err = cmd.Start(); err != nil {
		stop()
		stdin.Close()
		stdout.Close()
		allCleanup()
		return nil, "", false, err
	}
	go p.read(stdout)
	go func() {
		err := cmd.Wait()
		if err == nil {
			err = fmt.Errorf("Cursor SDK bridge exited")
		}
		// The launcher's own refusal (a slot launch, PLAT-872-86) is only on stderr; without it an exit read as a bare 125.
		if tail := strings.TrimSpace(stderr.String()); tail != "" {
			err = fmt.Errorf("%w: %s", err, tail)
		}
		exitReason := "process_exited"
		if processCtx.Err() != nil {
			exitReason = "stopped"
		}
		p.process.Exited(c.logger, exitReason)
		p.finish(err)
		allCleanup()
		close(p.exited)
	}()
	account := c.cursorAccountKey(opts)
	startModel, remembered := withRememberedParams(cursorSDKModel(model), account, time.Now())
	start := map[string]any{"type": "start", "cwd": cwd, "model": startModel}
	if storeRoot != "" {
		start["store_root"] = storeRoot
		if resume != "" && len(opts.CodingProviderHistory) == 0 {
			start["require_native_resume"] = true
		}
		if _, err := os.Stat(filepath.Join(storeRoot, ".agentworks-native-handoff.json")); err == nil {
			start["require_native_resume"] = resume != ""
		}
	}
	if artifacts != cwd {
		start["dirs"] = []string{artifacts}
	}
	// Pass this chat's own MCP servers (with the AgentWorks bridge) explicitly, from the files prepared for
	// it above. Those files are always in this chat's own folder (never a shared working folder): the SDK
	// merges the MCP servers of every project folder it loads.
	if raw, readErr := os.ReadFile(filepath.Join(artifacts, ".cursor", "mcp.json")); readErr == nil {
		var mcp struct {
			Servers map[string]any `json:"mcpServers"`
		}
		if json.Unmarshal(raw, &mcp) == nil && len(mcp.Servers) > 0 {
			start["mcp_servers"] = mcp.Servers
		}
	}
	// Only an SDK agent handle can be resumed; any other ID starts a new agent with saved history.
	if strings.HasPrefix(resume, "agent-") {
		start["agent_id"] = resume
	}
	if mode := cursorMetaString(opts, MetadataKeyMode); mode == "plan" {
		start["mode"] = mode
	}
	if cursorMetaBool(opts, MetadataKeyDenyBuiltinTools) && !cursorFullNativeFromOptions(opts) {
		start["tools"] = []string{"mcp"}
	}
	if storeRoot == "" {
		// Without a private runtime every bridge opens the SDK's one shared store; two opening it at the same
		// moment fail with "database is locked". Start them one at a time; runs still go in parallel.
		cursorSDKSharedStoreStart.Lock()
		defer cursorSDKSharedStoreStart.Unlock()
	}
	if err = p.send(start); err != nil {
		p.close()
		return nil, "", false, err
	}
	readyCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	select {
	case event := <-p.events:
		if event.Type != "ready" || event.AgentID == "" {
			p.close()
			if m := cursorModelRefusal.FindStringSubmatch(event.Error); m != nil {
				// The account's model list (on a company account, the models its administrator allows) has no such
				// model: say so plainly, with what is allowed.
				return nil, "", false, &llmerrors.Error{Kind: llmerrors.KindModelNotFound, Provider: "cursor-cli", Model: m[1],
					Err: fmt.Errorf("%s is not allowed on this account; allowed: %s. Pick another model.", m[1], strings.TrimSpace(m[2]))}
			}
			return nil, "", false, fmt.Errorf("Cursor SDK did not start: %s", event.Error)
		}
		if event.Stale != "" && c.logger != nil {
			c.logger.Infof("[CURSOR_SDK] stale_active_run agent=%s action=%s new=%s", event.StaleAgent, event.Stale, event.AgentID)
		}
		catalog := event.ModelCatalog
		if remembered {
			catalog = "remembered"
		} else if catalog == "ok" {
			rememberParams(account, startModel["id"].(string), event.ModelParams, time.Now())
		}
		if c.logger != nil {
			servers, _ := start["mcp_servers"].(map[string]any)
			names := make([]string, 0, len(servers))
			for name := range servers {
				names = append(names, name)
			}
			sort.Strings(names)
			// Names only: which MCP servers this agent was given ("MCP server does not exist" shows here).
			c.logger.Infof("[CURSOR_SDK] mcp agent=%s servers=%v artifacts_private=%t", event.AgentID, names, artifacts != cwd)
			lookup := "-"
			if event.ModelLookupMS != nil {
				lookup = fmt.Sprintf("%dms", *event.ModelLookupMS)
			}
			c.logger.Infof("[CURSOR_SDK] start agent=%s catalog=%s lookup=%s params=%q", event.AgentID, catalog, lookup, event.ModelParams)
			c.logger.Infof("[CURSOR_BACKGROUND] owner=%s agent=%s action=skipped reason=detached_shell_wake_unavailable policy=foreground_wait", owner, event.AgentID)
		}
		return p, event.AgentID, event.Resumed, nil
	case <-p.done:
		return nil, "", false, p.failure()
	case <-readyCtx.Done():
		p.close()
		return nil, "", false, readyCtx.Err()
	}
}

func closeCursorSDKInstance(owner string, expected *cursorSDKSession) {
	cursorSDKSessions.Lock()
	s := cursorSDKSessions.sessions[owner]
	if expected != nil && s != expected {
		cursorSDKSessions.Unlock()
		return
	}
	delete(cursorSDKSessions.sessions, owner)
	cursorSDKSessions.Unlock()
	if s != nil {
		s.mu.Lock()
		p := s.proc
		if s.idle != nil {
			s.idle.Stop()
		}
		s.mu.Unlock()
		if p != nil {
			p.close()
		}
	}
}

func (c *CursorCLIAdapter) generateContentSDK(ctx context.Context, messages []llmtypes.MessageContent, opts *llmtypes.CallOptions) (response *llmtypes.ContentResponse, err error) {
	if opts.StreamChan != nil {
		defer close(opts.StreamChan)
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Minute)
	defer cancel()
	emit := func(chunk llmtypes.StreamChunk) {
		if opts.StreamChan != nil {
			select {
			case opts.StreamChan <- chunk:
			case <-ctx.Done():
			}
		}
	}
	cwd := cursorWorkingDirFromOptions(opts)
	if cwd == "" {
		if cwd, err = os.Getwd(); err != nil {
			return nil, err
		}
	}
	if cwd, err = filepath.Abs(cwd); err != nil {
		return nil, err
	}
	owner := cursorInteractiveSessionIDFromOptions(opts)
	if owner == "" {
		owner = fmt.Sprintf("cursor-sdk-%d", time.Now().UnixNano())
	}
	ctx = llmtypes.ScopeCodingAgentQuestions(ctx, "cursor-cli", owner)
	system, conversation := splitCursorSystemPrompt(messages)
	if system == "" {
		system = llmtypes.CodingProviderLaunchSystemPromptFromOptions(opts)
	}
	// PLAT-746-999: the SDK finishes the run without notifying us when a detached shell exits.
	if system != "" {
		system += "\n\n"
	}
	system += "For shell commands expected to finish within about 10 minutes, wait in the foreground until they finish; detached shell completion does not wake this chat."
	model := cursorModelWithReasoning(cursorCLIModelForLaunch(c.modelID), opts)
	if override := cursorMetaString(opts, MetadataKeyCursorModel); override != "" {
		model = cursorModelWithReasoning(override, opts)
	}
	config := map[string]any{"cwd": cwd, "runtime": opts.CLIRuntimeDir, "system": system, "model": model, "env": llmtypes.MergeCodingAgentSecretEnvironment(buildCursorStructuredEnv(c.apiKey), opts), "security": opts.CLISecurity, "skills": llmtypes.AttachedSkillsFromOptions(opts)}
	if opts.Metadata != nil {
		for k, v := range opts.Metadata.Custom {
			if k != MetadataKeyResumeSessionID && k != llmtypes.CodingProviderLaunchOnlyMetadataKey {
				config[k] = v
			}
		}
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	fingerprint := sha256.Sum256(raw)
	parts := llmtypes.AgentProcessDigests(config)
	cursorSDKSessions.Lock()
	s := cursorSDKSessions.sessions[owner]
	if s == nil {
		s = &cursorSDKSession{}
		cursorSDKSessions.sessions[owner] = s
	}
	cursorSDKSessions.Unlock()
	if !s.turnMu.TryLock() {
		return nil, fmt.Errorf("Cursor is already working; queue this message")
	}
	defer s.turnMu.Unlock()
	// The turn takes steering from its first moment, including while the bridge starts and before the
	// prompt is sent: a message 2s in otherwise fell back to an after-turn injection (RTS 2026-10-27).
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.running, s.prompted = false, false; s.mu.Unlock() }()
	s.mu.Lock()
	if s.idle != nil {
		s.idle.Stop()
	}
	p, agentID, old, hasHistory, oldParts := s.proc, s.agentID, s.fingerprint, s.hasHistory, s.parts
	s.mu.Unlock()
	defer func() {
		if !cursorPersistentInteractiveFromOptions(opts) || err != nil {
			closeCursorSDKInstance(owner, s)
			return
		}
		s.mu.Lock()
		s.idle = time.AfterFunc(cursorInteractiveIdleTimeout(), func() {
			if s.turnMu.TryLock() {
				defer s.turnMu.Unlock()
				closeCursorSDKInstance(owner, s)
			}
		})
		s.mu.Unlock()
	}()
	resume := cursorResumeSessionIDFromOptions(opts)
	launchReason := "first"
	if p != nil {
		select {
		case <-p.done:
			p.close()
			p = nil
			launchReason = "process_exited"
		default:
		}
	}
	if p != nil && (old != fingerprint || (resume != "" && resume != agentID)) {
		launchReason = "other_conversation"
		if old != fingerprint {
			launchReason = "launch_changed:" + llmtypes.AgentProcessChanged(oldParts, parts)
		}
		p.close()
		p = nil
	}
	if p == nil {
		if old != ([32]byte{}) && old != fingerprint {
			agentID = ""
		}
		if resume != "" {
			agentID = resume
		}
		requested := agentID
		var resumed bool
		if p, agentID, resumed, err = c.launchSDK(ctx, opts, cwd, owner, system, model, agentID); err != nil {
			return nil, err
		}
		hasHistory = resumed
		s.mu.Lock()
		s.proc, s.agentID, s.cwd, s.model, s.fingerprint, s.hasHistory, s.parts = p, agentID, cwd, model, fingerprint, hasHistory, parts
		s.mu.Unlock()
		p.process.Launched(c.logger, launchReason)
		if requested != "" && !resumed && c.logger != nil {
			c.logger.Infof("[CURSOR_SDK] resume_missing fallback=new chat=%q requested=%q new=%q", owner, requested, agentID)
		}
		if opts.OnCodingProviderSession != nil {
			opts.OnCodingProviderSession(cursorSDKSessionHandle(s))
		}
	} else {
		p.process.Reused(c.logger, "launch_unchanged")
	}
	if llmtypes.CodingProviderLaunchOnlyFromOptions(opts) {
		return cursorSDKResponse(s, "", nil), nil
	}
	resumePrompt := ""
	if hasHistory {
		resumePrompt = agentID
	} else if len(opts.CodingProviderHistory) > 0 {
		_, conversation = splitCursorSystemPrompt(opts.CodingProviderHistory)
	}
	prompt := buildCursorStructuredPrompt("", conversation, resumePrompt)
	ctx = cursorSDKImageContext(ctx, owner, conversation)
	if strings.TrimSpace(prompt) == "" && len(llmtypes.NativeImageInputsForSession(ctx, owner)) == 0 {
		return nil, fmt.Errorf("Cursor prompt is empty")
	}
	response, err = c.consumeSDKTurn(ctx, s, p, prompt, emit, owner)
	if err == nil {
		s.mu.Lock()
		s.hasHistory = true
		s.mu.Unlock()
	}
	return response, err
}

func (c *CursorCLIAdapter) consumeSDKTurn(ctx context.Context, s *cursorSDKSession, p *cursorSDKProcess, prompt string, emit func(llmtypes.StreamChunk), owners ...string) (*llmtypes.ContentResponse, error) {
	owner := ""
	if len(owners) > 0 {
		owner = owners[0]
	}
	meta := map[string]any{"cursor_session_id": s.agentID, "cursor_stream_source": "sdk"}
	p.mu.Lock()
	p.imageActive = true
	p.mu.Unlock()
	defer p.clearImages()
	imageID, imageErr := p.registerImages(ctx, owner)
	if imageErr != nil {
		return nil, imageErr
	}
	if err := p.send(map[string]any{"type": "prompt", "text": prompt, "image_id": imageID}); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.prompted = true
	s.mu.Unlock()
	emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypePromptAccepted, Metadata: meta})
	var text strings.Builder
	afterTool := false
	type tool struct {
		name, args string
		started    time.Time
		ended      bool
	}
	tools := map[string]*tool{}
	var subagents []llmtypes.NativeSubagentTranscript
	for {
		select {
		case event := <-p.events:
			switch event.Type {
			case "delta":
				kind := llmtypes.StreamChunkTypeContent
				if event.Kind == "thinking" {
					kind = llmtypes.StreamChunkTypeReasoning
				} else {
					// Text written after a tool step is a new paragraph, not glued to the last sentence.
					if afterTool && text.Len() > 0 && !strings.HasSuffix(text.String(), "\n") {
						event.Text = "\n\n" + event.Text
					}
					afterTool = false
					text.WriteString(event.Text)
				}
				emit(llmtypes.StreamChunk{Type: kind, Content: event.Text, Metadata: map[string]any{"cursor_session_id": s.agentID, "cursor_stream_source": "sdk", llmtypes.ContentDeltaMetadataKey: true}})
			case "plan":
				if plan, ok := llmtypes.ParseCodingAgentPlan(event.Args, "todos"); ok {
					plan.Provider, plan.PlanID = "cursor-cli", s.agentID
					emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeCodingAgentPlan, CodingAgentPlan: plan, Metadata: meta})
				}
			case "tool":
				afterTool = true
				t := tools[event.CallID]
				if t == nil {
					args := string(event.Args)
					if args == "" || args == "null" {
						args = "{}"
					}
					name := event.Name
					name, args = cursorSDKMCPToolCall(name, args)
					t = &tool{name: name, args: args, started: time.Now()}
					tools[event.CallID] = t
					emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeToolCallStart, ToolCallID: event.CallID, ToolName: t.name, ToolArgs: t.args, Metadata: meta})
				}
				if !t.ended && (event.Status == "completed" || event.Status == "error") {
					t.ended = true
					result := string(event.Result)
					var plain string
					if json.Unmarshal(event.Result, &plain) == nil {
						result = plain
					} else if text, ok := cursorSDKMCPToolResult(event.Result); ok {
						result = text
					}
					if t.name == "task" {
						if child, answer, ok := cursorSDKSubagent(s.agentID, event.CallID, t.args, event.Result, event.Status == "error"); ok {
							result = answer
							subagents = append(subagents, child)
							if c.logger != nil {
								c.logger.Infof("[CURSOR_SDK] subagent agent=%s child=%s status=%s messages=%d", s.agentID, child.ID, child.Status, len(child.Messages))
							}
							emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeNativeSubagentTranscript, Metadata: map[string]interface{}{"native_subagents": subagents}})
						}
					}
					emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeToolCallEnd, ToolCallID: event.CallID, ToolName: t.name, ToolArgs: t.args, ToolResult: result, ToolDuration: time.Since(t.started), Metadata: meta})
					// Cursor ends its run once a plan is made, waiting for approval. The plan lives only in the
					// tool's arguments, so the person saw a bare "retrying" line; make the plan the reply.
					if plan := cursorSDKPlanText(t.name, t.args); plan != "" && event.Status == "completed" {
						if text.Len() > 0 && !strings.HasSuffix(text.String(), "\n") {
							plan = "\n\n" + plan
						}
						afterTool = false
						text.WriteString(plan)
						emit(llmtypes.StreamChunk{Type: llmtypes.StreamChunkTypeContent, Content: plan, Metadata: map[string]any{"cursor_session_id": s.agentID, "cursor_stream_source": "sdk", llmtypes.ContentDeltaMetadataKey: true}})
					}
				}
			case "stale":
				if c.logger != nil {
					c.logger.Infof("[CURSOR_SDK] stale_active_run agent=%s action=%s", event.AgentID, event.Action)
				}
			case "turn_ended":
				// Cursor says its turn ended; the run itself may still be open (see the bridge).
				if c.logger != nil {
					c.logger.Infof("[CURSOR_SDK] turn_ended agent=%s chars=%d tools=%d", s.agentID, text.Len(), len(tools))
				}
			case "turn_end_settled":
				// The bridge finished a turn Cursor had ended but whose run stayed open ("quiet", after
				// the quiet period) or ended with the SDK's resume "no progress" error ("no_progress").
				if c.logger != nil {
					c.logger.Infof("[CURSOR_SDK] turn_end_settled agent=%s reason=%s detail=%s chars=%d", s.agentID, event.Action, event.Status, text.Len())
				}
			case "image_order":
				reason := event.Status
				if reason == "" {
					reason = "receipt_order"
				}
				log.Printf("[CHAT_IMAGE] session=%s provider=cursor-cli steer=%s action=%s reason=%s", owner, event.ID, event.Action, reason)
			case "steer_outcome":
				// Whether the running run took a mid-turn message ("complete_delivered") or the bridge answers
				// it as a follow-up in this turn (any other outcome).
				if c.logger != nil {
					c.logger.Infof("[CURSOR_SDK] steer_outcome agent=%s steer=%s outcome=%s", s.agentID, event.ID, event.Status)
				}
			case "result":
				// A message sent after this point starts the next turn rather than joining this one.
				s.mu.Lock()
				s.running, s.prompted = false, false
				s.mu.Unlock()
				switch event.Status {
				case "finished":
				case "cancelled":
					return nil, fmt.Errorf("Cursor turn cancelled")
				default:
					return nil, fmt.Errorf("Cursor SDK turn failed: %s", event.Error)
				}
				if len(tools) == 0 {
					if err := cursorPlanFailure(text.String(), s.model); err != nil {
						return nil, err
					}
				}
				// The streamed text spans every run of this turn: a steer the SDK did not take is answered
				// by a follow-up run, and its result alone would drop the first run's answer.
				final := text.String()
				if strings.TrimSpace(final) == "" {
					_ = json.Unmarshal(event.Result, &final)
				}
				if strings.TrimSpace(final) == "" && len(tools) == 0 {
					return nil, fmt.Errorf("Cursor returned no answer")
				}
				if err := cursorTransportFailure(final, s.model); err != nil {
					if c.logger != nil {
						c.logger.Infof("[CURSOR_SDK] transport_failure agent=%s: the turn's whole reply was a transport error", s.agentID)
					}
					return nil, err
				}
				var usage *llmtypes.Usage
				if u := event.Usage; u != nil {
					cached := u.CacheReadTokens
					usage = &llmtypes.Usage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, TotalTokens: u.TotalTokens, CacheTokens: &cached}
				}
				response := cursorSDKResponse(s, final, usage)
				if len(subagents) > 0 {
					gi := response.Choices[0].GenerationInfo
					if gi.Additional == nil {
						gi.Additional = map[string]interface{}{}
					}
					gi.Additional["native_subagents"] = subagents
				}
				return response, nil
			}
		case <-ctx.Done():
			_ = p.send(map[string]any{"type": "cancel"})
			p.close()
			return nil, ctx.Err()
		case <-p.done:
			return nil, p.failure()
		}
	}
}

// SendCursorSDKInput steers the running turn. Delivery is the bridge's receipt: the SDK adds the
// message at its next safe step, or the bridge answers it right after the run in the same turn.
func SendCursorSDKInput(ctx context.Context, owner, message string) error {
	cursorSDKSessions.Lock()
	s := cursorSDKSessions.sessions[strings.TrimSpace(owner)]
	cursorSDKSessions.Unlock()
	if s == nil {
		return fmt.Errorf("%w: no active Cursor SDK turn", llmtypes.ErrCodingAgentInputNotSent)
	}
	// While the turn is still starting, wait until the bridge holds its prompt. If the launch fails first,
	// the turn stops running and the message is reported not sent, so the host keeps it (never a lost ack).
	var p *cursorSDKProcess
	for {
		s.mu.Lock()
		proc, running, prompted := s.proc, s.running, s.prompted
		s.mu.Unlock()
		if !running {
			return fmt.Errorf("%w: no active Cursor SDK turn", llmtypes.ErrCodingAgentInputNotSent)
		}
		if proc != nil && prompted {
			p = proc
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	token := make([]byte, 8)
	_, _ = rand.Read(token)
	id := hex.EncodeToString(token)
	ack := make(chan bool, 1)
	p.mu.Lock()
	p.acks[id] = ack
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.acks, id); p.mu.Unlock() }()
	imageID, imageErr := p.registerImages(ctx, owner)
	if imageErr != nil {
		return imageErr
	}
	if err := p.send(map[string]any{"type": "steer", "id": id, "text": message, "image_id": imageID}); err != nil {
		return fmt.Errorf("%w: %v", llmtypes.ErrCodingAgentInputNotSent, err)
	}
	select {
	case accepted := <-ack:
		if !accepted {
			return fmt.Errorf("%w: the Cursor turn had finished", llmtypes.ErrCodingAgentInputNotSent)
		}
		return nil
	case <-p.done:
		return errors.Join(llmtypes.ErrCodingAgentInputNotSent, p.failure())
	case <-ctx.Done():
		return ctx.Err()
	}
}

func cursorSDKSessionHandle(s *cursorSDKSession) llmtypes.CodingProviderSessionHandle {
	return llmtypes.CodingProviderSessionHandle{Provider: "cursor-cli", Transport: "sdk", NativeSessionID: s.agentID, WorkingDir: s.cwd, Model: s.model, Status: llmtypes.CodingProviderSessionStatusIdle}
}

func cursorSDKResponse(s *cursorSDKSession, text string, usage *llmtypes.Usage) *llmtypes.ContentResponse {
	gi := &llmtypes.GenerationInfo{Additional: map[string]any{"cursor_session_id": s.agentID}}
	if usage != nil {
		// The SDK's inputTokens already contains cached input (its totalTokens is input + output; only the
		// Cursor CLI subtracts cache). Without this flag the cost ledger added cache reads a second time.
		gi.Additional["prompt_tokens_include_cache"] = true
		if usage.CacheTokens != nil {
			gi.Additional["cache_read_input_tokens"] = *usage.CacheTokens
		}
	}
	llmtypes.AttachCodingProviderSessionHandle(gi, cursorSDKSessionHandle(s))
	return &llmtypes.ContentResponse{Choices: []*llmtypes.ContentChoice{{Content: text, StopReason: "stop", GenerationInfo: gi}}, Usage: usage}
}

// SDKRuntimeAvailable checks the installed component without starting an agent.
func SDKRuntimeAvailable() error { _, err := cursorSDKEntry(); return err }

// cursorModelRefusal matches the Cursor SDK's refusal of a model missing from the account's model list.
var cursorModelRefusal = regexp.MustCompile(`Cannot use this model: ([^.\s]+(?:\.[^.\s]+)*)\. Available models: ([^.]*(?:\.[0-9][^.]*)*)\.`)

// cursorSDKMCPToolCall names an MCP call by its server and tool (mcp__<server>__<tool>) with the tool's own
// arguments, as people and receipts expect; the SDK reports every MCP call as "mcp" with a wrapper.
func cursorSDKMCPToolCall(name, args string) (string, string) {
	var mcp struct {
		Server string          `json:"providerIdentifier"`
		Tool   string          `json:"toolName"`
		Args   json.RawMessage `json:"args"`
	}
	if json.Unmarshal([]byte(args), &mcp) != nil || mcp.Server == "" || mcp.Tool == "" {
		return name, args
	}
	inner := string(mcp.Args)
	if inner == "" || inner == "null" {
		inner = "{}"
	}
	return "mcp__" + mcp.Server + "__" + mcp.Tool, inner
}

// cursorSDKMCPToolResult returns the text of an MCP result the SDK wraps as
// {"status":"success","value":{"content":[{"text":{"text":"..."}}]}}.
func cursorSDKMCPToolResult(raw json.RawMessage) (string, bool) {
	var wrapped struct {
		Status string `json:"status"`
		Value  struct {
			Content []struct {
				Text struct {
					Text string `json:"text"`
				} `json:"text"`
			} `json:"content"`
		} `json:"value"`
		Error any `json:"error"`
	}
	if json.Unmarshal(raw, &wrapped) != nil || wrapped.Status == "" || len(wrapped.Value.Content) == 0 {
		return "", false
	}
	parts := make([]string, 0, len(wrapped.Value.Content))
	for _, c := range wrapped.Value.Content {
		if c.Text.Text != "" {
			parts = append(parts, c.Text.Text)
		}
	}
	if len(parts) == 0 {
		return "", false
	}
	return strings.Join(parts, "\n"), true
}

// cursorChatRuntimeFolder reports whether dir is a per-chat runtime folder the application made for this chat
// (state/cli-runtimes/v1/<id>, in the app's or a slot's state), never a folder two chats share.
func cursorChatRuntimeFolder(dir string) bool {
	parent := filepath.Dir(filepath.Clean(dir))
	return filepath.Base(parent) == "v1" && filepath.Base(filepath.Dir(parent)) == "cli-runtimes"
}

// cursorSDKPlanText returns the reply text for a plan Cursor made with its createPlan tool, or "".
func cursorSDKPlanText(name, args string) string {
	if name != "createPlan" {
		return ""
	}
	var plan struct {
		Plan     string `json:"plan"`
		Overview string `json:"overview"`
	}
	if json.Unmarshal([]byte(args), &plan) != nil {
		return ""
	}
	body := strings.TrimSpace(plan.Plan)
	if body == "" {
		body = strings.TrimSpace(plan.Overview)
	}
	if body == "" {
		return ""
	}
	return body + "\n\nReply \"go\" to build this plan, or say what to change."
}
