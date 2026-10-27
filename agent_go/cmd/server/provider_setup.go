package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/manishiitg/mcpagent/llm"

	"github.com/manishiitg/coding-agent-loop/agent_go/internal/claudeauth"
	"github.com/manishiitg/coding-agent-loop/agent_go/internal/cursorauth"
)

const (
	providerSetupOutputLimit = 1024 * 1024
	providerSetupMaxDuration = 20 * time.Minute
	providerSetupRetention   = 10 * time.Minute
	providerSetupStopTimeout = 2 * time.Second
)

type providerSetupConflictError struct {
	provider string
}

func (e *providerSetupConflictError) Error() string {
	return fmt.Sprintf("a %s setup session is already running", e.provider)
}

type providerSetupCommand struct {
	command   string
	args      []string
	readPaths []string
}

// Provider setup never accepts a command from the browser. Each supported
// action maps to one reviewed argv so this feature cannot become a general
// remote shell.
var providerSetupCommands = map[string]map[string]providerSetupCommand{
	"claude-code": {
		"authenticate": {command: "claude", args: []string{"auth", "login"}},
		"inspect":      {command: "claude", args: []string{"--tools", ""}},
		"usage":        {command: "claude", args: []string{"--tools", "", "--strict-mcp-config"}},
	},
	"codex-cli": {
		// --device-auth prints a URL + code to authenticate from any device,
		// rather than starting a local callback server on localhost:1455 and
		// waiting for a browser redirect back to this same machine -- this
		// process almost always runs on a remote/headless server with no
		// local browser of its own, where the plain flow can never complete.
		"authenticate": {command: "codex", args: []string{"login", "--device-auth"}},
		"inspect":      {command: "codex", args: []string{"--sandbox", "read-only", "--ask-for-approval", "never"}},
		"usage":        {command: "codex", args: []string{"--sandbox", "read-only", "--ask-for-approval", "never", "--disable", "shell_tool"}},
	},
	"cursor-cli": {
		"authenticate": {command: "cursor-agent", args: []string{"login"}},
		"inspect":      {command: "cursor-agent", args: []string{"--mode", "ask", "--sandbox", "enabled"}},
	},
	"pi-cli": {
		// Pi exposes provider authentication from inside its TUI via /login.
		// Keep the PTY attached so the administrator can choose the underlying
		// provider and complete its browser/API-key flow without server access.
		"authenticate": {command: "pi"},
		"inspect":      {command: "pi"},
	},
	"muse-cli": {
		"authenticate": {command: "muse", args: []string{"login"}},
		"inspect":      {command: "muse", args: []string{"--disable-shell", "--disable-write"}},
		"usage":        {command: "muse", args: []string{"--disable-shell", "--disable-write"}},
	},
	"agy-cli": {
		// agy has no login subcommand: Google sign-in completes inside the
		// TUI, so authenticate opens the bare CLI like pi. No usage action:
		// agy exposes no verified quota slash command to drive.
		"authenticate": {command: "agy"},
		"inspect":      {command: "agy", args: []string{"--sandbox"}},
	},
}

// Install the registry's command only on a single-user desktop. A hosted
// server owns its CLI programs through deployment, independently of accounts.
var providerInstallHostOS = runtime.GOOS

func providerInstallAvailable(provider string) bool {
	return providerInstallHostOS == "darwin" && !IsMultiUserMode() && providerInstallCommand(provider) != ""
}

func providerSetupCommandFor(provider, action string) (providerSetupCommand, error) {
	if action == "install" {
		if !providerInstallAvailable(provider) {
			return providerSetupCommand{}, errors.New("provider installation is available only on a single-user Mac; server providers are managed by deployment")
		}
		return providerSetupCommand{command: "/bin/bash", args: []string{"-o", "pipefail", "-lc", providerInstallCommand(provider)}}, nil
	}
	actions, ok := providerSetupCommands[provider]
	if !ok {
		return providerSetupCommand{}, fmt.Errorf("guided setup is not available for provider %q", provider)
	}
	spec, ok := actions[action]
	if !ok {
		return providerSetupCommand{}, fmt.Errorf("guided %s is not available for provider %q", action, provider)
	}
	return spec, nil
}

