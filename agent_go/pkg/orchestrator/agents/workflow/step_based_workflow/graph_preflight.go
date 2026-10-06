package step_based_workflow

import (
	"fmt"
	"strings"
)

// GraphPreflight is the strict side of the reference map (PLAT-579): the input
// and output graph of a workflow is a declared contract, so a step whose inputs
// cannot be read is refused before anything is spent on it, with the exact
// input named. These are structural facts, not judgments: every kind below
// makes a step's input unreadable at run time.
//
//   - dependency_unproduced: no step produces the file.
//   - dependency_not_staged: a step writes it but no producer lists it in
//     context_output, so the platform looks in the consumer's own folder (a
//     message_sequence step is only handed the bare name and must hunt for it).
//   - dependency_step_without_output: the named producer step outputs nothing.
//   - relative_dependency_unresolved: a ../ path the script does not resolve.
//   - missing_step_ref: a path into a step folder that is not in the plan.
var graphStrictKinds = map[string]bool{
	"dependency_unproduced":          true,
	"dependency_not_staged":          true,
	"dependency_step_without_output": true,
	"relative_dependency_unresolved": true,
	"missing_step_ref":               true,
}

// GraphPreflight returns the strict input/output problems of one step
// (stepID set) or of the steps that always run (stepID empty). Steps behind a
// routing or branch step may never run, so a whole-workflow check leaves them to
// the check made when the step itself starts.
func GraphPreflight(workspacePath, stepID string) ([]RefMapIssue, error) {
	m, err := loadReferenceMap(refMapRoot(workspacePath))
	if err != nil || m == nil {
		return nil, err
	}
	stepID = strings.TrimSpace(stepID)
	unconditional := map[string]bool{}
	for _, s := range m.order {
		if s.typ == string(StepTypeRouting) || s.typ == string(StepTypeBranch) {
			break
		}
		unconditional[s.id] = true
	}
	var out []RefMapIssue
	for _, issue := range m.issues {
		id := strings.TrimPrefix(issue.Source, "step:")
		if id == issue.Source || !graphStrictKinds[issue.Kind] {
			continue
		}
		if stepID != "" && id != stepID || stepID == "" && !unconditional[id] {
			continue
		}
		out = append(out, issue)
	}
	return out, nil
}

// GraphPreflightMessage renders the problems as the refusal text.
func GraphPreflightMessage(issues []RefMapIssue) string {
	var b strings.Builder
	for i, issue := range issues {
		if i == 8 {
			fmt.Fprintf(&b, "\n- ... and %d more", len(issues)-i)
			break
		}
		fmt.Fprintf(&b, "\n- %s input %s (%s): %s", strings.TrimPrefix(issue.Source, "step:"), issue.Ref, issue.Kind, issue.Detail)
	}
	return b.String()
}
