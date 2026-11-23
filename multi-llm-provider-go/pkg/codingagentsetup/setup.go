package codingagentsetup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"charm.land/huh/v2"
)

type SetupOptions struct {
	Client        string
	Providers     []string
	Workspaces    []string
	Binary        string
	Interactive   bool
	SkipSmokeTest bool
}

func (e *Environment) Setup(ctx context.Context, options SetupOptions) error {
	binary, err := e.resolveBinary(options.Binary)
	if err != nil {
		return err
	}
	if options.Interactive {
		e.printSetupIntroduction()
	}
	hosts, err := e.resolveHosts(options.Client, options.Interactive)
	if err != nil {
		return err
	}
	providers, err := e.resolveProviders(options.Providers, hosts, options.Interactive)
	if err != nil {
		return err
	}
	workspaces, err := resolveWorkspaces(options.Workspaces)
	if err != nil {
		return err
	}
	project := ""
	if len(hosts) > 0 {
		project, err = e.workingDir()
		if err != nil {
			return err
		}
		if options.Interactive {
			if err := e.confirmProjectInstallation(project, hosts); err != nil {
				return err
			}
		}
		if err := preflightDelegationSkills(project, hosts); err != nil {
			return err
		}
	}
	if options.Interactive {
		e.printSetupPlan(hosts, providers, workspaces, project)
	}

	if !options.SkipSmokeTest {
		if err := e.smoke(ctx, binary, providers, workspaces); err != nil {
			return fmt.Errorf("MCP protocol smoke test failed: %w", err)
		}
		fmt.Fprintln(e.out, e.success("MCP protocol check passed."))
	}

	for _, host := range hosts {
		switch host {
		case "codex":
			if err := e.registerCodex(ctx, binary, providers, workspaces); err != nil {
				return err
			}
		case "claude":
			if err := e.registerClaude(ctx, binary, providers, workspaces); err != nil {
				return err
			}
		}
	}
	skillPaths, err := installDelegationSkills(project, hosts)
	if err != nil {
		return err
	}

	e.checkTargetReadiness(ctx, providers, options.Interactive)
	e.printDependencyWarnings(providers)
	e.printSetupCompletion(hosts, providers, binary, project, skillPaths)
	return nil
}

func (e *Environment) printSetupIntroduction() {
	fmt.Fprintln(e.out, e.title("Multi-LLM Provider Setup"))
	fmt.Fprintln(e.out, "Connect your coding CLIs through one local MCP server.")
	fmt.Fprintln(e.out, e.muted("A host CLI calls the MCP tools; a delegation target receives and runs the background job through tmux."))
	fmt.Fprintln(e.out, e.muted("A project-local skill teaches each selected host how to route work to an appropriate model."))
	fmt.Fprintln(e.out, e.muted("Delegated CLIs use their existing local login and run unattended, so normal coding tools do not wait for invisible approval prompts."))
	fmt.Fprintln(e.out)
}

func (e *Environment) printSetupCompletion(hosts, providers []string, binary, project string, skillPaths []string) {
	fmt.Fprintln(e.out)
	fmt.Fprintln(e.out, e.success("Setup complete."))
	if len(hosts) == 0 {
		fmt.Fprintf(e.out, "Binary installed at %s. No host CLI was configured.\n", binary)
		fmt.Fprintln(e.out, "Run llm-provider-mcp setup again when you are ready to register a host.")
		return
	}

	fmt.Fprintf(e.out, "Configured host%s: %s\n", pluralSuffix(len(hosts)), humanHostNames(hosts))
	if project != "" {
		fmt.Fprintf(e.out, "Project: %s\n", project)
	}
	fmt.Fprintf(e.out, "Delegation target%s: %s\n", pluralSuffix(len(providers)), humanProviderNames(providers))
	for index, skillPath := range skillPaths {
		label := "Host"
		if index < len(hosts) {
			label = delegationSkillHostLabel(hosts[index])
		}
		fmt.Fprintf(e.out, "%s skill: %s\n", label, skillPath)
	}
	fmt.Fprintln(e.out, "Tool policy: standard coding tools run unattended inside the selected project.")
	if contains(providers, "pi-cli") {
		fmt.Fprintln(e.out, e.style("33;1", "Warning: Pi CLI uses native local permissions and does not currently provide a hard workspace sandbox."))
	}
	if len(hosts) == 1 && hosts[0] == "claude" {
		fmt.Fprintln(e.out, "Next: start a new Claude Code session in this project and ask it to delegate a task.")
	} else {
		fmt.Fprintln(e.out, "Next: start a new configured host session and ask it to delegate a task.")
	}
	fmt.Fprintln(e.out, e.muted("The current trusted project is selected automatically."))
}