// OSC sequences (ESC ] ... BEL or ESC \) come first: matching only "ESC ]" left
// Muse's colour queries behind as "10;?11;?4;0;?..." in the usage text.
var providerSetupANSI = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b\[[0-9;:?>]*[ -/]*[@-~]|\x1b.|\x07`)

// providerSetupCursorMoves are the escapes a full-screen CLI draws rows with instead of newlines (move to row/column,
// next line, line down). Codex 0.161 draws its whole screen this way, so with escapes simply removed its "›" prompt was
// never at the start of a line, /status was never sent and Check usage returned the start screen (server B 2026-10-08).
var providerSetupCursorSpaces = regexp.MustCompile(`\x1b\[[0-9]*C`)

var providerUsagePercentage = regexp.MustCompile(`\d+(?:\.\d+)?%`)

var providerSetupCursorMoves = regexp.MustCompile(`\x1b\[[0-9;]*[HfEBd]`)

// providerUsageScreenText is a usage terminal's output as plain text, one screen row per line.
func providerUsageScreenText(raw string) string {
	text := providerSetupANSI.ReplaceAllString(providerSetupCursorMoves.ReplaceAllString(providerSetupCursorSpaces.ReplaceAllString(raw, " "), "\n"), "")
	// Claude's prompt can separate the marker from its hint with a non-breaking space.
	return strings.ReplaceAll(text, "\u00a0", " ")
}

// terminalQueryPattern finds what a full-screen CLI asks its terminal before it
// draws: cursor position, device attributes, and foreground/background/palette
// colours.
var terminalQueryPattern = regexp.MustCompile(`\x1b\[0?6n|\x1b\[0?c|\x1b\](10|11);\?(?:\x07|\x1b\\)|\x1b\]4;([0-9]+);\?(?:\x07|\x1b\\)`)

// terminalQueryReplies answers those queries as a plain terminal would. A
// read-only usage run has no browser terminal to answer them, and Muse gave up
// with "cursor position could not be read within a normal duration" instead
// of printing its usage (server B 2026-10-07, PLAT-688).
func terminalQueryReplies(output []byte) string {
	var b strings.Builder
	for _, m := range terminalQueryPattern.FindAllSubmatch(output, -1) {
		q := string(m[0])
		switch {
		case strings.HasSuffix(q, "6n"):
			b.WriteString("\x1b[1;1R")
		case strings.HasSuffix(q, "c"):
			b.WriteString("\x1b[?62;22c")
		case len(m[1]) > 0 && string(m[1]) == "10":
			b.WriteString("\x1b]10;rgb:ffff/ffff/ffff\x07")
		case len(m[1]) > 0:
			b.WriteString("\x1b]11;rgb:0000/0000/0000\x07")
		default:
			b.WriteString("\x1b]4;" + string(m[2]) + ";rgb:8080/8080/8080\x07")
		}
	}
	return b.String()
}

// Usage sessions answer terminal queries on the server. Browser replies are
// filtered from usage input, including replies generated by transcript replay.
func (s *providerSetupSession) answerTerminalQueries(output []byte) {
	if reply := terminalQueryReplies(output); reply != "" {
		_ = s.write(reply)
	}
}

var providerUsageCommands = map[string]string{
	"claude-code": "/usage",
	"codex-cli":   "/status",
	"muse-cli":    "/usage",
}

type providerSetupSnapshot struct {
	ID           string    `json:"id"`
	Provider     string    `json:"provider"`
	Action       string    `json:"action"`
	Status       string    `json:"status"`
	ExitCode     *int      `json:"exit_code,omitempty"`
	Error        string    `json:"error,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	UsageCommand string    `json:"usage_command,omitempty"`
	InputLimit   int       `json:"input_limit,omitempty"`
}

type providerSetupSession struct {
	bindingID   string
	mu          sync.Mutex
	id          string
	provider    string
	action      string
	ownerID     string
	status      string
	exitCode    *int
	errMessage  string
	createdAt   time.Time
	updatedAt   time.Time
	terminal    *os.File
	cancel      context.CancelFunc
	output      []byte
	subscribers map[chan []byte]struct{}
	done        chan struct{}
	// readOnly sessions belong to someone who does not manage the account:
	// the server drives the usage command and every input is dropped.
	readOnly          bool
	usageSubmitted    time.Time
	usageOutputOffset int
	usageStartupDone  chan struct{}
	usageStartupErr   error
	usageInput        providerUsageInput
}

// Usage managers may type short input and navigate the CLI. Account/session
// changes are cancelled server-side; non-managers never get interactive input.
func (s *providerSetupSession) userInput(data string) error {
	s.mu.Lock()
	if s.readOnly {
		s.mu.Unlock()
		return nil
	}
	var inputErr error
	if s.action == "usage" {
		data, inputErr = s.usageInput.filter(data)
	}
	s.mu.Unlock()
	if data != "" {
		if err := s.write(data); err != nil {
			return err
		}
	}
	return inputErr
}

func (s *providerSetupSession) snapshot() providerSetupSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := providerSetupSnapshot{
		ID:        s.id,
		Provider:  s.provider,
		Action:    s.action,
		Status:    s.status,
		ExitCode:  s.exitCode,
		Error:     s.errMessage,
		CreatedAt: s.createdAt,
		UpdatedAt: s.updatedAt,
	}
	if s.action == "usage" {
		result.UsageCommand = providerUsageCommands[s.provider]
		result.InputLimit = providerUsageInputLimit
	}
	return result
}

func (s *providerSetupSession) isOwnedBy(userID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ownerID == userID
}

func (s *providerSetupSession) isRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status == "running"
}

func (s *providerSetupSession) appendOutput(chunk []byte) {
	if len(chunk) == 0 {
		return
	}
	copyChunk := append([]byte(nil), chunk...)
	s.mu.Lock()
	s.output = append(s.output, copyChunk...)
	if len(s.output) > providerSetupOutputLimit {
		s.output = append([]byte(nil), s.output[len(s.output)-providerSetupOutputLimit:]...)
	}
	s.updatedAt = time.Now().UTC()
	answerQueries := s.readOnly || s.action == "usage"
	for subscriber := range s.subscribers {
		select {
		case subscriber <- copyChunk:
		default:
			// A reconnect receives the bounded transcript seed. Never let a slow
			// browser block the provider command or its authentication prompts.
		}
	}
	s.mu.Unlock()
	if answerQueries {
		s.answerTerminalQueries(copyChunk)
	}
}

func (s *providerSetupSession) outputText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.output)
}

func (s *providerSetupSession) finish(err error) {
	s.mu.Lock()
	if s.status != "running" {
		s.mu.Unlock()
		return
	}
	code := 0
	if err != nil {
		code = -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode()
		}
		s.status = "failed"
		s.errMessage = err.Error()
	} else {
		s.status = "completed"
	}
	s.exitCode = &code
	s.updatedAt = time.Now().UTC()
	for subscriber := range s.subscribers {
		close(subscriber)
		delete(s.subscribers, subscriber)
	}
	provider := s.provider
	log.Printf("[PROVIDER_SETUP] session=%s provider=%s action=%s finished status=%s exit_code=%d", s.id, s.provider, s.action, s.status, code)
	s.mu.Unlock()
	invalidateProviderAuthProbe(provider)
}

