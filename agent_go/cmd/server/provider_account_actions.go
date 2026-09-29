package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/mux"
)

// Per-account actions (docs/design/provider_accounts.md, "Implementation
// notes"): a non-interactive status check and sign-out, for the server
// account and every user account alike. Both run the CLI's own command with
// that account's environment (its own HOME for a user account, the service
// HOME for the server account). No credential value is ever returned.

// providerStatusCommands are each CLI's own non-interactive status command,
// verified with --help on 2026-09-28:
//   - claude auth status --json   (loggedIn, email, orgName; it reports
//     loggedIn for ANY non-empty token, so a real check follows on verify)
//   - codex login status          (exit 0 and "Logged in using ...")
//   - cursor-agent status --format json (isAuthenticated, userInfo.email)
//
// Muse has no status command; its login is read from auth.json (see
// museAccountStatus).
var providerStatusCommands = map[string]providerSetupCommand{
	"claude-code": {command: "claude", args: []string{"auth", "status", "--json"}},
	"codex-cli":   {command: "codex", args: []string{"login", "status"}},
	"cursor-cli":  {command: "cursor-agent", args: []string{"status", "--format", "json"}},
}

// claudeVerifyCommand is the one real round trip that proves a Claude login
// works (the cheapest model, one turn, no MCP servers).
var claudeVerifyCommand = providerSetupCommand{command: "claude", args: []string{"-p", "hi", "--model", "claude-haiku-4-5", "--max-turns", "1", "--strict-mcp-config", "--permission-mode", "dontAsk"}}

// providerLogoutCommands are each CLI's own logout, verified with --help on
// 2026-09-28: `claude auth logout`, `codex logout`, `cursor-agent logout`,
// `muse logout`. Every supported browser-login CLI has one, so no login
// files are removed by hand.
var providerLogoutCommands = map[string]providerSetupCommand{
	"claude-code": {command: "claude", args: []string{"auth", "logout"}},
	"codex-cli":   {command: "codex", args: []string{"logout"}},
	"cursor-cli":  {command: "cursor-agent", args: []string{"logout"}},
	"muse-cli":    {command: "muse", args: []string{"logout"}},
}

var (
	providerStatusTimeout = 20 * time.Second
	providerVerifyTimeout = 90 * time.Second
	providerLogoutTimeout = 30 * time.Second
)

// providerAccountStatus is what a status check may return: a state, and the
// account identity when the CLI reports one. Never a credential.
type providerAccountStatus struct {
	// State is signed_in, signed_out, key_rejected or unknown.
	State     string    `json:"state"`
	Identity  string    `json:"identity,omitempty"`
	Detail    string    `json:"detail,omitempty"`
	Verified  bool      `json:"verified"`
	CheckedAt time.Time `json:"checked_at"`
}

// providerAccountTarget is the environment an action on one account runs
// with.
type providerAccountTarget struct {
	ID         string
	Provider   string
	Server     bool
	Record     *storedProviderConnection
	Env        []string
	Home       string
	OwnerLabel string
}

func (t providerAccountTarget) label() string {
	if t.Server {
		return "server account"
	}
	return "private account " + t.ID
}

// resolveProviderAccountTarget finds account id and builds the environment
// its actions run with: a user account's own HOME, or the service HOME with
// the server's keys for the server account.
func (api *StreamingAPI) resolveProviderAccountTarget(ctx context.Context, id, provider string) (providerAccountTarget, error) {
	if strings.HasPrefix(id, "global:") {
		provider = strings.TrimPrefix(id, "global:")
		if !providerEnabled(provider) {
			return providerAccountTarget{}, fmt.Errorf("provider is not enabled")
		}
		home, _ := os.UserHomeDir()
		return providerAccountTarget{ID: id, Provider: provider, Server: true, Env: providerConnectionSetupEnvironment(MergedProviderAPIKeys(ctx)), Home: home}, nil
	}
	providerConnectionsMu.Lock()
	records, err := loadProviderConnections(ctx)
	providerConnectionsMu.Unlock()
	if err != nil {
		return providerAccountTarget{}, err
	}
	for i := range records {
		if records[i].ID != id {
			continue
		}
		record := records[i]
		if !providerEnabled(record.Provider) {
			return providerAccountTarget{}, fmt.Errorf("provider is not enabled")
		}
		keys, err := providerConnectionRuntimeKeys(record)
		if err != nil {
			return providerAccountTarget{}, err
		}
		return providerAccountTarget{ID: id, Provider: record.Provider, Record: &record, Env: providerConnectionSetupEnvironment(keys), Home: keys.RuntimeEnvironment["HOME"]}, nil
	}
	return providerAccountTarget{}, fmt.Errorf("connection unavailable")
}

