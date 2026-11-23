package codingagentsetup

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type recordedCommand struct {
	name string
	args []string
}

type recordingRunner struct {
	commands []recordedCommand
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.commands = append(r.commands, recordedCommand{name: name, args: append([]string(nil), args...)})
	return nil, nil
}

type sequenceRunner struct {
	commands  []recordedCommand
	responses [][]byte
}

func (r *sequenceRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.commands = append(r.commands, recordedCommand{name: name, args: append([]string(nil), args...)})
	if len(r.responses) == 0 {
		return nil, nil
	}
	response := r.responses[0]
	r.responses = r.responses[1:]
	return response, nil
}

func TestInteractiveSetupChoosesHostsThenTargetsWithoutWorkspacePrompt(t *testing.T) {
	binary := testBinary(t)
	project := t.TempDir()
	in := strings.NewReader("1,2\n1,2\n\n")
	var out, errOut bytes.Buffer
	environment := NewEnvironment(in, &out, &errOut)
	environment.SetLookPath(installedLookPath("codex", "claude", "cursor-agent", "pi", "tmux"))
	runner := &recordingRunner{}
	environment.SetRunner(runner)
	environment.SetWorkingDirectory(func() (string, error) { return project, nil })
	environment.SetSmokeTest(func(context.Context, string, []string, []string) error { return nil })

	err := environment.Setup(context.Background(), SetupOptions{Binary: binary, Interactive: true})
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	if !strings.Contains(out.String(), "Step 1 - Choose host CLIs") ||
		!strings.Contains(out.String(), "Hosts are where you will ask one coding agent") ||
		!strings.Contains(out.String(), "Select one or both hosts with commas, such as 1,2") ||
		!strings.Contains(out.String(), "Step 2 - Choose delegation targets") ||
		!strings.Contains(out.String(), "such as 1,3") ||
		!strings.Contains(out.String(), "Step 3 - Confirm project installation") ||
		!strings.Contains(out.String(), "delegation skill is installed only in the current project") ||
		!strings.Contains(out.String(), "Claude Code's MCP registration is also local") ||
		!strings.Contains(out.String(), "Setup summary") {
		t.Fatalf("output = %q", out.String())
	}
	if strings.Contains(out.String(), "\x1b[") {
		t.Fatalf("redirected output contains ANSI color codes: %q", out.String())
	}
	if strings.Contains(out.String(), "working_dir") || strings.Contains(out.String(), "users should not be asked") {
		t.Fatalf("completion leaked internal tool guidance: %q", out.String())
	}
	if !strings.Contains(out.String(), "Configured hosts: Codex CLI, Claude Code") ||
		!strings.Contains(out.String(), "Delegation targets: Cursor Agent, Pi CLI") ||
		!strings.Contains(out.String(), "Codex skill:") ||
		!strings.Contains(out.String(), "Claude Code skill:") {
		t.Fatalf("completion is not user-facing: %q", out.String())
	}
	for _, path := range []string{
		filepath.Join(project, ".agents", "skills", "delegate-coding-agent", "SKILL.md"),
		filepath.Join(project, ".claude", "skills", "delegate-coding-agent", "SKILL.md"),
	} {
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("read installed skill %q: %v", path, readErr)
		}
		if !strings.Contains(string(content), "name: delegate-coding-agent") {
			t.Fatalf("installed skill %q is not the delegation skill", path)
		}
		if _, statErr := os.Stat(filepath.Join(filepath.Dir(path), "agents", "openai.yaml")); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("optional OpenAI metadata was installed for %q: %v", path, statErr)
		}
	}
	if len(runner.commands) != 8 {
		t.Fatalf("commands = %#v", runner.commands)
	}
	for _, command := range runner.commands {
		joined := strings.Join(command.args, " ")
		if strings.Contains(joined, "LLM_PROVIDER_MCP_WORKSPACE_ROOTS") {
			t.Fatalf("registration unexpectedly pinned a workspace: %s", joined)
		}
	}
	if got := strings.Join(runner.commands[1].args, " "); !strings.Contains(got, "LLM_PROVIDER_MCP_ALLOWED_PROVIDERS=cursor-cli,pi-cli") {
		t.Fatalf("Codex add args = %q", got)
	}
	if got := strings.Join(runner.commands[5].args, " "); !strings.Contains(got, "mcp add --scope local") {
		t.Fatalf("Claude add args = %q", got)
	}
}