func (s *providerSetupSession) subscribe() ([]byte, <-chan []byte, func()) {
	channel := make(chan []byte, 128)
	s.mu.Lock()
	seed := append([]byte(nil), s.output...)
	if s.status == "running" {
		s.subscribers[channel] = struct{}{}
	} else {
		close(channel)
	}
	s.mu.Unlock()
	return seed, channel, func() {
		s.mu.Lock()
		if _, ok := s.subscribers[channel]; ok {
			delete(s.subscribers, channel)
			close(channel)
		}
		s.mu.Unlock()
	}
}

func (s *providerSetupSession) write(data string) error {
	s.mu.Lock()
	terminal := s.terminal
	running := s.status == "running"
	s.mu.Unlock()
	if !running || terminal == nil {
		return errors.New("provider setup is not running")
	}
	_, err := io.WriteString(terminal, data)
	return err
}

func (s *providerSetupSession) resize(cols, rows int) error {
	cols, rows, ok := clampLiveAttachGeometry(cols, rows)
	if !ok {
		return errors.New("invalid terminal size")
	}
	s.mu.Lock()
	terminal := s.terminal
	running := s.status == "running"
	s.mu.Unlock()
	if !running || terminal == nil {
		return nil
	}
	return pty.Setsize(terminal, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (s *providerSetupSession) stop() {
	s.mu.Lock()
	if s.status != "running" {
		s.mu.Unlock()
		return
	}
	s.status = "cancelled"
	s.errMessage = "Cancelled by user"
	s.updatedAt = time.Now().UTC()
	cancel := s.cancel
	terminal := s.terminal
	provider := s.provider
	for subscriber := range s.subscribers {
		close(subscriber)
		delete(s.subscribers, subscriber)
	}
	s.mu.Unlock()
	invalidateProviderAuthProbe(provider)
	if cancel != nil {
		cancel()
	}
	if terminal != nil {
		_ = terminal.Close()
	}
}

type providerSetupManager struct {
	mu       sync.Mutex
	sessions map[string]*providerSetupSession
	starting map[string]bool
}

func newProviderSetupManager() *providerSetupManager {
	return &providerSetupManager{
		sessions: make(map[string]*providerSetupSession),
		starting: make(map[string]bool),
	}
}

func (m *providerSetupManager) start(ownerID, provider, action string, cols, rows int, environment []string, cleanup func(), replaceRunning bool, connectionIDs ...string) (*providerSetupSession, error) {
	return m.startWithArgs(ownerID, provider, action, cols, rows, environment, cleanup, replaceRunning, nil, connectionIDs...)
}

// providerSetupModelArgs is the model flag for an inspect or usage terminal: the account's first allowed model, so the CLI does not
// open on its own default model (which the account may not allow) and a manager typing into it cannot reach a model the account
// does not offer. Nothing is added when the account allows every model, or for a provider whose flag is not known.
func providerSetupModelArgs(provider string, allowed []string) []string {
	flag := map[string]string{"codex-cli": "-m", "claude-code": "--model"}[provider]
	if flag == "" || len(allowed) == 0 {
		return nil
	}
	return []string{flag, allowed[0]}
}

func (m *providerSetupManager) startWithArgs(ownerID, provider, action string, cols, rows int, environment []string, cleanup func(), replaceRunning bool, extraArgs []string, connectionIDs ...string) (*providerSetupSession, error) {
	bindingID := provider
	if len(connectionIDs) > 0 && connectionIDs[0] != "" {
		bindingID = connectionIDs[0]
	}
	if cleanup == nil {
		cleanup = func() {}
	}
	spec, err := providerSetupCommandFor(provider, action)
	if err == nil && provider == "cursor-cli" && action == "authenticate" {
		ctx, cancel := context.WithTimeout(context.Background(), providerSetupMaxDuration)
		spec, err = cursorAccountCommand(ctx, "login")
		cancel()
	}
	if err != nil {
		cleanup()
		return nil, err
	}
	if _, err := exec.LookPath(spec.command); err != nil {
		cleanup()
		return nil, fmt.Errorf("required command %q is not available on the server", spec.command)
	}

	m.mu.Lock()
	if m.starting[bindingID] {
		m.mu.Unlock()
		cleanup()
		return nil, &providerSetupConflictError{provider: provider}
	}
	var sessionsToStop []*providerSetupSession
	for _, existing := range m.sessions {
		if existing.provider == provider && (existing.bindingID == bindingID || (existing.bindingID == "" && bindingID == provider)) && existing.isRunning() {
			if !replaceRunning {
				m.mu.Unlock()
				cleanup()
				return nil, &providerSetupConflictError{provider: provider}
			}
			sessionsToStop = append(sessionsToStop, existing)
			delete(m.sessions, existing.id)
		}
	}
	m.starting[bindingID] = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.starting, bindingID)
		m.mu.Unlock()
	}()

	for _, existing := range sessionsToStop {
		existing.stop()
		select {
		case <-existing.done:
		case <-time.After(providerSetupStopTimeout):
		}
	}

	id, err := randomProviderSetupID()
	if err != nil {
		cleanup()
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), providerSetupMaxDuration)
	command := exec.CommandContext(ctx, spec.command, append(append([]string{}, spec.args...), extraArgs...)...)
	if bindingID != provider {
		for _, entry := range environment {
			if strings.HasPrefix(entry, "HOME=") {
				command.Dir = strings.TrimPrefix(entry, "HOME=")
			}
		}
	}
	if action == "usage" {
		workDir, mkdirErr := os.MkdirTemp("", "agentworks-provider-usage-*")
		if mkdirErr != nil {
			cancel()
			cleanup()
			return nil, fmt.Errorf("prepare provider usage workspace: %w", mkdirErr)
		}
		previousCleanup := cleanup
		cleanup = func() {
			previousCleanup()
			_ = os.RemoveAll(workDir)
		}
		command.Dir = workDir
	}
	if environment == nil {
		environment = minimalChildEnv()
	}
	command.Env = append(environment, "TERM=xterm-256color", "COLORTERM=truecolor")
	cols, rows, validSize := clampLiveAttachGeometry(cols, rows)
	if !validSize {
		cols, rows = liveAttachDefaultCols, liveAttachDefaultRows
	}
	// A personal account's terminal runs confined to that account's private home (provider_setup_confine.go).
	releaseConfinement, confineErr := confineProviderSetup(command, provider, environment, providerSetupIsPersonalBinding(provider, bindingID), spec.readPaths...)
	if confineErr != nil {
		cancel()
		cleanup()
		return nil, fmt.Errorf("start %s %s: %w", provider, action, confineErr)
	}
	previousCleanup := cleanup
	cleanup = func() { previousCleanup(); releaseConfinement() }
	terminal, err := pty.StartWithSize(command, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		cancel()
		cleanup()
		return nil, fmt.Errorf("start %s %s: %w", provider, action, err)
	}
	now := time.Now().UTC()
	session := &providerSetupSession{bindingID: bindingID,
		id:          id,
		provider:    provider,
		action:      action,
		ownerID:     ownerID,
		status:      "running",
		createdAt:   now,
		updatedAt:   now,
		terminal:    terminal,
		cancel:      cancel,
		subscribers: make(map[chan []byte]struct{}),
		done:        make(chan struct{}),
	}
	if action == "usage" {
		session.usageStartupDone = make(chan struct{})
	}
	m.mu.Lock()
	m.sessions[id] = session
	m.mu.Unlock()
	go func() {
		defer cleanup()
		buffer := make([]byte, 32*1024)
		for {
			n, readErr := terminal.Read(buffer)
			if n > 0 {
				session.appendOutput(buffer[:n])
			}
			if readErr != nil {
				break
			}
		}
		waitErr := command.Wait()
		cancel()
		_ = terminal.Close()
		session.finish(waitErr)
		close(session.done)
		time.AfterFunc(providerSetupRetention, func() { m.remove(id, false) })
	}()

	return session, nil
}

