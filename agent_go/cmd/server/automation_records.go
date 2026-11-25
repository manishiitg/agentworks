package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/manishiitg/coding-agent-loop/agent_go/pkg/fsutil"
	"github.com/manishiitg/coding-agent-loop/workspace/crewroot"
	workspaceutils "github.com/manishiitg/coding-agent-loop/workspace/utils"
)

// Automation records (#3156): the schedules, functions and webhooks of every
// Crew, workflow and Goal are rows in the server state database, not
// schedules.json / functions.json / webhooks.json files that the scheduler
// re-read for every Crew on every pass.
//
// The workspace document API stays their one interface. readFileFromWorkspace,
// writeFileToWorkspace and deleteWorkspaceFile send these paths here after
// their publication, Team staging and Team policy steps, so every caller keeps
// its checks unchanged. Only records in a Crew's settings folder or a Goal's
// own folder move; published packages and other copies stay files.
//
// One-off move: the first read or write of a folder's record takes the old
// file's bytes as its row, appends them to the private backup
// automation-records.imported.jsonl beside the database, and deletes the file.
// Remove the move once every server has started on this version.

var automationRecordNames = map[string]bool{"schedules.json": true, "functions.json": true, "webhooks.json": true}

type automationRow struct {
	dev, ino uint64
	document sql.NullString
}

// automationRecords mirrors the table; one version row tells every read
// whether another server process (a deploy's overlap) changed it.
var automationRecords struct {
	sync.Mutex
	file    string
	version int64
	rows    map[string]automationRow
}

// automationRecordPath returns the physical file a workspace path names when
// that path is an automation record kept in the database.
func automationRecordPath(file string) (string, bool) {
	file = strings.Trim(path.Clean("/"+strings.TrimSpace(file)), "/")
	if !automationRecordNames[path.Base(file)] || !colocatedWorkspace() {
		return "", false
	}
	docs := fsutil.WorkspaceDocsRoot()
	physical, err := workspaceutils.ResolveUserPath(docs, file, "")
	if err != nil {
		return "", false
	}
	if parts := strings.Split(file, "/"); len(parts) == 3 && parts[0] == "Workflow" && !strings.HasPrefix(parts[1], ".") {
		return physical, true // a Goal or Relay outside any Crew
	}
	_, roots, bound, err := crewroot.Reverse(docs, physical)
	if err != nil || !bound || roots.SettingsDir() == "" || !crewroot.Within(roots.SettingsDir(), physical) {
		return "", false
	}
	return physical, true
}

// folderIdentity is the device and inode of the folder holding a record.
func folderIdentity(dir string) (uint64, uint64, bool) {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return 0, 0, false
	}
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return 0, 0, false
	}
	number := func(name string) (uint64, bool) {
		field := value.FieldByName(name)
		switch {
		case !field.IsValid():
			return 0, false
		case field.CanUint():
			return field.Uint(), true
		case field.CanInt():
			return uint64(field.Int()), true
		}
		return 0, false
	}
	dev, okDev := number("Dev")
	ino, okIno := number("Ino")
	return dev, ino, okDev && okIno
}

func automationRowsLocked(ctx context.Context, db *sql.DB) error {
	file, err := serverStateDatabasePath()
	if err != nil {
		return err
	}
	var version int64
	if err := db.QueryRowContext(ctx, `SELECT COALESCE((SELECT version FROM state_versions WHERE name='automation_records'),0)`).Scan(&version); err != nil {
		return err
	}
	if automationRecords.rows != nil && automationRecords.file == file && automationRecords.version == version {
		return nil
	}
	rows, err := db.QueryContext(ctx, `SELECT file, folder_dev, folder_ino, document FROM automation_records`)
	if err != nil {
		return err
	}
	defer rows.Close()
	loaded := map[string]automationRow{}
	for rows.Next() {
		var key string
		var dev, ino int64
		var row automationRow
		if err := rows.Scan(&key, &dev, &ino, &row.document); err != nil {
			return err
		}
		row.dev, row.ino = uint64(dev), uint64(ino)
		loaded[key] = row
	}
	if err := rows.Err(); err != nil {
		return err
	}
	automationRecords.file, automationRecords.version, automationRecords.rows = file, version, loaded
	return nil
}

