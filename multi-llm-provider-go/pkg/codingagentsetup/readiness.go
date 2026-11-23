package codingagentsetup

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type ProviderReadiness struct {
	Provider        string `json:"provider"`
	DisplayName     string `json:"display_name"`
	CLI             string `json:"cli"`
	Installed       bool   `json:"installed"`
	Authenticated   bool   `json:"authenticated"`
	Ready           bool   `json:"ready"`
	Status          string `json:"status"`
	ModelsAvailable int    `json:"models_available,omitempty"`
	CheckCommand    string `json:"check_command"`
	LoginCommand    string `json:"login_command"`
	Detail          string `json:"detail,omitempty"`
}

type piModel struct {
	Provider string
	Model    string
}

func (model piModel) selector() string {
	return model.Provider + "/" + model.Model
}

func (e *Environment) ProviderReadiness(ctx context.Context, providers []string) []ProviderReadiness {
	result := make([]ProviderReadiness, 0, len(providers))
	for _, provider := range providers {
		readiness := e.probeProvider(ctx, provider)
		result = append(result, readiness)
	}
	return result
}

func (e *Environment) checkTargetReadiness(ctx context.Context, providers []string, interactive bool) {
	fmt.Fprintln(e.out)
	fmt.Fprintln(e.out, e.heading("Target authentication"))
	fmt.Fprintln(e.out, "Each selected target must be installed and authenticated before it can receive delegated work.")
	fmt.Fprintln(e.out, e.muted("Authentication stays inside the provider's native CLI; llm-provider never reads or stores credentials."))

	readiness := e.ProviderReadiness(ctx, providers)
	for index := range readiness {
		item := &readiness[index]
		e.printProviderReadiness(*item)
		if item.Ready || !item.Installed || !interactive {
			continue
		}
		confirmed, err := e.confirm(fmt.Sprintf("Open %s authentication now?", item.DisplayName), false)
		if err != nil {
			e.warn("could not read %s authentication choice: %v", item.DisplayName, err)
			continue
		}
		if !confirmed {
			fmt.Fprintf(e.out, "Skipped %s authentication. Later, run %s.\n", item.DisplayName, item.LoginCommand)
			continue
		}
		if err := e.runProviderLogin(ctx, item.Provider); err != nil {
			e.warn("%s authentication exited with an error: %v", item.DisplayName, err)
			continue
		}
		*item = e.probeProvider(ctx, item.Provider)
		if item.Ready {
			fmt.Fprintln(e.out, e.success(item.DisplayName+" authentication is ready."))
		} else {
			e.warn("%s is still not authenticated; run %s before delegating", item.DisplayName, item.LoginCommand)
		}
	}

	if !interactive || readyCount(readiness) == 0 {
		return
	}
	fmt.Fprintln(e.out)
	fmt.Fprintln(e.out, e.heading("Optional live verification"))
	fmt.Fprintf(e.out, "%d selected targets are ready. A live test sends one small read-only prompt to each ready target.\n", readyCount(readiness))
	fmt.Fprintln(e.out, e.muted("Live tests may consume provider credits and are never run without confirmation."))
	confirmed, err := e.confirm("Run live connectivity tests now?", false)
	if err != nil {
		e.warn("could not read live-test choice: %v", err)
		return
	}
	if !confirmed {
		fmt.Fprintln(e.out, "Skipped live tests. Authentication checks completed.")
		return
	}
	for _, item := range readiness {
		if item.Ready {
			e.runProviderLiveTest(ctx, item.Provider)
		}
	}
}