// driveProviderUsage collects usage for people without interactive account access. It only
// writes a fixed provider command and, in the throwaway usage directory, the
// minimum keys needed to accept a workspace-trust prompt.
func driveProviderUsage(ctx context.Context, session *providerSetupSession) {
	if session.usageStartupDone != nil {
		defer close(session.usageStartupDone)
	}
	command := providerUsageCommands[session.provider]
	if command == "" {
		return
	}
	started := time.Now()
	startupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	fail := func(step string, err error) {
		session.mu.Lock()
		session.usageStartupErr = err
		status, size := session.status, len(session.output)
		session.mu.Unlock()
		log.Printf("[PROVIDER_USAGE] session=%s provider=%s startup_failed step=%s elapsed_ms=%d status=%s output_bytes=%d error=%q", session.id, session.provider, step, time.Since(started).Milliseconds(), status, size, err)
	}
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	baseline, trustAttempts := 0, 0
	for {
		select {
		case <-startupCtx.Done():
			fail("prompt", startupCtx.Err())
			return
		case <-session.done:
			fail("process_exit", errors.New("provider stopped before its command prompt"))
			return
		case <-ticker.C:
			raw := session.outputText()
			visible := raw
			if baseline > 0 {
				if baseline >= len(raw) {
					continue
				}
				visible = raw[baseline:]
			}
			output := providerUsageScreenText(visible)
			if providerUsageTrustPrompt(session.provider, output) {
				if session.provider == "claude-code" && claudeUsageTrustChoice(output) == 0 {
					continue
				}
				trustAttempts++
				var err error
				baseline, err = acceptProviderUsageTrust(startupCtx, session, session.provider, output)
				if err != nil {
					fail("trust", err)
					return
				}
				log.Printf("[PROVIDER_USAGE] session=%s provider=%s trust_key_sent attempt=%d elapsed_ms=%d", session.id, session.provider, trustAttempts, time.Since(started).Milliseconds())
				continue
			}
			if !providerUsagePromptReady(session.provider, output) {
				continue
			}
			session.mu.Lock()
			updated := session.updatedAt
			session.mu.Unlock()
			// Claude can draw its first prompt while startup is still settling.
			// An immediate /usage can stay on its loading screen indefinitely.
			if session.provider == "claude-code" && time.Since(updated) < providerUsageSettle {
				continue
			}
			session.mu.Lock()
			session.usageOutputOffset = len(session.output)
			session.mu.Unlock()
			if err := session.write(command + "\r"); err != nil {
				fail("command_write", err)
				return
			}
			session.mu.Lock()
			session.usageSubmitted = time.Now()
			session.mu.Unlock()
			log.Printf("[PROVIDER_USAGE] session=%s provider=%s command_submitted command=%s trust_attempts=%d elapsed_ms=%d", session.id, session.provider, command, trustAttempts, time.Since(started).Milliseconds())
			return
		}
	}
}

