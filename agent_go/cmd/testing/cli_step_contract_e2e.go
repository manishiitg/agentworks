package testing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

// cli-step-contract is the workflow-step half of the sandbox contract: the
// permissions a workflow's own steps run with. A step turn is bridge-only
// (mcp_only) by design, so this also covers that mode for a real CLI.
//
// A two-step workflow is built: an agent step (message_sequence) that runs a
// harness-written file-access script through the api-bridge shell, and a
// scripted step whose saved main.py runs the same script. Both write their
// results into their own execution folder. Run mode starts the workflow
// through a real chat (run_full_workflow); every verdict is read from the disk.
//
// Expected, for a step: its own execution folder is writable; the workflow's
// planning/, any other workflow, an unattached workflow, and a private
// workspace folder are not.
var cliStepContractFlags struct {
	serverURL     string
	provider      string
	model         string
	workspaceDocs string
	timeout       time.Duration
	keepFixture   bool
}

const (
	stepContractAgentID  = "step-contract-agent"
	stepContractScriptID = "step-contract-script"
)

var cliStepContractCmd = &cobra.Command{
	Use:   "cli-step-contract",
	Short: "Run the workflow-step permission contract (agent step and scripted step) through a live server",
	RunE: func(cmd *cobra.Command, args []string) error {
		provider := strings.TrimSpace(cliStepContractFlags.provider)
		model := strings.TrimSpace(cliStepContractFlags.model)
		if model == "" {
			model = defaultCodingAgentE2EModel(provider)
		}
		timeout := cliStepContractFlags.timeout
		if timeout <= 0 {
			timeout = 12 * time.Minute
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		workflowAutoNotificationE2EFlags.workspaceDocs = cliStepContractFlags.workspaceDocs
		docs, err := resolveWorkflowAutoNotificationWorkspaceDocs()
		if err != nil {
			return err
		}
		layout, cleanup, err := newCLISandboxContractLayoutFor(docs, "", cliStepContractFlags.keepFixture, provider, model, true)
		if err != nil {
			return err
		}
		defer cleanup()
		if err := layout.writeStepPlan(); err != nil {
			return err
		}

		client := &codingAgentChatE2EClient{
			baseURL:        strings.TrimRight(cliStepContractFlags.serverURL, "/"),
			token:          strings.TrimSpace(os.Getenv("AGENTWORKS_AUTH_TOKEN")),
			http:           &http.Client{Timeout: 90 * time.Second},
			agentMode:      "workflow_phase",
			selectedFolder: layout.relMain,
			presetQueryID:  layout.presetID,
			phaseID:        "workflow-builder",
			workshopMode:   "run",
			enabledServers: "api-bridge",
			timeout:        timeout,
		}
		if err := client.ensureUserAuth(ctx); err != nil {
			return err
		}
		sessionID := "cli-step-contract-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
		fmt.Printf("cli-step-contract provider=%s model=%s session=%s workflow=%s\n", provider, model, sessionID, layout.relMain)
		query := `Call the run_full_workflow tool exactly once with group_name="default". If it is not a direct tool in this session, call it through the MCP bridge API that get_api_spec describes. Do not call execute_step and do not start the workflow any other way. After run_full_workflow returns, reply exactly RUN_WORKFLOW_TOOL_STARTED. Do not ask a question.`
		if _, err := client.startQuery(ctx, sessionID, provider, model, query); err != nil {
			return fmt.Errorf("start the run turn: %w", err)
		}
		defer func() { _ = client.stopSession(ctx, sessionID) }()

		for _, id := range []string{stepContractAgentID, stepContractScriptID} {
			if err := layout.waitForStepResult(ctx, id); err != nil {
				layout.printStepSessionTail(id)
				return err
			}
		}
		layout.printStepInfo()
		failures := layout.verifyStepResults()
		if len(failures) > 0 {
			for _, failure := range failures {
				fmt.Printf("FAIL %s\n", failure)
			}
			return fmt.Errorf("%d step contract check(s) failed for %s", len(failures), provider)
		}
		fmt.Printf("PASS cli-step-contract provider=%s\n", provider)
		return nil
	},
}

func init() {
	f := cliStepContractCmd.Flags()
	f.StringVar(&cliStepContractFlags.serverURL, "server-url", "http://localhost:18743", "coding-agent-loop server URL")
	f.StringVar(&cliStepContractFlags.provider, "provider", "claude-code", "coding CLI that runs the chat and the agent step")
	f.StringVar(&cliStepContractFlags.model, "model", "", "model ID; defaults to the provider-specific E2E model")
	f.StringVar(&cliStepContractFlags.workspaceDocs, "workspace-docs", "", "absolute path to the server's workspace-docs")
	f.DurationVar(&cliStepContractFlags.timeout, "timeout", 12*time.Minute, "overall timeout")
	f.BoolVar(&cliStepContractFlags.keepFixture, "keep-fixture", false, "keep the fixtures for debugging")
}

func (l *cliSandboxContractLayout) stepOutDir(stepID string) string {
	return filepath.Join(l.absMain, "runs", "iteration-0", "default", "execution", stepID)
}

func (l *cliSandboxContractLayout) stepResultPath(stepID string) string {
	return filepath.Join(l.stepOutDir(stepID), "contract-results.txt")
}

// stepScriptPath: a step's execution folder is emptied when the step starts, so
// the script lives in the workflow's db/ folder, which steps read.
func (l *cliSandboxContractLayout) stepScriptPath(stepID string) string {
	return filepath.Join(l.absMain, "db", "contract-check", stepID+"-run.sh")
}

// stepInfoChecks are reported, not asserted: what a step may do with its own
// workflow is a design question the owner decides (an agent step could not run
// a script from the workflow's code/ folder through the bridge shell).
func (l *cliSandboxContractLayout) stepInfoChecks() []struct{ label, cmd string } {
	q := shellSingleQuoteE2E
	return []struct{ label, cmd string }{
		{"read own workflow.json", "cat " + q(filepath.Join(l.absMain, "workflow.json")) + " >/dev/null"},
		{"read own code/ folder", "cat " + q(filepath.Join(l.absMain, "code", "contract-check", "notes.md")) + " >/dev/null"},
		{"write own code/ folder", "echo x >> " + q(filepath.Join(l.absMain, "code", "contract-check", "notes.md"))},
		{"write own db/ folder", "echo x > " + q(filepath.Join(l.absMain, "db", "contract-write.txt"))},
		{"read own planning/ folder", "cat " + q(filepath.Join(l.absMain, "planning", "plan.json")) + " >/dev/null"},
	}
}

// stepScript is the harness script both steps run: the contract's file actions
// plus a write to the step's own execution folder, results appended to $1.
func (l *cliSandboxContractLayout) stepScript() string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n# Written by the cli-step-contract harness. $1 = results file, $2 = the step's execution folder.\n")
	b.WriteString("res=\"$1\"; dir=\"$2\"\n")
	b.WriteString(": > \"$res\" 2>/dev/null\n")
	b.WriteString("out=$( ( echo own > \"$dir/own-output.txt\" ) 2>&1 ) && r=worked || r=failed\n")
	b.WriteString("{ echo '== own'; echo \"result: $r\"; echo \"$out\"; } >> \"$res\"\n")
	for i, info := range l.stepInfoChecks() {
		fmt.Fprintf(&b, "out=$( ( %s ) 2>&1 ) && r=worked || r=failed\n{ echo '== info %d: %s'; echo \"result: $r\"; } >> \"$res\" 2>/dev/null\n", info.cmd, i+1, info.label)
	}
	for i, check := range l.checks() {
		fmt.Fprintf(&b, "out=$( ( %s ) 2>&1 ) && r=worked || r=failed\n{ echo '== check %d'; echo \"result: $r\"; echo \"$out\"; } >> \"$res\" 2>/dev/null\n", check, i+1)
	}
	return b.String()
}

