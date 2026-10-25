package server

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/schedulerstate"
)

func (s *SchedulerService) pruneWebhookRuns(workspace string) {
	if err := s.pruneWebhookRunsChecked(workspace); err != nil {
		scheduleLogf("[WEBHOOK_RETENTION] cleanup failed for %s: %v", workspace, err)
	}
}
func (s *SchedulerService) pruneWebhookRunsChecked(workspace string) error {
	lock := scheduleRunFileLock(workspace + "/run-folder-allocation")
	lock.Lock()
	defer lock.Unlock()
	root, err := webhookWorkspaceRoot(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), "runs")
	if err != nil {
		return err
	}
	if err := removeRecordedExpiredFoldersFromRunIndex(root, "webhooks/expired"); err != nil {
		return err
	}
	completed := []schedulerstate.Run{}
	for _, entry := range entries {
		if !entry.IsDir() || !webhookFolderPattern.MatchString(entry.Name()) {
			continue
		}
		raw, e := root.ReadFile("runs/" + entry.Name() + "/.webhook-run-id")
		if e != nil {
			continue
		}
		run, e := s.existingWebhookRun(context.Background(), string(raw))
		if errors.Is(e, schedulerstate.ErrRunNotFound) {
			continue
		}
		if e != nil {
			return e
		}
		if run.TriggerSource == "webhook" && run.ScopeID == workspace && run.RunFolder == entry.Name() && run.CompletedAt != nil {
			completed = append(completed, run)
		}
	}
	sort.Slice(completed, func(i, j int) bool { return completed[i].CompletedAt.After(*completed[j].CompletedAt) })
	keep := DefaultRunRetentionCount
	if len(completed) <= keep {
		return nil
	}
	if err := root.MkdirAll("webhooks/expired", 0700); err != nil {
		return err
	}
	removed := map[string]bool{}
	for _, run := range completed[keep:] {
		// Persist expiration before removing artifacts; a crash can safely retry cleanup.
		b, _ := json.Marshal(map[string]string{"run_id": run.RunID, "run_folder": run.RunFolder, "expired_at": time.Now().UTC().Format(time.RFC3339Nano)})
		if err := root.WriteFile("webhooks/expired/"+run.RunID+".json", b, 0600); err != nil {
			return err
		}
		if err := root.RemoveAll("runs/" + run.RunFolder); err != nil {
			return err
		}
		removed[run.RunFolder] = true
	}
	return removeExpiredFoldersFromRunIndex(root, removed)
}
func webhookArtifactsExpired(workspace, runID string) bool {
	if strings.ContainsAny(runID, "/\\") {
		return false
	}
	root, e := webhookWorkspaceRoot(workspace)
	if e != nil {
		return false
	}
	defer root.Close()
	_, e = root.Stat("webhooks/expired/" + runID + ".json")
	return e == nil
}

func (s *SchedulerService) pruneScheduledRuns(workspace string) {
	if err := s.pruneScheduledRunsChecked(workspace); err != nil {
		scheduleLogf("[SCHEDULE_RETENTION] cleanup failed for %s: %v", workspace, err)
	}
}

func (s *SchedulerService) pruneScheduledRunsChecked(workspace string) error {
	lock := scheduleRunFileLock(workspace + "/run-folder-allocation")
	lock.Lock()
	defer lock.Unlock()
	root, err := webhookWorkspaceRoot(workspace)
	if err != nil {
		return err
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), "runs")
	if err != nil {
		return err
	}
	if err := removeRecordedExpiredFoldersFromRunIndex(root, "schedules/expired"); err != nil {
		return err
	}
	completed := []schedulerstate.Run{}
	for _, entry := range entries {
		if !entry.IsDir() || !scheduledRunFolderPattern.MatchString(entry.Name()) {
			continue
		}
		raw, readErr := root.ReadFile("runs/" + entry.Name() + "/.schedule-run-id")
		if readErr != nil {
			continue
		}
		run, readErr := s.existingWebhookRun(context.Background(), string(raw))
		if errors.Is(readErr, schedulerstate.ErrRunNotFound) {
			continue
		}
		if readErr != nil {
			return readErr
		}
		if run.ScopeID == workspace && run.RunFolder == entry.Name() && run.CompletedAt != nil {
			completed = append(completed, run)
		}
	}
	sort.Slice(completed, func(i, j int) bool { return completed[i].CompletedAt.After(*completed[j].CompletedAt) })
	keep := DefaultRunRetentionCount
	if len(completed) <= keep {
		return nil
	}
	if err := root.MkdirAll("schedules/expired", 0700); err != nil {
		return err
	}
	removed := map[string]bool{}
	for _, run := range completed[keep:] {
		body, _ := json.Marshal(map[string]string{"run_id": run.RunID, "run_folder": run.RunFolder, "expired_at": time.Now().UTC().Format(time.RFC3339Nano)})
		if err := root.WriteFile("schedules/expired/"+run.RunID+".json", body, 0600); err != nil {
			return err
		}
		if err := root.RemoveAll("runs/" + run.RunFolder); err != nil {
			return err
		}
		removed[run.RunFolder] = true
	}
	return removeExpiredFoldersFromRunIndex(root, removed)
}

func scheduleArtifactsExpired(workspace, runID string) bool {
	if strings.ContainsAny(runID, "/\\") {
		return false
	}
	root, err := webhookWorkspaceRoot(workspace)
	if err != nil {
		return false
	}
	defer root.Close()
	_, err = root.Stat("schedules/expired/" + runID + ".json")
	return err == nil
}

func removeExpiredFoldersFromRunIndex(root *os.Root, removed map[string]bool) error {
	if len(removed) == 0 {
		return nil
	}
	raw, err := root.ReadFile("runs/run_index.json")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var index map[string]interface{}
	if err := json.Unmarshal(raw, &index); err != nil {
		return err
	}
	for _, field := range []string{"retained_iterations", "scheduled_iterations", "webhook_iterations", "slack_iterations"} {
		values, ok := index[field].([]interface{})
		if !ok {
			continue
		}
		kept := make([]interface{}, 0, len(values))
		for _, value := range values {
			name, _ := value.(string)
			if !removed[name] {
				kept = append(kept, value)
			}
		}
		index[field] = kept
	}
	index["updated_at"] = time.Now().UTC().Format(time.RFC3339Nano)
	encoded, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	return root.WriteFile("runs/run_index.json", encoded, 0600)
}

func removeRecordedExpiredFoldersFromRunIndex(root *os.Root, directory string) error {
	entries, err := fs.ReadDir(root.FS(), directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	removed := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, readErr := root.ReadFile(directory + "/" + entry.Name())
		if readErr != nil {
			return readErr
		}
		var marker struct {
			RunFolder string `json:"run_folder"`
		}
		if json.Unmarshal(raw, &marker) == nil && marker.RunFolder != "" {
			removed[marker.RunFolder] = true
		}
	}
	return removeExpiredFoldersFromRunIndex(root, removed)
}
