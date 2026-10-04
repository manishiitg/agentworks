package step_based_workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// A script call references a saved, plan-local orphan definition. It cannot
// contain code, routes, model selection, or free-form agent instructions.
type MessageSequenceScriptCall struct {
	ID         string                 `json:"id"`
	StepID     string                 `json:"step_id"`
	Parameters map[string]interface{} `json:"parameters,omitempty"`
}

const maxSequenceScriptParallel = 8

var sequenceScriptIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

func validateMessageSequenceScriptItem(item MessageSequenceItem) error {
	if !sequenceScriptIDPattern.MatchString(item.ID) {
		return fmt.Errorf("scripted item id %q must be a safe identifier (letters, numbers, hyphens, underscores; max 128)", item.ID)
	}
	if len(item.ScriptedSteps) == 0 {
		return fmt.Errorf("scripted item %q requires scripted_steps", item.ID)
	}
	if item.MaxParallel < 0 || item.MaxParallel > maxSequenceScriptParallel {
		return fmt.Errorf("scripted item %q max_parallel must be 0..%d (0 means sequential)", item.ID, maxSequenceScriptParallel)
	}
	if item.Message != "" || item.SourceSQL != "" || item.MaxIterations != 0 || item.Kind != "" ||
		item.WriteAccess != (MessageSequenceWriteAccess{}) || item.ValidationSchema != nil || item.Prevalidation != nil {
		return fmt.Errorf("scripted item %q uses only scripted_steps/max_parallel; messages, foreach, permissions and validation belong to their own items or script definition", item.ID)
	}
	seen := map[string]bool{}
	for _, call := range item.ScriptedSteps {
		if !sequenceScriptIDPattern.MatchString(call.ID) || !sequenceScriptIDPattern.MatchString(call.StepID) {
			return fmt.Errorf("scripted item %q requires safe call id and step_id, got %q / %q", item.ID, call.ID, call.StepID)
		}
		if seen[call.ID] {
			return fmt.Errorf("scripted item %q has duplicate call id %q", item.ID, call.ID)
		}
		seen[call.ID] = true
	}
	return nil
}

type resolvedSequenceScript struct {
	Call MessageSequenceScriptCall
	Step *RegularPlanStep
}

func resolveSequenceScripts(item MessageSequenceItem, plan *PlanningResponse) ([]resolvedSequenceScript, error) {
	if err := validateMessageSequenceScriptItem(item); err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, fmt.Errorf("scripted item %q: script definitions unavailable", item.ID)
	}
	// Only explicit orphan definitions are callable. This avoids accidentally
	// replaying a main-flow step, following its next_step_id, or invoking an agent.
	orphans := make(map[string]PlanStepInterface, len(plan.OrphanSteps))
	for _, step := range plan.OrphanSteps {
		orphans[step.GetID()] = step
	}
	resolved := make([]resolvedSequenceScript, 0, len(item.ScriptedSteps))
	for _, call := range item.ScriptedSteps {
		step, ok := orphans[call.StepID].(*RegularPlanStep)
		if !ok || !isScriptedStep(step, step.AgentConfigs) {
			return nil, fmt.Errorf("scripted item %q call %q: %q must reference a scripted regular step in orphan_steps; agentic workers are not supported", item.ID, call.ID, call.StepID)
		}
		if step.ValidationSchema == nil || (len(step.ValidationSchema.Files) == 0 && len(step.ValidationSchema.DB) == 0) {
			return nil, fmt.Errorf("scripted step %q requires a non-empty validation_schema", step.ID)
		}
		if err := validateSchemaLimits(step.ValidationSchema); err != nil {
			return nil, fmt.Errorf("scripted step %q: %w", step.ID, err)
		}
		parameters, err := validateAndResolveScriptParameters(step, call.Parameters)
		if err != nil {
			return nil, fmt.Errorf("scripted item %q call %q: %w", item.ID, call.ID, err)
		}
		call.Parameters = parameters
		resolved = append(resolved, resolvedSequenceScript{Call: call, Step: step})
	}
	return resolved, nil
}

func validateMessageSequenceScriptReferences(plan *PlanningResponse) error {
	for _, steps := range [][]PlanStepInterface{plan.Steps, plan.OrphanSteps} {
		for _, info := range collectAllSteps(steps) {
			sequence, ok := info.Step.(*MessageSequencePlanStep)
			if !ok {
				continue
			}
			for _, item := range sequence.Items {
				if item.Type == "scripted" {
					if _, err := resolveSequenceScripts(item, plan); err != nil {
						return fmt.Errorf("message_sequence %q: %w", sequence.ID, err)
					}
				}
			}
		}
	}
	return nil
}

type sequenceScriptResult struct {
	ID        string `json:"id"`
	StepID    string `json:"step_id"`
	Status    string `json:"status"`
	OutputDir string `json:"output_dir,omitempty"`
	Error     string `json:"error,omitempty"`
}

