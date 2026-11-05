package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
)

// One-off removal of Brain's retired per-person schedules (PLAT-871-31).
// Run before the scheduler starts. Keep chats/run history and every other
// product's state; the original state file is backed up before any write.
// Remove this migration once all installations have started on this version.
func retireBrainSchedules(docs string) error {
	users, err := os.ReadDir(filepath.Join(docs, "_users"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var failures []error
	for _, user := range users {
		if !user.IsDir() {
			continue
		}
		history := filepath.Join(docs, "_users", user.Name(), "chat_history")
		info, err := os.Lstat(history)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if !info.IsDir() {
			continue
		}
		file := filepath.Join(history, productScheduleStateFile)
		removed, backup, err := retireBrainScheduleFile(file)
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", file, err))
			continue
		}
		if removed > 0 {
			log.Printf("[BRAIN_SCHEDULES] event=retired user=%s removed=%d backup=%s", user.Name(), removed, filepath.Base(backup))
		}
	}
	return errors.Join(failures...)
}

func retireBrainScheduleFile(file string) (int, string, error) {
	info, err := os.Lstat(file)
	if os.IsNotExist(err) {
		return 0, "", nil
	}
	if err != nil {
		return 0, "", err
	}
	if !info.Mode().IsRegular() {
		return 0, "", fmt.Errorf("schedule state is not a regular file")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return 0, "", err
	}
	var state map[string]json.RawMessage
	if err := json.Unmarshal(raw, &state); err != nil {
		return 0, "", err
	}
	removed := 0
	for key := range state {
		if strings.HasPrefix(key, "knowledgebase/") {
			delete(state, key)
			removed++
		}
	}
	if removed == 0 {
		return 0, "", nil
	}
	backup := file + ".before-brain-retirement-" + time.Now().UTC().Format("20060102T150405.000000000Z") + ".bak"
	out, err := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return 0, "", fmt.Errorf("backup: %w", err)
	}
	_, writeErr := out.Write(raw)
	closeErr := out.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		return 0, "", fmt.Errorf("backup: %w", err)
	}
	if err := fsutil.WriteJSONAtomic(file, state, 0600); err != nil {
		return 0, backup, err
	}
	return removed, backup, nil
}
