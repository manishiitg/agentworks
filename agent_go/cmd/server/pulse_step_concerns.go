package server

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/testmode"
)

// Steps raise concerns as plain `CONCERNS:` lines in their completion summary
// (scripted steps print them to stdout, which becomes the step result). This
// collects the lines written since the previous Pulse started and hands them
// to Pulse as a compact list, so no reviewer has to search run folders. It
// only reads: Pulse decides whether a concern is a real issue, and nothing here
// files a finding.

const (
	stepConcernLookbackWithoutPulse = 7 * 24 * time.Hour
	stepConcernMaxItems             = 50
	stepConcernMaxText              = 400
)

// StepConcern is one distinct concern text from one step.
type StepConcern struct {
	StepID      string   `json:"step_id"`
	Text        string   `json:"text"`
	SeenCount   int      `json:"seen_count"`
	RunFolders  []string `json:"run_folders"`
	LastSeenAt  string   `json:"last_seen_at"`
	SourceFiles []string `json:"source_files"`
}

// StepConcernsView is what Pulse reads.
type StepConcernsView struct {
	Since     string        `json:"since"`
	Concerns  []StepConcern `json:"concerns"`
	Total     int           `json:"total"`
	Truncated bool          `json:"truncated,omitempty"`
	Note      string        `json:"note"`
}

// parseStepConcernLines returns the payloads of `CONCERNS:` lines. A line may
// be wrapped in backticks. Empty payloads and instruction placeholders such as
// "<what happened ...>" are ignored.
func parseStepConcernLines(text string) []string {
	var out []string
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		line = strings.TrimSpace(strings.Trim(line, "`"))
		line = strings.TrimLeft(line, "-* ")
		if len(line) < len("CONCERNS:") || !strings.EqualFold(line[:len("CONCERNS:")], "CONCERNS:") {
			continue
		}
		payload := strings.TrimSpace(line[len("CONCERNS:"):])
		if payload == "" || strings.HasPrefix(payload, "<") || strings.EqualFold(payload, "none") {
			continue
		}
		if len(payload) > stepConcernMaxText {
			payload = payload[:stepConcernMaxText] + "…"
		}
		out = append(out, payload)
	}
	return out
}

type stepConcernSource struct {
	stepID, runFolder, path string
	modTime                 time.Time
	texts                   []string
}

func readStepConcernSource(path string, modTime time.Time) (stepConcernSource, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return stepConcernSource{}, false
	}
	src := stepConcernSource{path: path, modTime: modTime}
	switch filepath.Base(path) {
	case "execution-final-summary.json":
		var summary struct {
			StepID          string `json:"step_id"`
			RunFolder       string `json:"run_folder"`
			ExecutionResult string `json:"execution_result"`
		}
		if json.Unmarshal(raw, &summary) != nil {
			return src, false
		}
		src.stepID, src.runFolder = summary.StepID, summary.RunFolder
		src.texts = []string{summary.ExecutionResult}
	case "session.json":
		var session struct {
			StepID    string `json:"step_id"`
			RunFolder string `json:"run_folder"`
			Entries   []struct {
				Summary string `json:"summary"`
			} `json:"entries"`
		}
		if json.Unmarshal(raw, &session) != nil {
			return src, false
		}
		src.stepID, src.runFolder = session.StepID, session.RunFolder
		for _, entry := range session.Entries {
			src.texts = append(src.texts, entry.Summary)
		}
	default:
		return src, false
	}
	return src, true
}

