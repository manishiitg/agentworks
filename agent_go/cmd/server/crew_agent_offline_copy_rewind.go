package server

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// Edit and resend drops the Team's copy of the chat (#3707; Team Module Manager, 26 Nov). The copy only grows, so
// teammates could still read the turns an edit removed. A rewind of a chat that has a copy deletes its local source
// journal and leaves a marker; the next sync asks the server to drop its copy (history and chunks; returned answers
// stay) and uploads the shortened chat from the start. No backup: removing what was edited out is the point. Only a
// rewind does this; compaction and trimmed windows keep appending.

const crewOfflineRewindMarker = "reset-after-edit"

// crewOfflineSourceJournalDir is the chat's private source journal folder (see crewOfflineAppJournal), or "".
func crewOfflineSourceJournalDir(record ProductConversationRecord) string {
	roots, found, err := crewStorageRoots(record.WorkspacePath)
	if err != nil || !found || record.ConversationID == "" || record.SessionID == "" {
		return ""
	}
	return filepath.Join(roots.Private, "offline-source-journals", record.ConversationID, crewTransferDigest(record.SessionID)[:16])
}

// markCrewOfflineRewind runs after a rewind of a chat with a Team copy: the local journal starts over and the next
// sync resets the server's copy.
func markCrewOfflineRewind(record ProductConversationRecord) error {
	dir := crewOfflineSourceJournalDir(record)
	if dir == "" {
		return fmt.Errorf("source journal unavailable")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, crewOfflineRewindMarker), []byte("rewind\n"), 0600); err != nil {
		return err
	}
	for _, name := range []string{"messages.jsonl", "source.json", "ack.json"} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	log.Printf("[AGENT_OFFLINE_COPY] session=%s event=reset_marked reason=rewind", record.SessionID)
	return nil
}

func crewOfflineRewindPending(record ProductConversationRecord) bool {
	dir := crewOfflineSourceJournalDir(record)
	if dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, crewOfflineRewindMarker))
	return err == nil
}

// clearCrewOfflineRewind runs once the server confirmed the reset. A lost confirmation leaves the marker, and the
// next sync resets again, which is harmless.
func clearCrewOfflineRewind(record ProductConversationRecord) {
	if dir := crewOfflineSourceJournalDir(record); dir != "" {
		_ = os.Remove(filepath.Join(dir, crewOfflineRewindMarker))
	}
}

// resetCrewOfflineCopy drops the server's copy of one source: its history (cursor and private tail) and chunks.
// Returned answers stay. Caller holds the Team mutex and has proved the exact source and its owner. Safe to repeat.
func resetCrewOfflineCopy(project, agent, dir string, b crewOfflineBinding) error {
	if !b.valid() {
		return fmt.Errorf("invalid source session")
	}
	// readCrewOfflineHistory refuses a copy made from a different source; no copy yet is fine.
	if _, err := readCrewOfflineHistory(dir, b); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(dir, "history.json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.RemoveAll(filepath.Join(dir, "chunks")); err != nil {
		return err
	}
	log.Printf("[AGENT_OFFLINE_COPY] project=%s agent=%s session=%s event=reset reason=rewind", project, agent, b.Session)
	return nil
}
