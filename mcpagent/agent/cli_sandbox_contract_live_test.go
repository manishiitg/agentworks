package mcpagent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/manishiitg/mcpagent/llm"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// TestCLISandboxContract runs the owner's sandbox self-test against every
// coding CLI in Full CLI mode, through the real launch options this package
// builds (the same path a Builder chat takes). Each CLI gets the Builder
// layout: a private runtime folder inside the app's state, with its workflow
// linked as project/, another workflow, an attached (read-only) workflow, the
// app's config.json and auth/, and another chat's runtime folder.
//
// Every verdict is read from disk or from tokens the CLI could only have seen
// by reading a file, never from the model's own report. A CLI that stops on its
// own approval or trust screen times out and fails. Problems this catches were
// found one CLI at a time in owner testing (2026-10-03/04): Claude refusing
// edits through project/, Codex's trust screen, Cursor's approval prompts,
// Muse hanging at start.
//
// Enable with RUN_CLI_SANDBOX_CONTRACT=1 (real provider requests); limit with
// CLI_SANDBOX_CONTRACT_PROVIDERS=Claude,Codex. macOS runs under Seatbelt; Linux
// needs AGENTWORKS_LANDLOCK_RUNNER.
func TestCLISandboxContract(t *testing.T) {
	if os.Getenv("RUN_CLI_SANDBOX_CONTRACT") != "1" {
		t.Skip("set RUN_CLI_SANDBOX_CONTRACT=1 for the live cross-CLI sandbox contract")
	}
	t.Setenv("MCP_BRIDGE_BINARY", ensureRealBridgeBinary(t))
	selected := map[string]bool{}
	for _, name := range strings.Split(os.Getenv("CLI_SANDBOX_CONTRACT_PROVIDERS"), ",") {
		if name = strings.TrimSpace(name); name != "" {
			selected[strings.ToLower(name)] = true
		}
	}
	for _, tc := range multiTurnProviderCases {
		tc := tc
		if len(selected) > 0 && !selected[strings.ToLower(tc.name)] {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.binary); err != nil {
				t.Skipf("%s unavailable", tc.binary)
			}
			t.Parallel()
			runCLISandboxContract(t, tc)
		})
	}
}

type sandboxContractLayout struct {
	root, workspace, workflow, otherWorkflow, attached string
	app, runtime, otherRuntime                         string
	tokens                                             map[string]string
}

