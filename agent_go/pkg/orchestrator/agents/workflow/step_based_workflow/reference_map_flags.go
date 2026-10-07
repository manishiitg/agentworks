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
// Only new breaks flag, except an unreadable input (the GraphPreflight kinds),
// which is flagged once even when it is old because it breaks the step's next
// run (PLAT-582). Other breaks that already existed stay Plan Drift's evidence
// (get_plan_prompt_health) but do not keep it due forever, so a break Drift
// cannot or should not fix never blocks Technical and Architecture. The flags
// live in planning/reference_map_flags.json, never in step_config.json, so a
// background sync cannot race a builder edit of the plan's own config.

const referenceMapFlagsFile = "reference_map_flags.json"

// referenceMapFlagsVersion changes whenever the flagging rules change, so a
// workflow whose files did not change is still re-evaluated once under the new
// rules (PLAT-582 added old unreadable inputs; without this they were never seen
// on an unchanged workflow; version 4 added removed_tool, 2026-10-07).
const referenceMapFlagsVersion = 4

type referenceMapFlag struct {
	FlaggedAt string   `json:"flagged_at"`
	Reasons   []string `json:"reasons"`
}

type referenceMapFlagState struct {
	Version     int      `json:"version,omitempty"`
	Fingerprint string   `json:"fingerprint"`
	Baseline    []string `json:"baseline_breaks"`
	// StrictFlagged holds the unreadable-input problems (GraphPreflight kinds)
	// already flagged once. Unlike other breaks, an old one is flagged too: it
	// breaks the step's next run, so Plan Drift must look at it at least once.
	StrictFlagged []string                    `json:"strict_flagged,omitempty"`
	Flags         map[string]referenceMapFlag `json:"flags,omitempty"`
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
	if fp := referenceMapFingerprint(root); fp != "" && (!initialized || fp != state.Fingerprint || state.Version != referenceMapFlagsVersion) {
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
		// Keys failing at the previous look. A problem flags when it starts
		// failing; one that is fixed leaves the list, so it flags again if it
		// comes back.
		strictSeen := map[string]bool{}
		for _, key := range state.StrictFlagged {
			strictSeen[key] = true
		}
		var strictNow []string
		var current []string
		for _, issue := range report.Issues {
			strict := graphStrictKinds[issue.Kind] && strings.HasPrefix(issue.Source, "step:")
			if issue.Severity != refSeverityBreak && !strict {
				continue
			}
			key := issue.Kind + "|" + issue.Source + "|" + issue.Ref
			current = append(current, key)
			if strict {
				strictNow = append(strictNow, key)
				if strictSeen[key] {
					continue
				}
			} else if !initialized || known[key] {
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
		// A step edited out of the description layout (PLAT-629) flags its step,
		// but only once the workflow is on the layout contract: before that every
		// step is in the old format, and the contract upgrade converts them.
		if workflowOnLayoutContract(root) {
			if raw, err := os.ReadFile(filepath.Join(root, PlanningFolderName, "plan.json")); err == nil {
				if descriptions, err := planStepDescriptionsFromPlanJSON(raw); err == nil {
					for id, description := range descriptions {
						missing := MissingDescriptionLayoutHeadings(description)
						if len(missing) == 0 {
							continue
						}
						key := "description_layout|step:" + id
						strictNow = append(strictNow, key)
						if strictSeen[key] {
							continue
						}
						flag := state.Flags[id]
						flag.FlaggedAt = now
						if len(flag.Reasons) < 3 {
							flag.Reasons = append(flag.Reasons, "the description lost the layout (missing "+strings.Join(missing, ", ")+")")
						}
						state.Flags[id] = flag
					}
				}
			}
		}
		sort.Strings(strictNow)
		state.StrictFlagged = strictNow
		sort.Strings(current)
		state.Baseline = current
		state.Fingerprint = fp
		state.Version = referenceMapFlagsVersion
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
	for _, rel := range []string{"workflow.json", "planning/plan.json", "planning/step_config.json", "evaluation/evaluation_plan.json", "soul/soul.md"} {
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

// workflowOnLayoutContract reports whether the workflow's workflow.json is at or
// past the step description layout contract.
func workflowOnLayoutContract(root string) bool {
	raw, err := os.ReadFile(filepath.Join(root, "workflow.json"))
	if err != nil {
		return false
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(raw, &manifest) != nil {
		return false
	}
	return contractVersionAtLeast(manifest.Version, StepDescriptionLayoutContractVersion)
}

func contractVersionAtLeast(version, minimum string) bool {
	parse := func(v string) []int {
		var out []int
		for _, part := range strings.Split(strings.TrimSpace(v), ".") {
			n := 0
			for _, c := range part {
				if c < '0' || c > '9' {
					return nil
				}
				n = n*10 + int(c-'0')
			}
			out = append(out, n)
		}
		return out
	}
	a, b := parse(version), parse(minimum)
	if a == nil || b == nil {
		return false
	}
	for i := 0; i < len(b); i++ {
		x := 0
		if i < len(a) {
			x = a[i]
		}
		if x != b[i] {
			return x > b[i]
		}
	}
	return true
}
