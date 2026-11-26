package server

import (
	"os"
	"sync"
)

// Only schema initialization, never membership, folder grants or file contents.
// Keep no database handles alive: replaced/empty files must initialize again.
var crewTeamLedgerInitialized = struct {
	sync.Mutex
	files map[crewTeamLedgerIdentity]os.FileInfo
}{files: map[crewTeamLedgerIdentity]os.FileInfo{}}

type crewTeamLedgerIdentity struct{ path, folder string }

func (l *crewTeamLedger) initialize(name string) error {
	key := crewTeamLedgerIdentity{name, l.folder}
	info, err := os.Stat(name)
	if err != nil {
		return err
	}
	crewTeamLedgerInitialized.Lock()
	previous, known := crewTeamLedgerInitialized.files[key]
	crewTeamLedgerInitialized.Unlock()
	if known && info.Size() > 0 && os.SameFile(previous, info) {
		return nil
	}
	// Disk/SQL work stays outside the process-wide metadata mutex.
	for _, ddl := range crewTeamLedgerSchema {
		if _, err = l.db.Exec(ddl); err != nil {
			return err
		}
	}
	if _, err = l.db.Exec(`INSERT OR IGNORE INTO folders(folder,revision) VALUES (?,0)`, l.folder); err != nil {
		return err
	}
	info, err = os.Stat(name)
	if err != nil {
		return err
	}
	crewTeamLedgerInitialized.Lock()
	defer crewTeamLedgerInitialized.Unlock()
	if len(crewTeamLedgerInitialized.files) >= 128 {
		for old := range crewTeamLedgerInitialized.files {
			delete(crewTeamLedgerInitialized.files, old)
			break
		}
	}
	crewTeamLedgerInitialized.files[key] = info
	return nil
}
