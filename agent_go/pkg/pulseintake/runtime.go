// Package pulseintake turns durable workflow evidence into small, typed review
// leads. It does not create Pulse findings, choose a repair, or block a run:
// those decisions remain with Gate and the relevant reviewer.
package pulseintake

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/testmode"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/workflowrun"
)

const (
	DetectorRuntime          = "runtime_artifacts"
	RuntimeDetectorVersion   = "runtime-artifacts/v2"
	CoverageVerified         = "verified"
	CoveragePartial          = "partial"
	CoverageNotInstrumented  = "not_instrumented"
	CoverageUnavailable      = "unavailable"
	maxRetainedRunsToInspect = 20
	severityHigh             = "high"
	severityMedium           = "medium"
)

// Finding is a deterministic fact for Gate to route to an agentic review. It
// is intentionally not a Pulse issue: the reviewer decides whether the fact
// matters, is a known/recovered failure, or warrants a durable finding.
type Finding struct {
	Kind       string `json:"kind"`
	Severity   string `json:"severity"`
	Subject    string `json:"subject"`
	Detail     string `json:"detail"`
	Evidence   string `json:"evidence"`
	ObservedAt string `json:"observed_at,omitempty"`
	RunFolder  string `json:"run_folder,omitempty"`
	StepID     string `json:"step_id,omitempty"`
	Artifact   string `json:"artifact,omitempty"`
}

type Result struct {
	Detector        string        `json:"detector"`
	DetectorVersion string        `json:"detector_version"`
	ObservedAt      string        `json:"observed_at"`
	CoverageStatus  string        `json:"coverage_status"`
	CoverageReason  string        `json:"coverage_reason,omitempty"`
	RunsInspected   int           `json:"runs_inspected"`
	Findings        []Finding     `json:"findings"`
	RunIndex        *RunIndex     `json:"run_index,omitempty"`
	RunIdentities   []RunIdentity `json:"run_identities,omitempty"`
}

type runMetadata struct {
	Status            string `json:"status"`
	ExecutionID       string `json:"execution_id"`
	PlanRevision      string `json:"plan_revision"`
	ActiveSlotAtStart string `json:"active_slot_at_start"`
	RunKind           string `json:"run_kind"`
	ScheduleRunID     string `json:"schedule_run_id"`
	ScheduleID        string `json:"schedule_id"`
}

type RunIndex struct {
	Version                   int      `json:"version"`
	ActiveIteration           string   `json:"active_iteration"`
	RetainedIterations        []string `json:"retained_iterations"`
	BuilderActiveIteration    string   `json:"builder_active_iteration,omitempty"`
	BuilderRetainedIterations []string `json:"builder_retained_iterations,omitempty"`
	ScheduledIterations       []string `json:"scheduled_iterations,omitempty"`
	WebhookIterations         []string `json:"webhook_iterations,omitempty"`
	LastTransition            string   `json:"last_transition"`
	FullRunPolicy             string   `json:"full_run_policy"`
	PartialGroupRunPolicy     string   `json:"partial_group_policy"`
	UpdatedAt                 string   `json:"updated_at"`
}

type RunIdentity struct {
	RunFolder        string `json:"run_folder"`
	LifecycleRole    string `json:"lifecycle_role"`
	ExecutionID      string `json:"execution_id,omitempty"`
	RunKind          string `json:"run_kind,omitempty"`
	ScheduleRunID    string `json:"schedule_run_id,omitempty"`
	ScheduleID       string `json:"schedule_id,omitempty"`
	PlanRevision     string `json:"plan_revision"`
	ProvenanceStatus string `json:"provenance_status"`
}

type timingArtifact struct {
	StepID       string `json:"step_id"`
	RetryAttempt int    `json:"retry_attempt"`
	LLM          struct {
		ErroredCount  int `json:"errored_count"`
		CanceledCount int `json:"canceled_count"`
	} `json:"llm"`
	Tools struct {
		ErroredCount int `json:"errored_count"`
		Calls        []struct {
			ToolName string          `json:"tool_name"`
			Status   string          `json:"status"`
			Result   json.RawMessage `json:"result"`
		} `json:"calls"`
	} `json:"tools"`
}

type runCandidate struct {
	dir     string
	rel     string
	modTime time.Time
}

