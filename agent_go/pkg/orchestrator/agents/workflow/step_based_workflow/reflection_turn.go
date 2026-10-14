package step_based_workflow

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// PLAT-055. One reflection turn replacing the KB turn followed by the learnings
// turn.
//
// The split was itself the defect. During the KB turn the agent has not yet
// worked out what it learned; by the learnings turn the KB door has closed and
// the database was never reachable from a reflection turn at all. "Which store
// owns this?" was therefore never a decision the agent got to make — it was
// decided by whichever door happened to be open, and learnings was usually the
// only one. That is how a validator-bug report, a percentile table and a live
// security inventory all ended up inside "skill" files.
//
// With every outlet open at once, routing becomes a real choice, and the rules
// below become followable rather than aspirational. It is also one LLM call per
// contributing step instead of two.

// StepReflectionTurnInput carries everything the merged turn needs.
type StepReflectionTurnInput struct {
	StepID string

	// LearningObjective gates the learnings half; empty means this step
	// contributes no learnings and that section is omitted.
	LearningObjective   string
	LearningsTargetPath string

	// KBAccess/KBContribution gate the knowledgebase half on the same terms the
	// standalone KB turn used.
	KBAccess          string
	KBContribution    string
	KBNotesTargetPath string
}

func (in StepReflectionTurnInput) writesLearnings() bool {
	return strings.TrimSpace(in.LearningObjective) != ""
}

func (in StepReflectionTurnInput) writesKB() bool {
	return kbAccessAllowsWrite(in.KBAccess) && strings.TrimSpace(in.KBContribution) != ""
}

// BuildStepReflectionTurn renders the merged post-completion turn. Returns
// empty when the step has nothing to reflect on and nothing to report.
func BuildStepReflectionTurn(in StepReflectionTurnInput) string {
	if strings.TrimSpace(in.StepID) == "" || (!in.writesLearnings() && !in.writesKB()) {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Reflection\n\n")
	b.WriteString("The task is complete and pre-validation passed. This post-run turn can save useful guidance for future runs; the conversation and results are already recorded. Do not repeat the task's actions.\n\n")
	b.WriteString("If there is a useful, verified contribution, open the workflow-reflection skill with read_skill for the recording procedure. The runtime adds only the contribution paths below for this turn; other permissions remain unchanged.\n\n")
	if in.writesLearnings() {
		target := strings.TrimRight(strings.TrimSpace(in.LearningsTargetPath), "/")
		if target == "" {
			target = filepath.Join(LearningsFolderName, GlobalLearningID)
		}
		fmt.Fprintf(&b, "**Learnings** — reusable execution technique, shared across this workflow.\nTarget: `%s`\nRequested contribution: %s\n\n", target, strings.TrimSpace(in.LearningObjective))
	}
	if in.writesKB() {
		target := strings.TrimRight(strings.TrimSpace(in.KBNotesTargetPath), "/")
		if target == "" {
			target = filepath.Join(KnowledgebaseFolderName, KBNotesFolderName)
		}
		fmt.Fprintf(&b, "**Knowledgebase** — verified business/domain context for later runs.\nTarget: `%s`\nRegistry: `%s/_index.json`; open workflow-knowledgebase for topic and registry rules.\nRequested contribution: %s\n\n", target, target, strings.TrimSpace(in.KBContribution))
	}
	b.WriteString("If nothing new is worth saving, say so briefly without editing. Otherwise summarize what you saved and list the files changed.\n")
	return b.String()
}

// LoadWorkflowDBTableNames lists the workflow's own tables.
//
// Platform-owned bookkeeping (Pulse lifecycle, report human inputs) is filtered
// out: naming those in a step's routing rule would invite a step to write into
// the harness's own records, and they are not where a step's observations
// belong. The point of the list is to show a step that a home for its
// measurement already exists.
func LoadWorkflowDBTableNames(ctx context.Context, workspacePath string) ([]string, error) {
	db, err := openRunConcernsDB(ctx, workspacePath, false)
	if err != nil || db == nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx,
		`SELECT name FROM sqlite_master WHERE type IN ('table','view') AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if isPlatformOwnedTable(name) {
			continue
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func isPlatformOwnedTable(name string) bool {
	switch {
	case strings.HasPrefix(name, "pulse_"):
		return true
	case strings.HasPrefix(name, "report_human_input"):
		return true
	// eval_results / eval_route_scores are retired eval tables: steps must
	// never write them, so they stay platform-owned as read-only history.
	case name == "run_concerns", name == "eval_results", name == "eval_route_scores":
		return true
	}
	return false
}
