package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"sort"
	"sync"
	"time"

	workflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
	"github.com/manishiitg/coding-agent-loop/workspace/workflowfiles"
)

// PLAT-746-513: menu reads never launch a Python inspector. The ordinary source
// outline/save paths supply this display-only count. File AND directory stamps
// invalidate it for edits, newly added modules and removals.
type crewStepStamp struct {
	Size int64
	Time time.Time
}
type crewStepCount struct {
	Count int
	Files map[string]crewStepStamp
}

var crewStepCounts sync.Map

func crewStepCountKey(root string) string { return workflow.GetPromptDocsRoot() + "\x00" + root }

func rememberCrewStepCount(root string, files map[string]string, graph json.RawMessage) {
	var outline struct {
		Nodes []struct {
			Role string `json:"role"`
		} `json:"nodes"`
	}
	if json.Unmarshal(graph, &outline) != nil || len(outline.Nodes) == 0 {
		return
	}
	entry := crewStepCount{Files: map[string]crewStepStamp{}}
	for _, node := range outline.Nodes {
		if node.Role == "" {
			entry.Count++
		}
	}
	filesystem := workflowfiles.WorkflowFS{Docs: workflow.GetPromptDocsRoot(), Root: root}
	paths := map[string]bool{"code": true}
	for file := range files {
		name := path.Join("code", file)
		paths[name] = true
		for dir := path.Dir(name); dir != "."; dir = path.Dir(dir) {
			paths[dir] = true
		}
	}
	for name := range paths {
		info, err := fs.Stat(filesystem, name)
		if err != nil {
			return
		}
		entry.Files[name] = crewStepStamp{info.Size(), info.ModTime()}
	}
	crewStepCounts.Store(crewStepCountKey(root), entry)
}

func cachedCrewStepCount(ctx context.Context, root string) (int, bool) {
	key := crewStepCountKey(root)
	value, ok := crewStepCounts.Load(key)
	if !ok {
		return 0, false
	}
	entry := value.(crewStepCount)
	filesystem := workflowfiles.WorkflowFS{Docs: workflow.GetPromptDocsRoot(), Root: root}
	for name, stamp := range entry.Files {
		if ctx.Err() != nil {
			return 0, false
		}
		info, err := fs.Stat(filesystem, name)
		if err != nil || info.Size() != stamp.Size || !info.ModTime().Equal(stamp.Time) {
			crewStepCounts.Delete(key)
			return 0, false
		}
	}
	return entry.Count, true
}

// One directory level and one metadata body, through the same admitted storage
// resolver as run/file routes. No artifacts, child runs or Resume calculation.
// Enumeration is bounded even if a workflow has an unexpectedly huge history.
func crewPanelLatestRun(ctx context.Context, root string) ([]RunFolderInfo, bool, error) {
	base, relative, err := workflowfiles.OpenWorkflowDirectory(workflow.GetPromptDocsRoot(), root, "runs")
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer base.Close()
	file, err := base.Open(relative)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	defer file.Close()
	const limit = 4096
	entries, err := file.ReadDir(limit + 1)
	if err != nil && err != io.EOF {
		return nil, false, err
	}
	if len(entries) > limit {
		// A partial unordered directory cannot identify the latest run honestly.
		return nil, true, nil
	}
	latest, newest := "", time.Time{}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if err := workflow.ValidateWorkflowRunFolder(entry.Name()); err != nil {
			continue
		}
		name := path.Join(relative, entry.Name(), "run_metadata.json")
		info, err := base.Stat(name)
		if err == nil && info.ModTime().After(newest) {
			latest, newest = entry.Name(), info.ModTime()
		}
	}
	if latest == "" {
		return nil, false, nil
	}
	metadataFile, err := base.Open(path.Join(relative, latest, "run_metadata.json"))
	if err != nil {
		return nil, false, err
	}
	defer metadataFile.Close()
	const maxMetadata = 4 << 20
	raw, err := io.ReadAll(io.LimitReader(metadataFile, maxMetadata+1))
	if err != nil {
		return nil, false, err
	}
	if len(raw) > maxMetadata {
		return nil, false, fmt.Errorf("run metadata exceeds its summary read budget")
	}
	metadata, err := decodeRunMetadata(string(raw))
	if err != nil {
		return nil, false, err
	}
	return []RunFolderInfo{{Name: latest, Metadata: metadata}}, false, nil
}

func crewPanelWorkflows(ctx context.Context, workspace string) (crewPanelSection, error) {
	section := crewPanelSection{}
	records, err := readCrewExecutables()
	if err != nil {
		return section, err
	}
	for _, binding := range records {
		if canonicalCrewWorkspaceRoot(binding.CrewRoot) != canonicalCrewWorkspaceRoot(workspace) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return section, err
		}
		label := "Workflow"
		filesystem := workflowfiles.WorkflowFS{Docs: workflow.GetPromptDocsRoot(), Root: binding.Root}
		header := "workflow.json"
		if binding.Kind == "script" {
			header = "code/script.json"
		}
		if raw, err := fs.ReadFile(filesystem, header); err == nil {
			var saved struct {
				Name  string `json:"name"`
				Label string `json:"label"`
			}
			if json.Unmarshal(raw, &saved) == nil {
				if binding.Kind == "script" && saved.Name != "" {
					label = saved.Name
				}
				if binding.Kind == "workflow" && saved.Label != "" {
					label = saved.Label
				}
			}
		}
		steps, known := crewScriptStepCount(ctx, binding)
		if !known {
			steps = -1
		}
		folders, longHistory, err := crewPanelLatestRun(ctx, binding.Root)
		if err != nil {
			return section, err
		}
		if len(folders) > 0 && folders[0].Metadata != nil && folders[0].Metadata.Status == "running" {
			section.Busy = true
		}
		detail := crewPanelWorkflowLine(folders, steps, time.Now())
		if longHistory {
			detail = "Step count not loaded · Run history is long"
			if steps >= 0 {
				detail = crewPanelCount(steps, "step") + " · Run history is long"
			}
		}
		section.Rows = append(section.Rows, crewPanelRow{ID: "workflow:" + binding.ID, Label: label, Detail: detail, Destination: "workflow"})
	}
	sort.Slice(section.Rows, func(i, j int) bool { return section.Rows[i].Label < section.Rows[j].Label })
	section.Detail = crewPanelCount(len(section.Rows), "workflow")
	return section, nil
}
