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
	},
	"codex-cli": {
		// --device-auth prints a URL + code to authenticate from any device,
		// rather than starting a local callback server on localhost:1455 and
		// waiting for a browser redirect back to this same machine -- this
		// process almost always runs on a remote/headless server with no
		// local browser of its own, where the plain flow can never complete.
		"authenticate": {command: "codex", args: []string{"login", "--device-auth"}},
		// --no-daemon: Codex 0.166 starts a shared background server that the setup sandbox cannot track
		// ("failed to record pid-managed app-server process"); this terminal needs none.
		"inspect": {command: "codex", args: []string{"--no-daemon", "--sandbox", "read-only", "--ask-for-approval", "never"}},
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
	},
	"agy-cli": {
		// agy has no login subcommand: Google sign-in completes inside the
		// TUI, so authenticate opens the bare CLI like pi.
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

func (s *providerSetupSession) userInput(data string) error {
	if data == "" {
		return nil
	}
	return s.write(data)
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

// providerSetupModelArgs is the model flag for an inspect terminal: the account's first allowed model, so the CLI does not
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
		// A user account: its owner and admins sign it in and inspect it.
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
	if environment == nil && request.Provider == "claude-code" && request.Action == "inspect" {
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
	if request.Provider == "claude-code" {
		seedClaudeTheme(setupHome(environment))
	}
	var modelArgs []string
	if request.Action == "inspect" {
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
