package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
)

// An explicit request for a different exact source starts a fresh cursor. The
// previous private metadata is backed up, never silently rebound to new bytes.
// Chunks stay inert in the same private source namespace and can be deduplicated.
// Caller holds the source mutex after proving the current maker and attachment.
func resetCrewOfflineHistoryForIntent(dir string, b crewOfflineBinding) (bool, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "history.json"))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var h crewOfflineHistory
	if json.Unmarshal(raw, &h) != nil || !h.crewOfflineBinding.valid() {
		return false, fmt.Errorf("the previous source history is invalid")
	}
	if sameCrewOfflineSource(h.crewOfflineBinding, b) {
		return false, nil
	}
	backup := filepath.Join(dir, "before-source-change-"+uuid.NewString())
	if err := os.Mkdir(backup, 0700); err != nil {
		return false, err
	}
	for _, name := range []string{"history.json", "returned-answers.json"} {
		data, e := os.ReadFile(filepath.Join(dir, name))
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return false, e
		}
		if e = os.WriteFile(filepath.Join(backup, name), data, 0600); e != nil {
			return false, e
		}
	}
	for _, name := range []string{"history.json", "returned-answers.json"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return false, err
		}
	}
	return true, nil
}