func providerUsageTrustPrompt(provider, output string) bool {
	lower := strings.ToLower(output)
	switch provider {
	case "claude-code":
		// Claude also positions words with cursor moves, which ANSI stripping
		// removes instead of leaving spaces. Match either rendering.
		compact := strings.Join(strings.Fields(lower), "")
		return strings.Contains(compact, "yes,itrustthisfolder") || strings.Contains(compact, "yes-itrustthisproject")
	case "codex-cli":
		return strings.Contains(lower, "do you trust the contents of this directory") && strings.Contains(lower, "press enter to continue")
	case "muse-cli":
		return strings.Contains(lower, "do you trust this workspace") || strings.Contains(lower, "do you trust the files in this folder")
	default:
		return false
	}
}

// Capture the raw offset BEFORE confirming. A fast CLI can print its real
// prompt before write returns; taking a cleaned-text length afterwards loses it.
func acceptProviderUsageTrust(ctx context.Context, session *providerSetupSession, provider, output string) (int, error) {
	if provider == "claude-code" {
		return acceptClaudeUsageTrust(ctx, session)
	}
	if provider == "codex-cli" && strings.Contains(strings.ToLower(output), "› 2. no, quit") {
		if err := session.write("\x1b[A"); err != nil {
			return 0, err
		}
		time.Sleep(250 * time.Millisecond)
	}
	offset := len(session.outputText())
	key := "\r"
	if provider == "muse-cli" {
		key = "1\r"
	}
	return offset, session.write(key)
}

// Claude can redraw the default selection while its input handlers settle.
// Wait for a quiet frame after each key and inspect the new selection before
// Enter. Other startup redraws are handled by the driver's next iteration.
func acceptClaudeUsageTrust(ctx context.Context, session *providerSetupSession) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	var lastKey time.Time
	for {
		select {
		case <-ctx.Done():
			return 0, fmt.Errorf("trust selection did not settle: %w", ctx.Err())
		case <-session.done:
			return 0, errors.New("provider stopped at its trust screen")
		case <-ticker.C:
			session.mu.Lock()
			ready := session.status == "running"
			quietSince, raw := session.updatedAt, string(session.output)
			session.mu.Unlock()
			if !ready {
				return 0, errors.New("provider stopped at its trust screen")
			}
			if lastKey.After(quietSince) {
				quietSince = lastKey
			}
			if time.Since(quietSince) < 300*time.Millisecond {
				continue
			}
			switch claudeUsageTrustChoice(providerUsageScreenText(raw)) {
			case -1:
				if err := session.write("\x1b[B"); err != nil {
					return 0, fmt.Errorf("select trust row: %w", err)
				}
				lastKey = time.Now()
			case 1:
				return len(raw), session.write("\r")
			}
		}
	}
}

// The transcript contains old redraws. Only its last selected trust row counts.
func claudeUsageTrustChoice(output string) int {
	compact := strings.Join(strings.Fields(strings.ToLower(output)), "")
	last, choice := -1, 0
	for _, marker := range []string{"❯", "›", ">"} {
		for _, row := range []struct {
			text   string
			choice int
		}{{"no,exit", -1}, {"1.no,exit", -1}, {"yes,itrustthisfolder", 1}, {"2.yes,itrustthisfolder", 1}} {
			if at := strings.LastIndex(compact, marker+row.text); at > last {
				last, choice = at, row.choice
			}
		}
	}
	return choice
}

func providerUsagePromptReady(provider, output string) bool {
	lines := strings.Split(strings.ReplaceAll(output, "\r", ""), "\n")
	start := len(lines) - 24
	if start < 0 {
		start = 0
	}
	for _, line := range lines[start:] {
		trimmed := strings.TrimSpace(line)
		if provider == "claude-code" && claudeUsageTrustChoice(trimmed) != 0 {
			continue
		}
		switch provider {
		case "claude-code", "muse-cli":
			if trimmed == "❯" || strings.HasPrefix(trimmed, "❯ ") {
				return true
			}
		case "codex-cli":
			if trimmed == "›" || strings.HasPrefix(trimmed, "› ") {
				return true
			}
		}
	}
	return false
}

func (m *providerSetupManager) get(id string) (*providerSetupSession, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	return session, ok
}

func (m *providerSetupManager) remove(id string, stop bool) {
	m.mu.Lock()
	session := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if stop && session != nil {
		session.stop()
	}
}

func randomProviderSetupID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("create provider setup id: %w", err)
	}
	return "provider-setup-" + hex.EncodeToString(value), nil
}

func (api *StreamingAPI) providerSetupManager() *providerSetupManager {
	api.providerSetupMu.Lock()
	defer api.providerSetupMu.Unlock()
	if api.providerSetups == nil {
		api.providerSetups = newProviderSetupManager()
	}
	return api.providerSetups
}

type startProviderSetupRequest struct {
	ConnectionID   string `json:"connection_id,omitempty"`
	Provider       string `json:"provider"`
	Action         string `json:"action"`
	WorkspacePath  string `json:"workspace_path,omitempty"`
	Cols           int    `json:"cols,omitempty"`
	Rows           int    `json:"rows,omitempty"`
	ReplaceRunning bool   `json:"replace_running,omitempty"`
}

