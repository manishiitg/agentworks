package step_based_workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	loggerv2 "github.com/manishiitg/mcpagent/logger/v2"
)

// The backlog of plan changes whose knock-on effects nobody has checked yet.
//
// A workflow is a web of implicit dependencies: a step's output feeds downstream
// steps, evals score it, the report dashboard queries its data, db stores it, and
// learnings/KB describe its behavior. Change a step and those silently rot.
//
// The framework already accounts for this. plan-change-impact names the six
// dimensions to reconcile, every plan-mod call is logged with its reason and
// old/new values, artifact_review is the module that sweeps them, and
// mark_changelog_artifact_reviewed stamps an entry once its blast radius has been
// traced.
//
// The one missing piece was arithmetic. Nothing counted the unstamped entries, so
// Pulse Gate had to open the changelog files and work it out on every run — the
// same shape of blind spot as learning_health, whose trigger has always been
// "plan or prompt changes affected step behavior" but which had no fact to fire
// on.
//
// So this counts them, and makes no judgement about which dependents actually
// broke: only tracing a change's surface against the real artifacts can tell a
// cosmetic edit from one that orphans a report query. An earlier version of this
// file tried to decide staleness in Go and was wrong for exactly that reason —
// in a live workflow, 78 of the plan-mod calls were update_step_config, which
// records no field diff at all, so a review_notes touch was indistinguishable
// from a rewritten step description.
//
// Computed on read. It needs no state, and an entry stays listed until it is
// actually stamped, so the backlog cannot be lost by anything failing to record.

// UnreviewedPlanChange is one plan-mod call whose blast radius has not been traced.
type UnreviewedPlanChange struct {
	ChangeID                   string           `json:"change_id,omitempty"`
	At                         string           `json:"at"`
	Tool                       string           `json:"tool"`
	Reason                     string           `json:"reason,omitempty"`
	StepIDs                    []string         `json:"step_ids,omitempty"`
	FieldsChanged              []string         `json:"fields_changed,omitempty"`
	SourceFile                 string           `json:"source_file"`
	Origin                     PlanChangeOrigin `json:"origin"`
	DependencyCoverageProblems []string         `json:"dependency_coverage_problems,omitempty"`
}

// PlanChangeBacklog is the summary handed to Pulse Gate.
type PlanChangeBacklog struct {
	UnreviewedCount                int                    `json:"unreviewed_count"`
	SupersededLegacyCount          int                    `json:"superseded_legacy_count,omitempty"`
	DependencyCoverageFailureCount int                    `json:"dependency_coverage_failure_count"`
	OldestAt                       string                 `json:"oldest_at,omitempty"`
	NewestAt                       string                 `json:"newest_at,omitempty"`
	Changes                        []UnreviewedPlanChange `json:"changes,omitempty"`
	Note                           string                 `json:"note,omitempty"`
}

// PlanChangeDependencyIntake is the deterministic half of plan-change review.
// It proves only whether current-contract changelog entries carry the complete
// six-surface receipt. A Technical reviewer still decides whether any covered
// dependency is actually stale and what, if anything, should change.
type PlanChangeDependencyIntake struct {
	Detector        string                 `json:"detector"`
	DetectorVersion string                 `json:"detector_version"`
	CoverageStatus  string                 `json:"coverage_status"`
	Failed          bool                   `json:"failed"`
	FailureCount    int                    `json:"failure_count"`
	Findings        []UnreviewedPlanChange `json:"findings"`
}

// maxListedPlanChanges caps the listed entries. The count is always exact; the
// list is a prompt for a decision, and planning/changelog/ holds the full record.
const maxListedPlanChanges = 12

// legacyPlanChangeHorizon: an old-format change (no change_id) older than this
// is left out of the backlog. Tracing a months-old diff one by one is wasted
// work once the steps it touched have changed again; what matters is whether
// anything is broken now, which the reference map checks on the current state
// and flags to Plan Drift (PLAT-561/565/579). Nothing is stamped, so the
// changelog keeps the full history (PLAT-593).
const legacyPlanChangeHorizon = 30 * 24 * time.Hour

var planChangeBacklogNow = time.Now

func workflowAbsPath(workspacePath string, parts ...string) string {
	all := append([]string{fsutil.WorkspaceDocsRoot(), filepath.FromSlash(strings.Trim(strings.TrimSpace(workspacePath), "/"))}, parts...)
	return filepath.Join(all...)
}

