package step_based_workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

// PLAT-556 decision 2: budgets are triggers, never gates. These measures make
// an Architecture consolidation due; they never block a run or an edit, and
// the reviewer still decides how to restructure.

const (
	// promptBudgetDuplicateWindow is the verbatim span (characters, after
	// whitespace is collapsed) that counts as text repeated across steps.
	promptBudgetDuplicateWindow = 300
	// learningSettledRuns is how many successful runs on one description hash,
	// with no new learning detected in the latest detections, make a read-write
	// step's learning "settled" (Architecture learning_quality decides whether
	// it becomes read).
	learningSettledRuns = 5
)

// promptLayoutSections is the step-description layout (step-description.md).
var promptLayoutSections = []string{"Goal", "Inputs", "Output", "Rules", "Done when", "Guides"}

// promptLayoutRequired are the sections every agentic step states; missing one
// of these makes a step "no layout". Inputs, Rules and Guides can be honestly
// empty for a small step, so they are reported but do not trigger.
var promptLayoutRequired = map[string]bool{"Goal": true, "Output": true, "Done when": true}

var (
	promptLayoutHeadingRe = regexp.MustCompile(`(?im)^#{1,3}[ \t]+(goal|inputs?|outputs?|rules|done when|guides)[ \t]*:?[ \t]*$`)
	promptDatedTextRe     = regexp.MustCompile(`(?i)\b\d{4}-\d{2}-\d{2}\b|\bmeasured\b|\bobserved on\b|\b(?:run|execution)[ _-]?id[ \t]*[:=#][ \t]*[A-Za-z0-9_.-]+|\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
)

// PromptBudgetStep is one step's budget measures.
type PromptBudgetStep struct {
	PlanMedianChars      int      `json:"plan_median_chars"`
	BudgetChars          int      `json:"budget_chars"`
	OverBudget           bool     `json:"over_budget,omitempty"`
	DatedTextCount       int      `json:"dated_text_count,omitempty"`
	DatedTextSamples     []string `json:"dated_text_samples,omitempty"`
	DuplicatedChars      int      `json:"duplicated_chars,omitempty"`
	DuplicatedWith       []string `json:"duplicated_with,omitempty"`
	LayoutChecked        bool     `json:"layout_checked"`
	MissingLayout        []string `json:"missing_layout_sections,omitempty"`
	NoLayout             bool     `json:"no_layout,omitempty"`
	ConsolidationReasons []string `json:"consolidation_reasons,omitempty"`
}

type promptBudgetInput struct {
	id          string
	stepType    StepType
	scriptOnly  bool
	description string
}

func promptBudgetHasLayout(stepType StepType, scriptOnly bool) bool {
	switch stepType {
	case StepTypeMessageSeq, StepTypeOrchestrator, StepTypeTodoTaskLegacy:
		return true
	}
	// A regular step is scripted by definition (PLAT-287); its description is
	// not an agent charter, and routing/branch/human-input steps have none.
	_ = scriptOnly
	return false
}

// applyPromptBudget fills the per-step budget fields and the report totals.
func applyPromptBudget(report *PromptHealthReport, inputs []promptBudgetInput) {
	lengths := make([]int, len(inputs))
	for i, in := range inputs {
		lengths[i] = utf8.RuneCountInString(in.description)
	}
	duplicated, duplicatedWith := promptBudgetDuplication(inputs)
	byID := map[string]*PromptHealthStep{}
	for i := range report.Steps {
		byID[report.Steps[i].ID] = &report.Steps[i]
	}
	report.PlanMedianChars = medianInts(lengths, -1)
	for i, in := range inputs {
		step := byID[in.id]
		if step == nil {
			continue
		}
		median := medianInts(lengths, i)
		budget := int(float64(median) * descriptionSizeSevereMultiplier)
		if budget < descriptionSizeSevereFloor {
			budget = descriptionSizeSevereFloor
		}
		b := PromptBudgetStep{PlanMedianChars: median, BudgetChars: budget}
		if lengths[i] > budget {
			b.OverBudget = true
			b.ConsolidationReasons = append(b.ConsolidationReasons, "over_budget")
		}
		for _, match := range promptDatedTextRe.FindAllStringIndex(in.description, -1) {
			b.DatedTextCount++
			if len(b.DatedTextSamples) < 3 {
				b.DatedTextSamples = append(b.DatedTextSamples, promptBudgetSnippet(in.description, match[0], match[1]))
			}
		}
		if b.DatedTextCount > 0 {
			b.ConsolidationReasons = append(b.ConsolidationReasons, "dated_text")
		}
		if duplicated[i] > 0 {
			b.DuplicatedChars = duplicated[i]
			b.DuplicatedWith = duplicatedWith[i]
			b.ConsolidationReasons = append(b.ConsolidationReasons, "duplicated_text")
		}
		if promptBudgetHasLayout(in.stepType, in.scriptOnly) {
			b.LayoutChecked = true
			present := map[string]bool{}
			for _, m := range promptLayoutHeadingRe.FindAllStringSubmatch(in.description, -1) {
				present[canonicalLayoutSection(m[1])] = true
			}
			for _, section := range promptLayoutSections {
				if present[section] {
					continue
				}
				b.MissingLayout = append(b.MissingLayout, section)
				if promptLayoutRequired[section] {
					b.NoLayout = true
				}
			}
			if b.NoLayout {
				b.ConsolidationReasons = append(b.ConsolidationReasons, "no_layout")
			}
		}
		step.Budget = &b
		if b.OverBudget {
			report.StepsOverBudget++
		}
		report.DatedTextCount += b.DatedTextCount
		report.DuplicatedBudgetChars += b.DuplicatedChars
		if b.NoLayout {
			report.StepsWithoutLayout++
		}
		if len(b.ConsolidationReasons) > 0 {
			report.ConsolidationDueSteps = append(report.ConsolidationDueSteps, in.id)
		}
		if lengths[i] > report.LargestDescriptionChars {
			report.LargestDescriptionChars = lengths[i]
			report.LargestDescriptionStepID = in.id
		}
	}
	sort.Strings(report.ConsolidationDueSteps)
}

func canonicalLayoutSection(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "goal":
		return "Goal"
	case "input", "inputs":
		return "Inputs"
	case "output", "outputs":
		return "Output"
	case "rules":
		return "Rules"
	case "done when":
		return "Done when"
	case "guides":
		return "Guides"
	}
	return raw
}

// medianInts returns the median of values, skipping index skip (-1 skips
// nothing) and zero lengths. It matches planMedianOtherStepDescriptionLen's
// "other steps" baseline used by the edit-time OVER BUDGET nudge.
func medianInts(values []int, skip int) int {
	var lens []int
	for i, v := range values {
		if i == skip || v <= 0 {
			continue
		}
		lens = append(lens, v)
	}
	if len(lens) == 0 {
		return 0
	}
	sort.Ints(lens)
	mid := len(lens) / 2
	if len(lens)%2 == 0 {
		return (lens[mid-1] + lens[mid]) / 2
	}
	return lens[mid]
}

func promptBudgetSnippet(text string, start, end int) string {
	from := start - 40
	if from < 0 {
		from = 0
	}
	to := end + 40
	if to > len(text) {
		to = len(text)
	}
	for from > 0 && !utf8.RuneStart(text[from]) {
		from--
	}
	for to < len(text) && !utf8.RuneStart(text[to]) {
		to++
	}
	return strings.Join(strings.Fields(text[from:to]), " ")
}

// promptBudgetDuplication counts, per step, the characters covered by a
// 300-character window that also appears verbatim (whitespace collapsed) in
// another step. Rabin-Karp keeps it linear in the plan's text size.
func promptBudgetDuplication(inputs []promptBudgetInput) ([]int, [][]string) {
	const base = 1099511628211
	texts := make([][]byte, len(inputs))
	for i, in := range inputs {
		texts[i] = []byte(strings.Join(strings.Fields(in.description), " "))
	}
	w := promptBudgetDuplicateWindow
	var pow uint64 = 1
	for i := 0; i < w-1; i++ {
		pow *= base
	}
	hashes := make([][]uint64, len(inputs))
	owners := map[uint64]map[int]struct{}{}
	for i, text := range texts {
		if len(text) < w {
			continue
		}
		var h uint64
		for j := 0; j < w; j++ {
			h = h*base + uint64(text[j])
		}
		hashes[i] = make([]uint64, 0, len(text)-w+1)
		for pos := 0; ; pos++ {
			hashes[i] = append(hashes[i], h)
			if owners[h] == nil {
				owners[h] = map[int]struct{}{}
			}
			owners[h][i] = struct{}{}
			if pos+w >= len(text) {
				break
			}
			h = (h-uint64(text[pos])*pow)*base + uint64(text[pos+w])
		}
	}
	covered := make([]int, len(inputs))
	with := make([][]string, len(inputs))
	for i, hs := range hashes {
		if len(hs) == 0 {
			continue
		}
		mark := make([]bool, len(texts[i]))
		others := map[string]struct{}{}
		for pos, h := range hs {
			if len(owners[h]) < 2 {
				continue
			}
			shared := false
			for j := range owners[h] {
				if j != i {
					shared = true
					others[inputs[j].id] = struct{}{}
				}
			}
			if !shared {
				continue
			}
			for k := pos; k < pos+w; k++ {
				mark[k] = true
			}
		}
		for _, m := range mark {
			if m {
				covered[i]++
			}
		}
		for id := range others {
			with[i] = append(with[i], id)
		}
		sort.Strings(with[i])
	}
	return covered, with
}

// LearningSettledStep is a read-write learning step whose learning has
// settled: Architecture (focus learning_quality) decides whether it becomes
// learnings_access=read.
type LearningSettledStep struct {
	StepID              string `json:"step_id"`
	DescriptionHashRuns int    `json:"description_hash_runs"`
	SuccessfulRuns      int    `json:"successful_runs"`
	LastLearningAt      string `json:"last_learning_detected_at,omitempty"`
}

// PromptBudgetDue is the Go-precomputed Architecture trigger set Gate sees and
// the worklist enforces: steps due for a prompt_design consolidation, and
// read-write learning steps due for a learning_quality decision.
type PromptBudgetDue struct {
	Health          PromptHealthReport    `json:"health"`
	PromptDesign    []string              `json:"prompt_design_steps"`
	LearningSettled []LearningSettledStep `json:"learning_settled_steps,omitempty"`
	// Fingerprint changes whenever a flagged step's text or the flagged set
	// changes, so a reviewed, unchanged state is not made due again.
	Fingerprint string `json:"fingerprint,omitempty"`
}

// Any reports whether a budget trigger is active.
func (d PromptBudgetDue) Any() bool {
	return len(d.PromptDesign) > 0 || len(d.LearningSettled) > 0
}

// FocusKeys returns the Architecture focuses the triggers ask for.
func (d PromptBudgetDue) FocusKeys() []string {
	var keys []string
	if len(d.PromptDesign) > 0 {
		keys = append(keys, "prompt_design")
	}
	if len(d.LearningSettled) > 0 {
		keys = append(keys, "learning_quality")
	}
	return keys
}

// CollectPromptBudgetDue measures the authored plan on disk (no orchestrator
// needed) for the Pulse Gate. A missing plan is not an error.
func CollectPromptBudgetDue(ctx context.Context, workspacePath string) (PromptBudgetDue, error) {
	workspacePath = strings.Trim(strings.TrimSpace(workspacePath), "/")
	if workspacePath == "" {
		return PromptBudgetDue{}, nil
	}
	root := filepath.Join(fsutil.WorkspaceDocsRoot(), filepath.FromSlash(workspacePath))
	planRaw, err := os.ReadFile(filepath.Join(root, PlanningFolderName, "plan.json"))
	if err != nil {
		if os.IsNotExist(err) {
			return PromptBudgetDue{}, nil
		}
		return PromptBudgetDue{}, fmt.Errorf("read planning/plan.json: %w", err)
	}
	var plan PlanningResponse
	if err := json.Unmarshal(planRaw, &plan); err != nil {
		return PromptBudgetDue{}, fmt.Errorf("parse planning/plan.json: %w", err)
	}
	due := PromptBudgetDue{Health: BuildPromptHealthReport(plan.Steps)}
	due.PromptDesign = append([]string(nil), due.Health.ConsolidationDueSteps...)

	if configRaw, err := os.ReadFile(filepath.Join(root, PlanningFolderName, "step_config.json")); err == nil {
		if configs, parseErr := ParseStepConfigContent(string(configRaw)); parseErr == nil {
			for _, cfg := range configs {
				if cfg.AgentConfigs == nil || strings.TrimSpace(cfg.AgentConfigs.LearningsAccess) != "read-write" {
					continue
				}
				metaRaw, err := os.ReadFile(filepath.Join(root, "learnings", cfg.ID, ".learning_metadata.json"))
				if err != nil {
					continue
				}
				var meta LearningMetadata
				if json.Unmarshal(metaRaw, &meta) != nil {
					continue
				}
				if learningSettled(meta) {
					due.LearningSettled = append(due.LearningSettled, LearningSettledStep{
						StepID: cfg.ID, DescriptionHashRuns: meta.DescriptionHashRuns,
						SuccessfulRuns: meta.SuccessfulRuns, LastLearningAt: meta.LastLearningDetectedAt,
					})
				}
			}
		}
	}
	sort.Slice(due.LearningSettled, func(i, j int) bool { return due.LearningSettled[i].StepID < due.LearningSettled[j].StepID })

	if due.Any() {
		descByID := map[string]string{}
		for _, info := range collectAllSteps(plan.Steps) {
			if info.Step != nil {
				descByID[info.Step.GetID()] = info.Step.GetDescription()
			}
		}
		h := sha256.New()
		for _, id := range due.PromptDesign {
			sum := sha256.Sum256([]byte(descByID[id]))
			fmt.Fprintf(h, "p:%s:%x\n", id, sum[:6])
		}
		for _, s := range due.LearningSettled {
			fmt.Fprintf(h, "l:%s:%d\n", s.StepID, s.DescriptionHashRuns/learningSettledRuns)
		}
		due.Fingerprint = hex.EncodeToString(h.Sum(nil))[:16]
	}
	return due, nil
}

// learningSettled: at least learningSettledRuns successful runs on the current
// description hash, and none of the latest detections found new learning.
func learningSettled(meta LearningMetadata) bool {
	if meta.DescriptionHashRuns < learningSettledRuns {
		return false
	}
	history := meta.DetectionHistory
	if len(history) > learningSettledRuns {
		history = history[len(history)-learningSettledRuns:]
	}
	for _, entry := range history {
		if entry.HasNewLearning {
			return false
		}
	}
	return true
}
