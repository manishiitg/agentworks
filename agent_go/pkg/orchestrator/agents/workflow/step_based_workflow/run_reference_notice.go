package step_based_workflow

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func (hcpo *StepBasedWorkflowOrchestrator) observeRunReferences(stepID string) {
	if hcpo.isRelayExecution() {
		return
	}
	root := hcpo.GetWorkspacePath()
	defer func() {
		if recovered := recover(); recovered != nil {
			hcpo.GetLogger().Warn(fmt.Sprintf("[RUN REFERENCES] workflow=%s step=%s check=unavailable; execution continues after observation failure", root, stepID))
		}
	}()
	report, err := quickRunReferenceMap(root)
	if err != nil {
		hcpo.GetLogger().Warn(fmt.Sprintf("[RUN REFERENCES] workflow=%s step=%s check=unavailable; execution continues: %v", root, stepID, err))
		return
	}
	hcpo.GetLogger().Info(fmt.Sprintf("[RUN REFERENCES] workflow=%s step=%s breaks=%d warnings=%d scope=plan_graph advisory=true", root, stepID, report.Breaks, report.Warnings))
	shown := 0
	for _, issue := range report.Issues {
		if issue.Severity == refSeverityInfo || stepID != "" && strings.HasPrefix(issue.Source, "step:") && issue.Source != "step:"+stepID {
			continue
		}
		hcpo.GetLogger().Warn(fmt.Sprintf("[RUN REFERENCES] workflow=%s source=%s kind=%s reference=%s", root, issue.Source, issue.Kind, issue.Ref))
		shown++
		if shown == 5 {
			break
		}
	}
}

// quickRunReferenceMap intentionally does not load the full workflow map. A run
// must not reread every knowledge/skill document just to log optional advice.
// The strict input guard still uses the complete, unchanged GraphPreflight path.
func quickRunReferenceMap(workspacePath string) (ReferenceMapReport, error) {
	const maxBytes = 1 << 20
	remaining := int64(maxBytes)
	read := func(path string) ([]byte, error) {
		if remaining <= 0 {
			return nil, fmt.Errorf("plan graph observation byte limit reached")
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Size() > remaining {
			return nil, fmt.Errorf("plan graph observation file exceeds remaining limit")
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		raw, err := io.ReadAll(io.LimitReader(f, remaining+1))
		remaining -= int64(len(raw))
		if remaining < 0 {
			return nil, fmt.Errorf("plan graph observation byte limit reached")
		}
		return raw, err
	}
	root := refMapRoot(workspacePath)
	raw, err := read(filepath.Join(root, PlanningFolderName, "plan.json"))
	if err != nil {
		return ReferenceMapReport{}, err
	}
	m := &referenceMap{
		root: root, readScript: read, steps: map[string]*refStep{}, retired: map[string]bool{},
		evalIDs: map[string]bool{}, index: map[string]bool{}, seen: map[string]bool{},
		producer: map[string][]string{}, declarer: map[string][]string{},
	}
	if err := m.loadPlan(raw); err != nil {
		return ReferenceMapReport{}, err
	}
	m.checkDependencies()
	return m.report(), nil
}
