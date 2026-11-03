package server

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
)

// A Mac Crew lives either on the Mac or online (owner, 3 Nov, the decisions log): where it lives is the binding's
// Editing side, and "Move online" / "Move back to this Mac" are the only transfers. Nothing syncs in the
// background, so every pair is a relay pair.

// migrateCrewSyncToRelay turns each older synced pair (continuous, manual or off) into a relay pair once: it stops
// syncing, keeps the files where they are, and moves an unfinished background upload or download aside (its stage
// is kept as a .bak next to it). An unfinished move keeps its pending action so the owner can finish it. Each
// binding file is backed up before it changes. Runs on the Mac and on the server.
func migrateCrewSyncToRelay() {
	state, err := crewroot.StateRoot()
	if err != nil {
		return
	}
	dir := filepath.Join(state, "crew-sync")
	entries, _ := os.ReadDir(dir)
	stamp := time.Now().UTC().Format("20060102T150405Z")
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if entry.IsDir() || !ok || strings.HasSuffix(id, "-files") {
			continue
		}
		b, e := crewroot.ReadSync(id)
		if e != nil || b == nil || (b.Mode == "relay" && b.PendingAction != "update" && b.PendingAction != "download") {
			continue
		}
		raw, e := os.ReadFile(filepath.Join(dir, entry.Name()))
		if e != nil || os.WriteFile(filepath.Join(dir, entry.Name()+"."+stamp+".bak"), raw, 0600) != nil {
			log.Printf("[CREW_SYNC] project=%s migrate=skipped reason=%q", id, "binding backup failed")
			continue
		}
		old, pending := b.Mode, b.PendingAction
		if pending == "update" || pending == "download" {
			if p, e := crewSyncManifestPath(id); e == nil {
				if _, e = os.Stat(p + ".pending"); e == nil {
					_, _ = moveOldCrewTransfer(p+".pending", id)
				}
			}
			b.PendingAction = ""
		}
		b.Mode = "relay"
		if e = crewroot.ReplaceSync(id, *b); e != nil {
			log.Printf("[CREW_SYNC] project=%s migrate=failed reason=%q", id, e.Error())
			continue
		}
		log.Printf("[CREW_SYNC] project=%s side=%s editing=%s migrate=relay old_mode=%q dropped_pending=%q", id, b.Side, b.Editing, old, map[bool]string{true: pending}[pending == "update" || pending == "download"])
	}
}
