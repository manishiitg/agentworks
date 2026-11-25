package server

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// One-off (#4009): Watcher records name their check script, and since #3156 those records are rows in the server
// state database, which the workspace's startup move of the platform's scripts (code/ -> .agentworks/) cannot see.
// Rewrite the old paths in every row once, after each changed row is appended to a private backup beside the
// database. Remove this once every server and laptop has started on this version.
var automationRecordScriptPaths = strings.NewReplacer("code/reports/managed/", "dashboards/", "code/triggers/", "watchers/", "code/motion/", "motion/")

func rewriteAutomationRecordScriptPaths(ctx context.Context) {
	if !colocatedWorkspace() {
		return
	}
	db, err := serverStateHandle(ctx)
	if err != nil {
		log.Printf("[PLATFORM_SCRIPTS_MIGRATION] records unavailable: %v", err)
		return
	}
	automationRecords.Lock()
	defer automationRecords.Unlock()
	rows, err := db.QueryContext(ctx, `SELECT file, document FROM automation_records WHERE document LIKE '%code/triggers/%' OR document LIKE '%code/reports/managed/%' OR document LIKE '%code/motion/%'`)
	if err != nil {
		log.Printf("[PLATFORM_SCRIPTS_MIGRATION] records read failed: %v", err)
		return
	}
	type found struct{ file, document string }
	var changes []found
	for rows.Next() {
		var f found
		if err := rows.Scan(&f.file, &f.document); err == nil {
			changes = append(changes, f)
		}
	}
	rows.Close()
	if len(changes) == 0 {
		return
	}
	dbPath, err := serverStateDatabasePath()
	if err != nil {
		log.Printf("[PLATFORM_SCRIPTS_MIGRATION] records backup failed: %v", err)
		return
	}
	backup, err := os.OpenFile(filepath.Join(filepath.Dir(dbPath), "automation-records.platform-scripts.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		log.Printf("[PLATFORM_SCRIPTS_MIGRATION] records backup failed: %v", err)
		return
	}
	defer backup.Close()
	rewritten := 0
	for _, f := range changes {
		line, _ := json.Marshal(map[string]string{"file": f.file, "content": f.document, "rewritten_at": time.Now().UTC().Format(time.RFC3339)})
		if _, err := backup.Write(append(line, '\n')); err != nil {
			log.Printf("[PLATFORM_SCRIPTS_MIGRATION] records backup failed: %v", err)
			return
		}
		if _, err := db.ExecContext(ctx, `UPDATE automation_records SET document=?, updated_at=CURRENT_TIMESTAMP WHERE file=?`, automationRecordScriptPaths.Replace(f.document), f.file); err != nil {
			log.Printf("[PLATFORM_SCRIPTS_MIGRATION] record %s not rewritten: %v", f.file, err)
			continue
		}
		rewritten++
		log.Printf("[PLATFORM_SCRIPTS_MIGRATION] record rewritten file=%s", f.file)
	}
	_, _ = db.ExecContext(ctx, `INSERT INTO state_versions(name, version) VALUES ('automation_records', 1) ON CONFLICT(name) DO UPDATE SET version=version+1`)
	automationRecords.rows = nil
	log.Printf("[PLATFORM_SCRIPTS_MIGRATION] records rewritten=%d", rewritten)
}