// writeStepPlan replaces the fixture's plan with the two contract steps and
// locks the scripted step's saved code.
func (l *cliSandboxContractLayout) writeStepPlan() error {
	if err := os.MkdirAll(l.stepOutDir(stepContractAgentID), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(l.stepOutDir(stepContractScriptID), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(l.absMain, "db", "contract-check"), 0o755); err != nil {
		return err
	}
	for _, id := range []string{stepContractAgentID, stepContractScriptID} {
		if err := os.WriteFile(l.stepScriptPath(id), []byte(l.stepScript()), 0o755); err != nil {
			return err
		}
	}
	q := shellSingleQuoteE2E
	// The agent step cannot read files from the workflow's db/ or code/ folders and
	// its own execution folder is emptied at start, so the script reaches it the way
	// production data does: an upstream step writes it into its output folder and the
	// agent step declares it as a context dependency.
	agentScriptName := "contract-agent-script.sh"
	agentScriptPath := filepath.Join(l.stepOutDir(stepContractScriptID), agentScriptName)
	agentMessage := fmt.Sprintf("Use the declared MCP api-bridge execute_shell_command tool, never a built-in shell/file tool, to run exactly this command and nothing else:\nsh %s %s %s\nIt is a platform file-access verification; some of its actions are expected to be refused by the sandbox and that is the intended result. Do not read the script, do not retry or work around anything. After the command finishes, reply exactly:\nCONTRACT_STEP_DONE\nSTATUS: COMPLETED",
		q(agentScriptPath), q(l.stepResultPath(stepContractAgentID)), q(l.stepOutDir(stepContractAgentID)))
	scriptMain := fmt.Sprintf(`import os, subprocess, sys
out_dir = os.environ.get("STEP_OUTPUT_DIR") or %q
res = os.path.join(out_dir, "contract-results.txt")
with open(os.path.join(out_dir, %q), "w") as f:
    f.write(%q)
subprocess.run(["sh", %q, res, out_dir], check=False)
print("CONTRACT_SCRIPT_DONE")
`, l.stepOutDir(stepContractScriptID), agentScriptName, l.stepScript(), l.stepScriptPath(stepContractScriptID))
	if err := os.MkdirAll(filepath.Join(l.absMain, "code", stepContractScriptID), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(l.absMain, "code", stepContractScriptID, "main.py"), []byte(scriptMain), 0o644); err != nil {
		return err
	}
	plan := map[string]interface{}{"steps": []map[string]interface{}{
		{
			"type": "regular", "id": stepContractScriptID, "title": "Contract scripted step",
			"description":          "Run the harness file-access script from saved code and leave the agent step's script in the output folder.",
			"context_dependencies": []string{}, "context_output": agentScriptName,
		},
		{
			"type": "message_sequence", "id": stepContractAgentID, "title": "Contract agent step",
			"description":          "Run the harness file-access script and report that it finished.",
			"context_dependencies": []string{agentScriptName}, "context_output": "",
			"items": []map[string]interface{}{{"id": "run-script", "type": "user_message", "message": agentMessage}},
		},
	}}
	stepConfig := map[string]interface{}{"steps": []map[string]interface{}{
		{"id": stepContractScriptID, "title": "Contract scripted step", "agent_configs": map[string]interface{}{"use_code_execution_mode": true, "lock_code": true}},
	}}
	if err := writeJSONFile(filepath.Join(l.absMain, "planning", "plan.json"), plan); err != nil {
		return err
	}
	if err := writeJSONFile(filepath.Join(l.absMain, "planning", "step_config.json"), stepConfig); err != nil {
		return err
	}
	baseline, _ := os.ReadFile(filepath.Join(l.absMain, "planning", "plan.json"))
	l.planBaseline = string(baseline)
	return nil
}

func (l *cliSandboxContractLayout) waitForStepResult(ctx context.Context, stepID string) error {
	path := l.stepResultPath(stepID)
	for {
		data, err := os.ReadFile(path) // #nosec G304 -- fixture
		// The script writes the file in one pass and its last check is the
		// private-folder read: wait for the final block, not just the file.
		if err == nil && strings.Contains(string(data), fmt.Sprintf("== check %d", len(l.checks()))) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("step %s never wrote its results (%s): %w", stepID, path, ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
}

// verifyStepResults reads each step's results from the disk.
func (l *cliSandboxContractLayout) verifyStepResults() []string {
	var failures []string
	for _, id := range []string{stepContractAgentID, stepContractScriptID} {
		data, _ := os.ReadFile(l.stepResultPath(id)) // #nosec G304 -- fixture
		results := string(data)
		fail := func(format string, args ...interface{}) {
			failures = append(failures, id+": "+fmt.Sprintf(format, args...))
		}
		if !strings.Contains(results, "== own\nresult: worked") {
			fail("could not write its own execution folder")
		}
		for _, name := range []string{"other", "attached", "private"} {
			if strings.Contains(results, l.tokens[name]) {
				fail("read a folder outside its grants (%s)", name)
			}
		}
		for _, path := range []string{filepath.Join(l.absAttached, "outside.txt"), filepath.Join(l.absOther, "outside.txt")} {
			if _, err := os.Stat(path); err == nil {
				fail("wrote %s, which must be refused", path)
			}
		}
	}
	if current, _ := os.ReadFile(filepath.Join(l.absMain, "planning", "plan.json")); string(current) != l.planBaseline {
		failures = append(failures, "a step wrote the protected planning/plan.json")
	}
	return failures
}

// printStepInfo prints each step's informational results (see stepInfoChecks).
func (l *cliSandboxContractLayout) printStepInfo() {
	for _, id := range []string{stepContractAgentID, stepContractScriptID} {
		data, _ := os.ReadFile(l.stepResultPath(id)) // #nosec G304 -- fixture
		lines := strings.Split(string(data), "\n")
		fmt.Printf("info (%s):\n", id)
		for i, line := range lines {
			if strings.HasPrefix(line, "== info") && i+1 < len(lines) {
				fmt.Printf("  %s -> %s\n", strings.TrimPrefix(line, "== "), strings.TrimPrefix(lines[i+1], "result: "))
			}
		}
	}
}

// printStepSessionTail prints the last messages of a step's conversation, to
// show why it never wrote its results.
func (l *cliSandboxContractLayout) printStepSessionTail(stepID string) {
	data, err := os.ReadFile(filepath.Join(l.stepOutDir(stepID), "session.json")) // #nosec G304 -- fixture
	if err != nil {
		fmt.Printf("(no session.json for %s: %v)\n", stepID, err)
		return
	}
	var session struct {
		History []struct {
			Role  string
			Parts []map[string]interface{}
		} `json:"conversation_history"`
	}
	if json.Unmarshal(data, &session) != nil {
		return
	}
	start := len(session.History) - 3
	if start < 0 {
		start = 0
	}
	for _, message := range session.History[start:] {
		for _, part := range message.Parts {
			raw, _ := json.Marshal(part)
			fmt.Printf("  [%s] %s\n", message.Role, truncateE2E(string(raw), 900))
		}
	}
}