// CollectPlanChangeBacklog scans planning/changelog/ for entries that have not
// been stamped artifact_review.done, newest first.
//
// Returns nil when there is no changelog or nothing is outstanding — a workflow
// whose changes have all been reconciled must add nothing to Pulse's context.
func CollectPlanChangeBacklog(workspacePath string) *PlanChangeBacklog {
	workspacePath = strings.Trim(strings.TrimSpace(workspacePath), "/")
	if workspacePath == "" {
		return nil
	}
	dir := workflowAbsPath(workspacePath, PlanningFolderName, "changelog")
	files, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	type dated struct {
		at     time.Time
		change UnreviewedPlanChange
	}
	var pending []dated
	superseded := 0
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			continue
		}
		var file PlanChangelog
		if json.Unmarshal(raw, &file) != nil {
			// A malformed changelog file is not evidence that its changes were
			// reviewed, but guessing its contents would be worse. Skip it; the
			// other files still produce a real backlog.
			continue
		}
		for _, e := range file.Entries {
			// A change whose every named field is cosmetic (wording, review
			// notes, schedule timing, model/tier settings) cannot orphan a
			// dependent step, eval, report, DB contract, or KB entry, so it
			// never belongs in this backlog. An entry with no field names at
			// all (an untyped update_step_config call, or a step add/delete)
			// stays conservative and is still counted; see
			// planChangeFieldsAreDriftMaterial.
			if !planChangeFieldsAreDriftMaterial(planChangelogEntryFieldNames(e)) {
				continue
			}
			coverageProblems := planDependencyCoverageProblems(e)
			// Legacy reviewed entries predate structured dependency receipts. Do
			// not reopen the historical fleet. Every current-contract mutation has
			// a change_id, so it must pass the stronger deterministic boundary.
			if e.ArtifactReview != nil && e.ArtifactReview.Done && (strings.TrimSpace(e.ChangeID) == "" || len(coverageProblems) == 0) {
				continue
			}
			ts, parseErr := time.Parse(time.RFC3339, strings.TrimSpace(e.Timestamp))
			if parseErr == nil && strings.TrimSpace(e.ChangeID) == "" && planChangeBacklogNow().Sub(ts) > legacyPlanChangeHorizon {
				superseded++
				continue
			}
			if parseErr != nil {
				// Undateable but still unreviewed: keep it, sorted last, rather than
				// dropping an outstanding change because its timestamp is malformed.
				ts = time.Time{}
			}
			change := toUnreviewedPlanChange(e, f.Name())
			if strings.TrimSpace(e.ChangeID) != "" {
				change.DependencyCoverageProblems = coverageProblems
			}
			pending = append(pending, dated{at: ts.UTC(), change: change})
		}
	}
	if len(pending) == 0 {
		return nil
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].at.After(pending[j].at) })

	backlog := &PlanChangeBacklog{
		UnreviewedCount:       len(pending),
		SupersededLegacyCount: superseded,
		NewestAt:              pending[0].change.At,
		OldestAt:              pending[len(pending)-1].change.At,
	}
	for _, p := range pending {
		if len(p.change.DependencyCoverageProblems) > 0 {
			backlog.DependencyCoverageFailureCount++
		}
	}
	for i, p := range pending {
		if i >= maxListedPlanChanges {
			break
		}
		backlog.Changes = append(backlog.Changes, p.change)
	}
	backlog.Note = fmt.Sprintf(
		"%d plan-mod change(s) lack a complete structured dependency review, so their knock-on effects across downstream steps, validation, evaluation, reporting, database contracts, and learnings/knowledge may not have been reconciled. Evidence, not a verdict: many changes have no blast radius at all. The reviewer must return an evidence-backed disposition for every surface before mark_changelog_artifact_reviewed can close an entry — read_skill(skills=[{\"name\":\"builder-reference\",\"path\":\"references/plan-change-impact.md\"}]) is the procedure. Entries stay listed until closure, so deferring loses nothing.",
		len(pending))
	if superseded > 0 {
		backlog.Note += fmt.Sprintf(" %d older old-format change(s) (no change_id, more than 30 days old) are not listed: the reference map checks the current workflow for anything they could have broken.", superseded)
	}
	if len(pending) > len(backlog.Changes) {
		backlog.Note += fmt.Sprintf(" Showing the %d most recent; the rest are in planning/changelog/.", len(backlog.Changes))
	}
	return backlog
}

// BuildPlanChangeDependencyIntake projects the exact current-contract coverage
// failures into the deterministic Gate feed without duplicating the changelog
// scan or promoting them to Pulse issues.
func BuildPlanChangeDependencyIntake(backlog *PlanChangeBacklog) PlanChangeDependencyIntake {
	result := PlanChangeDependencyIntake{
		Detector:        "plan_change_dependencies",
		DetectorVersion: "plan-change-dependencies/v1",
		CoverageStatus:  "verified",
		Findings:        []UnreviewedPlanChange{},
	}
	if backlog == nil || backlog.DependencyCoverageFailureCount == 0 {
		return result
	}
	result.Failed = true
	result.FailureCount = backlog.DependencyCoverageFailureCount
	for _, change := range backlog.Changes {
		if len(change.DependencyCoverageProblems) > 0 {
			result.Findings = append(result.Findings, change)
		}
	}
	return result
}