func humanHostNames(hosts []string) string {
	names := make([]string, 0, len(hosts))
	for _, host := range hosts {
		switch host {
		case "codex":
			names = append(names, "Codex CLI")
		case "claude":
			names = append(names, "Claude Code")
		default:
			names = append(names, host)
		}
	}
	return strings.Join(names, ", ")
}

func humanProviderNames(providers []string) string {
	names := make([]string, 0, len(providers))
	for _, provider := range providers {
		if agent, ok := findAgent(provider); ok {
			names = append(names, agent.DisplayName)
		} else {
			names = append(names, provider)
		}
	}
	return strings.Join(names, ", ")
}

func pluralSuffix(count int) string {
	if count == 1 {
		return ""
	}
	return "s"
}

func (e *Environment) resolveBinary(configured string) (string, error) {
	binary := strings.TrimSpace(configured)
	var err error
	if binary == "" {
		binary, err = e.executable()
		if err != nil {
			return "", fmt.Errorf("resolve MCP executable: %w", err)
		}
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return "", fmt.Errorf("resolve MCP executable: %w", err)
	}
	info, err := os.Stat(binary)
	if err != nil {
		return "", fmt.Errorf("MCP executable %q: %w", binary, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("MCP executable %q is a directory", binary)
	}
	return binary, nil
}

func (e *Environment) resolveHosts(client string, interactive bool) ([]string, error) {
	client = strings.ToLower(strings.TrimSpace(client))
	if client != "" {
		switch client {
		case "none":
			return nil, nil
		case "codex":
			return e.requireHosts([]string{"codex"})
		case "claude":
			return e.requireHosts([]string{"claude"})
		case "both":
			return e.requireHosts([]string{"codex", "claude"})
		default:
			return nil, fmt.Errorf("client must be none, codex, claude, or both")
		}
	}
	if !interactive {
		return nil, nil
	}

	choices := []selectionChoice{
		{ID: "codex", Label: "Codex CLI", Detail: "Use llm-provider tools from new Codex sessions.", Installed: e.cliInstalled("codex")},
		{ID: "claude", Label: "Claude Code", Detail: "Use llm-provider tools from new Claude Code sessions.", Installed: e.cliInstalled("claude")},
	}
	fmt.Fprintln(e.out, e.heading("Step 1 - Choose host CLIs"))
	fmt.Fprintln(e.out, "Hosts are where you will ask one coding agent to delegate work to another.")
	fmt.Fprintln(e.out, e.muted("Codex and Claude Code are shown because they are the currently supported MCP hosts; selecting both installs the same tools in both."))
	if e.terminalUI {
		fmt.Fprintln(e.out, e.style("93", "Nothing is preselected. Use Up/Down to move, Space to select one or both hosts, and Enter to confirm."))
	} else {
		fmt.Fprintln(e.out, e.style("93", "Select one or both hosts with commas, such as 1,2."))
	}
	selected, err := e.chooseMany("Select host CLIs", choices)
	if err != nil {
		return nil, err
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("select at least one host CLI")
	}
	return e.requireHosts(selected)
}

func (e *Environment) requireHosts(hosts []string) ([]string, error) {
	for _, host := range hosts {
		cli := host
		if host == "claude" {
			cli = "claude"
		}
		if !e.cliInstalled(cli) {
			return nil, fmt.Errorf("cannot register with %s: %s CLI is not installed", host, cli)
		}
	}
	return hosts, nil
}

func (e *Environment) resolveProviders(configured, hosts []string, interactive bool) ([]string, error) {
	configured = splitList(configured)
	if len(configured) > 0 {
		providers, err := validateProviders(configured)
		if err != nil {
			return nil, err
		}
		return providers, validateHostTargets(hosts, providers)
	}
	choices := make([]selectionChoice, 0, len(supportedAgents))
	for _, agent := range supportedAgents {
		if !targetAvailableToHosts(agent.Provider, hosts) {
			continue
		}
		detail := "Receive delegated background coding jobs."
		switch agent.Provider {
		case "cursor-cli":
			detail = "Run delegated jobs with Cursor Agent and its available models."
		case "pi-cli":
			detail = "Run Gemini, OpenRouter, MiniMax, GLM, and Kimi models through Pi."
		case "codex-cli":
			detail = "Run delegated background jobs with Codex CLI."
		case "claude-code":
			detail = "Run delegated background jobs with Claude Code."
		}
		choices = append(choices, selectionChoice{
			ID:        agent.Provider,
			Label:     agent.DisplayName,
			Detail:    detail,
			Installed: e.installed(agent),
		})
	}
	defaults := installedIDs(choices)
	if !interactive {
		if len(defaults) == 0 {
			for _, choice := range choices {
				defaults = append(defaults, choice.ID)
			}
		}
		providers, err := validateProviders(defaults)
		if err != nil {
			return nil, err
		}
		return providers, validateHostTargets(hosts, providers)
	}
	fmt.Fprintln(e.out)
	fmt.Fprintln(e.out, e.heading("Step 2 - Choose delegation targets"))
	fmt.Fprintln(e.out, "Targets are the local CLIs that can receive asynchronous coding jobs from your selected hosts.")
	fmt.Fprintln(e.out, e.muted("Installed targets can run immediately. Missing targets may be enabled later after their CLI is installed and authenticated."))
	if e.terminalUI {
		fmt.Fprintln(e.out, e.style("93", "Nothing is preselected. Use Up/Down to move, Space to select multiple targets, and Enter to confirm."))
	} else {
		fmt.Fprintln(e.out, e.style("93", "Select one or more targets with commas, such as 1,3."))
	}
	selected, err := e.chooseMany("Select delegation targets", choices)
	if err != nil {
		return nil, err
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("select at least one delegation target")
	}
	providers, err := validateProviders(selected)
	if err != nil {
		return nil, err
	}
	return providers, validateHostTargets(hosts, providers)
}

func targetAvailableToHosts(provider string, hosts []string) bool {
	return len(hosts) != 1 || !providerMatchesHost(provider, hosts[0])
}

func providerMatchesHost(provider, host string) bool {
	switch host {
	case "codex":
		return provider == "codex-cli"
	case "claude":
		return provider == "claude-code"
	default:
		return false
	}
}

func providersForHost(providers []string, host string) []string {
	filtered := make([]string, 0, len(providers))
	for _, provider := range providers {
		if !providerMatchesHost(provider, host) {
			filtered = append(filtered, provider)
		}
	}
	return filtered
}

func validateHostTargets(hosts, providers []string) error {
	for _, host := range hosts {
		if len(providersForHost(providers, host)) == 0 {
			return fmt.Errorf("%s needs at least one delegation target other than itself", humanHostNames([]string{host}))
		}
	}
	return nil
}

type selectionChoice struct {
	ID        string
	Label     string
	Detail    string
	Installed bool
}

func (e *Environment) chooseMany(title string, choices []selectionChoice) ([]string, error) {
	if e.terminalUI {
		return e.chooseManyTerminal(title, choices)
	}
	if strings.TrimSpace(title) != "" {
		fmt.Fprintln(e.out, e.heading(title+":"))
	}
	for i, choice := range choices {
		status := e.style("91", "not found")
		if choice.Installed {
			status = e.style("92", "installed")
		}
		number := e.style("93;1", strconv.Itoa(i+1)+")")
		label := e.style("97;1", fmt.Sprintf("%-12s", choice.Label))
		providerID := e.style("94", fmt.Sprintf("%-12s", choice.ID))
		fmt.Fprintf(e.out, "  %s %s %s [%s]\n", number, label, providerID, status)
		if choice.Detail != "" {
			fmt.Fprintf(e.out, "     %s\n", e.muted(choice.Detail))
		}
	}
	answer, err := e.prompt("Choose comma-separated entries: ")
	if err != nil {
		return nil, err
	}
	if answer == "" {
		return nil, nil
	}
	if strings.EqualFold(answer, "none") {
		return nil, nil
	}
	selected := make([]string, 0)
	for _, token := range strings.Split(answer, ",") {
		token = strings.TrimSpace(token)
		index, numberErr := strconv.Atoi(token)
		if numberErr == nil && index >= 1 && index <= len(choices) {
			selected = appendUnique(selected, choices[index-1].ID)
			continue
		}
		matched := false
		for _, choice := range choices {
			if strings.EqualFold(token, choice.ID) {
				selected = appendUnique(selected, choice.ID)
				matched = true
				break
			}
		}
		if !matched {
			return nil, fmt.Errorf("invalid selection %q", token)
		}
	}
	return selected, nil
}

func (e *Environment) chooseManyTerminal(title string, choices []selectionChoice) ([]string, error) {
	selected := make([]string, 0, len(choices))
	options := make([]huh.Option[string], 0, len(choices))
	for _, choice := range choices {
		status := "not installed"
		if choice.Installed {
			status = "installed"
		}
		label := fmt.Sprintf("%s (%s) - %s", choice.Label, choice.ID, status)
		options = append(options, huh.NewOption(label, choice.ID))
	}
	field := huh.NewMultiSelect[string]().
		Title(title).
		Description("Nothing is preselected. Use Up/Down to move, Space to toggle, and Enter to confirm.").
		Options(options...).
		Filterable(false).
		Width(88).
		Height(len(choices) + 4).
		Validate(func(values []string) error {
			if len(values) == 0 {
				return fmt.Errorf("select at least one option")
			}
			return nil
		}).
		Value(&selected)
	form := huh.NewForm(huh.NewGroup(field)).
		WithInput(e.input).
		WithOutput(e.out).
		WithTheme(huh.ThemeFunc(huh.ThemeCharm)).
		WithShowHelp(true)
	if err := form.Run(); err != nil {
		return nil, fmt.Errorf("select %s: %w", strings.ToLower(title), err)
	}
	return selected, nil
}

func (e *Environment) confirm(title string, defaultValue bool) (bool, error) {
	if !e.terminalUI {
		options := "[y/N]"
		if defaultValue {
			options = "[Y/n]"
		}
		answer, err := e.prompt(fmt.Sprintf("%s %s: ", title, options))
		if err != nil {
			return false, err
		}
		if strings.TrimSpace(answer) == "" {
			return defaultValue, nil
		}
		if yes(answer) {
			return true, nil
		}
		if no(answer) {
			return false, nil
		}
		return false, fmt.Errorf("answer yes or no")
	}

	confirmed := defaultValue
	field := huh.NewConfirm().
		Title(title).
		Affirmative("Yes").
		Negative("No").
		Value(&confirmed)
	form := huh.NewForm(huh.NewGroup(field)).
		WithInput(e.input).
		WithOutput(e.out).
		WithTheme(huh.ThemeFunc(huh.ThemeCharm)).
		WithShowHelp(true)
	if err := form.Run(); err != nil {
		return false, fmt.Errorf("confirm %s: %w", strings.ToLower(title), err)
	}
	return confirmed, nil
}

func (e *Environment) confirmProjectInstallation(project string, hosts []string) error {
	fmt.Fprintln(e.out)
	fmt.Fprintln(e.out, e.heading("Step 3 - Confirm project installation"))
	fmt.Fprintln(e.out, "The delegation skill is installed only in the current project, not globally.")
	fmt.Fprintf(e.out, "%s %s\n", e.style("94;1", "  Current project:"), project)
	for _, destination := range delegationSkillDestinations(project, hosts) {
		label := "  " + delegationSkillHostLabel(destination.host) + " skill:"
		fmt.Fprintf(e.out, "%s %s\n", e.style("94;1", label), filepath.Join(destination.directory, "SKILL.md"))
	}
	if contains(hosts, "claude") {
		fmt.Fprintln(e.out, e.muted("Claude Code's MCP registration is also local to this project and user."))
	}
	fmt.Fprintln(e.out, e.muted("The generated skill files are local configuration and should not be committed unless you intentionally want to share them."))
	if home, homeErr := os.UserHomeDir(); homeErr == nil && filepath.Clean(home) == filepath.Clean(project) {
		e.warn("the current directory is your home directory, not a specific project; cancel and run setup from the intended project if that was not deliberate")
	}
	confirmed, err := e.confirm("Install the host configuration and project-local delegation skill?", true)
	if err != nil {
		return err
	}
	if !confirmed {
		return fmt.Errorf("project installation cancelled; change to the intended project directory and run setup again")
	}
	return nil
}

func (e *Environment) printSetupPlan(hosts, providers, workspaces []string, project string) {
	fmt.Fprintln(e.out)
	fmt.Fprintln(e.out, e.heading("Setup summary"))
	hostLabel := "none (binary only)"
	if len(hosts) > 0 {
		hostLabel = strings.Join(hosts, ", ")
	}
	fmt.Fprintf(e.out, "%s %s\n", e.style("94;1", fmt.Sprintf("  %-20s", "MCP hosts:")), hostLabel)
	fmt.Fprintf(e.out, "%s %s\n", e.style("94;1", fmt.Sprintf("  %-20s", "Delegation targets:")), strings.Join(providers, ", "))
	fmt.Fprintf(e.out, "%s %s\n", e.style("94;1", fmt.Sprintf("  %-20s", "Tool policy:")), "unattended standard coding tools; no approval prompts")
	if project != "" {
		fmt.Fprintf(e.out, "%s %s\n", e.style("94;1", fmt.Sprintf("  %-20s", "Project skills:")), strings.Join(delegationSkillEntryPaths(project, hosts), ", "))
	}
	if contains(hosts, "claude") {
		fmt.Fprintf(e.out, "%s %s\n", e.style("94;1", fmt.Sprintf("  %-20s", "Claude registration:")), "local to "+project)
	}
	if len(workspaces) == 0 {
		fmt.Fprintf(e.out, "%s %s\n", e.style("94;1", fmt.Sprintf("  %-20s", "Working directory:")), "current trusted project, supplied automatically by the host")
	} else {
		fmt.Fprintf(e.out, "%s %s\n", e.style("94;1", fmt.Sprintf("  %-20s", "Allowed workspaces:")), strings.Join(workspaces, string(os.PathListSeparator)))
	}
	fmt.Fprintln(e.out)
	fmt.Fprintln(e.out, e.muted("Checking the MCP protocol and writing the selected host registrations and project skills..."))
}

func delegationSkillHostLabel(host string) string {
	switch host {
	case "codex":
		return "Codex"
	case "claude":
		return "Claude Code"
	default:
		return host
	}
}

func (e *Environment) registerCodex(ctx context.Context, binary string, providers, workspaces []string) error {
	providers = providersForHost(providers, "codex")
	_, _ = e.runner.Run(ctx, "codex", "mcp", "remove", ServerName)
	args := []string{"mcp", "add"}
	args = append(args, registrationEnvArgs("codex", providers, workspaces)...)
	args = append(args, ServerName, "--", binary)
	if output, err := e.runner.Run(ctx, "codex", args...); err != nil {
		return commandError("register MCP server with Codex", output, err)
	}
	fmt.Fprintln(e.out, e.success("Registered with Codex CLI."))
	return nil
}

func (e *Environment) registerClaude(ctx context.Context, binary string, providers, workspaces []string) error {
	providers = providersForHost(providers, "claude")
	for _, scope := range []string{"local", "user", "project"} {
		_, _ = e.runner.Run(ctx, "claude", "mcp", "remove", "--scope", scope, ServerName)
	}
	args := []string{"mcp", "add", "--scope", "local", ServerName}
	env := registrationEnvironment(providers, workspaces)
	if len(env) > 0 {
		args = append(args, "-e")
		for _, pair := range env {
			args = append(args, pair)
		}
	}
	args = append(args, "--", binary)
	if output, err := e.runner.Run(ctx, "claude", args...); err != nil {
		return commandError("register MCP server with Claude Code", output, err)
	}
	fmt.Fprintln(e.out, e.success("Registered with Claude Code locally in the current project."))
	return nil
}

func currentDirectory() (string, error) {
	directory, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve current project directory: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return "", fmt.Errorf("resolve current project directory: %w", err)
	}
	return filepath.Clean(resolved), nil
}

