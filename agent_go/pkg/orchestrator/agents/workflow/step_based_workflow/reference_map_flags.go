package step_based_workflow

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// A reference that stops resolving makes Plan Drift due (PLAT-565). Whatever
// wrote the file -- a plan tool, a builder CLI's native edit, a shell command,
// Pulse -- the Go side sees only the result: when the workflow's tracked files
// change it recomputes the reference map and compares the breaks with the set
// it saw last time. A NEW break flags its owning step (or the workflow, for a
// note, eval or learnings file) until a drift review is recorded after the flag.
//
// Only new breaks flag. Breaks that already existed stay Plan Drift's evidence
// (get_plan_prompt_health) but do not keep it due forever, so a break Drift
// cannot or should not fix never blocks Technical and Architecture. The flags
// live in planning/reference_map_flags.json, never in step_config.json, so a
// background sync cannot race a builder edit of the plan's own config.

const referenceMapFlagsFile = "reference_map_flags.json"

type referenceMapFlag struct {
	FlaggedAt string   `json:"flagged_at"`
	Reasons   []string `json:"reasons"`
}

type referenceMapFlagState struct {
	Fingerprint string                      `json:"fingerprint"`
	Baseline    []string                    `json:"baseline_breaks"`
	Flags       map[string]referenceMapFlag `json:"flags,omitempty"`
}

var referenceMapFlagsMu sync.Mutex

// openReferenceMapFlags returns step id -> reason for every step (or the
// workflow-level record) that has a break introduced since its last review.
// It refreshes the flags first when the workflow's tracked files changed. Best
// effort: any failure returns no flags and never fails a due computation.
func openReferenceMapFlags(workspacePath string, byID map[string]StepConfig, planStepIDs map[string]bool) map[string]string {
	referenceMapFlagsMu.Lock()
	defer referenceMapFlagsMu.Unlock()

	root := refMapRoot(workspacePath)
	statePath := filepath.Join(root, PlanningFolderName, referenceMapFlagsFile)
	var state referenceMapFlagState
	initialized := false
	if raw, err := os.ReadFile(statePath); err == nil && json.Unmarshal(raw, &state) == nil {
		initialized = true
	}
	if fp := referenceMapFingerprint(root); fp != "" && (!initialized || fp != state.Fingerprint) {
		report, err := BuildReferenceMap(root)
		if err != nil {
			return nil
		}
		now := time.Now().UTC().Format(time.RFC3339)
		known := map[string]bool{}
		for _, key := range state.Baseline {
			known[key] = true
		}
		if state.Flags == nil {
			state.Flags = map[string]referenceMapFlag{}
		}
		var current []string
		for _, issue := range report.Issues {
			if issue.Severity != refSeverityBreak {
				continue
			}
			key := issue.Kind + "|" + issue.Source + "|" + issue.Ref
			current = append(current, key)
			if !initialized || known[key] {
				continue
			}
			owner := referenceMapOwner(issue.Source)
			flag := state.Flags[owner]
			flag.FlaggedAt = now
			if len(flag.Reasons) < 3 {
				flag.Reasons = append(flag.Reasons, fmt.Sprintf("%s %s in %s", issue.Kind, issue.Ref, issue.Source))
			}
			state.Flags[owner] = flag
		}
		sort.Strings(current)
		state.Baseline = current
		state.Fingerprint = fp
		saveReferenceMapFlags(statePath, state)
	}

	open := map[string]string{}
	for id, flag := range state.Flags {
		if id != WorkflowDriftReviewStepID && !planStepIDs[id] {
			continue
		}
		flaggedAt, err := time.Parse(time.RFC3339, flag.FlaggedAt)
		if err != nil {
			continue
		}
		if cfg, ok := byID[id]; ok && cfg.AgentConfigs != nil && cfg.AgentConfigs.DriftReview != nil {
			if reviewedAt, err := time.Parse(time.RFC3339, cfg.AgentConfigs.DriftReview.ReviewedAt); err == nil && reviewedAt.After(flaggedAt) {
				continue
			}
		}
		open[id] = "New broken references since the last review: " + strings.Join(flag.Reasons, "; ") + "."
	}
	return open
}

// referenceMapOwner maps where a break is written to who must review it: a
// step's own text or config is that step; notes, evals and learnings files are
// workflow-level.
func referenceMapOwner(source string) string {
	for _, prefix := range []string{"step:", "step_config:"} {
		if id := strings.TrimPrefix(source, prefix); id != source && id != "" {
			return id
		}
	}
	return WorkflowDriftReviewStepID
}

func saveReferenceMapFlags(path string, state referenceMapFlagState) {
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if os.WriteFile(tmp, raw, 0o644) != nil {
		return
	}
	if os.Rename(tmp, path) != nil {
		_ = os.Remove(tmp)
	}
}

// referenceMapFingerprint hashes the path, size and modification time of every
// file the map reads, so an unchanged workflow costs only a stat walk.
func referenceMapFingerprint(root string) string {
	h := fnv.New64a()
	files := 0
	add := func(path string, info fs.FileInfo) {
		files++
		fmt.Fprintf(h, "%s|%d|%d;", path, info.Size(), info.ModTime().UnixNano())
	}
	for _, rel := range []string{"planning/plan.json", "planning/step_config.json", "evaluation/evaluation_plan.json", "soul/soul.md"} {
		if info, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
			add(rel, info)
		}
	}
	for _, dir := range []string{"knowledgebase", "learnings"} {
		_ = filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
				return nil
			}
			if info, err := d.Info(); err == nil {
				add(path, info)
			}
			return nil
		})
	}
	if files == 0 {
		return ""
	}
	return fmt.Sprintf("%x", h.Sum64())
}