// providerAccountManagedBy reports whether caller manages the account: the
// owner or an admin for a user account, an admin for the server account.
func providerAccountManagedBy(target providerAccountTarget, caller string, admin bool) bool {
	if target.Server {
		return admin
	}
	return admin || (target.Record != nil && target.Record.OwnerUserID == caller)
}

// runProviderAccountCommand runs one CLI command with the account's
// environment in a throwaway directory and returns its output.
func runProviderAccountCommand(ctx context.Context, target providerAccountTarget, spec providerSetupCommand, timeout time.Duration) (string, error) {
	dir, err := os.MkdirTemp("", "agentworks-provider-action-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(runCtx, spec.command, spec.args...)
	command.Env = target.Env
	command.Dir = dir
	output, err := command.CombinedOutput()
	if len(output) > 64*1024 {
		output = output[:64*1024]
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return string(output), fmt.Errorf("timed out")
	}
	return string(output), err
}

var providerSecretLike = regexp.MustCompile(`(?i)(sk-[a-z0-9_\-]{6,}|[a-z0-9_\-]{32,})`)

// safeProviderText keeps one short line of CLI text with anything that looks
// like a key or token removed.
func safeProviderText(raw string) string {
	text := cleanProviderUsageText(raw)
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			line = providerSecretLike.ReplaceAllString(line, "[hidden]")
			if len(line) > 200 {
				line = line[:200]
			}
			return line
		}
	}
	return ""
}

func safeIdentity(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || providerSecretLike.MatchString(value) || len(value) > 120 {
		return ""
	}
	return value
}

func envValue(env []string, name string) string {
	for i := len(env) - 1; i >= 0; i-- {
		if key, value, ok := strings.Cut(env[i], "="); ok && key == name {
			return value
		}
	}
	return ""
}

// checkProviderAccountStatus runs the account's status check. verify adds the
// real round trip for Claude Code.
func checkProviderAccountStatus(ctx context.Context, target providerAccountTarget, verify bool) providerAccountStatus {
	status := providerAccountStatus{State: "unknown", CheckedAt: time.Now().UTC()}
	switch target.Provider {
	case "claude-code":
		output, err := runProviderAccountCommand(ctx, target, providerStatusCommands["claude-code"], providerStatusTimeout)
		var parsed struct {
			LoggedIn bool   `json:"loggedIn"`
			Email    string `json:"email"`
			OrgName  string `json:"orgName"`
		}
		if jsonErr := json.Unmarshal([]byte(strings.TrimSpace(output)), &parsed); jsonErr != nil {
			status.Detail = statusError(err, output)
			return status
		}
		if !parsed.LoggedIn {
			status.State = "signed_out"
			return status
		}
		status.State, status.Identity = "signed_in", safeIdentity(firstNonEmptyTrimmed(parsed.Email, parsed.OrgName))
		if verify {
			status.Verified = true
			if verifyOutput, verifyErr := runProviderAccountCommand(ctx, target, claudeVerifyCommand, providerVerifyTimeout); verifyErr != nil {
				status.State, status.Detail = "key_rejected", safeProviderText(verifyOutput)
			}
		}
	case "codex-cli":
		output, err := runProviderAccountCommand(ctx, target, providerStatusCommands["codex-cli"], providerStatusTimeout)
		line := safeProviderText(output)
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				status.State, status.Detail = "signed_out", line
			} else {
				status.Detail = statusError(err, output)
			}
			return status
		}
		status.State = "signed_in"
		// "Logged in using ChatGPT" / "Logged in using an API key - sk-...":
		// keep the method, never what follows the dash.
		method, _, _ := strings.Cut(strings.TrimPrefix(line, "Logged in using "), " - ")
		status.Identity = safeIdentity(method)
	case "cursor-cli":
		output, err := runProviderAccountCommand(ctx, target, providerStatusCommands["cursor-cli"], providerStatusTimeout)
		var parsed struct {
			IsAuthenticated bool `json:"isAuthenticated"`
			UserInfo        struct {
				Email string `json:"email"`
			} `json:"userInfo"`
		}
		if jsonErr := json.Unmarshal([]byte(strings.TrimSpace(output)), &parsed); jsonErr != nil {
			status.Detail = statusError(err, output)
			return status
		}
		switch {
		case parsed.IsAuthenticated:
			status.State, status.Identity = "signed_in", safeIdentity(parsed.UserInfo.Email)
		case envValue(target.Env, "CURSOR_API_KEY") != "":
			status.State = "key_rejected"
		default:
			status.State = "signed_out"
		}
	case "muse-cli":
		status.State, status.Identity = museAccountStatus(target)
	default:
		status.Detail = "This CLI has no status check."
	}
	return status
}

