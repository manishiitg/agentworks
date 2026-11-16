package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// GitHub #2820: one-click Connect Claude Code / Codex in the Mac app. The Mac's own person runs the existing
// `agentworks setup <provider> --hooks` (the same command, run as a subprocess, never a copy of its steps), so native
// chats get the status line, live delivery and the Team join tools. Only this Mac's own local app may run it.

type codingAgentApp struct {
	Provider  string `json:"provider"`
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Hooks     bool   `json:"hooks"`
}

type codingAgentSetupResult struct {
	Provider string   `json:"provider"`
	Name     string   `json:"name"`
	OK       bool     `json:"ok"`
	Lines    []string `json:"lines"`
	Next     string   `json:"next"`
}

var codingAgentSetupANSI = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

const codingAgentCLIMissing = "The agentworks command is not installed on this Mac. Update the AgentWorks app (Settings > Updates), then try again."

func codingAgentSetupRun(ctx context.Context, args ...string) (string, error) {
	bin, err := exec.LookPath("agentworks")
	if err != nil {
		return "", errors.New(codingAgentCLIMissing)
	}
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdin = strings.NewReader("") // trusting hooks stays the person's choice inside Claude or Codex
	cmd.Stdout, cmd.Stderr = &out, &out
	err = cmd.Run()
	return codingAgentSetupANSI.ReplaceAllString(out.String(), ""), err
}

func codingAgentSetupStatus(ctx context.Context) ([]codingAgentApp, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	raw, err := codingAgentSetupRun(ctx, "setup", "status")
	if err != nil {
		if err.Error() == codingAgentCLIMissing {
			return nil, err
		}
		return nil, errors.New("Could not check Claude Code and Codex on this Mac. Update the AgentWorks app (Settings > Updates), then try again.")
	}
	var out struct {
		Apps []codingAgentApp `json:"apps"`
	}
	if err = json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, errors.New("Could not read the Claude Code and Codex check on this Mac. Update the AgentWorks app, then try again.")
	}
	return out.Apps, nil
}

func connectCodingAgent(ctx context.Context, provider string) (codingAgentSetupResult, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	apps, err := codingAgentSetupStatus(ctx)
	if err != nil {
		log.Printf("[AGENT_SETUP] provider=%s hooks=true outcome=check_failed", provider)
		return codingAgentSetupResult{}, err
	}
	var app *codingAgentApp
	for i := range apps {
		if apps[i].Provider == provider {
			app = &apps[i]
		}
	}
	if app == nil {
		return codingAgentSetupResult{}, errors.New("Choose Claude Code or Codex.")
	}
	if !app.Installed {
		log.Printf("[AGENT_SETUP] provider=%s hooks=true outcome=not_installed", provider)
		return codingAgentSetupResult{}, fmt.Errorf("%s is not installed on this Mac. Install it, open it once, then click Connect again.", app.Name)
	}
	runCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	raw, err := codingAgentSetupRun(runCtx, "setup", provider, "--hooks")
	var lines []string
	for _, line := range strings.Split(raw, "\n") {
		if line = strings.TrimSpace(line); line != "" && !strings.HasPrefix(line, "Next:") {
			lines = append(lines, line)
		}
	}
	if err != nil {
		log.Printf("[AGENT_SETUP] provider=%s hooks=true outcome=failed", provider)
		reason := "Setup did not finish."
		if len(lines) > 0 {
			reason = lines[len(lines)-1]
		}
		return codingAgentSetupResult{}, fmt.Errorf("%s was not connected: %s Open %s once, then click Connect again.", app.Name, strings.TrimSuffix(reason, "."), app.Name)
	}
	log.Printf("[AGENT_SETUP] provider=%s hooks=true outcome=connected already=%t", provider, app.Hooks)
	next := "Restart " + app.Name + " to finish, then ask a chat to join your Team."
	if provider == "codex" {
		next = "Restart Codex to finish. In Codex Settings > Hooks, trust the AgentWorks hooks, then ask a chat to join your Team."
	}
	return codingAgentSetupResult{Provider: provider, Name: app.Name, OK: true, Lines: lines, Next: next}, nil
}

// handleCodingAgentSetup: GET lists Claude Code and Codex on this Mac; POST {"provider"} connects one.
func (api *StreamingAPI) handleCodingAgentSetup(w http.ResponseWriter, r *http.Request) {
	if !computerUseAllowed(GetUserFromContext(r.Context())) {
		externalError(w, http.StatusForbidden, "local_computer_required", "Connect Claude Code or Codex from the AgentWorks app on your Mac.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodGet {
		apps, err := codingAgentSetupStatus(r.Context())
		if err != nil {
			externalError(w, http.StatusServiceUnavailable, "setup_unavailable", err.Error())
			return
		}
		externalJSON(w, map[string]any{"apps": apps})
		return
	}
	var in struct {
		Provider string `json:"provider"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		externalError(w, http.StatusBadRequest, "invalid_request", "Choose Claude Code or Codex.")
		return
	}
	out, err := connectCodingAgent(r.Context(), in.Provider)
	if err != nil {
		externalError(w, http.StatusUnprocessableEntity, "setup_failed", err.Error())
		return
	}
	externalJSON(w, out)
}

// registerCodingAgentSetupTool gives the Mac owner's own app chat the same action as the Connect button.
func (api *StreamingAPI) registerCodingAgentSetupTool(registrar definitionToolRegistrar, gate *productToolGate, c *UserClaims, readOnly bool, session, root string) error {
	if readOnly || !computerUseAllowed(c) || !websiteDeviceClaims(c) || !crewProjectOwnedByCaller(c.UserID, root) {
		return nil
	}
	gate.Declare("connect_coding_agent")
	return registrar.RegisterCustomToolWithTimeout("connect_coding_agent",
		"Connect Claude Code or Codex on this Mac to AgentWorks, the same as the Connect button in Settings > AI accounts. action=status lists which are installed and already connected; action=connect with provider=claude|codex installs the AgentWorks plugin with hooks (status line, live messages, Team join). Running it again is safe. Use it when the person asks; afterwards tell them to restart that app.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action":   map[string]any{"type": "string", "enum": []string{"status", "connect"}},
				"provider": map[string]any{"type": "string", "enum": []string{"claude", "codex"}},
			},
			"required": []string{"action"},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			if _, err := api.interactiveLaptopOwner(ctx, c, session, root); err != nil {
				return "", err
			}
			var out any
			var err error
			if externalArg(args, "action") == "connect" {
				out, err = connectCodingAgent(ctx, externalArg(args, "provider"))
			} else {
				var apps []codingAgentApp
				apps, err = codingAgentSetupStatus(ctx)
				out = map[string]any{"apps": apps}
			}
			if err != nil {
				return "", err
			}
			data, err := json.Marshal(out)
			return string(data), err
		}, 4*time.Minute, "workspace_advanced")
}
