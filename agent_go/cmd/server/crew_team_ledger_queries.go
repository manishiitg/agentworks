package server

import (
	"context"
	"database/sql"
	"encoding/json"
)

type crewTeamLedgerSummary struct {
	files   int
	bytes   int64
	last    crewTeamEvent
	clashes []string
}

// Polling Team cards asks for totals, the last change and open clash names,
// never the full history, chunk lists or retry ledger.
func readCrewTeamLedgerSummary(project string, folder crewTeamFolder, writerLocked bool) (crewTeamLedgerSummary, error) {
	var out crewTeamLedgerSummary
	l, err := openCrewTeamLedger(project, writerLocked)
	if err != nil {
		return out, err
	}
	defer l.db.Close()
	tx, err := l.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT path,size FROM versions WHERE folder=? AND hash IS NOT NULL`, l.folder)
	if err != nil {
		return out, err
	}
	for rows.Next() {
		var path string
		var size int64
		if err = rows.Scan(&path, &size); err != nil {
			rows.Close()
			return out, err
		}
		if folder.holds(path) {
			out.files++
			out.bytes += size
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	err = tx.QueryRow(`SELECT path,kind,revision,actor,author,at FROM events WHERE folder=? ORDER BY ordinal DESC LIMIT 1`, l.folder).Scan(&out.last.Path, &out.last.Kind, &out.last.Revision, &out.last.Actor, &out.last.Author, &out.last.At)
	if err != nil && err != sql.ErrNoRows {
		return out, err
	}
	rows, err = tx.Query(`SELECT path FROM clashes WHERE folder=? AND resolved=0 ORDER BY ordinal DESC`, l.folder)
	if err != nil {
		return out, err
	}
	seen := map[string]bool{}
	for rows.Next() {
		var path string
		if err = rows.Scan(&path); err != nil {
			rows.Close()
			return out, err
		}
		if !seen[path] {
			out.clashes = append(out.clashes, path)
			seen[path] = true
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return out, err
	}
	return out, tx.Commit()
}

// A sync delta reads only event identities after the cursor and current file
// rows for the resulting page. Whole revisions and the caller's guards stay in
// the existing sync handler; a reshare event still counts even if a file's own
// version revision is older.
func readCrewTeamLedgerChanges(project string, since uint64, limit int) (crewTeamFileState, []string, uint64, error) {
	state := crewTeamFileState{Versions: map[string]crewTeamVersion{}, OnPurpose: map[string]string{}}
	l, err := openCrewTeamLedger(project, false)
	if err != nil {
		return state, nil, 0, err
	}
	defer l.db.Close()
	tx, err := l.db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return state, nil, 0, err
	}
	defer tx.Rollback()
	if err = tx.QueryRow(`SELECT revision FROM folders WHERE folder=?`, l.folder).Scan(&state.Revision); err != nil {
		return state, nil, 0, err
	}
	if since > state.Revision {
		since = 0
	}
	rows, err := tx.Query(`SELECT path,revision FROM events WHERE folder=? AND revision>? ORDER BY revision,ordinal`, l.folder, since)
	if err != nil {
		return state, nil, 0, err
	}
	for rows.Next() {
		var e crewTeamEvent
		if err = rows.Scan(&e.Path, &e.Revision); err != nil {
			rows.Close()
			return state, nil, 0, err
		}
		state.Events = append(state.Events, e)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return state, nil, 0, err
	}
	paths, through := crewTeamChangedSince(state, since, limit)
	state.Events = nil // identities were only for paging, not a partial history
	stmt, err := tx.Prepare(`SELECT hash,size,executable,chunks,revision,author,modified_at FROM versions WHERE folder=? AND path=?`)
	if err != nil {
		return state, nil, 0, err
	}
	defer stmt.Close()
	for _, path := range paths {
		var v crewTeamVersion
		var hash sql.NullString
		var size int64
		var executable bool
		var chunks string
		err = stmt.QueryRow(l.folder, path).Scan(&hash, &size, &executable, &chunks, &v.Revision, &v.Author, &v.ModifiedAt)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return state, nil, 0, err
		}
		v.File = teamLedgerFile(hash, size, executable)
		if err = json.Unmarshal([]byte(chunks), &v.Chunks); err != nil {
			return state, nil, 0, err
		}
		state.Versions[path] = v
	}
	rows, err = tx.Query(`SELECT path,actor FROM on_purpose WHERE folder=?`, l.folder)
	if err != nil {
		return state, nil, 0, err
	}
	for rows.Next() {
		var path, actor string
		if err = rows.Scan(&path, &actor); err != nil {
			rows.Close()
			return state, nil, 0, err
		}
		state.OnPurpose[path] = actor
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return state, nil, 0, err
	}
	return state, paths, through, tx.Commit()
}
