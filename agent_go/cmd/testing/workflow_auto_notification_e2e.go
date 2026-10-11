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

	server "github.com/manishiitg/coding-agent-loop/agent_go/cmd/server"
	"github.com/spf13/cobra"
)

var workflowAutoNotificationE2EFlags struct {
	serverURL     string
	provider      string
	model         string
	sessionID     string
	workspaceDocs string
	timeout       time.Duration
	keepFixture   bool
}

var workflowAutoNotificationE2ECmd = &cobra.Command{
	Use:   "workflow-auto-notification-e2e",
	Short: "Run a real workflow AUTO-notification e2e against a live builder server",
	Long: `Runs a real end-to-end check through the live coding-agent-loop HTTP API.

This test:
1. Creates a real temporary workflow under workspace-docs/Workflow.
2. Starts a real workflow Run-mode turn and asks it to call run_full_workflow.
3. Polls /api/sessions/{session_id}/events until the typed
   background_agent_started event proves the workflow launched.
4. Runs a real plan step that writes and reads a file through api-bridge.
5. Waits for the step-completion [AUTO-NOTIFICATION] and verifies that it
   carries only the final assistant result, never the MCP call/output trail.
6. Stops the chat session with cancelAgents=true.

It intentionally does not call runWorkflowInternal directly and does not use
the private session-scoped custom-tool HTTP endpoint.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		provider := strings.TrimSpace(workflowAutoNotificationE2EFlags.provider)
		model := strings.TrimSpace(workflowAutoNotificationE2EFlags.model)
		if model == "" {
			model = defaultCodingAgentE2EModel(provider)
		}
		if provider == "" || model == "" {
			return fmt.Errorf("provider and model are required")
		}

		sessionID := strings.TrimSpace(workflowAutoNotificationE2EFlags.sessionID)
		if sessionID == "" {
			sessionID = fmt.Sprintf("workflow-auto-notification-e2e-%s", strings.ReplaceAll(uuid.NewString(), "-", ""))
		}

		timeout := workflowAutoNotificationE2EFlags.timeout
		if timeout <= 0 {
			timeout = 8 * time.Minute
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		workspaceDocs, err := resolveWorkflowAutoNotificationWorkspaceDocs()
		if err != nil {
			return err
		}
		fixture, cleanup, err := createWorkflowAutoNotificationFixture(workspaceDocs, workflowAutoNotificationE2EFlags.keepFixture, provider, model)
		if err != nil {
			return err
		}
		defer cleanup()
		relWorkflow := fixture.relWorkflow

		client := &codingAgentChatE2EClient{
			baseURL:        strings.TrimRight(workflowAutoNotificationE2EFlags.serverURL, "/"),
			token:          strings.TrimSpace(os.Getenv("AGENTWORKS_AUTH_TOKEN")),
			http:           &http.Client{Timeout: 90 * time.Second},
			agentMode:      "workflow_phase",
			selectedFolder: fixture.relWorkflow,
			presetQueryID:  fixture.presetID,
			phaseID:        "workflow-builder",
			workshopMode:   "run",
			enabledServers: "api-bridge",
			timeout:        timeout,
		}
		if err := client.ensureUserAuth(ctx); err != nil {
			return err
		}

		fmt.Printf("Running workflow auto-notification e2e provider=%s model=%s session=%s workflow=%s\n", provider, model, sessionID, relWorkflow)

		// run_full_workflow is a direct tool for some providers and, in code
		// execution mode (Codex), reachable only through the MCP bridge's HTTP
		// API (get_api_spec, then POST /tools/custom/run_full_workflow). The old
		// "do not use shell/curl" wording forbade Codex's only route, so it
		// passed or refused depending on whether the model obeyed it.
		query := `Call the run_full_workflow tool exactly once with group_name="default". If it is not a direct tool in this session, call it through the MCP bridge API that get_api_spec describes. Do not call execute_step and do not start the workflow any other way. After run_full_workflow returns, reply exactly RUN_WORKFLOW_TOOL_STARTED. Do not ask a question.`
		queryID, err := client.startQuery(ctx, sessionID, provider, model, query)
		if err != nil {
			return fmt.Errorf("start main agent run_workflow turn: %w", err)
		}
		fmt.Printf("Started main agent query=%s; waiting for workflow start event\n", queryID)

		agentID, err := client.waitForWorkflowStartEvidence(ctx, sessionID, 3*time.Minute)
		if err != nil {
			return err
		}
		fmt.Printf("Observed workflow start for agent_id=%s\n", agentID)

		if err := client.waitForWorkflowCompletionAutoNotification(ctx, sessionID, fixture.completionToken, 5*time.Minute); err != nil {
			return err
		}
		proof, err := os.ReadFile(fixture.bridgeProofPath)
		if err != nil {
			return fmt.Errorf("workflow plan step did not create MCP bridge proof %s: %w", fixture.bridgeProofPath, err)
		}
		if strings.TrimSpace(string(proof)) != fixture.completionToken {
			return fmt.Errorf("MCP bridge proof = %q, want %q", strings.TrimSpace(string(proof)), fixture.completionToken)
		}
		fmt.Println("Observed clean workflow completion AUTO notification after MCP bridge file operation")

		_ = client.stopSession(ctx, sessionID)

		fmt.Println("PASS workflow MCP-bridge completion auto-notification e2e")
		return nil
	},
}

func init() {
	workflowAutoNotificationE2ECmd.Flags().StringVar(&workflowAutoNotificationE2EFlags.serverURL, "server-url", "http://localhost:18743", "coding-agent-loop server URL")
	workflowAutoNotificationE2ECmd.Flags().StringVar(&workflowAutoNotificationE2EFlags.provider, "provider", "codex-cli", "provider used for the main multi-agent chat turn")
	workflowAutoNotificationE2ECmd.Flags().StringVar(&workflowAutoNotificationE2EFlags.model, "model", "", "model ID; defaults to the provider-specific E2E model")
	workflowAutoNotificationE2ECmd.Flags().StringVar(&workflowAutoNotificationE2EFlags.sessionID, "session-id", "", "session ID to reuse; generated when omitted")
	workflowAutoNotificationE2ECmd.Flags().StringVar(&workflowAutoNotificationE2EFlags.workspaceDocs, "workspace-docs", "", "absolute path to workspace-docs; defaults to WORKSPACE_DOCS_PATH or ../workspace-docs")
	workflowAutoNotificationE2ECmd.Flags().DurationVar(&workflowAutoNotificationE2EFlags.timeout, "timeout", 8*time.Minute, "overall test timeout")
	workflowAutoNotificationE2ECmd.Flags().BoolVar(&workflowAutoNotificationE2EFlags.keepFixture, "keep-fixture", false, "keep the temporary workflow fixture for debugging")
}

func resolveWorkflowAutoNotificationWorkspaceDocs() (string, error) {
	if value := strings.TrimSpace(workflowAutoNotificationE2EFlags.workspaceDocs); value != "" {
		if !filepath.IsAbs(value) {
			return "", fmt.Errorf("--workspace-docs must be absolute, got %q", value)
		}
		return value, nil
	}
	if value := strings.TrimSpace(os.Getenv("WORKSPACE_DOCS_PATH")); value != "" {
		if !filepath.IsAbs(value) {
			return "", fmt.Errorf("WORKSPACE_DOCS_PATH must be absolute, got %q", value)
		}
		return value, nil
	}
	candidate := filepath.Clean("../workspace-docs")
	abs, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	if info, statErr := os.Stat(abs); statErr == nil && info.IsDir() {
		return abs, nil
	}
	return "", fmt.Errorf("workspace-docs not found; pass --workspace-docs or set WORKSPACE_DOCS_PATH")
}

type workflowAutoNotificationFixture struct {
	relWorkflow     string
	presetID        string
	completionToken string
	bridgeProofPath string
}

func createWorkflowAutoNotificationFixture(workspaceDocs string, keep bool, provider, model string) (workflowAutoNotificationFixture, func(), error) {
	shortID := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	relWorkflow := "Workflow/_e2e_auto_notification_" + shortID
	absWorkflow := filepath.Join(workspaceDocs, filepath.FromSlash(relWorkflow))
	completionToken := "WORKFLOW_AUTO_NOTIFICATION_STEP_OK_" + shortID
	presetID := "wf_auto_notification_" + shortID
	// A real workflow step may write only inside its execution folder (plus its
	// workflow DB). Keeping the proof there exercises the production folder
	// guard instead of asking the agent to bypass it.
	bridgeProofPath := filepath.Join(absWorkflow, "runs", "iteration-0", "default", "execution", "step-auto-notification", "mcp-bridge-proof.txt")
	fixture := workflowAutoNotificationFixture{
		relWorkflow:     relWorkflow,
		presetID:        presetID,
		completionToken: completionToken,
		bridgeProofPath: bridgeProofPath,
	}
	cleanup := func() {}
	if !keep {
		cleanup = func() {
			_ = os.RemoveAll(absWorkflow)
		}
	}

	if err := os.MkdirAll(filepath.Join(absWorkflow, "planning"), 0o755); err != nil {
		return workflowAutoNotificationFixture{}, cleanup, err
	}
	if err := os.MkdirAll(filepath.Join(absWorkflow, "variables"), 0o755); err != nil {
		return workflowAutoNotificationFixture{}, cleanup, err
	}

	noGlobalSecrets := []string{}
	now := time.Now().UTC().Format(time.RFC3339)
	fixtureModel := map[string]interface{}{
		"provider": provider,
		"model_id": model,
	}
	manifest := map[string]interface{}{
		"schema_version": 1,
		// A fresh fixture is created on the current workflow contract. Since
		// contract upgrades became manual-only (428623c07), run_full_workflow
		// refuses a workflow without it ("workflow_contract_migration_required:
		// v1.0.0"), which stopped the auto-notification P0 before any step ran.
		"version":             server.WorkflowContractCurrentVersion,
		"code_layout_version": 1,
		"id":                  presetID,
		"label":               "E2E Auto Notification Workflow",
		"objective":           "Exercise workflow start auto-notification wiring.",
		"capabilities": map[string]interface{}{
			"selected_servers":             []string{},
			"selected_tools":               []string{},
			"selected_skills":              []string{},
			"selected_secrets":             []string{},
			"selected_global_secret_names": noGlobalSecrets,
			"browser_mode":                 "none",
			"use_code_execution_mode":      false,
			"llm_config": map[string]interface{}{
				"schema_version": 2,
				"mode":           "explicit",
				"builder_llm":    fixtureModel,
				"pulse_llm":      fixtureModel,
				"tiered_config": map[string]interface{}{
					"tier_1": fixtureModel,
					"tier_2": fixtureModel,
					"tier_3": fixtureModel,
				},
			},
		},
		"execution_defaults": map[string]interface{}{
			"always_use_same_run": true,
			"disable_learning":    true,
			"workshop_mode":       "run",
		},
		"ownership":  map[string]interface{}{},
		"schedules":  []interface{}{},
		"created_at": now,
		"updated_at": now,
	}
	plan := map[string]interface{}{
		"steps": []map[string]interface{}{
			{
				// Under the current contract a "regular" step is a scripted step (the
				// agent authors main.py). This smoke step is conversational, so it is a
				// one-turn agent, the default step type.
				"type":        "agent",
				"id":          "step-auto-notification",
				"title":       "Auto notification smoke step",
				"description": "Run the bridge smoke command and return the completion token.",
				"items": []map[string]interface{}{{
					"id":      "run-bridge-command",
					"type":    "user_message",
					"message": fmt.Sprintf("Use the declared MCP api-bridge execute_shell_command tool, never a built-in shell/file tool, to run exactly this command:\nprintf '%%s\\n' %s > %s && cat %s\nAfter the bridge call succeeds, return exactly these two lines and nothing else. Do not include the tool name, arguments, command, or output envelope:\n%s\nSTATUS: COMPLETED", shellSingleQuoteE2E(completionToken), shellSingleQuoteE2E(bridgeProofPath), shellSingleQuoteE2E(bridgeProofPath), completionToken),
				}},
				"context_dependencies": []string{},
				"context_output":       "auto_notification.json",
			},
		},
	}
	variables := map[string]interface{}{
		"variables":       []interface{}{},
		"groups":          []map[string]interface{}{{"name": "default", "values": map[string]string{}, "enabled": true}},
		"extraction_date": now,
	}

	if err := writeJSONFile(filepath.Join(absWorkflow, "workflow.json"), manifest); err != nil {
		cleanup()
		return workflowAutoNotificationFixture{}, cleanup, err
	}
	if err := writeJSONFile(filepath.Join(absWorkflow, "planning", "plan.json"), plan); err != nil {
		cleanup()
		return workflowAutoNotificationFixture{}, cleanup, err
	}
	if err := writeJSONFile(filepath.Join(absWorkflow, "variables", "variables.json"), variables); err != nil {
		cleanup()
		return workflowAutoNotificationFixture{}, cleanup, err
	}
	return fixture, cleanup, nil
}

func shellSingleQuoteE2E(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func writeJSONFile(path string, value interface{}) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func (c *codingAgentChatE2EClient) waitForWorkflowStartEvidence(ctx context.Context, sessionID string, timeout time.Duration) (string, error) {
	start := time.Now()
	deadline := e2eDeadline(ctx, timeout)
	since := 0
	var lastRaw string
	var startedAgentID string
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return startedAgentID, err
		}
		resp, raw, err := c.getEventsSince(ctx, sessionID, since)
		if err != nil {
			return startedAgentID, err
		}
		lastRaw = raw
		since = advanceE2ECursor(since, resp.LastProcessedIndex)
		for _, event := range resp.Events {
			eventType := fmt.Sprint(event["type"])
			switch eventType {
			case "background_agent_started":
				agentID := strings.TrimSpace(eventPayloadString(event, "agent_id"))
				if agentID != "" {
					fmt.Printf("Observed workflow background start: agent_id=%s\n", agentID)
					return agentID, nil
				}
				continue
			default:
				continue
			}
		}
		// Some CLI-backed turns can briefly report the HTTP session as completed
		// before the provider finishes dispatching tool calls. Keep the workflow
		// fixture alive and wait for the actual background_agent_started event
		// instead of treating that transient status as proof the workflow was not
		// launched.
		if resp.SessionStatus == "error" || resp.SessionStatus == "stopped" {
			return startedAgentID, fmt.Errorf("session ended with status %s before workflow start AUTO notification; raw=%s", resp.SessionStatus, truncateE2E(lastRaw, 2000))
		}
		if err := sleepContext(ctx, time.Second); err != nil {
			return startedAgentID, err
		}
	}
	return startedAgentID, fmt.Errorf("timed out after %s waiting for workflow background_agent_started event; raw=%s", time.Since(start).Round(time.Millisecond), truncateE2E(lastRaw, 2500))
}

func (c *codingAgentChatE2EClient) waitForWorkflowCompletionAutoNotification(ctx context.Context, sessionID, completionToken string, timeout time.Duration) error {
	start := time.Now()
	deadline := e2eDeadline(ctx, timeout)
	since := 0
	var lastRaw string
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		resp, raw, err := c.getEventsSince(ctx, sessionID, since)
		if err != nil {
			return err
		}
		lastRaw = raw
		since = advanceE2ECursor(since, resp.LastProcessedIndex)
		for _, event := range resp.Events {
			if fmt.Sprint(event["type"]) != "user_message" {
				continue
			}
			content := eventPayloadString(event, "content")
			trimmed := strings.TrimSpace(content)
			if !strings.HasPrefix(trimmed, "[AUTO-NOTIFICATION]") {
				continue
			}
			isTargetStep := strings.Contains(content, "step=step-auto-notification") || strings.Contains(content, "Step -> step-auto-notification")
			if !isTargetStep && !strings.Contains(content, completionToken) {
				continue
			}
			if !strings.Contains(content, "status=completed") {
				return fmt.Errorf("workflow step AUTO notification was not completed: %s", truncateE2E(content, 2000))
			}
			wantResult := "Result: " + completionToken + "\nSTATUS: COMPLETED"
			if !strings.Contains(content, wantResult) {
				return fmt.Errorf("workflow step AUTO notification did not carry exact final result %q: %s", wantResult, truncateE2E(content, 2500))
			}
			for _, forbidden := range []string{
				"api-bridge.",
				"api_bridge_",
				"execute_shell_command(",
				`"command":`,
				`"stdout":`,
				"ctrl+o to expand",
				"Called\n└",
				"Calling api-bridge",
			} {
				if strings.Contains(content, forbidden) {
					return fmt.Errorf("workflow step AUTO notification leaked MCP/TUI trail %q: %s", forbidden, truncateE2E(content, 2500))
				}
			}
			return nil
		}
		if resp.SessionStatus == "error" || resp.SessionStatus == "stopped" {
			return fmt.Errorf("session ended with status %s before workflow completion AUTO notification; raw=%s", resp.SessionStatus, truncateE2E(lastRaw, 2500))
		}
		if err := sleepContext(ctx, time.Second); err != nil {
			return err
		}
	}
	return fmt.Errorf("timed out after %s waiting for clean workflow completion AUTO notification token=%s; raw=%s", time.Since(start).Round(time.Millisecond), completionToken, truncateE2E(lastRaw, 3000))
}
