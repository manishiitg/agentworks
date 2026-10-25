package server

import (
	"encoding/json"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	stepworkflow "github.com/manishiitg/coding-agent-loop/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow"
)

// Slack has a separate retention family. Unknown or incomplete evidence is
// retained, and the current run of any live conversation is always protected.
func (api *StreamingAPI) pruneSlackRuns(workspace string) error {
	lock := scheduleRunFileLock(workspace + "/run-folder-allocation")
	lock.Lock()
	defer lock.Unlock()
	root, err := webhookWorkspaceRoot(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	protected := map[string]bool{}
	api.scheduleInvocations.Range(func(key, value interface{}) bool {
		invocation := value.(*stepworkflow.ExternalInvocation)
		if invocation.Kind == "slack" {
			if active, ok := api.getActiveSession(key.(string)); ok && (active.Status == "running" || active.HasRunningBackgroundAgents || active.NeedsUserInput) {
				protected[invocation.BoundRunFolder()] = true
			}
		}
		return true
	})
	entries, err := fs.ReadDir(root.FS(), "runs")
	if err != nil {
		return err
	}
	type completedRun struct {
		name string
		at   time.Time
	}
	completed := []completedRun{}
	for _, entry := range entries {
		if !entry.IsDir() || !numberedRunFolderPattern.MatchString(entry.Name()) || !strings.Contains(entry.Name(), "-slack-") || protected[entry.Name()] {
			continue
		}
		if _, err := root.ReadFile("runs/" + entry.Name() + "/.slack-run-id"); err != nil {
			continue
		}
		known, closed := false, true
		var latest time.Time
		err := fs.WalkDir(root.FS(), "runs/"+entry.Name(), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || path.Base(p) != "run_metadata.json" {
				return nil
			}
			raw, err := root.ReadFile(p)
			if err != nil {
				return err
			}
			var meta struct {
				Status      string `json:"status"`
				CompletedAt string `json:"completed_at"`
			}
			if json.Unmarshal(raw, &meta) != nil {
				closed = false
				return nil
			}
			known = true
			at, err := time.Parse(time.RFC3339Nano, meta.CompletedAt)
			if err != nil || meta.Status == "running" || meta.Status == "pending" {
				closed = false
				return nil
			}
			if at.After(latest) {
				latest = at
			}
			return nil
		})
		if err != nil {
			return err
		}
		if known && closed {
			completed = append(completed, completedRun{entry.Name(), latest})
		}
	}
	sort.Slice(completed, func(i, j int) bool { return completed[i].at.After(completed[j].at) })
	keep := DefaultRunRetentionCount
	if len(completed) <= keep {
		return nil
	}
	removed := map[string]bool{}
	for _, run := range completed[keep:] {
		if err := root.RemoveAll("runs/" + run.name); err != nil {
			return err
		}
		removed[run.name] = true
	}
	return removeExpiredFoldersFromRunIndex(root, removed)
}