// collectStepConcerns scans the workflow's retained runs for step summaries
// written after since.
func collectStepConcerns(workspacePath string, since time.Time) StepConcernsView {
	view := StepConcernsView{
		Since:    since.UTC().Format(time.RFC3339),
		Concerns: []StepConcern{},
		Note:     "CONCERNS: lines steps wrote since the previous Pulse started. Leads, not findings: judge each against the evidence, reuse an existing issue for the same root cause, and treat outcome concerns as Goal Work evidence.",
	}
	runsRoot := filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(strings.Trim(workspacePath, "/")), "runs")
	type key struct{ step, text string }
	grouped := map[key]*StepConcern{}
	lastSeen := map[key]time.Time{}
	_ = filepath.WalkDir(runsRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// Pulse's own run folders and test-mode runs (PLAT-562) are not step evidence.
			if (d.Name() == "pulse" || strings.HasPrefix(d.Name(), testmode.FolderPrefix)) && filepath.Dir(path) == runsRoot {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if name != "execution-final-summary.json" && name != "session.json" {
			return nil
		}
		if name == "session.json" && filepath.Base(filepath.Dir(filepath.Dir(path))) != "execution" {
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.ModTime().After(since) {
			return nil
		}
		src, ok := readStepConcernSource(path, info.ModTime())
		if !ok {
			return nil
		}
		stepID := strings.TrimSpace(src.stepID)
		if stepID == "" {
			stepID = filepath.Base(filepath.Dir(filepath.Dir(path)))
		}
		rel, _ := filepath.Rel(filepath.Dir(runsRoot), path)
		for _, text := range src.texts {
			for _, concern := range parseStepConcernLines(text) {
				k := key{stepID, concern}
				item := grouped[k]
				if item == nil {
					item = &StepConcern{StepID: stepID, Text: concern, RunFolders: []string{}, SourceFiles: []string{}}
					grouped[k] = item
				}
				item.SeenCount++
				if src.runFolder != "" && !slices.Contains(item.RunFolders, src.runFolder) {
					item.RunFolders = append(item.RunFolders, src.runFolder)
				}
				if !slices.Contains(item.SourceFiles, filepath.ToSlash(rel)) && len(item.SourceFiles) < 3 {
					item.SourceFiles = append(item.SourceFiles, filepath.ToSlash(rel))
				}
				if src.modTime.After(lastSeen[k]) {
					lastSeen[k] = src.modTime
					item.LastSeenAt = src.modTime.UTC().Format(time.RFC3339)
				}
			}
		}
		return nil
	})
	for _, item := range grouped {
		view.Concerns = append(view.Concerns, *item)
	}
	sort.Slice(view.Concerns, func(i, j int) bool {
		if view.Concerns[i].LastSeenAt != view.Concerns[j].LastSeenAt {
			return view.Concerns[i].LastSeenAt > view.Concerns[j].LastSeenAt
		}
		return view.Concerns[i].StepID < view.Concerns[j].StepID
	})
	view.Total = len(view.Concerns)
	if len(view.Concerns) > stepConcernMaxItems {
		view.Concerns = view.Concerns[:stepConcernMaxItems]
		view.Truncated = true
	}
	return view
}

// stepConcernWindowStart is when the previous Pulse started; without one, the
// last week.
func stepConcernWindowStart(ctx context.Context, workspacePath string) time.Time {
	state, err := readPulseScheduleState(ctx, workspacePath)
	if err == nil && state != nil && !state.PreviousStartedAt.IsZero() {
		return state.PreviousStartedAt
	}
	return time.Now().UTC().Add(-stepConcernLookbackWithoutPulse)
}

func readStepConcernsView(ctx context.Context, workspacePath string) (string, error) {
	out, err := json.MarshalIndent(collectStepConcerns(workspacePath, stepConcernWindowStart(ctx, workspacePath)), "", "  ")
	return string(out), err
}

// ScheduleRunHealth summarizes whether a schedule's recent invocations
// actually ran the workflow. Plain workflow schedules now fail when nothing
// starts; direct-message schedules may legitimately run nothing, so Pulse
// judges those from this summary.
type ScheduleRunHealth struct {
	ScheduleID        string `json:"schedule_id"`
	Name              string `json:"name"`
	CustomMessages    bool   `json:"custom_messages"`
	RecentRuns        int    `json:"recent_runs"`
	RanWorkflow       int    `json:"ran_workflow"`
	RanNothing        int    `json:"ran_nothing"`
	Unknown           int    `json:"unknown"`
	LastRanWorkflowAt string `json:"last_ran_workflow_at,omitempty"`
	LastStatus        string `json:"last_status,omitempty"`
	LastStartedAt     string `json:"last_started_at,omitempty"`
}

const scheduleRunHealthWindow = 10