func statusError(err error, output string) string {
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return "The CLI is not installed on the server."
	}
	if detail := safeProviderText(output); detail != "" {
		return detail
	}
	if err != nil {
		return "The status check failed."
	}
	return ""
}

// museAccountStatus reads Muse's own login record. Muse has no status
// command; `muse login` stores the Meta login in
// $XDG_CONFIG_HOME/muse/auth.json (else $HOME/.config/muse/auth.json)
// under providers.meta. Only user_email is read back.
func museAccountStatus(target providerAccountTarget) (state, identity string) {
	if envValue(target.Env, "META_API_KEY") != "" {
		return "signed_in", "API key"
	}
	configHome := envValue(target.Env, "XDG_CONFIG_HOME")
	if configHome == "" {
		home := envValue(target.Env, "HOME")
		if home == "" {
			home = target.Home
		}
		configHome = filepath.Join(home, ".config")
	}
	raw, err := os.ReadFile(filepath.Join(configHome, "muse", "auth.json"))
	if err != nil {
		return "signed_out", ""
	}
	var parsed struct {
		Providers map[string]struct {
			UserEmail string `json:"user_email"`
		} `json:"providers"`
	}
	if json.Unmarshal(raw, &parsed) != nil {
		return "unknown", ""
	}
	meta, ok := parsed.Providers["meta"]
	if !ok {
		return "signed_out", ""
	}
	return "signed_in", safeIdentity(meta.UserEmail)
}

// GET /api/provider-connections/{id}/status[?verify=1&workspace_path=] —
// anyone who may see and use the account, or who manages it.
func (api *StreamingAPI) handleProviderAccountStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	caller, ok := requireSignedIn(w, r)
	if !ok {
		return
	}
	target, err := api.resolveProviderAccountTarget(r.Context(), mux.Vars(r)["connectionID"], "")
	if err != nil {
		http.Error(w, "connection unavailable", http.StatusNotFound)
		return
	}
	manager := providerAccountManagedBy(target, caller, currentUserIsAdmin(r))
	if !manager {
		allowed := false
		if target.Server {
			allowed = api.serverAccountAvailableToCaller(r.Context(), caller, target.Provider)
		} else if _, admitErr := api.admitProviderAccount(r.Context(), providerAccountScope{Principal: caller, WorkspacePath: r.URL.Query().Get("workspace_path")}, target.Provider, target.ID); admitErr == nil {
			allowed = true
		}
		if !allowed {
			http.Error(w, "connection unavailable", http.StatusNotFound)
			return
		}
	}
	// The real check sends one model request on the account: only its
	// managers may spend that; everyone else gets the free status command.
	verify := r.URL.Query().Get("verify") == "1" && manager
	status := checkProviderAccountStatus(r.Context(), target, verify)
	rememberProviderAccountStatus(target.ID, status)
	_ = json.NewEncoder(w).Encode(status)
}

// POST /api/provider-connections/{id}/sign-out — runs the CLI's own logout in
// the account's environment. User browser-login accounts: owner or admin.
// The server account: admins only. The account record stays.
func (api *StreamingAPI) handleProviderAccountSignOut(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	caller, ok := requireSignedIn(w, r)
	if !ok {
		return
	}
	target, err := api.resolveProviderAccountTarget(r.Context(), mux.Vars(r)["connectionID"], "")
	if err != nil {
		http.Error(w, "connection unavailable", http.StatusNotFound)
		return
	}
	if !providerAccountManagedBy(target, caller, currentUserIsAdmin(r)) {
		if target.Server {
			writeWorkflowPermissionDenied(w, "admin")
		} else {
			http.Error(w, "connection unavailable", http.StatusNotFound)
		}
		return
	}
	if target.Record != nil && target.Record.AuthMethod != "cli_login" {
		http.Error(w, "this account uses a key, not a browser login; remove it or change its key instead", http.StatusBadRequest)
		return
	}
	spec, ok := providerLogoutCommands[target.Provider]
	if !ok {
		http.Error(w, "this CLI has no sign-out", http.StatusBadRequest)
		return
	}
	// Stop the CLIs running on this account before its login goes away.
	closed := api.closeSessionsOnProviderAccount(target.ID)
	log.Printf("[PROVIDER_SETUP] %s sign-out for %s by %s (stopped %d session(s))", target.Provider, target.label(), caller, closed)
	output, runErr := runProviderAccountCommand(r.Context(), target, spec, providerLogoutTimeout)
	if runErr != nil {
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "sign-out failed: " + firstNonEmptyTrimmed(safeProviderText(output), runErr.Error())})
		return
	}
	status := checkProviderAccountStatus(r.Context(), target, false)
	rememberProviderAccountStatus(target.ID, status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{"signed_out": true, "status": status})
}