// workflowProviderSetupEnvironment builds an isolated CLI environment for an
// account-inspection terminal. The credential is resolved server-side and is
// never returned to the browser or stored in the setup session snapshot.
//
// Claude gets a throwaway config directory in addition to stripped Anthropic
// variables. That prevents a rejected workflow token from silently falling
// back to the server's saved Claude login. Cursor similarly loses any ambient
// login token before the workflow API key is injected.
func workflowProviderSetupEnvironment(provider string, keys *llm.ProviderAPIKeys) ([]string, func(), error) {
	if keys == nil {
		return nil, nil, errors.New("no workflow provider credential is configured")
	}
	before := os.Environ()
	label := "workflow-setup provider=" + provider
	switch provider {
	case string(llm.ProviderClaudeCode):
		if keys.ClaudeCodeOAuthToken == nil || strings.TrimSpace(*keys.ClaudeCodeOAuthToken) == "" {
			return nil, nil, errors.New("no Claude Code setup token is configured for this workflow")
		}
		configDir, err := os.MkdirTemp("", "agentworks-claude-terminal-*")
		if err != nil {
			return nil, nil, errors.New("could not prepare the workflow Claude terminal")
		}
		cleanup := func() { _ = os.RemoveAll(configDir) }
		environment := claudeauth.CheckEnv(os.Environ(), configDir, strings.TrimSpace(*keys.ClaudeCodeOAuthToken))
		logChildEnv(label, before, environment)
		return environment, cleanup, nil
	case string(llm.ProviderCursorCLI):
		if keys.CursorCLI == nil || strings.TrimSpace(*keys.CursorCLI) == "" {
			return nil, nil, errors.New("no Cursor API key is configured for this workflow")
		}
		environment := cursorauth.CheckEnv(os.Environ())
		environment = append(environment, "CURSOR_API_KEY="+strings.TrimSpace(*keys.CursorCLI))
		logChildEnv(label, before, environment)
		return environment, func() {}, nil
	default:
		return nil, nil, fmt.Errorf("workflow-scoped terminals are not available for provider %q", provider)
	}
}

// The server's Claude chats use an explicit setup token when configured.
// Usage and inspection must receive that same credential instead of falling
// back to a different saved login. Keep other service secrets out of the PTY.
func serverClaudeSetupEnvironment(keys *llm.ProviderAPIKeys) []string {
	environment := append(minimalChildEnv(), passthroughChildEnv("CLAUDE_CONFIG_DIR")...)
	if keys != nil && keys.ClaudeCodeOAuthToken != nil && strings.TrimSpace(*keys.ClaudeCodeOAuthToken) != "" {
		environment = append(environment, "CLAUDE_CODE_OAUTH_TOKEN="+strings.TrimSpace(*keys.ClaudeCodeOAuthToken))
	}
	logChildEnv("provider-setup:claude-code:server", os.Environ(), environment)
	return environment
}

func (api *StreamingAPI) handleStartProviderSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	var request startProviderSetupRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 16*1024)).Decode(&request); err != nil {
		http.Error(w, `{"error":"invalid request"}`, http.StatusBadRequest)
		return
	}
	request.Provider = strings.TrimSpace(request.Provider)
	request.Action = strings.TrimSpace(request.Action)
	request.WorkspacePath = strings.TrimSpace(request.WorkspacePath)
	if request.Action == "install" && (!providerInstallAvailable(request.Provider) || request.ConnectionID != "" || request.WorkspacePath != "") {
		http.Error(w, `{"error":"Install providers from a single-user Mac. Server installations are managed by deployment."}`, http.StatusForbidden)
		return
	}
	var environment []string
	var cleanup func()
	caller := GetUserIDFromContext(r.Context())
	admin := currentUserIsAdmin(r)
	accountOwner := ""
	if request.ConnectionID != "" && !strings.HasPrefix(request.ConnectionID, "global:") {
		// A user account: its owner and admins sign it in and inspect it;
		// someone it is shared with may only see usage, run by the server.
		record, err := api.providerSetupAccount(r.Context(), request, caller, admin)
		if err != nil {
			http.Error(w, "connection unavailable or unauthorized", http.StatusForbidden)
			return
		}
		if request.Action == "authenticate" {
			home, homeErr := providerConnectionHome(record.ID)
			if homeErr != nil || detachPrivateProviderCredentialLinks(home) != nil {
				http.Error(w, "cannot prepare independent provider login", http.StatusInternalServerError)
				return
			}
		}
		keys, err := providerConnectionRuntimeKeys(*record)
		if err != nil {
			http.Error(w, "connection unavailable or unauthorized", http.StatusForbidden)
			return
		}
		accountOwner = record.OwnerUserID
		environment = providerConnectionSetupEnvironment(providerConnectionEnvLabel(request.Provider, request.ConnectionID), keys)
	} else if !admin {
		// The server account: signing it in or inspecting it is admin-only.
		// Anyone it is available to may see its usage, run by the server.
		if request.Action != "usage" || !api.serverAccountAvailableToCaller(r.Context(), caller, request.Provider) {
			writeWorkflowPermissionDenied(w, "admin")
			return
		}
	}
	if request.WorkspacePath != "" && request.ConnectionID == "" {
		if request.Action != "inspect" {
			http.Error(w, `{"error":"workflow context is only supported for account inspection"}`, http.StatusBadRequest)
			return
		}
		if !requireWorkflowVisible(w, r, request.WorkspacePath) {
			return
		}
		keys, resolveErr := api.resolveEffectiveAPIKeys(r.Context(), GetUserIDFromContext(r.Context()), request.WorkspacePath, nil)
		if resolveErr != nil {
			http.Error(w, `{"error":"could not resolve the workflow provider credential"}`, http.StatusInternalServerError)
			return
		}
		var environmentErr error
		environment, cleanup, environmentErr = workflowProviderSetupEnvironment(request.Provider, keys)
		if environmentErr != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": environmentErr.Error()})
			return
		}
	}
	if environment == nil && request.Provider == "claude-code" && (request.Action == "usage" || request.Action == "inspect") {
		environment = serverClaudeSetupEnvironment(MergedProviderAPIKeys(r.Context()))
	}
	// Which login a setup session touches: the server's own, or a private
	// account's (paths only, never credentials).
	target, home := "server account", "service HOME"
	if request.ConnectionID != "" && !strings.HasPrefix(request.ConnectionID, "global:") {
		target = "user account " + request.ConnectionID + " (owner " + accountOwner + ")"
		for _, entry := range environment {
			if value, ok := strings.CutPrefix(entry, "HOME="); ok {
				home = value
			}
		}
	}
	log.Printf("[PROVIDER_SETUP] %s %s for %s (HOME %s) by %s", request.Provider, request.Action, target, home, caller)
	if request.Action == "usage" && nativeProviderUsage(request.Provider) {
		nativeEnvironment := environment
		if nativeEnvironment == nil {
			nativeEnvironment = providerConnectionSetupEnvironment(providerConnectionEnvLabel(request.Provider, request.ConnectionID), MergedProviderAPIKeys(r.Context()), true)
		}
		if writeNativeProviderUsage(w, r, request.Provider, nativeEnvironment) {
			return
		}
	}
	// Someone who does not manage the account never gets an interactive
	// terminal: the server runs the usage command and returns its text.
	manages := admin || (request.ConnectionID != "" && !strings.HasPrefix(request.ConnectionID, "global:") && accountOwner == caller)
	if request.Provider == "claude-code" {
		seedClaudeTheme(setupHome(environment))
	}
	var modelArgs []string
	if request.Action == "usage" || request.Action == "inspect" {
		if allowed, _, allowErr := accountAllowedModels(r.Context(), request.Provider, request.ConnectionID); allowErr == nil {
			modelArgs = providerSetupModelArgs(request.Provider, allowed)
		}
	}
	session, err := api.providerSetupManager().startWithArgs(GetUserIDFromContext(r.Context()), request.Provider, request.Action, request.Cols, request.Rows, environment, cleanup, request.ReplaceRunning, modelArgs, request.ConnectionID)
	if err != nil {
		status := http.StatusBadRequest
		var conflict *providerSetupConflictError
		if errors.As(err, &conflict) {
			status = http.StatusConflict
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	if !manages {
		session.mu.Lock()
		session.readOnly = true
		session.mu.Unlock()
		go driveProviderUsage(r.Context(), session)
		text := collectProviderUsageOutput(session, providerUsageCollectTimeout)
		pending := providerUsageResultPending(session.provider, text)
		api.providerSetupManager().remove(session.id, true)
		session.mu.Lock()
		submitted := !session.usageSubmitted.IsZero()
		session.mu.Unlock()
		if !submitted {
			writeAgentProfileError(w, http.StatusBadGateway, "The provider usage check stopped during startup. Try Check usage again.")
			return
		}
		if pending {
			log.Printf("[PROVIDER_USAGE] session=%s provider=%s result_timeout output_bytes=%d", session.id, session.provider, len(text))
			writeAgentProfileError(w, http.StatusBadGateway, "The provider has not returned its usage details yet. Try Check usage again.")
			return
		}
		log.Printf("[PROVIDER_USAGE] session=%s provider=%s collected output_bytes=%d", session.id, session.provider, len(text))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"usage_output": text})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"session": session.snapshot()})
}