// CheckRuntime reads only compact, persisted run metadata and timing receipts.
// It purposefully does not infer errors from arbitrary natural-language output.
func CheckRuntime(workspacePath string, now time.Time) Result {
	result := Result{
		Detector: DetectorRuntime, DetectorVersion: RuntimeDetectorVersion,
		ObservedAt: now.UTC().Format(time.RFC3339), Findings: []Finding{},
	}
	runsRoot, err := resolveRunsRoot(workspacePath)
	if err != nil {
		result.CoverageStatus, result.CoverageReason = CoverageUnavailable, err.Error()
		return result
	}
	candidates, err := retainedRunCandidates(runsRoot)
	if err != nil {
		result.CoverageStatus, result.CoverageReason = CoverageUnavailable, err.Error()
		return result
	}
	if len(candidates) == 0 {
		result.CoverageStatus, result.CoverageReason = CoverageNotInstrumented, "no retained run_metadata.json files"
		return result
	}
	if len(candidates) > maxRetainedRunsToInspect {
		candidates = candidates[:maxRetainedRunsToInspect]
		result.CoverageStatus = CoveragePartial
		result.CoverageReason = fmt.Sprintf("inspected newest %d retained runs", maxRetainedRunsToInspect)
	} else {
		result.CoverageStatus = CoverageVerified
	}
	if index, indexErr := loadRunIndex(runsRoot); indexErr == nil {
		result.RunIndex = index
	} else {
		result.CoverageStatus = CoveragePartial
		result.CoverageReason = appendReason(result.CoverageReason, "run identity index unavailable: "+indexErr.Error())
	}
	for _, run := range candidates {
		result.RunsInspected++
		if identity, identityErr := inspectRunIdentity(run, result.RunIndex); identityErr == nil {
			result.RunIdentities = append(result.RunIdentities, identity)
		} else {
			result.CoverageStatus = CoveragePartial
			result.CoverageReason = appendReason(result.CoverageReason, fmt.Sprintf("%s identity: %v", run.rel, identityErr))
		}
		findings, err := inspectRun(run)
		if err != nil {
			result.CoverageStatus = CoveragePartial
			result.CoverageReason = appendReason(result.CoverageReason, fmt.Sprintf("%s: %v", run.rel, err))
			continue
		}
		result.Findings = append(result.Findings, findings...)
	}
	sort.SliceStable(result.Findings, func(i, j int) bool {
		if result.Findings[i].Severity != result.Findings[j].Severity {
			return result.Findings[i].Severity == severityHigh
		}
		return result.Findings[i].Artifact < result.Findings[j].Artifact
	})
	return result
}

func loadRunIndex(runsRoot string) (*RunIndex, error) {
	raw, err := os.ReadFile(filepath.Join(runsRoot, "run_index.json"))
	if err != nil {
		return nil, err
	}
	var index RunIndex
	if err := json.Unmarshal(raw, &index); err != nil {
		return nil, err
	}
	if index.Version <= 0 || strings.TrimSpace(index.ActiveIteration) == "" {
		return nil, fmt.Errorf("missing version or active_iteration")
	}
	return &index, nil
}

func inspectRunIdentity(run runCandidate, index *RunIndex) (RunIdentity, error) {
	raw, err := os.ReadFile(filepath.Join(run.dir, "run_metadata.json"))
	if err != nil {
		return RunIdentity{}, err
	}
	var metadata runMetadata
	if err := json.Unmarshal(raw, &metadata); err != nil {
		return RunIdentity{}, err
	}
	topLevel := strings.Split(filepath.ToSlash(run.rel), "/")[0]
	role := "unknown"
	if index != nil {
		if index.Version < 2 && topLevel == index.ActiveIteration {
			role = "active"
		} else if topLevel == index.BuilderActiveIteration {
			role = "builder_active"
		} else if containsString(index.ScheduledIterations, topLevel) {
			role = "scheduled"
		} else if containsString(index.WebhookIterations, topLevel) {
			role = "webhook"
		} else if containsString(index.BuilderRetainedIterations, topLevel) {
			role = "builder_retained"
		} else {
			for _, retained := range index.RetainedIterations {
				if topLevel == retained {
					if index.Version < 2 {
						role = "retained"
					}
					break
				}
			}
		}
	}
	if metadata.RunKind == "schedule" {
		role = "scheduled"
	}
	planRevision := strings.TrimSpace(metadata.PlanRevision)
	status := "verified"
	if strings.TrimSpace(metadata.ExecutionID) == "" || planRevision == "" {
		status = "unknown_legacy"
	}
	if planRevision == "" {
		planRevision = "unknown_legacy"
	}
	return RunIdentity{
		RunFolder: run.rel, LifecycleRole: role,
		ExecutionID: strings.TrimSpace(metadata.ExecutionID), RunKind: strings.TrimSpace(metadata.RunKind),
		ScheduleRunID: strings.TrimSpace(metadata.ScheduleRunID), ScheduleID: strings.TrimSpace(metadata.ScheduleID), PlanRevision: planRevision,
		ProvenanceStatus: status,
	}, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func resolveRunsRoot(workspacePath string) (string, error) {
	workspacePath = strings.Trim(strings.TrimSpace(strings.ReplaceAll(workspacePath, "\\\\", "/")), "/")
	if workspacePath == "" {
		return "", fmt.Errorf("workspace_path is required")
	}
	cleaned := filepath.Clean(filepath.FromSlash(workspacePath))
	if filepath.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workspace_path must stay inside the workspace root")
	}
	root, err := filepath.Abs(fsutil.WorkspaceDocsRoot())
	if err != nil {
		return "", fmt.Errorf("resolve workspace root: %w", err)
	}
	runsRoot, err := filepath.Abs(filepath.Join(root, cleaned, "runs"))
	if err != nil {
		return "", fmt.Errorf("resolve runs root: %w", err)
	}
	rel, err := filepath.Rel(root, runsRoot)
	if err != nil || filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workspace_path escapes the workspace root")
	}
	return runsRoot, nil
}