// A finite worker pool owns every process until completion. Ordinary failure
// does not hide the rest of the batch; Stop prevents pending calls from starting
// and cancels active calls through their shared context. No LLM fallback exists.
func runSequenceScriptBatch(ctx context.Context, calls []resolvedSequenceScript, parallel int, run func(context.Context, resolvedSequenceScript) (string, error)) ([]sequenceScriptResult, error) {
	if parallel <= 0 {
		parallel = 1
	}
	if parallel > maxSequenceScriptParallel {
		parallel = maxSequenceScriptParallel
	}
	if parallel > len(calls) {
		parallel = len(calls)
	}
	results := make([]sequenceScriptResult, len(calls))
	jobs := make(chan int, len(calls))
	// Repeated invocations of one source serialize, including its metadata writes.
	locks := map[string]*sync.Mutex{}
	for i, call := range calls {
		results[i] = sequenceScriptResult{ID: call.Call.ID, StepID: call.Call.StepID, Status: "not_started"}
		locks[call.Call.StepID] = &sync.Mutex{}
		jobs <- i
	}
	close(jobs)
	var workers sync.WaitGroup
	for worker := 0; worker < parallel; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := range jobs {
				lock := locks[calls[i].Call.StepID]
				lock.Lock()
				if ctx.Err() != nil {
					results[i].Error = ctx.Err().Error()
					lock.Unlock()
					continue
				}
				outputDir, err := run(ctx, calls[i])
				results[i].OutputDir = outputDir
				results[i].Status = "completed"
				if ctx.Err() != nil {
					results[i].Status = "cancelled"
					results[i].Error = ctx.Err().Error()
				} else if err != nil {
					results[i].Status = "failed"
					results[i].Error = err.Error()
				}
				lock.Unlock()
			}
		}()
	}
	workers.Wait()
	var failures []error
	for _, result := range results {
		if result.Status != "completed" {
			failures = append(failures, fmt.Errorf("script %s (%s): %s: %s", result.ID, result.StepID, result.Status, result.Error))
		}
	}
	return results, errors.Join(append(failures, ctx.Err())...)
}

func sequenceSavedScriptError(result *ScriptedFastPathResult) error {
	if result == nil {
		return fmt.Errorf("saved script returned no result")
	}
	switch {
	case result.HarnessFailure:
		return fmt.Errorf("%w: %s", ErrScriptedHarnessRejection, result.HarnessError)
	case result.HarnessTimeout:
		return fmt.Errorf("%w: %s", ErrScriptedHarnessTimeout, result.TimeoutError)
	case result.TerminalRefusal:
		return scriptedTerminalStopError("sequence script", result.TerminalRefusalReason)
	case !result.RanScript:
		return fmt.Errorf("saved main.py is unavailable; author and test it in Workshop before running")
	case !result.Success:
		return fmt.Errorf("saved script failed execution or validation: %s", result.Error)
	default:
		return nil
	}
}

func (hcpo *StepBasedWorkflowOrchestrator) executeMessageSequenceScripts(ctx context.Context, step *MessageSequencePlanStep, item MessageSequenceItem, stepIndex int, stepPath string, session *messageSequenceSession) (string, error) {
	if session == nil || session.delegation != nil {
		return "", fmt.Errorf("scripted batch items require a message_sequence; orchestrators use their existing scripted route tools")
	}
	calls, err := resolveSequenceScripts(item, session.scriptedPlan)
	if err != nil {
		return "", err
	}
	// Preflight every definition before launching any side effects.
	for _, call := range calls {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		source, err := hcpo.ReadWorkspaceFile(ctx, hcpo.scriptedSourceDir(call.Step.ID)+"/main.py")
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if err != nil || strings.TrimSpace(source) == "" {
			return "", fmt.Errorf("scripted call %q: saved main.py unavailable for %q; author and test it in Workshop", call.Call.ID, call.Step.ID)
		}
	}
	runPath := hcpo.GetWorkspacePath()
	if hcpo.selectedRunFolder != "" {
		runPath = filepath.Join(runPath, "runs", hcpo.selectedRunFolder)
	}
	executionRoot := filepath.Join(runPath, "execution")
	itemPath := filepath.Join(
		getExecutionFolderPath(executionRoot, step.ID, stepPath),
		"scripts", "items", workflowSafeIDPart(item.ID, "item"),
	)
	batchPath := filepath.Join(itemPath, "calls")
	results, batchErr := runSequenceScriptBatch(ctx, calls, item.MaxParallel, func(ctx context.Context, call resolvedSequenceScript) (string, error) {
		outputPath := filepath.Join(batchPath, workflowSafeIDPart(call.Call.ID, "call"))
		if err := createFolderViaAPI(ctx, outputPath); err != nil {
			return outputPath, err
		}
		ctx = withScriptedDelegationContext(ctx, "", call.Call.ID, "", call.Call.Parameters)
		result := hcpo.tryRunSavedScriptedScript(ctx, call.Step, stepIndex, stepPath, session.scriptedPlan.Steps, outputPath, executionRoot, hcpo.resolveDBAccess(call.Step.AgentConfigs))
		// Preserve stdout and diagnostics outside the LLM context, including on Stop.
		if result != nil {
			logCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			defer cancel()
			log := result.Output
			if result.Error != "" {
				log += "\n" + result.Error
			}
			if err := hcpo.WriteWorkspaceFile(logCtx, outputPath+"/script-output.log", log); err != nil {
				return outputPath, errors.Join(sequenceSavedScriptError(result), fmt.Errorf("save script output: %w", err))
			}
		}
		return outputPath, sequenceSavedScriptError(result)
	})
	receipt, err := json.MarshalIndent(results, "", "  ")
	if err != nil {
		return "", errors.Join(batchErr, err)
	}
	receiptPath := filepath.Join(itemPath, "results.json")
	logCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	if err := hcpo.WriteWorkspaceFile(logCtx, receiptPath, string(receipt)); err != nil {
		return "", errors.Join(batchErr, fmt.Errorf("save script batch results: %w", err))
	}
	if batchErr != nil {
		return string(receipt), fmt.Errorf("script batch %q did not complete successfully; results: %s: %w", item.ID, receiptPath, batchErr)
	}
	summary := fmt.Sprintf("Script batch %s: all %d saved scripts executed and validated. Results: %s\n%s", item.ID, len(results), receiptPath, receipt)
	// The next conversational turn receives facts and paths, not a new child chat.
	session.LastRuntimeContext += "\n\n" + summary
	return summary, nil
}