func collectScheduleRunHealth(ctx context.Context, workspacePath string, manifest *WorkflowManifest) []ScheduleRunHealth {
	out := []ScheduleRunHealth{}
	if manifest == nil {
		return out
	}
	runs, err := ReadScheduleRuns(ctx, workspacePath)
	if err != nil {
		return out
	}
	for _, sched := range manifest.Schedules {
		if !sched.Enabled || sched.ID == "" {
			continue
		}
		health := ScheduleRunHealth{ScheduleID: sched.ID, Name: sched.Name, CustomMessages: len(sched.Messages) > 0 || strings.TrimSpace(sched.Query) != ""}
		var mine []ScheduleRunEntry
		for _, run := range runs {
			if run.ScheduleID == sched.ID && run.TriggerSource != "manual" {
				mine = append(mine, run)
			}
		}
		sort.Slice(mine, func(i, j int) bool { return mine[i].StartedAt.After(mine[j].StartedAt) })
		for i, run := range mine {
			if i == 0 {
				health.LastStatus = run.Status
				health.LastStartedAt = run.StartedAt.UTC().Format(time.RFC3339)
			}
			if run.RanWorkflow != nil && *run.RanWorkflow && health.LastRanWorkflowAt == "" {
				health.LastRanWorkflowAt = run.StartedAt.UTC().Format(time.RFC3339)
			}
			if i >= scheduleRunHealthWindow {
				continue
			}
			health.RecentRuns++
			switch {
			case run.RanWorkflow == nil:
				health.Unknown++
			case *run.RanWorkflow:
				health.RanWorkflow++
			default:
				health.RanNothing++
			}
		}
		out = append(out, health)
	}
	return out
}

func scheduleRunHealthForView(ctx context.Context, workspacePath string) []ScheduleRunHealth {
	manifest, found, err := ReadWorkflowManifest(ctx, workspacePath)
	if err != nil || !found {
		return []ScheduleRunHealth{}
	}
	return collectScheduleRunHealth(ctx, workspacePath, manifest)
}

// Step outputs: what each step said it produced in its latest runs, side by
// side. A step can finish "completed" on every run while doing no new work
// (for example by re-verifying an earlier run's records); a single run looks
// healthy, and only the lineup across runs shows it. This only reads: the
// reviewer judges whether a lineup is a problem.

const (
	stepOutputRunsPerStep = 4
	stepOutputMaxSteps    = 40
	stepOutputMaxText     = 360
)

// StepOutputRun is one run of one step.
type StepOutputRun struct {
	RunFolder  string `json:"run_folder"`
	FinishedAt string `json:"finished_at"`
	Result     string `json:"result"`
	SourceFile string `json:"source_file"`
}

// StepOutputs is one step's latest runs, newest first.
type StepOutputs struct {
	StepID string          `json:"step_id"`
	Runs   []StepOutputRun `json:"runs"`
}

// StepOutputsView is what Pulse reads.
type StepOutputsView struct {
	Steps     []StepOutputs `json:"steps"`
	Truncated bool          `json:"truncated,omitempty"`
	Note      string        `json:"note"`
}

// stepOutputResultText keeps the end of a step's summary, where agents state
// what they did, without its CONCERNS and STATUS lines.
func stepOutputResultText(text string) string {
	var kept []string
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		upper := strings.ToUpper(strings.TrimLeft(strings.Trim(line, "`"), "-* "))
		if line == "" || strings.HasPrefix(upper, "CONCERNS:") || strings.HasPrefix(upper, "STATUS:") {
			continue
		}
		kept = append(kept, line)
	}
	out := strings.Join(kept, " ")
	if r := []rune(out); len(r) > stepOutputMaxText {
		out = "…" + string(r[len(r)-stepOutputMaxText:])
	}
	return out
}

// readStepOutputSource returns the step's own account of one run. For a
// agent the configured turns' summaries carry it; the generic
// "Agent ... completed" execution summary does not.
func readStepOutputSource(path string) (stepID, runFolder, text string, ok bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", "", "", false
	}
	switch filepath.Base(path) {
	case "execution-final-summary.json":
		var summary struct {
			StepID          string `json:"step_id"`
			RunFolder       string `json:"run_folder"`
			ExecutionResult string `json:"execution_result"`
		}
		if json.Unmarshal(raw, &summary) != nil || strings.HasPrefix(summary.ExecutionResult, "Agent ") {
			return "", "", "", false
		}
		return summary.StepID, summary.RunFolder, summary.ExecutionResult, true
	case "session.json":
		var session struct {
			StepID    string `json:"step_id"`
			RunFolder string `json:"run_folder"`
			Entries   []struct {
				ItemType string `json:"item_type"`
				Summary  string `json:"summary"`
			} `json:"entries"`
		}
		if json.Unmarshal(raw, &session) != nil {
			return "", "", "", false
		}
		var parts []string
		for _, entry := range session.Entries {
			if entry.ItemType == "prevalidation" || strings.TrimSpace(entry.Summary) == "" {
				continue
			}
			parts = append(parts, entry.Summary)
		}
		if len(parts) == 0 {
			return "", "", "", false
		}
		return session.StepID, session.RunFolder, strings.Join(parts, "\n"), true
	}
	return "", "", "", false
}