func registrationEnvArgs(host string, providers, workspaces []string) []string {
	pairs := registrationEnvironment(providers, workspaces)
	args := make([]string, 0, len(pairs)*2)
	for _, pair := range pairs {
		if host == "codex" {
			args = append(args, "--env", pair)
		}
	}
	return args
}

func registrationEnvironment(providers, workspaces []string) []string {
	pairs := []string{"LLM_PROVIDER_MCP_ALLOWED_PROVIDERS=" + strings.Join(providers, ",")}
	if len(workspaces) > 0 {
		pairs = append(pairs, "LLM_PROVIDER_MCP_WORKSPACE_ROOTS="+strings.Join(workspaces, string(os.PathListSeparator)))
	}
	return pairs
}

func (e *Environment) printDependencyWarnings(providers []string) {
	if !e.cliInstalled("tmux") {
		e.warn("tmux is not installed; delegated jobs cannot run")
	}
	for _, provider := range providers {
		agent, _ := findAgent(provider)
		if !e.installed(agent) {
			e.warn("%s is enabled but %s is not installed", provider, agent.CLI)
		}
	}
}

func (e *Environment) cliInstalled(cli string) bool {
	_, err := e.lookPath(cli)
	return err == nil
}

func resolveWorkspaces(configured []string) ([]string, error) {
	configured = splitList(configured)
	result := make([]string, 0, len(configured))
	for _, workspace := range configured {
		absolute, err := filepath.Abs(workspace)
		if err != nil {
			return nil, fmt.Errorf("resolve workspace %q: %w", workspace, err)
		}
		resolved, err := filepath.EvalSymlinks(absolute)
		if err != nil {
			return nil, fmt.Errorf("resolve workspace %q: %w", workspace, err)
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return nil, fmt.Errorf("workspace %q: %w", workspace, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("workspace %q is not a directory", workspace)
		}
		result = appendUnique(result, filepath.Clean(resolved))
	}
	return result, nil
}

func validateProviders(providers []string) ([]string, error) {
	result := make([]string, 0, len(providers))
	for _, provider := range providers {
		provider = strings.ToLower(strings.TrimSpace(provider))
		if _, ok := findAgent(provider); !ok {
			return nil, fmt.Errorf("unsupported coding-agent provider %q", provider)
		}
		result = appendUnique(result, provider)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("at least one provider is required")
	}
	return result, nil
}

func findAgent(provider string) (Agent, bool) {
	for _, agent := range supportedAgents {
		if agent.Provider == provider {
			return agent, true
		}
	}
	return Agent{}, false
}

func installedIDs(choices []selectionChoice) []string {
	result := make([]string, 0, len(choices))
	for _, choice := range choices {
		if choice.Installed {
			result = append(result, choice.ID)
		}
	}
	return result
}

func splitList(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				result = append(result, part)
			}
		}
	}
	return result
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func appendUnique(values []string, value string) []string {
	if !contains(values, value) {
		return append(values, value)
	}
	return values
}

func commandError(action string, output []byte, err error) error {
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return fmt.Errorf("%s: %w", action, err)
	}
	return fmt.Errorf("%s: %w: %s", action, err, detail)
}