func saveAutomationRowLocked(ctx context.Context, db *sql.DB, physical string, row automationRow) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO automation_records(file, folder_dev, folder_ino, document, updated_at) VALUES (?,?,?,?,CURRENT_TIMESTAMP)
		ON CONFLICT(file) DO UPDATE SET folder_dev=excluded.folder_dev, folder_ino=excluded.folder_ino, document=excluded.document, updated_at=excluded.updated_at`,
		physical, int64(row.dev), int64(row.ino), row.document); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO state_versions(name, version) VALUES ('automation_records', 1) ON CONFLICT(name) DO UPDATE SET version=version+1`); err != nil {
		return err
	}
	var version int64
	if err = tx.QueryRowContext(ctx, `SELECT version FROM state_versions WHERE name='automation_records'`).Scan(&version); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if automationRecords.rows != nil && automationRecords.version == version-1 {
		automationRecords.version = version
		automationRecords.rows[physical] = row
	} else {
		automationRecords.rows = nil
	}
	return nil
}

// importAutomationFileLocked moves a folder's old record file in: its bytes
// (read through the workspace service, as every caller did before) become the
// row and a line in the private backup beside the database, then the file is
// deleted. An absent file becomes an empty row.
func importAutomationFileLocked(ctx context.Context, db *sql.DB, file, physical string, dev, ino uint64) (automationRow, error) {
	raw, exists, err := readFileFromWorkspaceQueryUncached(ctx, file, "", 0)
	if err != nil {
		return automationRow{}, err
	}
	row := automationRow{dev: dev, ino: ino, document: sql.NullString{String: raw, Valid: exists}}
	if exists {
		if err = backupAutomationFile(file, physical, raw); err != nil {
			return automationRow{}, err
		}
	}
	if err = saveAutomationRowLocked(ctx, db, physical, row); err != nil {
		return automationRow{}, err
	}
	if exists {
		log.Printf("[SERVER_STATE] event=import table=automation_records file=%s source_bytes=%d", file, len(raw))
		if err := deleteWorkspaceDocument(ctx, file); err != nil {
			log.Printf("[SERVER_STATE] event=source_retirement_failed table=automation_records file=%s error=%v", file, err)
		}
	}
	return row, nil
}

