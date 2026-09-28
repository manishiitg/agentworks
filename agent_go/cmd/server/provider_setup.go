package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
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
	command string
	args    []string
}

// Provider setup never accepts a command from the browser. Each supported
// action maps to one reviewed argv so this feature cannot become a general
// remote shell.
var providerSetupCommands = map[string]map[string]providerSetupCommand{
	"claude-code": {
		"authenticate": {command: "claude", args: []string{"auth", "login"}},
		"inspect":      {command: "claude", args: []string{"--tools", ""}},
		"usage":        {command: "claude", args: []string{"--tools", ""}},
	},
	"codex-cli": {
		// --device-auth prints a URL + code to authenticate from any device,
		// rather than starting a local callback server on localhost:1455 and
		// waiting for a browser redirect back to this same machine -- this
		// process almost always runs on a remote/headless server with no
		// local browser of its own, where the plain flow can never complete.
		"authenticate": {command: "codex", args: []string{"login", "--device-auth"}},
		"inspect":      {command: "codex", args: []string{"--sandbox", "read-only", "--ask-for-approval", "never"}},
		"usage":        {command: "codex", args: []string{"--sandbox", "read-only", "--ask-for-approval", "never"}},
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

var providerSetupANSI = regexp.MustCompile(`\x1b\[[0-9;:?>]*[ -/]*[@-~]|\x1b.`)

var providerUsageCommands = map[string]string{
	"claude-code": "/usage",
	"codex-cli":   "/status",
	"muse-cli":    "/usage",
}

type providerSetupSnapshot struct {
	ID        string    `json:"id"`
	Provider  string    `json:"provider"`
	Action    string    `json:"action"`
	Status    string    `json:"status"`
	ExitCode  *int      `json:"exit_code,omitempty"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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
}

func (s *providerSetupSession) snapshot() providerSetupSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return providerSetupSnapshot{
		ID:        s.id,
		Provider:  s.provider,
		Action:    s.action,
		Status:    s.status,
		ExitCode:  s.exitCode,
		Error:     s.errMessage,
		CreatedAt: s.createdAt,
		UpdatedAt: s.updatedAt,
	}
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
	for subscriber := range s.subscribers {
		select {
		case subscriber <- copyChunk:
		default:
			// A reconnect receives the bounded transcript seed. Never let a slow
			// browser block the provider command or its authentication prompts.
		}
	}
	s.mu.Unlock()
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
	bindingID := provider
	if len(connectionIDs) > 0 && connectionIDs[0] != "" {
		bindingID = connectionIDs[0]
	}
	if cleanup == nil {
		cleanup = func() {}
	}
	actions, ok := providerSetupCommands[provider]
	if !ok {
		cleanup()
		return nil, fmt.Errorf("guided setup is not available for provider %q", provider)
	}
	spec, ok := actions[action]
	if !ok {
		cleanup()
		return nil, fmt.Errorf("guided %s is not available for provider %q", action, provider)
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
	command := exec.CommandContext(ctx, spec.command, spec.args...)
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
		environment = os.Environ()
	}
	command.Env = append(environment, "TERM=xterm-256color", "COLORTERM=truecolor")
	cols, rows, validSize := clampLiveAttachGeometry(cols, rows)
	if !validSize {
		cols, rows = liveAttachDefaultCols, liveAttachDefaultRows
	}
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
	m.mu.Lock()
	m.sessions[id] = session
	m.mu.Unlock()
	if action == "usage" {
		go driveProviderUsage(ctx, session)
	}

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

// driveProviderUsage turns the reviewed usage action into one click. It only
// writes a fixed provider command and, in the throwaway usage directory, the
// minimum keys needed to accept a workspace-trust prompt.
func driveProviderUsage(ctx context.Context, session *providerSetupSession) {
	usageCommand := providerUsageCommands[session.provider]
	if usageCommand == "" {
		return
	}
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	baseline := 0
	trustHandled := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-deadline.C:
			return
		case <-ticker.C:
			output := providerSetupANSI.ReplaceAllString(session.outputText(), "")
			if !trustHandled && providerUsageTrustPrompt(session.provider, output) {
				if !acceptProviderUsageTrust(session, session.provider, output) {
					return
				}
				trustHandled = true
				baseline = len(output)
				continue
			}
			visible := output
			if baseline >= len(output) && baseline > 0 {
				continue
			}
			if baseline > 0 {
				visible = output[baseline:]
			}
			if providerUsagePromptReady(session.provider, visible) {
				_ = session.write(usageCommand + "\r")
				return
			}
		}
	}
}

func providerUsageTrustPrompt(provider, output string) bool {
	lower := strings.ToLower(output)
	switch provider {
	case "claude-code":
		return strings.Contains(lower, "yes, i trust this folder") || strings.Contains(lower, "yes - i trust this project")
	case "codex-cli":
		return strings.Contains(lower, "do you trust the contents of this directory") && strings.Contains(lower, "press enter to continue")
	case "muse-cli":
		return strings.Contains(lower, "do you trust this workspace") || strings.Contains(lower, "do you trust the files in this folder")
	default:
		return false
	}
}

func acceptProviderUsageTrust(session *providerSetupSession, provider, output string) bool {
	lower := strings.ToLower(output)
	switch provider {
	case "claude-code":
		if strings.Contains(lower, "❯ no, exit") || strings.Contains(lower, "❯ 1. no, exit") {
			if session.write("\x1b[B") != nil {
				return false
			}
			time.Sleep(250 * time.Millisecond)
		}
		return session.write("\r") == nil
	case "codex-cli":
		if strings.Contains(lower, "› 2. no, quit") {
			if session.write("\x1b[A") != nil {
				return false
			}
			time.Sleep(250 * time.Millisecond)
		}
		return session.write("\r") == nil
	case "muse-cli":
		return session.write("1\r") == nil
	default:
		return false
	}
}

func providerUsagePromptReady(provider, output string) bool {
	lines := strings.Split(strings.ReplaceAll(output, "\r", ""), "\n")
	start := len(lines) - 24
	if start < 0 {
		start = 0
	}
	for _, line := range lines[start:] {
		trimmed := strings.TrimSpace(line)
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
		return claudeauth.CheckEnv(os.Environ(), configDir, strings.TrimSpace(*keys.ClaudeCodeOAuthToken)), cleanup, nil
	case string(llm.ProviderCursorCLI):
		if keys.CursorCLI == nil || strings.TrimSpace(*keys.CursorCLI) == "" {
			return nil, nil, errors.New("no Cursor API key is configured for this workflow")
		}
		environment := cursorauth.CheckEnv(os.Environ())
		environment = append(environment, "CURSOR_API_KEY="+strings.TrimSpace(*keys.CursorCLI))
		return environment, func() {}, nil
	default:
		return nil, nil, fmt.Errorf("workflow-scoped terminals are not available for provider %q", provider)
	}
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
	var environment []string
	var cleanup func()
	if request.ConnectionID != "" && !strings.HasPrefix(request.ConnectionID, "global:") {
		keys, err := api.connectionAPIKeys(r.Context(), GetUserIDFromContext(r.Context()), request.Provider, request.ConnectionID)
		if err != nil {
			http.Error(w, "connection unavailable or unauthorized", 403)
			return
		}
		environment = providerConnectionSetupEnvironment(keys)
	} else if !currentUserIsAdmin(r) {
		writeWorkflowPermissionDenied(w, "admin")
		return
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
	session, err := api.providerSetupManager().start(GetUserIDFromContext(r.Context()), request.Provider, request.Action, request.Cols, request.Rows, environment, cleanup, request.ReplaceRunning, request.ConnectionID)
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"session": session.snapshot()})
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
					_ = session.write(message.Data)
				}
			case "resize":
				_ = session.resize(message.Cols, message.Rows)
			}
		}
	}()

	for {
		select {
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