func TestChecklistDoesNotImplicitlySelectInstalledOptions(t *testing.T) {
	var out bytes.Buffer
	environment := NewEnvironment(strings.NewReader("1\n"), &out, &out)
	selected, err := environment.chooseMany("Select host CLIs", []selectionChoice{
		{ID: "codex", Label: "Codex CLI", Installed: true},
		{ID: "claude", Label: "Claude Code", Installed: true},
	})
	if err != nil {
		t.Fatalf("chooseMany() error = %v; output = %q", err, out.String())
	}
	if strings.Join(selected, ",") != "codex" {
		t.Fatalf("chooseMany() selected = %v; installed options were implicitly selected", selected)
	}
}

func TestTerminalConfirmationAcceptsDefaultWithOneEnter(t *testing.T) {
	t.Setenv("TERM", "dumb")
	var out bytes.Buffer
	environment := NewEnvironment(strings.NewReader("\n"), &out, &out)
	environment.terminalUI = true
	confirmed, err := environment.confirm("Install project configuration?", true)
	if err != nil {
		t.Fatalf("confirm() error = %v; output = %q", err, out.String())
	}
	if !confirmed {
		t.Fatalf("confirm() = false; output = %q", out.String())
	}
}

func TestSingleHostProviderChoicesOmitMatchingTarget(t *testing.T) {
	tests := []struct {
		name           string
		host           string
		selectedIndex  string
		wantProvider   string
		hiddenProvider string
	}{
		{name: "codex", host: "codex", selectedIndex: "3\n", wantProvider: "claude-code", hiddenProvider: "codex-cli"},
		{name: "claude", host: "claude", selectedIndex: "3\n", wantProvider: "codex-cli", hiddenProvider: "claude-code"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var out bytes.Buffer
			environment := NewEnvironment(strings.NewReader(test.selectedIndex), &out, &out)
			environment.SetLookPath(installedLookPath("cursor-agent", "pi", "codex", "claude"))
			providers, err := environment.resolveProviders(nil, []string{test.host}, true)
			if err != nil {
				t.Fatalf("resolveProviders() error = %v", err)
			}
			if strings.Join(providers, ",") != test.wantProvider {
				t.Fatalf("resolveProviders() = %v, want %s", providers, test.wantProvider)
			}
			if strings.Contains(out.String(), test.hiddenProvider) {
				t.Fatalf("provider choices contain selected host target %q: %s", test.hiddenProvider, out.String())
			}
		})
	}
}

