package testing

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
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
		failures = append(failures, layout.verifyDatabase(ctx)...)
		failures = append(failures, layout.verifyRouteTool(ctx)...)
		failures = append(failures, layout.verifyAuthoredAgent(ctx)...)
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
	if err := l.createContractDB(); err != nil {
		return err
	}
	scriptMain := fmt.Sprintf(`import os, subprocess, sys
out_dir = os.environ.get("STEP_OUTPUT_DIR") or %q
res = os.path.join(out_dir, "contract-results.txt")
with open(os.path.join(out_dir, %q), "w") as f:
    f.write(%q)
subprocess.run(["sh", %q, res, out_dir], check=False)
`, l.stepOutDir(stepContractScriptID), agentScriptName, l.stepScript(), l.stepScriptPath(stepContractScriptID))
	scriptMain += stepContractDBScript + "print(\"CONTRACT_SCRIPT_DONE\")\n"
	if err := l.writeRouteScript(); err != nil {
		return err
	}
	if err := l.writeAuthoredRouteScript(); err != nil {
		return err
	}
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
			"items": []map[string]interface{}{
				{"id": "run-script", "type": "user_message", "message": agentMessage},
				{"id": "db-write", "type": "user_message", "message": stepContractAgentDBMessage},
				{"id": "route-tool", "type": "user_message", "message": l.routeToolMessage()},
			},
			"predefined_routes": []map[string]interface{}{{
				"route_id": stepContractRouteID, "route_name": "Contract lookup",
				"condition": "When a customer record is needed by id",
				"sub_agent_step": map[string]interface{}{
					"type": "regular", "id": stepContractRouteStepID, "title": "Contract lookup",
					"description":          "Return the customer record for one id as JSON.",
					"context_dependencies": []string{}, "context_output": "",
					"script_parameters": map[string]interface{}{
						"customer_id": map[string]interface{}{"type": "string", "description": "The customer id to look up", "required": true},
					},
				},
			}},
		},
		{
			// PLAT-441: an authored agent (a Relay agent's shape) owning a saved
			// script tool keeps its own system prompt and JSON answer.
			"type": "message_sequence", "id": stepContractAuthoredID, "title": "Contract authored agent",
			"description":          "Answer with the customer's name as JSON.",
			"context_dependencies": []string{}, "context_output": "",
			"authored_prompt": true,
			"system_prompt":   "You look up customers. Call your tool " + stepContractAuthoredTool + " with the customer id you are given. Answer with exactly one JSON object and nothing else: {\"name\": \"<the name field the tool returned>\"}.",
			"items": []map[string]interface{}{
				{"id": "ask", "type": "user_message", "message": "Look up customer " + stepContractCustomerID + "."},
			},
			"predefined_routes": []map[string]interface{}{{
				"route_id": stepContractAuthoredRouteID, "route_name": "Authored lookup",
				"condition": "When a customer record is needed by id",
				"sub_agent_step": map[string]interface{}{
					"type": "regular", "id": stepContractAuthoredRouteID, "title": "Authored lookup",
					"description":          "Return the customer record for one id as JSON.",
					"context_dependencies": []string{}, "context_output": "", "script_only": true,
					"script_parameters": map[string]interface{}{
						"customer_id": map[string]interface{}{"type": "string", "description": "The customer id to look up", "required": true},
					},
				},
			}},
		},
	}}
	stepConfig := map[string]interface{}{"steps": []map[string]interface{}{
		{"id": stepContractScriptID, "title": "Contract scripted step", "agent_configs": map[string]interface{}{"use_code_execution_mode": true, "lock_code": true}},
		{"id": stepContractRouteStepID, "title": "Contract lookup", "agent_configs": map[string]interface{}{"use_code_execution_mode": true, "lock_code": true}},
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

// The database half of the contract: a scripted step reaches the workflow
// database through the built-in agentworks_db helper (bulk write, transaction,
// paged read, and a schema statement that must be refused); an agent step through
// mutate_workflow_db. Both are checked from the database file on disk.
const stepContractDBScript = `
db_lines = []
try:
    from agentworks_db import DBError, execute, execute_many, query, scalar, transaction
    execute_many("INSERT INTO contract_rows(label) VALUES (?)", [["script-%d" % i] for i in range(3)])
    transaction([("UPDATE contract_rows SET label = ? WHERE label = ?", ["script-renamed", "script-0"]), ("DELETE FROM contract_rows WHERE label = ?", ["script-1"])])
    labels = sorted(row["label"] for row in query("SELECT label FROM contract_rows WHERE label LIKE 'script-%' ORDER BY label"))
    db_lines.append("helper-rows: " + ",".join(labels))
    db_lines.append("helper-count: %d" % scalar("SELECT COUNT(*) FROM contract_rows WHERE label LIKE 'script-%'"))
    try:
        execute("CREATE TABLE contract_ddl (a INTEGER)")
        db_lines.append("ddl: ALLOWED")
    except DBError:
        db_lines.append("ddl: refused")
except Exception as error:
    db_lines.append("helper-error: %s: %s" % (type(error).__name__, error))
with open(os.path.join(out_dir, "db-helper-results.txt"), "w") as f:
    f.write("\n".join(db_lines) + "\n")
`

const stepContractAgentDBMessage = "Now call the MCP tool mutate_workflow_db exactly once with sql \"INSERT INTO contract_rows(label) VALUES (?)\" and params [\"agent-step\"], then call query_workflow_db once with sql \"SELECT COUNT(*) AS n FROM contract_rows WHERE label = ?\" and params [\"agent-step\"]. Report the count. Do nothing else."

// The named route tool (PLAT-432): the agent step owns a saved scripted route,
// calls it by its own tool name, and writes down a value only the script knows.
const (
	stepContractRouteID     = "contract-lookup"
	stepContractRouteStepID = stepContractRouteID // a route and its step share one id
	stepContractRouteTool   = "contract_lookup"
	stepContractCustomerID  = "c-42"
)

func (l *cliSandboxContractLayout) routeToken() string { return "ROUTE_" + l.tokens["private"] }

const (
	stepContractAuthoredID      = "step-contract-authored"
	stepContractAuthoredRouteID = "authored-lookup"
	stepContractAuthoredTool    = "authored_lookup"
)

func (l *cliSandboxContractLayout) authoredToken() string { return "AUTHORED_" + l.tokens["private"] }

func (l *cliSandboxContractLayout) writeAuthoredRouteScript() error {
	dir := filepath.Join(l.absMain, "code", stepContractAuthoredRouteID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	script := fmt.Sprintf(`import json, os
params = json.loads(os.environ["STEP_PARAMS_JSON"])
with open(os.path.join(os.environ["STEP_OUTPUT_DIR"], "route_result.json"), "w") as f:
    json.dump({"customer_id": params["customer_id"], "name": %q}, f)
`, l.authoredToken())
	return os.WriteFile(filepath.Join(dir, "main.py"), []byte(script), 0o644)
}

// verifyAuthoredAgent checks the authored agent's final JSON carries the value
// only its script tool returns.
func (l *cliSandboxContractLayout) verifyAuthoredAgent(ctx context.Context) []string {
	summary := filepath.Join(l.absMain, "runs", "iteration-0", "default", "logs", stepContractAuthoredID, "execution", "execution-final-summary.json")
	deadline := time.Now().Add(5 * time.Minute)
	var data []byte
	for {
		data, _ = os.ReadFile(summary) // #nosec G304 -- fixture
		if len(data) > 0 || time.Now().After(deadline) || ctx.Err() != nil {
			break
		}
		time.Sleep(3 * time.Second)
	}
	if !strings.Contains(string(data), l.authoredToken()) {
		return []string{fmt.Sprintf("authored agent: final answer lacks the script tool's value %q (prompt lost, or tool not called): %s", l.authoredToken(), strings.TrimSpace(string(data)))}
	}
	return nil
}

func (l *cliSandboxContractLayout) routeToolResultPath() string {
	return filepath.Join(l.stepOutDir(stepContractAgentID), "route-tool.txt")
}

func (l *cliSandboxContractLayout) writeRouteScript() error {
	dir := filepath.Join(l.absMain, "code", stepContractRouteStepID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	script := fmt.Sprintf(`import json, os
params = json.loads(os.environ["STEP_PARAMS_JSON"])
out = os.environ["STEP_OUTPUT_DIR"]
with open(os.path.join(out, "params.json"), "w") as f:
    json.dump(params, f)
with open(os.path.join(out, "route_result.json"), "w") as f:
    json.dump({"customer_id": params["customer_id"], "name": %q}, f)
print("ROUTE_DONE")
`, l.routeToken())
	return os.WriteFile(filepath.Join(dir, "main.py"), []byte(script), 0o644)
}

func (l *cliSandboxContractLayout) routeToolMessage() string {
	return fmt.Sprintf("Now call the tool %s with customer_id %q (it is one of your tools; if it is not listed directly, find it with search_tools or the MCP bridge API, and do not use call_scripted_sub_agent). It returns JSON with a name field. Then use the MCP api-bridge execute_shell_command tool to write exactly that name value, nothing else, into %s, for example: printf '%%s' '<name>' > %s. Do nothing else.",
		stepContractRouteTool, stepContractCustomerID, l.routeToolResultPath(), shellSingleQuoteE2E(l.routeToolResultPath()))
}

// verifyRouteTool checks the agent called the named route tool and got its JSON.
func (l *cliSandboxContractLayout) verifyRouteTool(ctx context.Context) []string {
	deadline := time.Now().Add(3 * time.Minute)
	var got string
	for {
		data, _ := os.ReadFile(l.routeToolResultPath()) // #nosec G304 -- fixture
		got = strings.TrimSpace(string(data))
		if got != "" || time.Now().After(deadline) || ctx.Err() != nil {
			break
		}
		time.Sleep(3 * time.Second)
	}
	var failures []string
	if got != l.routeToken() {
		failures = append(failures, fmt.Sprintf("route tool: the agent wrote %q, want the script's value %q (named tool not called, or its JSON not returned)", got, l.routeToken()))
	}
	matches, _ := filepath.Glob(filepath.Join(l.stepOutDir(stepContractAgentID), "scripts", "routes", stepContractRouteID, "calls", "*", "params.json"))
	if len(matches) != 1 {
		failures = append(failures, fmt.Sprintf("route tool: want exactly one route call folder with params.json, found %d", len(matches)))
	} else if data, _ := os.ReadFile(matches[0]); !strings.Contains(string(data), stepContractCustomerID) { // #nosec G304 -- fixture
		failures = append(failures, "route tool: the script did not receive customer_id "+stepContractCustomerID+": "+string(data))
	}
	return failures
}

func (l *cliSandboxContractLayout) contractDBPath() string {
	return filepath.Join(l.absMain, "db", "db.sqlite")
}

// createContractDB makes the table the steps use. Schema changes are a Builder
// migration in production; the harness is not a step, so it creates the file.
func (l *cliSandboxContractLayout) createContractDB() error {
	out, err := exec.Command("python3", "-c", "import sqlite3,sys; c=sqlite3.connect(sys.argv[1]); c.execute('CREATE TABLE IF NOT EXISTS contract_rows (id INTEGER PRIMARY KEY, label TEXT NOT NULL UNIQUE)'); c.commit()", l.contractDBPath()).CombinedOutput()
	if err != nil {
		return fmt.Errorf("create the contract database: %w: %s", err, out)
	}
	return nil
}

func (l *cliSandboxContractLayout) databaseLabels() (labels []string, ddlTable bool, err error) {
	out, err := exec.Command("python3", "-c", "import sqlite3,sys; c=sqlite3.connect('file:'+sys.argv[1]+'?mode=ro', uri=True); print('\\n'.join(r[0] for r in c.execute('SELECT label FROM contract_rows ORDER BY label'))); print('DDL=%d' % c.execute(\"SELECT COUNT(*) FROM sqlite_master WHERE name='contract_ddl'\").fetchone()[0])", l.contractDBPath()).CombinedOutput()
	if err != nil {
		return nil, false, fmt.Errorf("read the contract database: %w: %s", err, out)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		switch {
		case line == "DDL=1":
			ddlTable = true
		case line != "" && line != "DDL=0":
			labels = append(labels, line)
		}
	}
	return labels, ddlTable, nil
}

// verifyDatabase checks both steps' writes. The agent step's insert can land a
// little after its results file, so it is awaited.
func (l *cliSandboxContractLayout) verifyDatabase(ctx context.Context) []string {
	var failures []string
	deadline := time.Now().Add(2 * time.Minute)
	var labels []string
	var ddlTable bool
	for {
		var err error
		if labels, ddlTable, err = l.databaseLabels(); err != nil {
			return append(failures, err.Error())
		}
		if strings.Contains(","+strings.Join(labels, ",")+",", ",agent-step,") || time.Now().After(deadline) || ctx.Err() != nil {
			break
		}
		time.Sleep(3 * time.Second)
	}
	joined := "," + strings.Join(labels, ",") + ","
	if !strings.Contains(joined, ",agent-step,") {
		failures = append(failures, "agent: mutate_workflow_db did not insert its row")
	}
	if strings.Count(joined, ",agent-step,") > 1 {
		failures = append(failures, "agent: the insert ran more than once")
	}
	if !strings.Contains(joined, ",script-renamed,") || !strings.Contains(joined, ",script-2,") || strings.Contains(joined, ",script-0,") || strings.Contains(joined, ",script-1,") {
		failures = append(failures, "script: the helper's bulk write and transaction did not leave script-renamed and script-2 only; rows="+strings.Join(labels, ","))
	}
	if ddlTable {
		failures = append(failures, "script: a CREATE TABLE went through the helper")
	}
	data, _ := os.ReadFile(filepath.Join(l.stepOutDir(stepContractScriptID), "db-helper-results.txt")) // #nosec G304 -- fixture
	results := string(data)
	for _, want := range []string{"helper-rows: script-2,script-renamed", "helper-count: 2", "ddl: refused"} {
		if !strings.Contains(results, want) {
			failures = append(failures, fmt.Sprintf("script: helper result missing %q: %s", want, strings.TrimSpace(results)))
		}
	}
	return failures
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