func retainedRunCandidates(runsRoot string) ([]runCandidate, error) {
	if _, err := os.Stat(runsRoot); err != nil {
		if os.IsNotExist(err) {
			return []runCandidate{}, nil
		}
		return nil, err
	}
	var out []runCandidate
	err := filepath.WalkDir(runsRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		// Test-mode runs (PLAT-562) are never Pulse evidence.
		if entry.IsDir() && strings.HasPrefix(entry.Name(), testmode.FolderPrefix) && filepath.Dir(path) == runsRoot {
			return filepath.SkipDir
		}
		if entry.IsDir() || entry.Name() != "run_metadata.json" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		dir := filepath.Dir(path)
		rel, err := filepath.Rel(runsRoot, dir)
		if err != nil {
			return err
		}
		out = append(out, runCandidate{dir: dir, rel: filepath.ToSlash(rel), modTime: info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].modTime.After(out[j].modTime) })
	return out, nil
}

func inspectRun(run runCandidate) ([]Finding, error) {
	metadataBytes, err := os.ReadFile(filepath.Join(run.dir, "run_metadata.json"))
	if err != nil {
		return nil, err
	}
	var metadata runMetadata
	if err := json.Unmarshal(metadataBytes, &metadata); err != nil {
		return nil, fmt.Errorf("decode run_metadata.json: %w", err)
	}
	rawStatus := strings.TrimSpace(metadata.Status)
	status := workflowrun.CanonicalStatus(rawStatus)
	observedAt := run.modTime.UTC().Format(time.RFC3339Nano)
	var findings []Finding
	if rawStatus != "" && status != workflowrun.StatusCompleted {
		findings = append(findings, Finding{
			Kind: "run_not_completed", Severity: severityHigh, Subject: "Run did not complete",
			Detail:   "The retained run has an explicit non-success terminal/runtime status.",
			Evidence: fmt.Sprintf("run_metadata.status=%q", metadata.Status), ObservedAt: observedAt, RunFolder: run.rel, Artifact: "run_metadata.json",
		})
	}
	if status != workflowrun.StatusCompleted {
		return findings, nil
	}
	err = filepath.WalkDir(filepath.Join(run.dir, "logs"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "-timing.json") || !strings.HasPrefix(entry.Name(), "execution-attempt-") {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var timing timingArtifact
		if err := json.Unmarshal(data, &timing); err != nil {
			return fmt.Errorf("decode %s: %w", filepath.ToSlash(path), err)
		}
		artifact, _ := filepath.Rel(run.dir, path)
		artifact = filepath.ToSlash(artifact)
		if timing.LLM.ErroredCount > 0 || timing.LLM.CanceledCount > 0 || timing.Tools.ErroredCount > 0 {
			findings = append(findings, Finding{
				Kind: "completed_run_child_errors", Severity: severityMedium, StepID: timing.StepID, RunFolder: run.rel, Artifact: artifact,
				Subject:    "Completed run had child-call errors",
				Detail:     "Child-call errors are diagnostic evidence, not a terminal failure. Check required outputs, successful recovery and material retry cost before selecting Technical Review.",
				Evidence:   fmt.Sprintf("llm.errored_count=%d; llm.canceled_count=%d; tools.errored_count=%d", timing.LLM.ErroredCount, timing.LLM.CanceledCount, timing.Tools.ErroredCount),
				ObservedAt: observedAt,
			})
		}
		for _, call := range timing.Tools.Calls {
			if strings.ToLower(strings.TrimSpace(call.Status)) != "success" || !structuredToolFailure(call.Result) {
				continue
			}
			findings = append(findings, Finding{
				Kind: "tool_success_with_structured_failure", Severity: severityHigh, StepID: timing.StepID, RunFolder: run.rel, Artifact: artifact,
				Subject:    "Successful tool call contains structured failure",
				Detail:     "The tool-call status is success but its structured result reports a failure.",
				Evidence:   fmt.Sprintf("tool=%q; status=%q; result contains isError=true or non-zero exit_code", call.ToolName, call.Status),
				ObservedAt: observedAt,
			})
		}
		return nil
	})
	return findings, err
}

func structuredToolFailure(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var decoded interface{}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return false
	}
	object, ok := decoded.(map[string]interface{})
	if !ok {
		encoded, isString := decoded.(string)
		if !isString || json.Unmarshal([]byte(encoded), &object) != nil {
			return false
		}
	}
	if isError, ok := object["isError"].(bool); ok && isError {
		return true
	}
	if exitCode, ok := numberValue(object["exit_code"]); ok && exitCode != 0 {
		return true
	}
	return false
}

func numberValue(value interface{}) (int64, bool) {
	switch v := value.(type) {
	case float64:
		return int64(v), v == float64(int64(v))
	case json.Number:
		n, err := v.Int64()
		return n, err == nil
	default:
		return 0, false
	}
}

func appendReason(existing, next string) string {
	if existing == "" {
		return next
	}
	return existing + "; " + next
}
