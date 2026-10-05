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

var providerSwitchE2EFlags struct {
	serverURL     string
	from          string
	to            string
	workspaceDocs string
	timeout       time.Duration
}

var providerSwitchE2ECmd = &cobra.Command{
	Use:   "provider-switch-e2e",
	Short: "Switch a workflow Builder chat's provider between turns and check every message runs",
	Long: `Real end-to-end check of the owner's report (2026-10-05): in a workflow
Builder chat the provider changes between turns and the next messages "just
don't run". It sends a long turn on --from, steers it, switches the workflow's
Builder LLM to --to, sends two more messages and requires each to produce its
own answer within a bound. Messages that stay queued fail the run.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		f := providerSwitchE2EFlags
		ctx, cancel := context.WithTimeout(context.Background(), f.timeout)
		defer cancel()
		docs := strings.TrimSpace(f.workspaceDocs)
		if docs == "" {
			docs = strings.TrimSpace(os.Getenv("WORKSPACE_DOCS_PATH"))
		}
		if docs == "" {
			return fmt.Errorf("--workspace-docs or WORKSPACE_DOCS_PATH is required")
		}
		fromModel, toModel := defaultCodingAgentE2EModel(f.from), defaultCodingAgentE2EModel(f.to)
		fixture, cleanup, err := createWorkflowAutoNotificationFixture(docs, false, f.from, fromModel)
		if err != nil {
			return err
		}
		defer cleanup()
		client := &codingAgentChatE2EClient{
			baseURL: strings.TrimRight(f.serverURL, "/"), token: strings.TrimSpace(os.Getenv("AGENTWORKS_AUTH_TOKEN")),
			http: &http.Client{Timeout: 90 * time.Second}, agentMode: "workflow_phase",
			selectedFolder: fixture.relWorkflow, presetQueryID: fixture.presetID, phaseID: "workflow-builder",
			workshopMode: "run", enabledServers: "api-bridge", timeout: f.timeout,
		}
		if err := client.ensureUserAuth(ctx); err != nil {
			return err
		}
		sessionID := "provider-switch-e2e-" + strings.ReplaceAll(uuid.NewString(), "-", "")
		id := strings.ReplaceAll(uuid.NewString(), "-", "")[:6]
		send := func(provider, model, label, query string) (time.Time, error) {
			at := time.Now()
			resp, _, err := client.startQueryWithResponseRaw(ctx, sessionID, provider, model, query)
			if err != nil {
				return at, fmt.Errorf("send %s: %w", label, err)
			}
			fmt.Printf("[switch] %s sent on %s: status=%s delivery=%s in %s\n", label, provider, resp.Status, resp.DeliveryStatus, time.Since(at).Round(time.Millisecond))
			return at, nil
		}
		tokenA := "A_DONE_" + id
		if _, err := send(f.from, fromModel, "A", "Use the api-bridge execute_shell_command tool to run `sleep 40`, then reply with exactly "+tokenA); err != nil {
			return err
		}
		time.Sleep(8 * time.Second)
		tokenB := "B_NOTED_" + id
		if _, err := send(f.from, fromModel, "B(steer)", "While that runs: when you are done also reply with exactly "+tokenB+". Do not call any tool for this."); err != nil {
			return err
		}
		time.Sleep(6 * time.Second)
		if err := setBuilderLLM(filepath.Join(docs, filepath.FromSlash(fixture.relWorkflow)), f.to, toModel); err != nil {
			return err
		}
		fmt.Printf("[switch] Builder LLM now %s/%s\n", f.to, toModel)
		tokens := map[string]time.Time{}
		for _, label := range []string{"C", "D"} {
			token := label + "_OK_" + id
			at, err := send(f.to, toModel, label, "Reply with exactly "+token+" and nothing else. Do not call any tool.")
			if err != nil {
				return err
			}
			tokens[token] = at
			time.Sleep(10 * time.Second)
		}
		for token, at := range tokens {
			done, err := client.waitForTokenInCompletion(ctx, sessionID, token, 5*time.Minute)
			if err != nil {
				return fmt.Errorf("FAIL: message %s never ran after the provider switch: %w", token, err)
			}
			fmt.Printf("[switch] %s answered %s after it was sent\n", token, done.Sub(at).Round(time.Second))
		}
		_ = client.stopSession(ctx, sessionID)
		fmt.Println("PASS provider switch between turns: every message ran")
		return nil
	},
}

func init() {
	providerSwitchE2ECmd.Flags().StringVar(&providerSwitchE2EFlags.serverURL, "server-url", "http://localhost:18743", "server URL")
	providerSwitchE2ECmd.Flags().StringVar(&providerSwitchE2EFlags.from, "from", "muse-cli", "starting provider")
	providerSwitchE2ECmd.Flags().StringVar(&providerSwitchE2EFlags.to, "to", "codex-cli", "provider switched to")
	providerSwitchE2ECmd.Flags().StringVar(&providerSwitchE2EFlags.workspaceDocs, "workspace-docs", "", "absolute path to workspace-docs")
	providerSwitchE2ECmd.Flags().DurationVar(&providerSwitchE2EFlags.timeout, "timeout", 12*time.Minute, "overall timeout")
}

func (c *codingAgentChatE2EClient) waitForTokenInCompletion(ctx context.Context, sessionID, token string, timeout time.Duration) (time.Time, error) {
	deadline := e2eDeadline(ctx, timeout)
	since := 0
	for time.Now().Before(deadline) {
		resp, _, err := c.getEventsSince(ctx, sessionID, since)
		if err != nil {
			return time.Time{}, err
		}
		since = advanceE2ECursor(since, resp.LastProcessedIndex)
		for _, event := range resp.Events {
			if fmt.Sprint(event["type"]) == "unified_completion" && strings.Contains(eventPayloadString(event, "final_result"), token) {
				return time.Now(), nil
			}
		}
		if err := sleepContext(ctx, 2*time.Second); err != nil {
			return time.Time{}, err
		}
	}
	return time.Time{}, fmt.Errorf("no unified_completion containing %s within %s", token, timeout)
}

// setBuilderLLM points the workflow manifest's Builder chat at a provider, the
// way the workflow's model setting does.
func setBuilderLLM(absWorkflow, provider, model string) error {
	manifestPath := filepath.Join(absWorkflow, "workflow.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return err
	}
	var manifest map[string]interface{}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return err
	}
	llm := manifest["capabilities"].(map[string]interface{})["llm_config"].(map[string]interface{})
	llm["builder_llm"] = map[string]interface{}{"provider": provider, "model_id": model}
	return writeJSONFile(manifestPath, manifest)
}