func newSandboxContractLayout(t *testing.T) sandboxContractLayout {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home folder")
	}
	// Under the home on purpose: Seatbelt leaves the home open, so the closed
	// app and workspace folders are exercised the way they are in the app.
	root, err := os.MkdirTemp(home, ".agentworks-cli-contract-")
	if err != nil {
		t.Skipf("cannot create a folder under the home: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	l := sandboxContractLayout{root: root, tokens: map[string]string{}}
	l.workspace = filepath.Join(root, "workspace-docs")
	l.workflow = filepath.Join(l.workspace, "Workflow", "mine")
	l.otherWorkflow = filepath.Join(l.workspace, "Workflow", "other")
	l.attached = filepath.Join(l.workspace, "Workflow", "attached")
	l.app = filepath.Join(root, "AgentWorks")
	l.runtime = filepath.Join(l.app, "state", "cli-runtimes", "v1", "mine-"+realBridgeRandHex(6))
	l.otherRuntime = filepath.Join(l.app, "state", "cli-runtimes", "v1", "other-"+realBridgeRandHex(6))
	for _, name := range []string{"soul", "other", "attached", "config", "auth", "otherRuntime", "personal"} {
		l.tokens[name] = strings.ToUpper(name) + "_" + realBridgeRandHex(5)
	}
	files := map[string]string{
		filepath.Join(l.workflow, "soul", "soul.md"):         "# Soul\n",
		filepath.Join(l.workflow, "planning", "plan.json"):   "{}\n",
		filepath.Join(l.otherWorkflow, "workflow.json"):      l.tokens["other"] + "\n",
		filepath.Join(l.attached, "workflow.json"):           l.tokens["attached"] + "\n",
		filepath.Join(l.app, "config.json"):                  l.tokens["config"] + "\n",
		filepath.Join(l.app, "state", "auth", "session.txt"): l.tokens["auth"] + "\n",
		filepath.Join(l.otherRuntime, "data.txt"):            l.tokens["otherRuntime"] + "\n",
		filepath.Join(root, "my-notes", "note.txt"):          l.tokens["personal"] + "\n",
		filepath.Join(l.runtime, ".keep"):                    "",
	}
	for path, body := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(l.workflow, filepath.Join(l.runtime, "project")); err != nil {
		t.Fatal(err)
	}
	return l
}

// sandboxContractPolicy is the policy the agent server builds for a Builder
// chat with one attached workflow (cmd/server/cli_landlock.go).
func sandboxContractPolicy(t *testing.T, l sandboxContractLayout, provider llm.Provider) *llmtypes.CLISecurityPolicy {
	t.Helper()
	policy := &llmtypes.CLISecurityPolicy{
		Mode:                llmtypes.CLISecurityModeIsolated,
		Provider:            string(provider),
		PrivateHome:         filepath.Join(l.runtime, ".sandbox-cache", "cli-home", string(provider)),
		WorkspaceWritePaths: []string{l.runtime, l.workflow},
		WorkspaceReadPaths:  []string{l.attached},
		ProtectedRoots:      []string{l.workspace, filepath.Join(l.app, "state"), l.app},
		BlockedWritePaths:   []string{filepath.Join(l.workflow, "planning")},
	}
	switch runtime.GOOS {
	case "darwin":
		policy.Seatbelt = true
	case "linux":
		policy.LandlockRunner = strings.TrimSpace(os.Getenv("AGENTWORKS_LANDLOCK_RUNNER"))
	}
	if !policy.Confined() {
		t.Skip("this host cannot confine a coding CLI (set AGENTWORKS_LANDLOCK_RUNNER on Linux)")
	}
	return policy
}

func runCLISandboxContract(t *testing.T, tc multiTurnProviderCase) {
	l := newSandboxContractLayout(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	session := "cli-contract-" + strings.ToLower(tc.name) + "-" + realBridgeRandHex(5)
	t.Cleanup(func() { closePersistentInteractiveSession(tc, session) })
	a, cleanup, err := buildRealBridgeAgent(ctx, tc, t.TempDir(), l.runtime, session, true)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	listener := &localLinkedToolListener{}
	a.addEventListener(listener)
	a.codingAgentToolsMode = codingAgentToolsFull
	a.cliSecurityPolicy = sandboxContractPolicy(t, l, tc.provider)

	editToken := "EDIT_" + realBridgeRandHex(5)
	ownToken := "OWN_" + realBridgeRandHex(5)
	osaMarker := filepath.Join(l.runtime, "osa-result.txt")
	q := func(p string) string { return "'" + p + "'" }
	steps := []string{
		"Use your own file-edit tool to append the line " + editToken + " to project/soul/soul.md.",
		"echo " + ownToken + " > own.txt",
		"echo x >> project/planning/plan.json",
		"cat " + q(filepath.Join(l.otherWorkflow, "workflow.json")),
		"cat " + q(filepath.Join(l.attached, "workflow.json")),
		"echo x > " + q(filepath.Join(l.attached, "outside.txt")),
		"cat " + q(filepath.Join(l.app, "config.json")),
		"cat " + q(filepath.Join(l.app, "state", "auth", "session.txt")),
		"cat " + q(filepath.Join(l.otherRuntime, "data.txt")),
		"echo x > ../outside.txt",
		"cat " + q(filepath.Join(l.root, "my-notes", "note.txt")),
	}
	if runtime.GOOS == "darwin" {
		steps = append(steps, `/usr/bin/osascript -e 'do shell script "touch `+osaMarker+`"'`)
	}
	var prompt strings.Builder
	prompt.WriteString("File-access check on disposable test fixtures that a test harness just created for this run (random marker text only; nothing real). ")
	prompt.WriteString("Please run each numbered item once, in order, from your working folder, using only your own shell and your own file-edit tool (not the api-bridge tools). ")
	prompt.WriteString("Some items are expected to fail with a permission error in this environment: that is a normal, intended result. Just report what happened for each item and continue with the next one. ")
	prompt.WriteString("For each item that prints file contents, copy the printed text into your reply verbatim.\n")
	for i, step := range steps {
		fmt.Fprintf(&prompt, "%d. %s\n", i+1, step)
	}
	prompt.WriteString("Finish with one line per item: number, worked or failed, and the printed text or exact error message.")

	answer, err := a.ask(ctx, prompt.String())
	if err != nil {
		t.Fatalf("%s did not finish the turn (an approval or trust screen also ends here): %v", tc.name, err)
	}
	t.Logf("%s reply:\n%.3000s", tc.name, answer)
	for _, receipt := range listener.receipts() {
		if strings.Contains(receipt.name, "execute_shell_command") || strings.Contains(receipt.name, "diff_patch_workspace_file") {
			t.Errorf("%s used the bridge tool %s; the contract is about its own tools", tc.name, receipt.name)
		}
	}

	read := func(path string) string {
		data, _ := os.ReadFile(path) // #nosec G304 -- test-owned fixture
		return string(data)
	}
	// Allowed.
	if !strings.Contains(read(filepath.Join(l.workflow, "soul", "soul.md")), editToken) {
		t.Errorf("%s: its own edit tool could not change project/soul/soul.md (its workflow)", tc.name)
	}
	if !strings.Contains(read(filepath.Join(l.runtime, "own.txt")), ownToken) {
		t.Errorf("%s: could not write its own runtime folder", tc.name)
	}
	if !strings.Contains(answer, l.tokens["attached"]) {
		t.Errorf("%s: could not read the attached workflow", tc.name)
	}
	if !strings.Contains(answer, l.tokens["personal"]) {
		t.Errorf("%s: could not read the person's own files (the home stays open)", tc.name)
	}
	// Refused.
	if strings.TrimSpace(read(filepath.Join(l.workflow, "planning", "plan.json"))) != "{}" {
		t.Errorf("%s: wrote the protected planning/plan.json", tc.name)
	}
	for _, name := range []string{"other", "config", "auth", "otherRuntime"} {
		if strings.Contains(answer, l.tokens[name]) {
			t.Errorf("%s: read a protected file (%s)", tc.name, name)
		}
	}
	for _, path := range []string{filepath.Join(l.attached, "outside.txt"), filepath.Join(filepath.Dir(l.runtime), "outside.txt"), osaMarker} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("%s: wrote %s, which must be refused", tc.name, path)
		}
	}
	if !t.Failed() {
		t.Logf("CLI_SANDBOX_CONTRACT_PASS provider=%s", tc.name)
	}
}
