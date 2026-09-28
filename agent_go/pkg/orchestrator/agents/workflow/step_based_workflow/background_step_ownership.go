package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	mcpexecutor "github.com/manishiitg/mcpagent/executor"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents"
	"github.com/manishiitg/multi-llm-provider-go/llmtypes"
)

// A run_in_background agent owns the steps it starts, the way a step owns its
// async sub-agents (reconcileAsyncSubAgentCalls) and a scheduled run owns its
// session (cmd/server/scheduled_turn_followups.go): when its turns end, the
// runtime waits for every step it started, hands it their results as its next
// turn, and repeats until a turn starts nothing new. Only then is it done.
//
// Before this, execute_step told a background agent "end your turn, you will
// be notified", but a background agent is finished once its turns end, so the
// notice went to the main session instead and the agent never saw its own
// result. Pulse reviewers that re-ran a fixed step to verify it ended without
// recording anything (social-media Technical Review, 2026-09-26: it started
// the verification at 18:20, ended at 18:22, the run finished at 18:35).
//
// The owning agent is found from the trusted caller session the MCP bridge
// attaches to every tool call, not from the Go context: bridge calls lose it,
// which is why such steps used to be recorded with no parent at all.

// backgroundStepOwner is one background agent's set of started steps.
type backgroundStepOwner struct {
	ownerID string // the background agent's execution ID
	mu      sync.Mutex
	started []string
	handed  map[string]bool
}

func newBackgroundStepOwner(ownerID string) *backgroundStepOwner {
	return &backgroundStepOwner{ownerID: ownerID, handed: map[string]bool{}}
}

func (o *backgroundStepOwner) add(executionID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.started = append(o.started, executionID)
}

// registerBackgroundStepOwner maps the background agent's tool session to its
// owner record until the returned release is called.
func (iwm *InteractiveWorkshopManager) registerBackgroundStepOwner(toolSessionID string, owner *backgroundStepOwner) func() {
	if strings.TrimSpace(toolSessionID) == "" || owner == nil {
		return func() {}
	}
	iwm.backgroundStepOwners.Store(toolSessionID, owner)
	return func() { iwm.backgroundStepOwners.Delete(toolSessionID) }
}

// backgroundStepOwnerFor returns the background agent that made this tool
// call, or nil when the caller is not a background agent.
func (iwm *InteractiveWorkshopManager) backgroundStepOwnerFor(ctx context.Context) *backgroundStepOwner {
	sessionID := strings.TrimSpace(mcpexecutor.SessionIDFromContext(ctx))
	if sessionID == "" {
		return nil
	}
	value, ok := iwm.backgroundStepOwners.Load(sessionID)
	if !ok {
		return nil
	}
	owner, _ := value.(*backgroundStepOwner)
	return owner
}

// stopUnhanded stops every step the owner started that is still running when
// it exits without handing their results back (its turn failed, it was
// cancelled, or it waited past the ceiling). Those steps were started with
// their notice suppressed, so nobody else would ever take their result.
func (o *backgroundStepOwner) stopUnhanded(stop func(executionID string)) {
	if o == nil || stop == nil {
		return
	}
	o.mu.Lock()
	var pending []string
	for _, id := range o.started {
		if !o.handed[id] {
			pending = append(pending, id)
		}
	}
	o.mu.Unlock()
	for _, id := range pending {
		stop(id)
	}
}

var backgroundStepPollInterval = time.Second

// backgroundStepCeiling bounds how long a background agent waits for the
// steps it started.
var backgroundStepCeiling = 3 * time.Hour