func (e *Environment) probeProvider(parent context.Context, provider string) ProviderReadiness {
	agent, known := findAgent(provider)
	result := ProviderReadiness{Provider: provider, Status: "unsupported"}
	if !known {
		result.Detail = "Unsupported coding-agent provider"
		return result
	}
	result.DisplayName = agent.DisplayName
	result.CLI = agent.CLI
	result.CheckCommand, result.LoginCommand = readinessCommands(provider)
	if !e.installed(agent) {
		result.Status = "missing"
		result.Detail = agent.CLI + " is not installed"
		return result
	}
	result.Installed = true

	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	var output []byte
	var err error
	switch provider {
	case "cursor-cli":
		output, err = e.runner.Run(ctx, "cursor-agent", "status", "--format", "json")
		result.Authenticated = cursorAuthenticated(output)
	case "codex-cli":
		output, err = e.runner.Run(ctx, "codex", "login", "status")
		result.Authenticated = codexAuthenticated(output, err)
	case "claude-code":
		output, err = e.runner.Run(ctx, "claude", "auth", "status", "--json")
		result.Authenticated = claudeAuthenticated(output)
	case "pi-cli":
		output, err = e.runner.Run(ctx, "pi", "--list-models")
		models := parsePiModels(string(output))
		result.ModelsAvailable = len(models)
		result.Authenticated = len(models) > 0
	}
	if err != nil && result.Authenticated {
		result.Authenticated = false
	}
	result.Ready = result.Installed && result.Authenticated
	if result.Ready {
		result.Status = "ready"
		if result.ModelsAvailable > 0 {
			result.Detail = fmt.Sprintf("%d models available", result.ModelsAvailable)
		}
		return result
	}
	result.Status = "not_authenticated"
	result.Detail = compactStatusDetail(string(output), err)
	return result
}

func readinessCommands(provider string) (check, login string) {
	switch provider {
	case "cursor-cli":
		return "cursor-agent status --format json", "cursor-agent login"
	case "codex-cli":
		return "codex login status", "codex login"
	case "claude-code":
		return "claude auth status --json", "claude auth login"
	case "pi-cli":
		return "pi --list-models", "pi, then /login and /quit"
	default:
		return "", ""
	}
}

func cursorAuthenticated(output []byte) bool {
	var status struct {
		IsAuthenticated bool   `json:"isAuthenticated"`
		Status          string `json:"status"`
	}
	if json.Unmarshal(output, &status) != nil {
		return false
	}
	return status.IsAuthenticated || strings.EqualFold(status.Status, "authenticated")
}

func claudeAuthenticated(output []byte) bool {
	var status struct {
		LoggedIn bool `json:"loggedIn"`
	}
	return json.Unmarshal(output, &status) == nil && status.LoggedIn
}

func codexAuthenticated(output []byte, commandErr error) bool {
	if commandErr != nil {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(string(output)))
	return strings.Contains(status, "logged in") && !strings.Contains(status, "not logged in")
}

func compactStatusDetail(output string, commandErr error) string {
	line := strings.TrimSpace(output)
	if index := strings.IndexByte(line, '\n'); index >= 0 {
		line = line[:index]
	}
	if len(line) > 180 {
		line = line[:180] + "..."
	}
	if line != "" {
		return line
	}
	if commandErr != nil {
		return commandErr.Error()
	}
	return "Authentication was not detected"
}

func (e *Environment) printProviderReadiness(item ProviderReadiness) {
	status := e.style("91;1", "not authenticated")
	if !item.Installed {
		status = e.style("91;1", "missing")
	} else if item.Ready {
		status = e.style("92;1", "ready")
	}
	fmt.Fprintf(e.out, "  %-12s [%s]\n", item.DisplayName, status)
	if item.Ready && item.ModelsAvailable > 0 {
		fmt.Fprintf(e.out, "     %d models available\n", item.ModelsAvailable)
	} else if !item.Ready {
		fmt.Fprintf(e.out, "     Login: %s\n", item.LoginCommand)
	}
}