func TestExplicitSelfTargetNeedsAnotherProvider(t *testing.T) {
	environment := NewEnvironment(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	_, err := environment.resolveProviders([]string{"codex-cli"}, []string{"codex"}, false)
	if err == nil || !strings.Contains(err.Error(), "other than itself") {
		t.Fatalf("resolveProviders() error = %v", err)
	}
}

func TestDualHostRegistrationsExcludeEachHostsOwnTarget(t *testing.T) {
	binary := testBinary(t)
	project := t.TempDir()
	var out bytes.Buffer
	environment := NewEnvironment(strings.NewReader(""), &out, &out)
	environment.SetLookPath(installedLookPath("codex", "claude", "tmux"))
	runner := &recordingRunner{}
	environment.SetRunner(runner)
	environment.SetWorkingDirectory(func() (string, error) { return project, nil })

	err := environment.Setup(context.Background(), SetupOptions{
		Binary:        binary,
		Client:        "both",
		Providers:     []string{"codex-cli", "claude-code"},
		SkipSmokeTest: true,
	})
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	if got := strings.Join(runner.commands[1].args, " "); !strings.Contains(got, "LLM_PROVIDER_MCP_ALLOWED_PROVIDERS=claude-code") || strings.Contains(got, "codex-cli") {
		t.Fatalf("Codex add args = %q", got)
	}
	if got := strings.Join(runner.commands[5].args, " "); !strings.Contains(got, "LLM_PROVIDER_MCP_ALLOWED_PROVIDERS=codex-cli") || strings.Contains(got, "claude-code") {
		t.Fatalf("Claude add args = %q", got)
	}
}

func TestNonInteractiveSetupCanPinExplicitWorkspace(t *testing.T) {
	binary := testBinary(t)
	project := t.TempDir()
	workspace := t.TempDir()
	var out bytes.Buffer
	environment := NewEnvironment(strings.NewReader(""), &out, &out)
	environment.SetLookPath(installedLookPath("codex", "cursor-agent", "tmux"))
	runner := &recordingRunner{}
	environment.SetRunner(runner)
	environment.SetWorkingDirectory(func() (string, error) { return project, nil })
	environment.SetSmokeTest(func(_ context.Context, _ string, providers, roots []string) error {
		if strings.Join(providers, ",") != "cursor-cli" || len(roots) != 1 {
			t.Fatalf("smoke providers=%v roots=%v", providers, roots)
		}
		return nil
	})

	err := environment.Setup(context.Background(), SetupOptions{
		Binary:      binary,
		Client:      "codex",
		Providers:   []string{"cursor-cli"},
		Workspaces:  []string{workspace},
		Interactive: false,
	})
	if err != nil {
		t.Fatalf("Setup() error = %v", err)
	}
	resolvedWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(runner.commands[1].args, " "); !strings.Contains(got, "LLM_PROVIDER_MCP_WORKSPACE_ROOTS="+resolvedWorkspace) {
		t.Fatalf("Codex add args = %q", got)
	}
}

func TestSetupRefusesToOverwriteUnmanagedSkill(t *testing.T) {
	project := t.TempDir()
	entry := filepath.Join(project, ".agents", "skills", "delegate-coding-agent", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(entry), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte("---\nname: custom\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	environment := NewEnvironment(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	environment.SetLookPath(installedLookPath("codex", "cursor-agent", "tmux"))
	environment.SetWorkingDirectory(func() (string, error) { return project, nil })
	runner := &recordingRunner{}
	environment.SetRunner(runner)
	err := environment.Setup(context.Background(), SetupOptions{
		Binary:        testBinary(t),
		Client:        "codex",
		Providers:     []string{"cursor-cli"},
		SkipSmokeTest: true,
	})
	if err == nil || !strings.Contains(err.Error(), "already contains an unmanaged skill") {
		t.Fatalf("Setup() error = %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("registration ran before skill conflict check: %#v", runner.commands)
	}
	removed, kept, removeErr := removeManagedDelegationSkills(project, []string{"codex"})
	if removeErr != nil || len(removed) != 0 || len(kept) != 1 {
		t.Fatalf("unmanaged removal removed=%v kept=%v err=%v", removed, kept, removeErr)
	}
	content, readErr := os.ReadFile(entry)
	if readErr != nil || string(content) != "---\nname: custom\n---\n" {
		t.Fatalf("unmanaged skill changed: content=%q err=%v", content, readErr)
	}
}

func TestManagedDelegationSkillCanBeUpdatedAndRemoved(t *testing.T) {
	project := t.TempDir()
	paths, err := installDelegationSkills(project, []string{"codex"})
	if err != nil {
		t.Fatalf("installDelegationSkills() error = %v", err)
	}
	if len(paths) != 1 {
		t.Fatalf("installDelegationSkills() paths = %v", paths)
	}
	if err := os.WriteFile(paths[0], []byte("stale managed skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	deprecatedMetadata := filepath.Join(filepath.Dir(paths[0]), "agents", "openai.yaml")
	if err := os.MkdirAll(filepath.Dir(deprecatedMetadata), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deprecatedMetadata, []byte("interface: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := installDelegationSkills(project, []string{"codex"}); err != nil {
		t.Fatalf("update managed skill: %v", err)
	}
	content, err := os.ReadFile(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "name: delegate-coding-agent") {
		t.Fatalf("managed skill was not refreshed: %q", content)
	}
	if _, err := os.Stat(deprecatedMetadata); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deprecated OpenAI metadata still exists: %v", err)
	}

	removed, kept, err := removeManagedDelegationSkills(project, []string{"codex"})
	if err != nil {
		t.Fatalf("removeManagedDelegationSkills() error = %v", err)
	}
	if len(removed) != 1 || len(kept) != 0 {
		t.Fatalf("removeManagedDelegationSkills() removed=%v kept=%v", removed, kept)
	}
	if _, err := os.Stat(filepath.Dir(paths[0])); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("managed skill directory still exists: %v", err)
	}
}

func TestSetupRejectsUnavailableExplicitHost(t *testing.T) {
	environment := NewEnvironment(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	environment.SetLookPath(installedLookPath())
	err := environment.Setup(context.Background(), SetupOptions{
		Binary:        testBinary(t),
		Client:        "claude",
		Providers:     []string{"cursor-cli"},
		SkipSmokeTest: true,
	})
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("Setup() error = %v", err)
	}
}

func TestDoctorReportsMissingDependency(t *testing.T) {
	var out bytes.Buffer
	environment := NewEnvironment(strings.NewReader(""), &out, &out)
	environment.SetLookPath(installedLookPath("cursor-agent"))
	environment.SetRunner(&sequenceRunner{responses: [][]byte{[]byte(`{"status":"authenticated","isAuthenticated":true}`)}})
	err := environment.Doctor(context.Background(), DoctorOptions{
		Providers:    []string{"cursor-cli"},
		SkipMCPCheck: true,
	})
	if !errors.Is(err, ErrDoctorFailed) || !strings.Contains(out.String(), "missing  tmux") {
		t.Fatalf("Doctor() error=%v output=%q", err, out.String())
	}
}

func TestWithoutEnvironmentRemovesInheritedMCPConfiguration(t *testing.T) {
	got := withoutEnvironment([]string{
		"PATH=/bin",
		"LLM_PROVIDER_MCP_STATE=/real/jobs.db",
		"LLM_PROVIDER_MCP_WORKSPACE_ROOTS=/real/project",
	}, "LLM_PROVIDER_MCP_STATE", "LLM_PROVIDER_MCP_WORKSPACE_ROOTS")
	if strings.Join(got, "|") != "PATH=/bin" {
		t.Fatalf("withoutEnvironment() = %v", got)
	}
}

func TestColorCanBeForcedForTerminalPreview(t *testing.T) {
	t.Setenv("CLICOLOR_FORCE", "1")
	t.Setenv("NO_COLOR", "1")
	environment := NewEnvironment(strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if got := environment.heading("Setup"); !strings.Contains(got, "\x1b[96;1m") {
		t.Fatalf("forced heading = %q", got)
	}
}

func TestParsePiModelsAndPreferFreeRouter(t *testing.T) {
	models := parsePiModels(`provider    model                    context  max-out  thinking  images
google      gemini-3.5-flash         1M       64K      yes       yes
openrouter  openrouter/free          128K     32K      yes       no
`)
	if len(models) != 2 {
		t.Fatalf("parsePiModels() = %#v", models)
	}
	if got := recommendedPiModel(models).selector(); got != "openrouter/openrouter/free" {
		t.Fatalf("recommendedPiModel() = %q", got)
	}
}

func TestParsePiModelsRecognizesMissingAuthentication(t *testing.T) {
	models := parsePiModels("No models available. Use /login to log into a provider via OAuth or API key.\n")
	if len(models) != 0 {
		t.Fatalf("parsePiModels() = %#v", models)
	}
}

func TestPiReadinessRunsLiveTestOnlyAfterConsent(t *testing.T) {
	var out bytes.Buffer
	environment := NewEnvironment(strings.NewReader("y\n"), &out, &out)
	environment.SetLookPath(installedLookPath("pi"))
	runner := &sequenceRunner{responses: [][]byte{
		[]byte("provider model context max-out thinking images\nopenrouter openrouter/free 128K 32K yes no\n"),
		[]byte("provider model context max-out thinking images\nopenrouter openrouter/free 128K 32K yes no\n"),
		[]byte("PI_OK\n"),
	}}
	environment.SetRunner(runner)

	environment.checkTargetReadiness(context.Background(), []string{"pi-cli"}, true)
	if len(runner.commands) != 3 {
		t.Fatalf("commands = %#v", runner.commands)
	}
	if got := strings.Join(runner.commands[2].args, " "); !strings.Contains(got, "--no-tools") || !strings.Contains(got, "--no-session") {
		t.Fatalf("Pi live test args = %q", got)
	}
	if !strings.Contains(out.String(), "pi-cli live connectivity test passed") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestDoctorWithoutParametersChecksAllProviderAuthFormats(t *testing.T) {
	var out bytes.Buffer
	environment := NewEnvironment(strings.NewReader(""), &out, &out)
	environment.SetLookPath(installedLookPath("tmux", "cursor-agent", "codex", "claude", "pi"))
	environment.SetRunner(&sequenceRunner{responses: [][]byte{
		[]byte(`{"status":"authenticated","isAuthenticated":true,"userInfo":{"email":"private@example.com"}}`),
		[]byte("provider model context max-out thinking images\nopenrouter openrouter/free 128K 32K yes no\n"),
		[]byte("Logged in using ChatGPT\n"),
		[]byte(`{"loggedIn":true,"email":"private@example.com"}`),
	}})

	err := environment.Doctor(context.Background(), DoctorOptions{SkipMCPCheck: true})
	if err != nil {
		t.Fatalf("Doctor() error = %v", err)
	}
	for _, provider := range []string{"Cursor Agent", "Codex CLI", "Claude Code", "Pi CLI"} {
		if !strings.Contains(out.String(), provider) {
			t.Fatalf("doctor output is missing %s: %s", provider, out.String())
		}
	}
	if strings.Contains(out.String(), "private@example.com") {
		t.Fatalf("Doctor leaked native account details: %s", out.String())
	}
}

func TestAuthenticationParsers(t *testing.T) {
	if !cursorAuthenticated([]byte(`{"isAuthenticated":true}`)) {
		t.Fatal("Cursor authenticated JSON was not recognized")
	}
	if !claudeAuthenticated([]byte(`{"loggedIn":true}`)) {
		t.Fatal("Claude authenticated JSON was not recognized")
	}
	if !codexAuthenticated([]byte("Logged in using ChatGPT\n"), nil) {
		t.Fatal("Codex authenticated status was not recognized")
	}
	if codexAuthenticated([]byte("Not logged in\n"), nil) {
		t.Fatal("Codex unauthenticated status was recognized as authenticated")
	}
}

func testBinary(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "llm-provider-mcp")
	if err := os.WriteFile(path, []byte("test"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func installedLookPath(names ...string) func(string) (string, error) {
	installed := make(map[string]bool, len(names))
	for _, name := range names {
		installed[name] = true
	}
	return func(name string) (string, error) {
		if installed[name] {
			return "/bin/" + name, nil
		}
		return "", os.ErrNotExist
	}
}