// collectStepOutputs lists each step's latest retained runs with the step's
// own summary of what it did.
func collectStepOutputs(workspacePath, onlyStep string) StepOutputsView {
	view := StepOutputsView{
		Steps: []StepOutputs{},
		Note:  "Each step's own summary of its latest retained runs, newest first. Compare runs of the same step: a step that completes every run but produces nothing new (re-checks or rebuilds an earlier run's output, reports zero new items or actions against its usual volume, or repeats the same result) is a silent no-op even though every status is success. Confirm against the system of record (new DB rows, receipts, published items) before filing.",
	}
	runsRoot := filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(strings.Trim(workspacePath, "/")), "runs")
	type runKey struct{ step, run string }
	latest := map[runKey]StepOutputRun{}
	latestTime := map[runKey]time.Time{}
	_ = filepath.WalkDir(runsRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if (d.Name() == "pulse" || strings.HasPrefix(d.Name(), testmode.FolderPrefix)) && filepath.Dir(path) == runsRoot {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if name != "execution-final-summary.json" && name != "session.json" {
			return nil
		}
		if name == "session.json" && filepath.Base(filepath.Dir(filepath.Dir(path))) != "execution" {
			return nil
		}
		stepID, runFolder, text, ok := readStepOutputSource(path)
		if !ok {
			return nil
		}
		if strings.TrimSpace(stepID) == "" {
			stepID = filepath.Base(filepath.Dir(filepath.Dir(path)))
		}
		if onlyStep != "" && stepID != onlyStep {
			return nil
		}
		rel, _ := filepath.Rel(filepath.Dir(runsRoot), path)
		if runFolder == "" {
			if parts := strings.Split(filepath.ToSlash(rel), "/"); len(parts) > 1 {
				runFolder = parts[1]
			}
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		k := runKey{stepID, runFolder}
		if !info.ModTime().After(latestTime[k]) {
			return nil
		}
		latestTime[k] = info.ModTime()
		latest[k] = StepOutputRun{
			RunFolder:  runFolder,
			FinishedAt: info.ModTime().UTC().Format(time.RFC3339),
			Result:     stepOutputResultText(text),
			SourceFile: filepath.ToSlash(rel),
		}
		return nil
	})
	byStep := map[string][]StepOutputRun{}
	for k, run := range latest {
		byStep[k.step] = append(byStep[k.step], run)
	}
	for stepID, runs := range byStep {
		sort.Slice(runs, func(i, j int) bool { return runs[i].FinishedAt > runs[j].FinishedAt })
		if len(runs) > stepOutputRunsPerStep {
			runs = runs[:stepOutputRunsPerStep]
		}
		view.Steps = append(view.Steps, StepOutputs{StepID: stepID, Runs: runs})
	}
	sort.Slice(view.Steps, func(i, j int) bool {
		if view.Steps[i].Runs[0].FinishedAt != view.Steps[j].Runs[0].FinishedAt {
			return view.Steps[i].Runs[0].FinishedAt > view.Steps[j].Runs[0].FinishedAt
		}
		return view.Steps[i].StepID < view.Steps[j].StepID
	})
	if len(view.Steps) > stepOutputMaxSteps {
		view.Steps = view.Steps[:stepOutputMaxSteps]
		view.Truncated = true
	}
	return view
}

func readStepOutputsView(workspacePath, stepID string) (string, error) {
	out, err := json.MarshalIndent(collectStepOutputs(workspacePath, strings.TrimSpace(stepID)), "", "  ")
	return string(out), err
}