var (
	providerUsageCollectTimeout = 45 * time.Second
	providerUsageSettle         = 2 * time.Second
)

// collectProviderUsageOutput waits for a server-driven usage command to print
// its answer (output settles after the command was sent, or the CLI exits)
// and returns the text without terminal escapes.
func collectProviderUsageOutput(session *providerSetupSession, timeout time.Duration) string {
	result := func() string {
		session.mu.Lock()
		offset := session.usageOutputOffset
		session.mu.Unlock()
		raw := session.outputText()
		if offset > 0 && offset <= len(raw) {
			raw = raw[offset:]
		}
		return cleanProviderUsageText(raw)
	}
	startupDone := session.usageStartupDone
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-startupDone:
			session.mu.Lock()
			failed := session.usageStartupErr != nil
			session.mu.Unlock()
			if failed {
				return result()
			}
			startupDone = nil
		case <-session.done:
			return result()
		default:
		}
		session.mu.Lock()
		submitted, updated := session.usageSubmitted, session.updatedAt
		session.mu.Unlock()
		if !submitted.IsZero() && time.Since(submitted) > providerUsageSettle && time.Since(updated) > providerUsageSettle && !providerUsageResultPending(session.provider, result()) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return result()
}

// The CLI draws its loading notice before the network reply. Quiet output alone
// does not mean quota data is ready. Match text after the last loading redraw so
// the retained transcript's old notice cannot hide a later result or error.
func providerUsageResultPending(provider, output string) bool {
	if provider != "claude-code" {
		return false
	}
	compact := strings.ToLower(strings.Join(strings.Fields(output), ""))
	marker := "loadingusagedata"
	at := strings.LastIndex(compact, marker)
	if at < 0 {
		return false
	}
	tail := compact[at+len(marker):]
	for _, result := range []string{"currentsession", "currentweek", "remaining", "notavailable", "unavailable", "failed", "error", "login", "signinto", "used"} {
		if strings.Contains(tail, result) {
			return false
		}
	}
	return !providerUsagePercentage.MatchString(tail)
}

func cleanProviderUsageText(raw string) string {
	text := providerUsageScreenText(raw)
	text = strings.ReplaceAll(text, "\r", "")
	return strings.TrimSpace(text)
}