func planDependencyCoverageProblems(entry PlanChangelogEntry) []string {
	if entry.ArtifactReview == nil {
		return []string{"artifact_review receipt is missing"}
	}
	problems := []string{}
	if !entry.ArtifactReview.Done {
		problems = append(problems, "artifact_review.done is not true")
	}
	for _, surface := range requiredPlanDependencySurfaces {
		review, exists := entry.ArtifactReview.Surfaces[surface]
		if !exists {
			problems = append(problems, surface+": disposition and evidence are missing")
			continue
		}
		switch review.Disposition {
		case "updated", "already_compatible", "not_applicable", "blocked", "broken":
		default:
			problems = append(problems, surface+": disposition is invalid")
		}
		if len(normalizedLifecycleStrings(review.Evidence)) == 0 {
			problems = append(problems, surface+": evidence is missing")
		}
		if (review.Disposition == "blocked" || review.Disposition == "broken") && len(normalizedLifecycleStrings(review.IssueIDs)) == 0 {
			problems = append(problems, surface+": blocked/broken disposition lacks a durable Pulse issue")
		}
	}
	return problems
}

// planChangelogEntryFieldNames extracts the named changed fields the same
// way toUnreviewedPlanChange does, for the materiality check that decides
// whether the entry belongs in the backlog at all.
func planChangelogEntryFieldNames(e PlanChangelogEntry) []string {
	var fields []string
	for _, c := range e.Changes {
		if f := strings.TrimSpace(c.Field); f != "" {
			fields = append(fields, f)
		}
	}
	return fields
}

func toUnreviewedPlanChange(e PlanChangelogEntry, sourceFile string) UnreviewedPlanChange {
	out := UnreviewedPlanChange{
		ChangeID:   strings.TrimSpace(e.ChangeID),
		At:         strings.TrimSpace(e.Timestamp),
		Tool:       strings.TrimSpace(e.Tool),
		Reason:     strings.TrimSpace(e.Reason),
		StepIDs:    e.StepIDs,
		SourceFile: sourceFile,
		Origin:     e.Origin,
	}
	// Field names are what make an entry triageable: they are the surface the
	// reviewer traces. update_step_config records none today, which is exactly
	// why the reviewer must open the artifacts rather than filter on a name.
	for _, c := range e.Changes {
		if f := strings.TrimSpace(c.Field); f != "" {
			out.FieldsChanged = append(out.FieldsChanged, f)
		}
	}
	return out
}

// LogCanonicalArtifactChange records a changelog entry for a canonical artifact
// that has no plan-modification tool of its own.
//
// planning/changelog only ever receives entries from plan-mod tool calls, and
// Artifact Review reads that changelog to detect drift. workflow.json has no
// such tool — nothing in the tool surface edits it — so every change to it
// arrived by direct write and left no record. Artifact Review filed this on
// social-media (AR-20260729-2) and could not close it: git showed later
// workflow.json commits with no changelog entry, and no safe backfill existed
// because the
// writes were never recorded in the first place.
//
// Best-effort by contract, like the plan tools: a workflow change that succeeded
// must not fail because its audit note could not be appended.
// target, beforeSnapshot, and afterSnapshot are optional. When the caller has
// the actual pre/post artifact content on hand (e.g. a direct file write that
// already read the previous bytes), pass it so BeforeRef/AfterRef reflect the
// real artifact instead of the tool-name-based Target guess and the
// Changes-derived refs, which collapse to the same sha256("[]") whenever
// changes is nil (PLAT-033).
func LogCanonicalArtifactChange(
	ctx context.Context,
	workspacePath string,
	tool string,
	reason string,
	changes []PlanFieldChange,
	readFile func(context.Context, string) (string, error),
	writeFile func(context.Context, string, string) error,
	logger loggerv2.Logger,
	target string,
	beforeSnapshot, afterSnapshot interface{},
) {
	if strings.TrimSpace(workspacePath) == "" || readFile == nil || writeFile == nil || logger == nil {
		return
	}
	if strings.TrimSpace(reason) == "" {
		reason = "Recorded automatically so this artifact's change is visible to artifact drift review."
	}
	logPlanChange(ctx, workspacePath, PlanChangelogEntry{
		Tool:           strings.TrimSpace(tool),
		Reason:         reason,
		Changes:        changes,
		Target:         strings.TrimSpace(target),
		BeforeSnapshot: beforeSnapshot,
		AfterSnapshot:  afterSnapshot,
	}, readFile, writeFile, logger)
}