// backupAutomationFile appends the old file to automation-records.imported.jsonl
// (0600) beside the database.
func backupAutomationFile(file, physical, raw string) error {
	db, err := serverStateDatabasePath()
	if err != nil {
		return err
	}
	line, err := json.Marshal(map[string]string{"file": file, "physical": physical, "content": raw, "imported_at": time.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(db), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(filepath.Join(filepath.Dir(db), "automation-records.imported.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err = out.Write(append(line, '\n')); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// currentAutomationRowLocked returns the folder's row, moving its old file in
// first when the folder has none yet (or was recreated).
func currentAutomationRowLocked(ctx context.Context, db *sql.DB, file, physical string, dev, ino uint64) (automationRow, error) {
	if err := automationRowsLocked(ctx, db); err != nil {
		return automationRow{}, err
	}
	if row, exists := automationRecords.rows[physical]; exists && row.dev == dev && row.ino == ino {
		return row, nil
	}
	return importAutomationFileLocked(ctx, db, file, physical, dev, ino)
}

// readAutomationRecord answers a workspace read of an automation record.
// handled is false for any other path, or while the record's folder does not
// exist (the workspace then answers as it always did).
func readAutomationRecord(ctx context.Context, file string) (content string, found, handled bool, err error) {
	physical, ok := automationRecordPath(file)
	if !ok {
		return "", false, false, nil
	}
	dev, ino, ok := folderIdentity(filepath.Dir(physical))
	if !ok {
		return "", false, false, nil
	}
	db, err := serverStateHandle(ctx)
	if err != nil {
		return "", false, true, err
	}
	automationRecords.Lock()
	defer automationRecords.Unlock()
	row, err := currentAutomationRowLocked(ctx, db, file, physical, dev, ino)
	if err != nil {
		return "", false, true, err
	}
	return row.document.String, row.document.Valid, true, nil
}

// writeAutomationRecord saves (content non-nil) or removes an automation
// record. handled is false where readAutomationRecord would not answer: the
// workspace then creates the folder and file, and the next read moves it in.
func writeAutomationRecord(ctx context.Context, file string, content *string) (bool, error) {
	physical, ok := automationRecordPath(file)
	if !ok {
		return false, nil
	}
	dev, ino, ok := folderIdentity(filepath.Dir(physical))
	if !ok {
		return false, nil
	}
	db, err := serverStateHandle(ctx)
	if err != nil {
		return true, err
	}
	automationRecords.Lock()
	defer automationRecords.Unlock()
	if _, err = currentAutomationRowLocked(ctx, db, file, physical, dev, ino); err != nil {
		return true, err
	}
	row := automationRow{dev: dev, ino: ino}
	if content != nil {
		row.document = sql.NullString{String: *content, Valid: true}
	}
	return true, saveAutomationRowLocked(ctx, db, physical, row)
}

// forgetAutomationRecordsUnder drops the rows of a folder being deleted, so a
// folder later created at the same place starts with none.
func forgetAutomationRecordsUnder(ctx context.Context, dir string) {
	if dir == "" || !colocatedWorkspace() {
		return
	}
	db, err := serverStateHandle(ctx)
	if err != nil {
		return
	}
	automationRecords.Lock()
	defer automationRecords.Unlock()
	prefix := strings.TrimSuffix(filepath.Clean(dir), string(filepath.Separator)) + string(filepath.Separator)
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(prefix)
	result, err := db.ExecContext(ctx, `DELETE FROM automation_records WHERE file LIKE ? ESCAPE '\'`, escaped+"%")
	if err != nil {
		log.Printf("[SERVER_STATE] event=forget_failed table=automation_records error=%v", err)
		return
	}
	if n, _ := result.RowsAffected(); n > 0 {
		_, _ = db.ExecContext(ctx, `INSERT INTO state_versions(name, version) VALUES ('automation_records', 1) ON CONFLICT(name) DO UPDATE SET version=version+1`)
		automationRecords.rows = nil
		log.Printf("[SERVER_STATE] event=forget table=automation_records rows=%d", n)
	}
}

// forgetWorkspaceFolderAutomationRecords is forgetAutomationRecordsUnder for a
// workspace folder path.
func forgetWorkspaceFolderAutomationRecords(ctx context.Context, folder string) {
	if !colocatedWorkspace() {
		return
	}
	physical, err := workspaceutils.ResolveUserPath(fsutil.WorkspaceDocsRoot(), strings.Trim(folder, "/"), "")
	if err == nil {
		forgetAutomationRecordsUnder(ctx, physical)
	}
}

// importAllAutomationRecords finishes the one-off move at startup: every
// registered Crew's and workflow's records are read once, so a record nobody
// uses (a function no one called yet) moves in now rather than on first use.
// Each read that finds an old file logs its own import line.
func importAllAutomationRecords(ctx context.Context) {
	if !colocatedWorkspace() {
		return
	}
	var roots, crewRoots, workflowRoots []string
	if records, err := defaultProjectOwners().All(); err != nil {
		log.Printf("[SERVER_STATE] event=import_all_failed table=automation_records error=%v", err)
	} else {
		for _, rec := range records {
			if rec.Product == crewProfileID {
				crewRoots = append(crewRoots, projectStorageRoot(rec))
			}
		}
	}
	roots = append(roots, crewRoots...)
	if executables, err := readCrewExecutables(); err != nil {
		log.Printf("[SERVER_STATE] event=import_all_failed table=automation_records error=%v", err)
	} else {
		for _, binding := range executables {
			roots = append(roots, binding.Root)
			// A Crew workflow's own timers follow the same rule as the Crew's schedules (#4046).
			workflowRoots = append(workflowRoots, binding.Root)
		}
	}
	failed := 0
	for _, root := range roots {
		for name := range automationRecordNames {
			if _, _, _, err := readAutomationRecord(ctx, path.Join(root, name)); err != nil {
				failed++
				log.Printf("[SERVER_STATE] event=import_failed table=automation_records file=%s error=%v", path.Join(root, name), err)
			}
		}
	}
	log.Printf("[SERVER_STATE] event=import_all table=automation_records roots=%d failed=%d", len(roots), failed)
	// #3286: a Crew has no message webhooks any more; back up and remove each Crew's old webhooks record.
	for _, root := range crewRoots {
		removeCrewMessageWebhooks(ctx, root)
		// #4046: a schedule that names no agent runs in its own chat.
		pinCrewSchedulesToOwnChat(ctx, root)
	}
	for _, root := range workflowRoots {
		pinCrewSchedulesToOwnChat(ctx, root)
	}
}

// crewPanelAutomationStamp versions what the Crew panel's Schedules and Triggers sections show: the scheduler's
// summary generation (jobs, runs and manifests) and the automation records version. Only a colocated workspace
// keeps the records in the database, so only there is the stamp complete.
func crewPanelAutomationStamp(ctx context.Context) (string, bool) {
	if !colocatedWorkspace() {
		return "", false
	}
	db, err := serverStateHandle(ctx)
	if err != nil {
		return "", false
	}
	var version int64
	if err := db.QueryRowContext(ctx, `SELECT COALESCE((SELECT version FROM state_versions WHERE name='automation_records'),0)`).Scan(&version); err != nil {
		return "", false
	}
	return strconv.FormatUint(scheduleSummaryGeneration.Load(), 10) + "/" + strconv.FormatInt(version, 10), true
}