func (api *StreamingAPI) resolveProviderSetup(w http.ResponseWriter, r *http.Request) (*providerSetupSession, bool) {
	id := strings.TrimSpace(mux.Vars(r)["id"])
	session, ok := api.providerSetupManager().get(id)
	if !ok {
		http.Error(w, `{"error":"provider setup session not found"}`, http.StatusNotFound)
		return nil, false
	}
	if !session.isOwnedBy(GetUserIDFromContext(r.Context())) {
		http.Error(w, `{"error":"provider setup session not found"}`, http.StatusNotFound)
		return nil, false
	}
	return session, true
}

func (api *StreamingAPI) handleProviderSetupSession(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusOK)
		return
	}
	session, ok := api.resolveProviderSetup(w, r)
	if !ok {
		return
	}
	if r.Method == http.MethodDelete {
		api.providerSetupManager().remove(session.id, true)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"session": session.snapshot()})
}

type providerSetupClientMessage struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

func (api *StreamingAPI) handleProviderSetupStream(w http.ResponseWriter, r *http.Request) {
	if !api.checkLiveAttachOrigin(r) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	session, ok := api.resolveProviderSetup(w, r)
	if !ok {
		return
	}
	upgrader := api.liveAttachUpgrader()
	connection, err := upgrader.Upgrade(unwrapResponseWriter(w), r, nil)
	if err != nil {
		return
	}
	defer connection.Close()

	seed, output, unsubscribe := session.subscribe()
	defer unsubscribe()
	if len(seed) > 0 {
		if err := connection.WriteMessage(websocket.BinaryMessage, seed); err != nil {
			return
		}
	}

	clientClosed := make(chan struct{})
	inputErrors := make(chan string, 1)
	go func() {
		defer close(clientClosed)
		for {
			var message providerSetupClientMessage
			if err := connection.ReadJSON(&message); err != nil {
				return
			}
			switch message.Type {
			case "input":
				if len(message.Data) <= 16*1024 {
					if err := session.userInput(message.Data); err != nil {
						select {
						case inputErrors <- err.Error():
						default:
						}
					}
				}
			case "resize":
				_ = session.resize(message.Cols, message.Rows)
			}
		}
	}()

	for {
		select {
		case inputError := <-inputErrors:
			if err := connection.WriteJSON(map[string]string{"type": "input_error", "error": inputError}); err != nil {
				return
			}
		case chunk, open := <-output:
			if !open {
				_ = connection.WriteJSON(map[string]interface{}{"type": "exit", "session": session.snapshot()})
				return
			}
			if err := connection.WriteMessage(websocket.BinaryMessage, chunk); err != nil {
				return
			}
		case <-clientClosed:
			return
		}
	}
}

// providerSetupAccount finds the user account a setup session targets and
// checks the caller may open it for request.Action.
func (api *StreamingAPI) providerSetupAccount(ctx context.Context, request startProviderSetupRequest, caller string, admin bool) (*storedProviderConnection, error) {
	providerConnectionsMu.Lock()
	records, err := loadProviderConnections(ctx)
	providerConnectionsMu.Unlock()
	if err != nil {
		return nil, err
	}
	for i := range records {
		record := records[i]
		if record.ID != request.ConnectionID {
			continue
		}
		if record.Provider != request.Provider || !providerEnabled(record.Provider) || personalProviderConnectionsLocked(record.Provider) {
			return nil, fmt.Errorf("connection unavailable")
		}
		if record.OwnerUserID == caller || admin {
			return &record, nil
		}
		if request.Action == "usage" {
			if _, err := api.admitProviderAccount(ctx, providerAccountScope{Principal: caller, WorkspacePath: request.WorkspacePath}, record.Provider, record.ID); err == nil {
				return &record, nil
			}
		}
		return nil, fmt.Errorf("connection unavailable")
	}
	return nil, fmt.Errorf("connection unavailable")
}

// serverAccountAvailableToCaller reports whether the server account of
// provider is available to this person, independently of the product.
func (api *StreamingAPI) serverAccountAvailableToCaller(ctx context.Context, caller, provider string) bool {
	availability, err := effectiveServerAccountAvailability(ctx, provider)
	if err != nil {
		return false
	}
	return availability.AvailableTo.admits(caller)
}

// setupHome is the HOME the setup CLI runs with: the account's own, else the service's.
func setupHome(environment []string) string {
	for _, entry := range environment {
		if value, ok := strings.CutPrefix(entry, "HOME="); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	home, _ := os.UserHomeDir()
	return home
}

// seedClaudeTheme gives Claude a theme before the account terminal opens, so its first-run
// "choose a theme" picker never takes over a sign-in or usage check (the chat adapter does the
// same for chats). A theme someone already chose is kept; nothing else in the file changes.
func seedClaudeTheme(home string) {
	if strings.TrimSpace(home) == "" {
		return
	}
	path := filepath.Join(home, ".claude.json")
	config := map[string]interface{}{}
	if raw, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(raw, &config) != nil {
			return // never rewrite a file we cannot read
		}
	} else if !os.IsNotExist(err) {
		return
	}
	if theme, _ := config["theme"].(string); strings.TrimSpace(theme) != "" {
		return
	}
	config["theme"] = "dark"
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(home, 0o700)
	_ = os.WriteFile(path, data, 0o600)
}

// providerSetupIsPersonalBinding says whether a Providers-screen terminal belongs to a personal account (its own private
// HOME, confined). The server's own account (binding "global:<provider>", managed by an admin, "Admin-managed account"
// in the UI) is not one: it has no private home to confine to, and starting it under the Landlock launcher with an empty
// working folder failed every Cursor/Claude/Codex/Muse terminal on a host that can confine (server A 2026-10-03).
func providerSetupIsPersonalBinding(provider, bindingID string) bool {
	return bindingID != provider && !strings.HasPrefix(bindingID, "global:")
}