func (e *Environment) runProviderLogin(ctx context.Context, provider string) error {
	switch provider {
	case "cursor-cli":
		fmt.Fprintln(e.out, e.muted("Cursor will open its native browser authentication flow."))
		return e.runAttached(ctx, "cursor-agent", "login")
	case "codex-cli":
		fmt.Fprintln(e.out, e.muted("Codex will open its native ChatGPT or API authentication flow."))
		return e.runAttached(ctx, "codex", "login")
	case "claude-code":
		fmt.Fprintln(e.out, e.muted("Claude Code will open its native Claude authentication flow."))
		return e.runAttached(ctx, "claude", "auth", "login")
	case "pi-cli":
		fmt.Fprintln(e.out, e.muted("Pi is opening. Type /login, configure a provider, then type /quit to return here."))
		return e.runAttached(ctx, "pi")
	default:
		return fmt.Errorf("unsupported coding-agent provider %q", provider)
	}
}

func (e *Environment) runProviderLiveTest(parent context.Context, provider string) {
	ctx, cancel := context.WithTimeout(parent, 120*time.Second)
	defer cancel()
	prompt := "Reply with exactly LLM_PROVIDER_OK. Do not use tools."
	var output []byte
	var err error
	switch provider {
	case "cursor-cli":
		output, err = e.runner.Run(ctx, "cursor-agent", "--print", "--output-format", "json", "--mode", "ask", prompt)
	case "codex-cli":
		output, err = e.runner.Run(ctx, "codex", "exec", "--ephemeral", "--sandbox", "read-only", "--skip-git-repo-check", "--json", prompt)
	case "claude-code":
		output, err = e.runner.Run(ctx, "claude", "--print", "--output-format", "json", "--tools", "", "--no-session-persistence", prompt)
	case "pi-cli":
		models, _, modelErr := e.piModels(ctx)
		if modelErr != nil || len(models) == 0 {
			e.warn("Pi live test could not resolve an available model")
			return
		}
		model := recommendedPiModel(models)
		output, err = e.runner.Run(ctx, "pi", "--provider", model.Provider, "--model", model.Model, "--print", "--no-tools", "--no-session", prompt)
	}
	if err != nil {
		e.warn("%s live test failed: %v", provider, commandError("run connectivity test", output, err))
		return
	}
	if strings.TrimSpace(string(output)) == "" {
		e.warn("%s live test returned an empty response", provider)
		return
	}
	fmt.Fprintln(e.out, e.success(fmt.Sprintf("%s live connectivity test passed.", provider)))
}

func (e *Environment) piModels(ctx context.Context) ([]piModel, string, error) {
	output, err := e.runner.Run(ctx, "pi", "--list-models")
	detail := strings.TrimSpace(string(output))
	if err != nil {
		return nil, detail, commandError("run pi --list-models", output, err)
	}
	return parsePiModels(detail), detail, nil
}

func parsePiModels(output string) []piModel {
	lines := strings.Split(strings.ReplaceAll(output, "\r", ""), "\n")
	models := make([]piModel, 0)
	for _, line := range lines {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 || (fields[0] == "provider" && fields[1] == "model") {
			continue
		}
		if strings.EqualFold(fields[0], "No") && strings.EqualFold(fields[1], "models") {
			return nil
		}
		if strings.Contains(fields[0], "/") || strings.HasSuffix(fields[0], ":") {
			continue
		}
		models = append(models, piModel{Provider: fields[0], Model: fields[1]})
	}
	return models
}

func recommendedPiModel(models []piModel) piModel {
	preferred := []string{
		"openrouter/openrouter/free",
		"google/gemini-3.8-flash",
		"google/gemini-3.7-flash",
		"google/gemini-3.5-flash-lite",
		"google/gemini-3.1-pro-preview",
		"minimax/MiniMax-M3",
		"zai/glm-5.3",
		"moonshotai/kimi-k3",
	}
	for _, selector := range preferred {
		for _, model := range models {
			if model.selector() == selector {
				return model
			}
		}
	}
	return models[0]
}

func readyCount(readiness []ProviderReadiness) int {
	count := 0
	for _, item := range readiness {
		if item.Ready {
			count++
		}
	}
	return count
}

func yes(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "y") || strings.EqualFold(strings.TrimSpace(value), "yes")
}

func no(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "n") || strings.EqualFold(strings.TrimSpace(value), "no")
}
