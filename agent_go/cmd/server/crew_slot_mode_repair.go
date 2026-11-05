package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/security"
)

var crewSlotModeRepairs sync.Map // slot + folder -> started in this process

// repairCrewSlotModesOnce gives the slot's group back the bits that older slot launches (an owner-only umask) left
// off git's files in the Crew's .git trees, so the platform can read them again (reply file snapshots skipped those
// repos). It runs as the slot itself, in the background, once per Crew: a marker in the app's private state records
// a finished pass. Nothing outside the Crew folder and no bits for others change.
func repairCrewSlotModesOnce(projectID, slot, dir string) {
	if slot == "" || dir == "" {
		return
	}
	key := slot + "\x00" + dir
	if _, started := crewSlotModeRepairs.LoadOrStore(key, true); started {
		return
	}
	state, err := workflowCLIStateRoot()
	if err != nil {
		return
	}
	sum := sha256.Sum256([]byte(key))
	marker := filepath.Join(state, "slot-mode-repair", hex.EncodeToString(sum[:12]))
	if _, err := os.Stat(marker); err == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		n, err := security.RepairSlotGitModes(ctx, slot, dir)
		if err != nil {
			log.Printf("[SLOT] repair_modes_failed project=%s files=%d: %v", projectID, n, err)
			crewSlotModeRepairs.Delete(key) // retried at a later turn
			return
		}
		log.Printf("[SLOT] repaired_modes project=%s files=%d", projectID, n)
		if err := os.MkdirAll(filepath.Dir(marker), 0o700); err == nil {
			_ = os.WriteFile(marker, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
		}
	}()
}