// waitForOwnedSteps waits until no step the owner started is still running,
// then returns the finished ones not yet handed back. Empty means the last
// turn started nothing new.
func (o *backgroundStepOwner) waitForOwnedSteps(ctx context.Context, registry *WorkshopStepRegistry, ceiling time.Duration) ([]WorkshopStepSnapshot, error) {
	started := time.Now()
	ticker := time.NewTicker(backgroundStepPollInterval)
	defer ticker.Stop()
	for {
		o.mu.Lock()
		ids := append([]string(nil), o.started...)
		o.mu.Unlock()
		running := 0
		var finished []WorkshopStepSnapshot
		for _, id := range ids {
			o.mu.Lock()
			handed := o.handed[id]
			o.mu.Unlock()
			if handed {
				continue
			}
			snap, ok := registry.GetSnapshot(id)
			if !ok {
				continue
			}
			if snap.Status == WorkshopStepRunning {
				running++
				continue
			}
			finished = append(finished, snap)
		}
		if running == 0 {
			o.mu.Lock()
			for _, snap := range finished {
				o.handed[snap.ID] = true
			}
			o.mu.Unlock()
			return finished, nil
		}
		if ceiling > 0 && time.Since(started) >= ceiling {
			return nil, fmt.Errorf("%d step run(s) still running after %s", running, ceiling)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

const maxBackgroundStepResultChars = 6000

// formatOwnedStepResults is the background agent's next turn.
func formatOwnedStepResults(results []WorkshopStepSnapshot) string {
	type entry struct {
		Step        string `json:"step"`
		ExecutionID string `json:"execution_id"`
		Status      string `json:"status"`
		Result      string `json:"result,omitempty"`
		Error       string `json:"error,omitempty"`
	}
	entries := make([]entry, 0, len(results))
	for _, r := range results {
		e := entry{Step: r.StepID, ExecutionID: r.ID, Status: string(r.Status)}
		result := strings.TrimSpace(r.Result)
		if len(result) > maxBackgroundStepResultChars {
			result = result[:maxBackgroundStepResultChars] + "…(truncated; query_step has the full result)"
		}
		e.Result = result
		if r.Err != nil {
			e.Error = r.Err.Error()
		}
		entries = append(entries, e)
	}
	encoded, _ := json.MarshalIndent(entries, "", "  ")
	return fmt.Sprintf(`[STEP RESULTS] The runtime waited for every step you started. These are their final results:

%s

Continue your task now with these results: check whether they show what you expected and record your outcome as your task requires. Start more steps only if the task needs them; the runtime will wait for those too. When nothing more is needed, finish with your final answer.`, string(encoded))
}

// handOwnedStepResults runs after a background agent's turns: it waits for the
// steps it started and continues the same conversation with their results,
// until a turn starts nothing new.
func handOwnedStepResults(ctx context.Context, agent agents.OrchestratorAgent, templateVars map[string]string, history []llmtypes.MessageContent, result string, owner *backgroundStepOwner, registry *WorkshopStepRegistry) (string, []llmtypes.MessageContent, error) {
	if owner == nil || registry == nil || agent == nil {
		return result, history, nil
	}
	const maxRounds = 50
	for round := 1; round <= maxRounds; round++ {
		finished, err := owner.waitForOwnedSteps(ctx, registry, backgroundStepCeiling)
		if err != nil {
			return result, history, fmt.Errorf("wait for owned steps: %w", err)
		}
		if len(finished) == 0 {
			return result, history, nil
		}
		turnVars := make(map[string]string, len(templateVars))
		for key, value := range templateVars {
			turnVars[key] = value
		}
		turnVars["Instruction"] = formatOwnedStepResults(finished)
		result, history, err = agent.Execute(ctx, turnVars, history)
		if err != nil {
			return result, history, fmt.Errorf("step-results turn %d failed: %w", round, err)
		}
	}
	return result, history, fmt.Errorf("stopped after %d step-results turns", maxRounds)
}

// ensurePulseReviewerRecorded gives a Pulse reviewer that is about to finish
// without its terminal result one more turn, in its own conversation, to
// record it (and hands back any step that turn starts).
func ensurePulseReviewerRecorded(ctx context.Context, agent agents.OrchestratorAgent, templateVars map[string]string, history []llmtypes.MessageContent, result, module, pulseRunID string, check func(context.Context, string, string) error, owner *backgroundStepOwner, registry *WorkshopStepRegistry) (string, error) {
	if strings.TrimSpace(module) == "" || check == nil || agent == nil {
		return result, nil
	}
	missing := check(ctx, module, pulseRunID)
	if missing == nil {
		return result, nil
	}
	vars := make(map[string]string, len(templateVars))
	for key, value := range templateVars {
		vars[key] = value
	}
	vars["Instruction"] = pulseReviewerRecordReminder(module, pulseRunID, missing)
	result, history, err := agent.Execute(ctx, vars, history)
	if err != nil {
		return "", fmt.Errorf("record-result turn: %w", err)
	}
	result, _, err = handOwnedStepResults(ctx, agent, templateVars, history, result, owner, registry)
	return result, err
}

// pulseReviewerRecordReminder is the reviewer's extra turn when it is about to
// finish without its terminal result.
func pulseReviewerRecordReminder(module, pulseRunID string, missing error) string {
	return fmt.Sprintf(`[RESULT NOT RECORDED] You are finishing the %s review for pulse_run_id=%q without its recorded result (%v). Record it now with record_pulse_result(module=%q, pulse_run_id=%q): what you checked, what you changed or verified, and the outcome for each issue you handled. Do not start new work.`, module, pulseRunID, missing, module, pulseRunID)
}
